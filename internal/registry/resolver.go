package registry

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
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
	dockerConfigPath string
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

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", err
	}
	for _, mt := range acceptedManifestTypes {
		req.Header.Add("Accept", mt)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest != "" {
		return digest, nil
	}

	etag := strings.Trim(strings.TrimPrefix(resp.Header.Get("ETag"), "W/"), "\"")
	if strings.HasPrefix(etag, "sha256:") {
		return etag, nil
	}

	return "", fmt.Errorf("no digest header returned")
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
