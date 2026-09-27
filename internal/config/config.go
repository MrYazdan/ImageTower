// Package config loads, applies defaults to, and validates Tower's YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ImageConfig describes a single watched image and the command to run when it updates.
type ImageConfig struct {
	Name    string `yaml:"name"`
	Tag     string `yaml:"tag"`
	Command string `yaml:"command"`
	AuthEnv string `yaml:"auth_env,omitempty"`
}

// Config is Tower's top-level runtime configuration.
type Config struct {
	LogLevel        string        `yaml:"log_level"`
	LogFormat       string        `yaml:"log_format"`
	LogFile         string        `yaml:"log_file"`
	LogMaxSizeMB    int           `yaml:"log_max_size_mb"`
	LogMaxBackups   int           `yaml:"log_max_backups"`
	CheckInterval   time.Duration `yaml:"check_interval"`
	Concurrency     int           `yaml:"concurrency"`
	CommandTimeout  time.Duration `yaml:"command_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	StateFile       string        `yaml:"state_file"`
	// DockerConfigPath is the path to Docker's config.json used for registry credentials.
	DockerConfigPath string `yaml:"docker_config_path"`
	// InsecureSkipVerify disables TLS certificate verification when contacting registries.
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
	Images             []ImageConfig `yaml:"images"`
}

// Load reads, parses, defaults, and validates the configuration at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	setDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// setDefaults fills in any unset or zero-valued fields with Tower's built-in defaults,
// expanding the "~/.docker/config.json" shorthand to an absolute path when possible.
func setDefaults(cfg *Config) {
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = "text"
	}
	if cfg.LogMaxSizeMB <= 0 {
		cfg.LogMaxSizeMB = 10
	}
	if cfg.LogMaxBackups <= 0 {
		cfg.LogMaxBackups = 3
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 30 * time.Second
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 4
	}
	if cfg.CommandTimeout == 0 {
		cfg.CommandTimeout = 2 * time.Minute
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 5 * time.Minute
	}
	if cfg.StateFile == "" {
		cfg.StateFile = "./state.json"
	}
	if cfg.DockerConfigPath == "" {
		cfg.DockerConfigPath = "~/.docker/config.json"
	}
	if cfg.DockerConfigPath == "~/.docker/config.json" {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.DockerConfigPath = filepath.Join(home, ".docker", "config.json")
		}
	}
}

// validate enforces bounds on timing/concurrency settings, requires at least one
// fully-specified image, rejects duplicate name:tag entries, and ensures the
// state file's parent directory is writable.
func validate(cfg *Config) error {
	if cfg.CheckInterval < 5*time.Second {
		return fmt.Errorf("check_interval must be >= 5s, got %s", cfg.CheckInterval)
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 128 {
		return fmt.Errorf("concurrency must be between 1 and 128, got %d", cfg.Concurrency)
	}
	if cfg.CommandTimeout < 1*time.Second {
		return fmt.Errorf("command_timeout must be >= 1s, got %s", cfg.CommandTimeout)
	}
	if cfg.ShutdownTimeout < 1*time.Second {
		return fmt.Errorf("shutdown_timeout must be >= 1s, got %s", cfg.ShutdownTimeout)
	}
	if len(cfg.Images) == 0 {
		return fmt.Errorf("at least one image must be configured")
	}

	seen := make(map[string]bool)
	for i, img := range cfg.Images {
		if img.Name == "" {
			return fmt.Errorf("images[%d]: name is required", i)
		}
		if img.Tag == "" {
			return fmt.Errorf("images[%d]: tag is required (got empty)", i)
		}
		if img.Command == "" {
			return fmt.Errorf("images[%d]: command is required", i)
		}
		key := img.Name + ":" + img.Tag
		if seen[key] {
			return fmt.Errorf("duplicate image %q", key)
		}
		seen[key] = true
	}

	if err := ensureWritableDir(filepath.Dir(cfg.StateFile)); err != nil {
		return fmt.Errorf("state_file directory not writable: %w", err)
	}

	return nil
}

// ensureWritableDir verifies that dir exists (creating it if necessary), is a
// directory, and is writable by creating and immediately removing a temporary
// file. The current directory (".") and empty values are treated as always
// writable and skipped, since they require no explicit check.
func ensureWritableDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	tmp, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return fmt.Errorf("cannot write to directory: %w", err)
	}
	tmp.Close()
	os.Remove(tmp.Name())
	return nil
}
