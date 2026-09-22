// Package registry resolves the current digest of a container image from a
// registry over HTTP. It issues an HTTP HEAD against the image manifest
// endpoint and reads the Docker-Content-Digest header, transparently handling
// Docker Hub / GitLab style Bearer token authentication (the registry's
// Www-Authenticate challenge/response flow) as well as HTTP Basic auth.
package registry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"tower/internal/config"
	"tower/internal/dockerconfig"
)

// DigestResolver resolves the immutable content digest of a container image,
// allowing callers to detect when an image has been updated (e.g. a new
// manifest published under the same tag).
type DigestResolver interface {
	Resolve(ctx context.Context, image config.ImageConfig) (string, error)
}

// acceptedManifestTypes are the media types sent in the Accept header when
// requesting a manifest. They cover both Docker (image manifest v2 and the
// multi-arch manifest list v2) and OCI (image manifest v1 and the multi-arch
// image index v1) formats, so a registry can respond with whichever schema it
// stores for the requested tag.
var acceptedManifestTypes = []string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.index.v1+json",
}

// HTTPResolver is a DigestResolver that talks to an OCI/Docker registry over
// HTTP. It caches Bearer tokens per registry/repository and reads credentials
// from environment variables or the docker config file.
type HTTPResolver struct {
	client           *http.Client
	mu               sync.RWMutex
	tokens           map[string]*tokenEntry
	dockerConfigPath string
}

// tokenEntry caches a Bearer token together with the time at which it should
// no longer be used.
type tokenEntry struct {
	token     string
	expiresAt time.Time
}

// NewHTTPResolver constructs an HTTPResolver. If dockerConfigPath is empty the
// default docker config path is used. insecureSkipVerify disables TLS
// certificate verification (intended for local/test registries only).
func NewHTTPResolver(dockerConfigPath string, insecureSkipVerify bool) *HTTPResolver {
	if dockerConfigPath == "" {
		dockerConfigPath = dockerconfig.DefaultConfigPath()
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecureSkipVerify,
		},
		Proxy: http.ProxyFromEnvironment,
	}

	return &HTTPResolver{
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		tokens:           make(map[string]*tokenEntry),
		dockerConfigPath: dockerConfigPath,
	}
}

// Resolve returns the current content digest for the given image. It first
// attempts to fetch the manifest using any cached Bearer token; on a 401 it
// inspects the Www-Authenticate challenge and either performs the Bearer
// token flow (Docker Hub / GitLab style) or retries with HTTP Basic auth.
func (r *HTTPResolver) Resolve(ctx context.Context, image config.ImageConfig) (string, error) {
	registryHost, repoPath, err := ParseImageName(image.Name)
	if err != nil {
		return "", err
	}

	scheme := "https"
	// Local registries almost always serve plain HTTP; default them to http
	// instead of failing the TLS handshake against an untrusted endpoint.
	if strings.HasPrefix(registryHost, "localhost:") || registryHost == "localhost" ||
		strings.HasPrefix(registryHost, "127.0.0.1:") || registryHost == "127.0.0.1" {
		scheme = "http"
	}

	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registryHost, repoPath, image.Tag)

	// Try with cached token if available (avoids an unnecessary 401 round-trip).
	cacheKey := fmt.Sprintf("%s/%s", registryHost, repoPath)
	cachedToken := r.getCachedToken(cacheKey)

	digest, authHeader, err := r.requestManifest(ctx, manifestURL, registryHost, repoPath, image, cachedToken)
	if err == nil {
		return digest, nil
	}

	if !isUnauthorized(err) {
		return "", err
	}

	// 401 Unauthorized received: parse the Www-Authenticate header to learn
	// which auth scheme the registry expects.
	authType, authParams := parseAuthenticateHeader(authHeader)
	// Bearer challenge: the registry advertises a token realm; we exchange
	// credentials for a short-lived token and retry the manifest request.
	if strings.EqualFold(authType, "Bearer") {
		realm, ok := authParams["realm"]
		if !ok || realm == "" {
			return "", fmt.Errorf("registry 401 response missing realm in Www-Authenticate: %s", authHeader)
		}

		token, err := r.fetchToken(ctx, realm, authParams, registryHost, repoPath, image)
		if err != nil {
			return "", fmt.Errorf("fetch token: %w", err)
		}

		digest, _, err = r.requestManifest(ctx, manifestURL, registryHost, repoPath, image, token)
		if err != nil {
			return "", err
		}
		return digest, nil
	}

	// Basic challenge: retry the manifest request directly with credentials
	// (only attempted when they weren't already sent on the first try).
	if strings.EqualFold(authType, "Basic") {
		user, pass, ok := r.getBasicCredentials(registryHost, image)
		if ok {
			digest, _, err = r.requestManifestWithBasic(ctx, manifestURL, user, pass)
			if err != nil {
				return "", err
			}
			return digest, nil
		}
	}

	return "", fmt.Errorf("authentication failed for %s: %w", manifestURL, err)
}

