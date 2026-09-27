package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
check_interval: 10s
concurrency: 2
command_timeout: 30s
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CheckInterval != 10*time.Second {
		t.Fatalf("interval: got %s, want 10s", cfg.CheckInterval)
	}
	if cfg.Concurrency != 2 {
		t.Fatalf("concurrency: got %d, want 2", cfg.Concurrency)
	}
	if cfg.CommandTimeout != 30*time.Second {
		t.Fatalf("timeout: got %s, want 30s", cfg.CommandTimeout)
	}
	if len(cfg.Images) != 1 {
		t.Fatalf("images: got %d, want 1", len(cfg.Images))
	}
	if cfg.Images[0].Name != "reg.example.com/app" {
		t.Fatalf("name: got %s", cfg.Images[0].Name)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("log_level: got %s, want info", cfg.LogLevel)
	}
	if cfg.CheckInterval != 30*time.Second {
		t.Errorf("check_interval: got %s, want 30s", cfg.CheckInterval)
	}
	if cfg.Concurrency != 4 {
		t.Errorf("concurrency: got %d, want 4", cfg.Concurrency)
	}
	if cfg.CommandTimeout != 2*time.Minute {
		t.Errorf("command_timeout: got %s, want 2m", cfg.CommandTimeout)
	}
	if cfg.LogMaxSizeMB != 10 {
		t.Errorf("log_max_size_mb: got %d, want 10", cfg.LogMaxSizeMB)
	}
	if cfg.LogMaxBackups != 3 {
		t.Errorf("log_max_backups: got %d, want 3", cfg.LogMaxBackups)
	}
}

func TestLoadDuplicateImages(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
check_interval: 10s
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected duplicate image error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error should mention duplicate, got: %v", err)
	}
}

func TestLoadSameNameDifferentTag(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
check_interval: 10s
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: v1
    command: echo hi
  - name: reg.example.com/app
    tag: v2
    command: echo hi
`)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("same name different tag should be valid: %v", err)
	}
}

func TestLoadEmptyImages(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
state_file: `+filepath.Join(dir, "state.json")+`
images: []
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected empty images error")
	}
}

func TestLoadMissingImages(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
state_file: `+filepath.Join(dir, "state.json")+`
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing images error")
	}
}

func TestLoadInvalidInterval(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
check_interval: 1s
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected interval too short error")
	}
	if !strings.Contains(err.Error(), "check_interval") {
		t.Fatalf("error should mention check_interval, got: %v", err)
	}
}

func TestLoadInvalidConcurrency(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
concurrency: 129
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected concurrency too high error")
	}
}

func TestLoadNegativeConcurrency(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
concurrency: -1
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected negative concurrency error")
	}
}

func TestLoadHighConcurrency(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
concurrency: 200
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected concurrency too high error")
	}
}

func TestLoadInvalidCommandTimeout(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
command_timeout: 500ms
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected command timeout error")
	}
}

func TestLoadMissingImageName(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing name error")
	}
}

func TestLoadMissingImageTag(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing tag error")
	}
}

func TestLoadMissingImageCommand(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
state_file: `+filepath.Join(dir, "state.json")+`
images:
  - name: reg.example.com/app
    tag: latest
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing command error")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected file not found error")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0o644)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected YAML parse error")
	}
}

func TestLoadUnwritableStateDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, permission test unreliable")
	}
	path := writeConfig(t, `
check_interval: 10s
state_file: /proc/nonexistent/state.json
images:
  - name: reg.example.com/app
    tag: latest
    command: echo hi
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unwritable state dir error")
	}
}
