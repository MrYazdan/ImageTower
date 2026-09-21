// Package dockerconfig reads and queries Docker's ~/.docker/config.json for registry credentials.
package dockerconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials represents authentication info for a registry.
type Credentials struct {
	Username      string
	Password      string
	IdentityToken string
	EncodedAuth   string
}

// IsEmpty returns true if no usable credentials are present.
func (c Credentials) IsEmpty() bool {
	return c.Username == "" && c.Password == "" && c.IdentityToken == "" && c.EncodedAuth == ""
}

// BearerToken returns a token suitable for Authorization: Bearer header.
// Returns empty string if no identity token is available.
func (c Credentials) BearerToken() string {
	return c.IdentityToken
}

// BasicAuth returns username and password if available.
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

// dockerConfigFile mirrors the subset of Docker's config.json we care about:
// the per-registry auth entries keyed by registry host.
type dockerConfigFile struct {
	Auths map[string]dockerAuthEntry `json:"auths"`
}

// dockerAuthEntry holds the auth fields for a single registry entry.
type dockerAuthEntry struct {
	Auth          string `json:"auth"`
	IdentityToken string `json:"identitytoken"`
}

// DefaultConfigPath returns the default docker config path.
func DefaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".docker", "config.json")
	}
	return filepath.Join(".docker", "config.json")
}

// Load reads and parses a docker config file.
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

// GetCredentialsForRegistry finds credentials for the given registry host.
// It loads the Docker config, then attempts a direct key match against the host
// (and its common URL variants) before falling back to a normalized host
// comparison, so that credentials stored under any of Docker Hub's several
// canonical keys can be located regardless of how the registry host is written.
func GetCredentialsForRegistry(path, registryHost string) (Credentials, error) {
	cfg, err := Load(path)
	if err != nil {
		return Credentials{}, err
	}

	candidates := getHostCandidates(registryHost)
	for _, c := range candidates {
		if entry, ok := cfg.Auths[c]; ok {
			creds := entryToCredentials(entry)
			if !creds.IsEmpty() {
				return creds, nil
			}
		}
	}

	// Try matching by extracted hostname from URL/key
	for key, entry := range cfg.Auths {
		host := extractHostFromKey(key)
		for _, c := range candidates {
			cleanCandidate := extractHostFromKey(c)
			if host == cleanCandidate || (isDockerHubHost(host) && isDockerHubHost(cleanCandidate)) {
				creds := entryToCredentials(entry)
				if !creds.IsEmpty() {
					return creds, nil
				}
			}
		}
	}

	return Credentials{}, nil
}

// getHostCandidates returns the set of keys under which a registry's credentials
// may be stored in Docker's config. Docker writes auth entries keyed by several
// equivalent forms (e.g. "registry-1.docker.io", "https://index.docker.io/v1/",
// "docker.io"), so for Docker Hub we return all known aliases and for other
// registries we enumerate the common http/https URL variants. Matching against
// every candidate maximizes the chance of finding a stored credential.
func getHostCandidates(registryHost string) []string {
	clean := strings.TrimPrefix(strings.TrimPrefix(registryHost, "https://"), "http://")
	clean = strings.TrimSuffix(clean, "/")

	if isDockerHubHost(clean) {
		return []string{
			"registry-1.docker.io",
			"https://index.docker.io/v1/",
			"index.docker.io",
			"docker.io",
		}
	}

	return []string{
		clean,
		"https://" + clean,
		"https://" + clean + "/",
		"https://" + clean + "/v1/",
		"https://" + clean + "/v2/",
		"http://" + clean,
		"http://" + clean + "/",
	}
}

// isDockerHubHost reports whether host is one of Docker Hub's canonical host
// names, so that any of them can be treated as equivalent when matching
// credentials (Docker Hub has historically used several different keys).
func isDockerHubHost(host string) bool {
	return host == "registry-1.docker.io" || host == "index.docker.io" || host == "docker.io"
}

// entryToCredentials maps a parsed auth entry into the public Credentials type.
func entryToCredentials(entry dockerAuthEntry) Credentials {
	return Credentials{
		EncodedAuth:   entry.Auth,
		IdentityToken: entry.IdentityToken,
	}
}

// extractHostFromKey normalizes a config auth key (which may be a bare host, a
// scheme-prefixed URL, or a URL with a /v1 or /v2 path) down to its bare
// hostname. This lets credentials stored under differently-formatted keys be
// compared on a like-for-like host basis.
func extractHostFromKey(key string) string {
	key = strings.TrimPrefix(key, "https://")
	key = strings.TrimPrefix(key, "http://")
	key = strings.TrimSuffix(key, "/")
	key = strings.TrimSuffix(key, "/v1")
	key = strings.TrimSuffix(key, "/v2")

	if idx := strings.Index(key, "/"); idx > 0 {
		key = key[:idx]
	}

	return key
}
