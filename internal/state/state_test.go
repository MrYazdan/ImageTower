package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStateSetGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	ref := "registry.example.com/app:latest"
	now := time.Now().UTC()
	st := ImageState{
		Digest:      "sha256:abc",
		LastChecked: now,
		LastUpdated: now,
	}
	if err := s.Set(ref, st); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Get(ref)
	if !ok {
		t.Fatal("expected state to exist")
	}
	if got.Digest != "sha256:abc" {
		t.Fatalf("digest: got %s, want sha256:abc", got.Digest)
	}
	if got.LastChecked.IsZero() {
		t.Fatal("LastChecked should not be zero")
	}
}

func TestStateGetNonexistent(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "state.json"))

	_, ok := s.Get("nonexistent:latest")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestStateUpdateChecked(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(filepath.Join(dir, "state.json"))

	ref := "app:latest"
	if err := s.UpdateChecked(ref, "sha256:check"); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Get(ref)
	if !ok {
		t.Fatal("expected state")
	}
	if got.Digest != "sha256:check" {
		t.Fatalf("digest: got %s", got.Digest)
	}
	if !got.LastChecked.After(time.Time{}) {
		t.Fatal("LastChecked should be set")
	}
	if !got.LastUpdated.IsZero() {
		t.Fatal("LastUpdated should not be set by UpdateChecked")
	}
}

func TestStateUpdateUpdated(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(filepath.Join(dir, "state.json"))

	ref := "app:latest"
	if err := s.UpdateUpdated(ref, "sha256:updated"); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Get(ref)
	if !ok {
		t.Fatal("expected state")
	}
	if got.Digest != "sha256:updated" {
		t.Fatalf("digest: got %s", got.Digest)
	}
	if got.LastChecked.IsZero() {
		t.Fatal("LastChecked should be set")
	}
	if got.LastUpdated.IsZero() {
		t.Fatal("LastUpdated should be set")
	}
}

func TestStateCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte("not valid json {["), 0o644)

	s, err := Load(path)
	if err == nil {
		t.Log("expected error on corrupt file")
	}
	if s == nil {
		t.Fatal("expected non-nil state even on error")
	}

	// State should still be usable after corruption
	if err := s.Set("test:latest", ImageState{Digest: "sha256:x"}); err != nil {
		t.Fatal(err)
	}
}

func TestStateNonexistentFile(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("nonexistent file should not error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil state")
	}
}

func TestStateAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, _ := Load(path)

	for i := 0; i < 10; i++ {
		if err := s.Set("img:tag", ImageState{Digest: "sha256:test"}); err != nil {
			t.Fatal(err)
		}
	}

	// Verify final file is valid
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]ImageState
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("state file should be valid JSON: %v", err)
	}
	if len(data) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(data))
	}

	// No leftover tmp files
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-tmp") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestStateOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, _ := Load(path)

	ref := "app:latest"
	s.Set(ref, ImageState{Digest: "sha256:v1"})
	s.Set(ref, ImageState{Digest: "sha256:v2"})

	got, _ := s.Get(ref)
	if got.Digest != "sha256:v2" {
		t.Fatalf("expected v2 after overwrite, got %s", got.Digest)
	}
}

func TestStateAll(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(filepath.Join(dir, "state.json"))

	s.Set("a:1", ImageState{Digest: "sha256:a"})
	s.Set("b:2", ImageState{Digest: "sha256:b"})

	all := s.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if _, ok := all["a:1"]; !ok {
		t.Fatal("missing a:1")
	}
	if _, ok := all["b:2"]; !ok {
		t.Fatal("missing b:2")
	}
}

func TestStateConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(filepath.Join(dir, "state.json"))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ref := "app:latest"
			s.Set(ref, ImageState{Digest: "sha256:concurrent"})
			s.Get(ref)
			s.All()
		}(i)
	}
	wg.Wait()
}

func TestStateReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s1, _ := Load(path)
	s1.Set("app:latest", ImageState{Digest: "sha256:persisted"})

	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := s2.Get("app:latest")
	if !ok {
		t.Fatal("expected persisted state after reload")
	}
	if got.Digest != "sha256:persisted" {
		t.Fatalf("digest: got %s, want sha256:persisted", got.Digest)
	}
}
