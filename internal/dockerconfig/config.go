package dockerconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Credentials struct {
	Username      string
	Password      string
	IdentityToken string
	EncodedAuth   string
}

func (c Credentials) IsEmpty() bool {
	return c.Username == "" && c.Password == "" && c.IdentityToken == "" && c.EncodedAuth == ""
}

func (c Credentials) BearerToken() string {
	return c.IdentityToken
}

func (c Credentials) BasicAuth() (string, string, bool) {
	if c.Username != "" {
		return c.Username, c.Password, true
	}
	if c.EncodedAuth != "" {
		decoded, err := base64.StdEncoding.DecodeString(c.EncodedAuth)
		if err != nil {
			return "", "", false
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], true
		}
	}
	return "", "", false
}

type dockerConfigFile struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

type dockerAuthEntry struct {
	Auth          string `json:"auth"`
	IdentityToken string `json:"identitytoken"`
}

func DefaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".docker", "config.json")
	}
	return filepath.Join(".docker", "config.json")
}

func Load(path string) (*dockerConfigFile, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &dockerConfigFile{Auths: make(map[string]dockerAuthEntry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read docker config: %w", err)
	}

	var cfg dockerConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse docker config: %w", err)
	}
	if cfg.Auths == nil {
		cfg.Auths = make(map[string]dockerAuthEntry)
	}

	return &cfg, nil
}

func GetCredentialsForRegistry(path, registryHost string) (Credentials, error) {
	cfg, err := Load(path)
	if err != nil {
		return Credentials{}, err
	}

	if entry, ok := cfg.Auths[registryHost]; ok {
		return Credentials{
			EncodedAuth:   entry.Auth,
			IdentityToken: entry.IdentityToken,
		}, nil
	}

	return Credentials{}, nil
}