// requestManifest performs an HTTP HEAD against the manifest endpoint and
// returns the image digest. On a 401 it returns the Www-Authenticate header
// value as the second result so the caller can drive the auth flow. If
// bearerToken is empty, credentials are resolved from the image's auth env var
// or the docker config file.
func (r *HTTPResolver) requestManifest(ctx context.Context, manifestURL, registryHost, repoPath string, image config.ImageConfig, bearerToken string) (string, string, error) {
	// HEAD avoids downloading the (potentially large) manifest body; the
	// digest is provided by response headers, which is all we need.
	method := http.MethodHead
	req, err := http.NewRequestWithContext(ctx, method, manifestURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}

	for _, mt := range acceptedManifestTypes {
		req.Header.Add("Accept", mt)
	}

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	} else {
		// Priority: explicit env var > docker config identitytoken > docker config basic auth
		if image.AuthEnv != "" {
			if token := os.Getenv(image.AuthEnv); token != "" {
				if strings.Contains(token, ":") {
					parts := strings.SplitN(token, ":", 2)
					req.SetBasicAuth(parts[0], parts[1])
				} else {
					req.Header.Set("Authorization", "Bearer "+token)
				}
			}
		} else if creds, err := dockerconfig.GetCredentialsForRegistry(r.dockerConfigPath, registryHost); err == nil && !creds.IsEmpty() {
			if token := creds.BearerToken(); token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			} else if user, pass, ok := creds.BasicAuth(); ok {
				req.SetBasicAuth(user, pass)
			}
		}
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", resp.Header.Get("Www-Authenticate"), &unauthorizedError{}
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("registry returned status %d for %s", resp.StatusCode, manifestURL)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest != "" {
		return digest, "", nil
	}

	etag := strings.Trim(strings.TrimPrefix(resp.Header.Get("ETag"), "W/"), "\"")
	if strings.HasPrefix(etag, "sha256:") {
		return etag, "", nil
	}

	// Some registries omit Docker-Content-Digest; fall back to a GET and
	// compute the sha256 of the manifest body ourselves.
	return r.calculateManifestDigest(ctx, manifestURL, req.Header)
}

// requestManifestWithBasic performs an HTTP HEAD against the manifest endpoint
// using HTTP Basic auth. It mirrors requestManifest but is used after a Basic
// challenge when no bearer token is involved.
func (r *HTTPResolver) requestManifestWithBasic(ctx context.Context, manifestURL, username, password string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", "", err
	}
	for _, mt := range acceptedManifestTypes {
		req.Header.Add("Accept", mt)
	}
	req.SetBasicAuth(username, password)

	resp, err := r.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest != "" {
		return digest, "", nil
	}

	etag := strings.Trim(strings.TrimPrefix(resp.Header.Get("ETag"), "W/"), "\"")
	if strings.HasPrefix(etag, "sha256:") {
		return etag, "", nil
	}

	return r.calculateManifestDigest(ctx, manifestURL, req.Header)
}

// calculateManifestDigest issues a GET for the manifest body and returns its
// sha256 digest. It is the last-resort fallback used when the registry does
// not expose the digest via response headers. The original request headers
// (Accept, Authorization, etc.) are reused so the auth context is preserved.
func (r *HTTPResolver) calculateManifestDigest(ctx context.Context, manifestURL string, headers http.Header) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header = headers.Clone()

	resp, err := r.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("get manifest returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}

	h := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(h[:])
	return digest, "", nil
}

// fetchToken performs the Bearer token challenge/response: it requests a token
// from the realm advertised in Www-Authenticate (passing the service and scope
// query parameters), then caches the resulting token. A valid cached token for
// the same registry/repository is returned without contacting the realm.
func (r *HTTPResolver) fetchToken(ctx context.Context, realm string, params map[string]string, registryHost, repoPath string, image config.ImageConfig) (string, error) {
	cacheKey := fmt.Sprintf("%s/%s", registryHost, repoPath)
	if token := r.getCachedToken(cacheKey); token != "" {
		return token, nil
	}

	realmURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("invalid realm URL %q: %w", realm, err)
	}

	q := realmURL.Query()
	if service, ok := params["service"]; ok && service != "" {
		q.Set("service", service)
	}
	if scope, ok := params["scope"]; ok && scope != "" {
		q.Set("scope", scope)
	} else if repoPath != "" {
		q.Set("scope", fmt.Sprintf("repository:%s:pull", repoPath))
	}
	realmURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realmURL.String(), nil)
	if err != nil {
		return "", err
	}

	// Attach authentication to token endpoint if available
	if user, pass, ok := r.getBasicCredentials(registryHost, image); ok {
		req.SetBasicAuth(user, pass)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request to %s: %w", realmURL.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("token endpoint %s returned %d: %s", realmURL.String(), resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	token := tok.Token
	if token == "" {
		token = tok.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("empty token received from %s", realmURL.String())
	}

	expiry := 5 * time.Minute
	if tok.ExpiresIn > 0 {
		expiry = time.Duration(tok.ExpiresIn) * time.Second
	}

	// Subtract a 30s safety margin so a cached token is never used right at
	// the edge of expiry (avoids a token that's already stale on the server).
	r.setCachedToken(cacheKey, token, expiry-30*time.Second)
	return token, nil
}

