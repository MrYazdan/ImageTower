package dockerconfig

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeDockerConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadNonexistentFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("nonexistent should not error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.Auths) != 0 {
		t.Fatalf("expected empty auths, got %d", len(cfg.Auths))
	}
}

func TestLoadValidConfig(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"registry.example.com": {
				"auth": "`+base64.StdEncoding.EncodeToString([]byte("user:pass"))+`"
			}
		}
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Auths) != 1 {
		t.Fatalf("expected 1 auth, got %d", len(cfg.Auths))
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	path := writeDockerConfig(t, "not json")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadEmptyFile(t *testing.T) {
	path := writeDockerConfig(t, "{}")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auths == nil {
		t.Fatal("expected non-nil auths map")
	}
}

func TestGetCredentialsExactMatch(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"registry.example.com": {
				"auth": "`+base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))+`"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if creds.IsEmpty() {
		t.Fatal("expected credentials")
	}

	user, pass, ok := creds.BasicAuth()
	if !ok {
		t.Fatal("expected basic auth")
	}
	if user != "myuser" || pass != "mypass" {
		t.Fatalf("got %s:%s, want myuser:mypass", user, pass)
	}
}

func TestGetCredentialsWithHttpsPrefix(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"https://registry.example.com": {
				"auth": "`+base64.StdEncoding.EncodeToString([]byte("user:pass"))+`"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if creds.IsEmpty() {
		t.Fatal("expected credentials with https:// prefix")
	}
}

func TestGetCredentialsWithV1Suffix(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"https://registry.example.com/v1/": {
				"auth": "`+base64.StdEncoding.EncodeToString([]byte("user:pass"))+`"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if creds.IsEmpty() {
		t.Fatal("expected credentials with v1 suffix")
	}
}

func TestGetCredentialsIdentityToken(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"registry.example.com": {
				"identitytoken": "my-identity-token"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if creds.IsEmpty() {
		t.Fatal("expected credentials")
	}
	if creds.BearerToken() != "my-identity-token" {
		t.Fatalf("bearer token: got %q, want 'my-identity-token'", creds.BearerToken())
	}
}

func TestGetCredentialsNoMatch(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"other-registry.com": {
				"auth": "dXNlcjpwYXNz"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !creds.IsEmpty() {
		t.Fatal("expected empty credentials for non-matching registry")
	}
}

func TestGetCredentialsInvalidBase64(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"registry.example.com": {
				"auth": "not-valid-base64!!!"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	// Should still have the encoded auth, but BasicAuth should fail
	if creds.EncodedAuth == "" {
		t.Fatal("expected encoded auth to be set")
	}
	_, _, ok := creds.BasicAuth()
	if ok {
		t.Fatal("expected BasicAuth to fail on invalid base64")
	}
}

func TestGetCredentialsBase64NoColon(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"registry.example.com": {
				"auth": "`+base64.StdEncoding.EncodeToString([]byte("noseparator"))+`"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	_, _, ok := creds.BasicAuth()
	if ok {
		t.Fatal("expected BasicAuth to fail when no colon in decoded value")
	}
}

func TestCredentialsIsEmpty(t *testing.T) {
	creds := Credentials{}
	if !creds.IsEmpty() {
		t.Fatal("empty credentials should be empty")
	}

	creds = Credentials{Username: "user"}
	if creds.IsEmpty() {
		t.Fatal("credentials with username should not be empty")
	}

	creds = Credentials{IdentityToken: "token"}
	if creds.IsEmpty() {
		t.Fatal("credentials with identity token should not be empty")
	}
}

func TestExtractHostFromKey(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"registry.example.com", "registry.example.com"},
		{"https://registry.example.com", "registry.example.com"},
		{"https://registry.example.com/", "registry.example.com"},
		{"https://registry.example.com/v1/", "registry.example.com"},
		{"https://registry.example.com/v2/", "registry.example.com"},
		{"http://registry.example.com", "registry.example.com"},
		{"https://registry.example.com/path/to/something", "registry.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractHostFromKey(tt.input)
			if got != tt.expect {
				t.Errorf("extractHostFromKey(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if filepath.Base(path) != "config.json" {
		t.Fatalf("expected config.json, got %s", filepath.Base(path))
	}
}

func TestGetCredentialsDockerHubAliases(t *testing.T) {
	path := writeDockerConfig(t, `{
		"auths": {
			"https://index.docker.io/v1/": {
				"auth": "`+base64.StdEncoding.EncodeToString([]byte("hubuser:hubpass"))+`"
			}
		}
	}`)

	creds, err := GetCredentialsForRegistry(path, "registry-1.docker.io")
	if err != nil {
		t.Fatal(err)
	}
	if creds.IsEmpty() {
		t.Fatal("expected credentials for registry-1.docker.io matching index.docker.io")
	}

	user, pass, ok := creds.BasicAuth()
	if !ok || user != "hubuser" || pass != "hubpass" {
		t.Fatalf("got %s:%s, want hubuser:hubpass", user, pass)
	}
}

