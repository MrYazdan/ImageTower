// Package state persists per-image digest, last-check, and last-update
// timestamps to a JSON file on disk. Writes are atomic: each update is
// serialized to a temporary file, fsync'd, and renamed over the target so a
// crash mid-write can never leave a partially written (and thus unusable on
// restart) state file.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ImageState records the observed state of a single watched image.
type ImageState struct {
	Digest      string    `json:"digest"`
	LastChecked time.Time `json:"last_checked"` // last time the registry was queried for a new digest
	LastUpdated time.Time `json:"last_updated"` // last time the image was actually re-deployed (zero if never)
}

// State is a concurrency-safe store of ImageState keyed by image reference.
// It is backed by a JSON file that is rewritten atomically on every mutation.
// The embedded RWMutex guards the in-memory data map; all exported methods
// acquire it, which is what makes State safe for concurrent use.
type State struct {
	mu   sync.RWMutex
	path string
	data map[string]ImageState
}

// Load opens (or initializes) the state file at path. A missing file is not an
// error: it returns a fresh, empty State that will be created on first write.
// A corrupt file is treated as empty rather than fatal, so the watcher can
// recover by re-discovering image state instead of refusing to start.
func Load(path string) (*State, error) {
	s := &State{
		path: path,
		data: make(map[string]ImageState),
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read state file: %w", err)
	}

	if err := json.Unmarshal(raw, &s.data); err != nil {
		return s, fmt.Errorf("parse state file (starting fresh): %w", err)
	}
	if s.data == nil {
		s.data = make(map[string]ImageState)
	}

	return s, nil
}

// Get returns the recorded state for imageRef and whether it was present.
func (s *State) Get(imageRef string) (ImageState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.data[imageRef]
	return st, ok
}

// Set replaces the stored state for imageRef and immediately persists it.
func (s *State) Set(imageRef string, st ImageState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[imageRef] = st
	return s.flushLocked()
}

// UpdateChecked records that imageRef was queried at the current time, storing
// the digest seen. It does not mark the image as updated (re-deployed).
func (s *State) UpdateChecked(imageRef, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.data[imageRef]
	existing.Digest = digest
	existing.LastChecked = time.Now().UTC()
	s.data[imageRef] = existing
	return s.flushLocked()
}

// UpdateUpdated records that imageRef was both re-checked and re-deployed at
// the current time, also persisting the digest that triggered the update.
func (s *State) UpdateUpdated(imageRef, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	existing := s.data[imageRef]
	existing.Digest = digest
	existing.LastChecked = now
	existing.LastUpdated = now
	s.data[imageRef] = existing
	return s.flushLocked()
}

// flushLocked serializes the in-memory state to s.path atomically. It must be
// called with s.mu held (the caller already holds the write lock).
//
// Atomicity is achieved via the classic write-ahead temp file pattern: the
// JSON is written to a unique temporary file in the same directory, fsync'd to
// force it to stable storage, then renamed over the real path. Because
// os.Rename is atomic on POSIX, a reader (or a restart of the watcher) either
// sees the complete previous file or the complete new file - never a partial
// one. This matters because a half-written state would otherwise be parsed as
// corrupt on the next boot, forcing the watcher to drop its checkpoint and
// potentially re-deploy every image at once. The temp file is removed on
// return so a failed write leaves no orphan behind.
func (s *State) flushLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".state-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(raw); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}

	return nil
}

// All returns a shallow copy of the full state map. The copy is taken under
// the read lock so callers get a consistent snapshot without holding the lock.
func (s *State) All() map[string]ImageState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ImageState, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}
