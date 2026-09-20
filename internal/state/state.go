package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type ImageState struct {
	Digest      string    `json:"digest"`
	LastChecked time.Time `json:"last_checked"`
	LastUpdated time.Time `json:"last_updated"`
}

type State struct {
	mu   sync.RWMutex
	path string
	data map[string]ImageState
}

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
		return s, fmt.Errorf("parse state file: %w", err)
	}
	if s.data == nil {
		s.data = make(map[string]ImageState)
	}

	return s, nil
}

func (s *State) Get(imageRef string) (ImageState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.data[imageRef]
	return st, ok
}

func (s *State) Set(imageRef string, st ImageState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[imageRef] = st
	return s.flushLocked()
}

func (s *State) UpdateChecked(imageRef, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.data[imageRef]
	existing.Digest = digest
	existing.LastChecked = time.Now().UTC()
	s.data[imageRef] = existing
	return s.flushLocked()
}

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

func (s *State) flushLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(s.path, raw, 0644)
}

func (s *State) All() map[string]ImageState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ImageState, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}
