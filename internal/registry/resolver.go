package registry

import (
	"context"
	"crypto/tls"
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

type DigestResolver interface {
	Resolve(ctx context.Context, image config.ImageConfig) (string, error)
}

var acceptedManifestTypes = []string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.index.v1+json",
}

type HTTPResolver struct {
	client           *http.Client
	mu               sync.RWMutex
	tokens           map[string]*tokenEntry
	dockerConfigPath string
}

type tokenEntry struct {
	token     string
	expiresAt time.Time
}

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

func (r *HTTPResolver) Resolve(ctx context.Context, image config.ImageConfig) (string, error) {
	registryHost, repoPath, err := ParseImageName(image.Name)
	if err != nil {
		return "", err
	}

	scheme := "https"
	if strings.HasPrefix(registryHost, "localhost:") || registryHost == "localhost" ||
		strings.HasPrefix(registryHost, "127.0.0.1:") || registryHost == "127.0.0.1" {
		scheme = "http"
	}

	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registryHost, repoPath, image.Tag)

	cacheKey := fmt.Sprintf("%s/%s", registryHost, repoPath)
	cachedToken := r.getCachedToken(cacheKey)

	digest, authHeader, err := r.requestManifest(ctx, manifestURL, registryHost, repoPath, image, cachedToken)
	if err == nil {
		return digest, nil
	}

	if !isUnauthorized(err) {
		return "", err
	}

	authType, authParams := parseAuthenticateHeader(authHeader)
	if strings.EqualFold(authType, "Bearer") {
		realm, ok := authParams["realm"]
		if !ok || realm == "" {
			return "", fmt.Errorf("registry 401 response missing realm: %s", authHeader)
		}

		token, err := r.fetchToken(ctx, realm, authParams, registryHost, repoPath, image)
		if err != nil {
			return "", fmt.Errorf("fetch token: %w", err)
		}

		digest, _, err = r.requestManifest(ctx, manifestURL, registryHost, repoPath, image, token)
		return digest, err
	}

	return "", fmt.Errorf("authentication failed: %w", err)
}

func (r *HTTPResolver) requestManifest(ctx context.Context, manifestURL, registryHost, repoPath string, image config.ImageConfig, bearerToken string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", "", err
	}

	for _, mt := range acceptedManifestTypes {
		req.Header.Add("Accept", mt)
	}

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", resp.Header.Get("Www-Authenticate"), &unauthorizedError{}
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("registry status %d", resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest != "" {
		return digest, "", nil
	}

	etag := strings.Trim(strings.TrimPrefix(resp.Header.Get("ETag"), "W/"), "\"")
	if strings.HasPrefix(etag, "sha256:") {
		return etag, "", nil
	}

	return "", "", fmt.Errorf("digest not found")
}

func (r *HTTPResolver) fetchToken(ctx context.Context, realm string, params map[string]string, registryHost, repoPath string, image config.ImageConfig) (string, error) {
	cacheKey := fmt.Sprintf("%s/%s", registryHost, repoPath)
	if token := r.getCachedToken(cacheKey); token != "" {
		return token, nil
	}

	realmURL, err := url.Parse(realm)
	if err != nil {
		return "", err
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

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}

	token := tok.Token
	if token == "" {
		token = tok.AccessToken
	}

	expiry := 5 * time.Minute
	if tok.ExpiresIn > 0 {
		expiry = time.Duration(tok.ExpiresIn) * time.Second
	}

	r.setCachedToken(cacheKey, token, expiry-30*time.Second)
	return token, nil
}

func (r *HTTPResolver) getCachedToken(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.tokens[key]; ok && time.Now().Before(entry.expiresAt) {
		return entry.token
	}
	return ""
}

func (r *HTTPResolver) setCachedToken(key, token string, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[key] = &tokenEntry{
		token:     token,
		expiresAt: time.Now().Add(ttl),
	}
}

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

func ParseImageName(name string) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("image name is empty")
	}

	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 1 {
		return "registry-1.docker.io", "library/" + name, nil
	}

	if !strings.Contains(parts[0], ".") && !strings.Contains(parts[0], ":") && parts[0] != "localhost" {
		return "registry-1.docker.io", name, nil
	}

	return parts[0], parts[1], nil
}

type unauthorizedError struct{}

func (e *unauthorizedError) Error() string {
	return "unauthorized: 401 from registry"
}

func isUnauthorized(err error) bool {
	_, ok := err.(*unauthorizedError)
	return ok
}
