// Package docker wraps the Docker CLI to pull images and verify their digests
// locally, avoiding the weight of a full Docker SDK dependency. It shells out to
// the docker binary, which keeps Tower's footprint small and lets us rely on the
// user's own authenticated, versioned CLI rather than reimplementing registry
// auth and transport.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tower/internal/config"
)

// Puller pulls container images and releases any associated resources.
type Puller interface {
	Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error
	Close() error
}

// DockerPuller pulls images via the docker CLI, optionally using a custom docker
// config for registry authentication.
type DockerPuller struct {
	dockerConfigPath string
}

// NewDockerPuller constructs a DockerPuller. dockerConfigPath is the path to a
// docker config.json (or its parent directory); pass "" to use the default
// docker config location.
func NewDockerPuller(dockerConfigPath string) (*DockerPuller, error) {
	return &DockerPuller{dockerConfigPath: dockerConfigPath}, nil
}

// configDir returns the directory to pass to the docker CLI via --config, or ""
// when the default docker config location should be used.
func (p *DockerPuller) configDir() string {
	if p.dockerConfigPath == "" || p.dockerConfigPath == "~/.docker/config.json" {
		return ""
	}
	dir := p.dockerConfigPath
	if filepath.Base(dir) == "config.json" {
		dir = filepath.Dir(dir)
	}
	return dir
}

// globalArgs returns docker global flags that must precede the subcommand.
func (p *DockerPuller) globalArgs(configDir string) []string {
	if configDir == "" {
		return nil
	}
	return []string{"--config", configDir}
}

// Pull pulls img and, when expectedDigest is non-empty, verifies that the image
// actually stored locally matches it before returning. This digest check is
// defense in depth: it guards against the registry having re-tagged the reference
// between resolution and pull, so the caller never deploys an image it did not
// intend to.
func (p *DockerPuller) Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error {
	ref := fmt.Sprintf("%s:%s", img.Name, img.Tag)
	configDir := p.configDir()

	args := append(p.globalArgs(configDir), "pull", ref)

	if err := p.run(ctx, configDir, args); err != nil {
		return fmt.Errorf("docker pull %s: %w", ref, err)
	}

	// Defense in depth (plan §4): confirm the image that actually landed in the
	// local store matches the digest the registry advertised. If the registry
	// re-tagged the reference between resolve and pull, we must not deploy it.
	if expectedDigest != "" {
		if err := p.verifyDigest(ctx, configDir, ref, expectedDigest); err != nil {
			return fmt.Errorf("docker pull %s: digest verification failed: %w", ref, err)
		}
	}

	return nil
}

// verifyDigest inspects the locally pulled image and ensures one of its
// RepoDigests matches expectedDigest. It fails closed: if the digest cannot be
// confirmed, it returns an error so the caller does not deploy or persist state.
func (p *DockerPuller) verifyDigest(ctx context.Context, configDir, ref, expectedDigest string) error {
	args := append(p.globalArgs(configDir), "image", "inspect", "--format", "{{json .RepoDigests}}", ref)

	out, err := p.output(ctx, configDir, args)
	if err != nil {
		return fmt.Errorf("inspect pulled image: %w", err)
	}

	var repoDigests []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &repoDigests); err != nil {
		return fmt.Errorf("parse RepoDigests %q: %w", out, err)
	}

	want := normalizeDigest(expectedDigest)
	for _, rd := range repoDigests {
		if idx := strings.Index(rd, "@"); idx >= 0 {
			if strings.EqualFold(normalizeDigest(rd[idx+1:]), want) {
				return nil
			}
		}
	}

	return fmt.Errorf("pulled image %s has no RepoDigest matching expected %s (got %v)", ref, expectedDigest, repoDigests)
}

// normalizeDigest canonicalizes a digest to its lowercased "sha256:<hex>" form.
// It tolerates bare hex (no "sha256:" prefix) so digests from different sources
// compare reliably regardless of how they were quoted.
func normalizeDigest(d string) string {
	d = strings.TrimSpace(d)
	if !strings.HasPrefix(d, "sha256:") {
		// Tolerate bare hex digests.
		d = "sha256:" + d
	}
	return strings.ToLower(d)
}

// run executes a docker command, capturing combined diagnostics on failure.
func (p *DockerPuller) run(ctx context.Context, configDir string, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if configDir != "" {
		// Propagate the parent environment so docker inherits PATH and any
		// credential helpers, then point it at the requested auth config.
		cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+configDir)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// output executes a docker command and returns its stdout.
func (p *DockerPuller) output(ctx context.Context, configDir string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if configDir != "" {
		// Propagate the parent environment so docker inherits PATH and any
		// credential helpers, then point it at the requested auth config.
		cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+configDir)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

// Close releases any resources held by the Puller. The DockerPuller keeps no
// long-lived resources, so this is a no-op that satisfies the Puller interface.
func (p *DockerPuller) Close() error {
	return nil
}
