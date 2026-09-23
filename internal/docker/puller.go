package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"tower/internal/config"
)

type Puller interface {
	Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error
	Close() error
}

type DockerPuller struct {
	dockerConfigPath string
}

func NewDockerPuller(dockerConfigPath string) (*DockerPuller, error) {
	return &DockerPuller{dockerConfigPath: dockerConfigPath}, nil
}

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

func (p *DockerPuller) globalArgs(configDir string) []string {
	if configDir == "" {
		return nil
	}
	return []string{"--config", configDir}
}

func (p *DockerPuller) Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error {
	ref := fmt.Sprintf("%s:%s", img.Name, img.Tag)
	configDir := p.configDir()

	args := append(p.globalArgs(configDir), "pull", ref)
	return p.run(ctx, configDir, args)
}

func (p *DockerPuller) run(ctx context.Context, configDir string, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if configDir != "" {
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

func (p *DockerPuller) Close() error {
	return nil
}
