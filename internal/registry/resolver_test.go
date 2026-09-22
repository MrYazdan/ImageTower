package registry

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tower/internal/config"
)

func TestParseAuthenticateHeader(t *testing.T) {
	header := `Bearer realm="https://gitlab.example.com/jwt/auth",service="container_registry",scope="repository:group/project:pull"`
	scheme, params := parseAuthenticateHeader(header)

	if scheme != "Bearer" {
		t.Fatalf("scheme: got %q, want 'Bearer'", scheme)
	}
	if params["realm"] != "https://gitlab.example.com/jwt/auth" {
		t.Errorf("realm: got %q", params["realm"])
	}
	if params["service"] != "container_registry" {
		t.Errorf("service: got %q", params["service"])
	}
	if params["scope"] != "repository:group/project:pull" {
		t.Errorf("scope: got %q", params["scope"])
	}
}

func TestParseImageName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		expectHost string
		expectRepo string
		expectErr  bool
	}{
		{"full registry", "registry.example.com/myapp", "registry.example.com", "myapp", false},
		{"nested repo", "registry.example.com/team/myapp", "registry.example.com", "team/myapp", false},
		{"gitlab with port", "gitlab.company.com:5050/team/project/app", "gitlab.company.com:5050", "team/project/app", false},
		{"docker hub single", "nginx", "registry-1.docker.io", "library/nginx", false},
		{"docker hub namespace", "myuser/myapp", "registry-1.docker.io", "myuser/myapp", false},
		{"localhost", "localhost:5000/myapp", "localhost:5000", "myapp", false},
		{"with port", "registry.example.com:8443/myapp", "registry.example.com:8443", "myapp", false},
		{"empty", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, repo, err := ParseImageName(tt.input)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tt.expectHost {
				t.Errorf("host: got %q, want %q", host, tt.expectHost)
			}
			if repo != tt.expectRepo {
				t.Errorf("repo: got %q, want %q", repo, tt.expectRepo)
			}
		})
	}
}

func TestGitLabAuthFlow(t *testing.T) {
	var authServer *httptest.Server
	authServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "gitlab-ci-token" || p != "secret-pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("service") != "container_registry" {
			t.Errorf("expected service=container_registry, got %s", r.URL.Query().Get("service"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"jwt-token-xyz","expires_in":300}`))
	}))
	defer authServer.Close()

	var regServer *httptest.Server
	regServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer jwt-token-xyz" {
			w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="container_registry",scope="repository:team/app:pull"`, authServer.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD, got %s", r.Method)
		}
		w.Header().Set("Docker-Content-Digest", "sha256:gitlabsuccess")
		w.WriteHeader(http.StatusOK)
	}))
	defer regServer.Close()

	// Strip http:// prefix to get host
	regHost := strings.TrimPrefix(regServer.URL, "http://")

	// Create temp docker config with credentials
	dir := t.TempDir()
	dockerConfigPath := filepath.Join(dir, "config.json")
	encodedAuth := base64.StdEncoding.EncodeToString([]byte("gitlab-ci-token:secret-pass"))
	dockerConfig := fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`, regHost, encodedAuth)
	if err := os.WriteFile(dockerConfigPath, []byte(dockerConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := NewHTTPResolver(dockerConfigPath, true)
	// Point client to handle the mock test server
	resolver.client = regServer.Client()

	img := config.ImageConfig{
		Name: fmt.Sprintf("%s/team/app", regHost),
		Tag:  "latest",
	}

	digest, err := resolver.Resolve(context.Background(), img)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if digest != "sha256:gitlabsuccess" {
		t.Fatalf("digest: got %q, want 'sha256:gitlabsuccess'", digest)
	}
}

func TestResolveETagFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"sha256:etagdigest123"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	regHost := strings.TrimPrefix(server.URL, "http://")
	resolver := NewHTTPResolver("", true)
	resolver.client = server.Client()

	img := config.ImageConfig{
		Name: fmt.Sprintf("%s/app", regHost),
		Tag:  "v1",
	}

	digest, err := resolver.Resolve(context.Background(), img)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if digest != "sha256:etagdigest123" {
		t.Fatalf("digest: got %q, want 'sha256:etagdigest123'", digest)
	}
}

func TestResolveContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	img := config.ImageConfig{Name: "registry.example.com/myapp", Tag: "latest"}
	resolver := NewHTTPResolver("/nonexistent/docker/config.json", false)

	_, err := resolver.Resolve(ctx, img)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestIsUnauthorized(t *testing.T) {
	err := &unauthorizedError{}
	if !isUnauthorized(err) {
		t.Fatal("expected unauthorized")
	}
	if isUnauthorized(context.Canceled) {
		t.Fatal("should not be unauthorized")
	}
}