// getBasicCredentials resolves HTTP Basic username/password for the registry,
// preferring the image's auth env var (expected as "user:pass") and falling
// back to the docker config file. The final bool reports whether any
// credentials were found.
func (r *HTTPResolver) getBasicCredentials(registryHost string, image config.ImageConfig) (string, string, bool) {
	if image.AuthEnv != "" {
		if v := os.Getenv(image.AuthEnv); v != "" && strings.Contains(v, ":") {
			parts := strings.SplitN(v, ":", 2)
			return parts[0], parts[1], true
		}
	}

	if creds, err := dockerconfig.GetCredentialsForRegistry(r.dockerConfigPath, registryHost); err == nil && !creds.IsEmpty() {
		return creds.BasicAuth()
	}

	return "", "", false
}

// getCachedToken returns a still-valid cached Bearer token for key, or an
// empty string if none is cached or it has already expired. Safe for
// concurrent use.
func (r *HTTPResolver) getCachedToken(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.tokens[key]; ok && time.Now().Before(entry.expiresAt) {
		return entry.token
	}
	return ""
}

// setCachedToken stores a Bearer token for key with the given time-to-live.
// Safe for concurrent use.
func (r *HTTPResolver) setCachedToken(key, token string, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[key] = &tokenEntry{
		token:     token,
		expiresAt: time.Now().Add(ttl),
	}
}

// parseAuthenticateHeader parses headers like:
// Bearer realm="https://gitlab.example.com/jwt/auth",service="container_registry",scope="repository:group/app:pull"
func parseAuthenticateHeader(header string) (string, map[string]string) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", nil
	}

	parts := strings.SplitN(header, " ", 2)
	scheme := parts[0]
	params := make(map[string]string)
	if len(parts) < 2 {
		return scheme, params
	}

	raw := parts[1]
	for len(raw) > 0 {
		raw = strings.TrimSpace(raw)
		eqIdx := strings.Index(raw, "=")
		if eqIdx == -1 {
			break
		}
		key := strings.TrimSpace(raw[:eqIdx])
		raw = strings.TrimSpace(raw[eqIdx+1:])

		var val string
		if strings.HasPrefix(raw, "\"") {
			raw = raw[1:]
			endQuote := strings.Index(raw, "\"")
			if endQuote == -1 {
				val = raw
				raw = ""
			} else {
				val = raw[:endQuote]
				raw = raw[endQuote+1:]
			}
		} else {
			commaIdx := strings.Index(raw, ",")
			if commaIdx == -1 {
				val = raw
				raw = ""
			} else {
				val = raw[:commaIdx]
				raw = raw[commaIdx+1:]
			}
		}
		params[key] = val

		if commaIdx := strings.Index(raw, ","); commaIdx != -1 {
			raw = raw[commaIdx+1:]
		}
	}

	return scheme, params
}

// ParseImageName splits an image reference into its registry host and
// repository path. References without an explicit registry (e.g. "nginx" or
// "user/repo") default to Docker Hub (registry-1.docker.io), and a bare
// repository name is placed under Docker Hub's "library/" namespace. A host is
// recognized as explicit when it contains a "." or ":" (a port) or is
// "localhost"; otherwise the first path segment is treated as the repository
// and Docker Hub is assumed to be the registry.
func ParseImageName(name string) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("image name is empty")
	}

	parts := strings.SplitN(name, "/", 2)

	// Single segment (e.g. "nginx") -> Docker Hub official image.
	if len(parts) == 1 {
		return "registry-1.docker.io", "library/" + name, nil
	}

	// No registry-style host -> Docker Hub, repository taken as-is.
	if !strings.Contains(parts[0], ".") && !strings.Contains(parts[0], ":") && parts[0] != "localhost" {
		return "registry-1.docker.io", name, nil
	}

	return parts[0], parts[1], nil
}

type unauthorizedError struct{}

func (e *unauthorizedError) Error() string {
	return "unauthorized: 401 from registry"
}

// isUnauthorized reports whether err is the sentinel 401 (unauthorized) error
// returned by requestManifest, indicating an auth challenge should be handled.
func isUnauthorized(err error) bool {
	_, ok := err.(*unauthorizedError)
	return ok
}
