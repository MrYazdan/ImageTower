package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	yamlData := `
images:
  - name: myrepo/app
    tag: latest
    command: echo hello
`
	tmp := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(tmp, []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	if cfg.LogLevel != "info" {
		t.Errorf("expected info, got %s", cfg.LogLevel)
	}
	if cfg.CheckInterval != 30*time.Second {
		t.Errorf("expected 30s, got %s", cfg.CheckInterval)
	}
	if cfg.Concurrency != 4 {
		t.Errorf("expected 4, got %d", cfg.Concurrency)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "no images",
			yaml: "check_interval: 10s
images: []",
		},
		{
			name: "duplicate image",
			yaml: `
images:
  - name: app
    tag: latest
    command: echo 1
  - name: app
    tag: latest
    command: echo 2
`,
		},
		{
			name: "short check interval",
			yaml: `
check_interval: 1s
images:
  - name: app
    tag: latest
    command: echo 1
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := filepath.Join(t.TempDir(), "cfg.yaml")
			_ = os.WriteFile(tmp, []byte(tt.yaml), 0644)
			_, err := Load(tmp)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}
