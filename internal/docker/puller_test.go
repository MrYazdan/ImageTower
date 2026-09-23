package docker

import (
	"context"
	"testing"
	"time"

	"tower/internal/config"
)

func TestNewDockerPuller(t *testing.T) {
	puller, err := NewDockerPuller("~/.docker/config.json")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if puller == nil {
		t.Fatal("expected non-nil puller")
	}
	if err := puller.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
}

func TestDockerPullerCancel(t *testing.T) {
	puller, err := NewDockerPuller("")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	img := config.ImageConfig{
		Name: "test.local/nonexistent",
		Tag:  "latest",
	}

	err = puller.Pull(ctx, img, "")
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestDockerPullerTimeout(t *testing.T) {
	puller, err := NewDockerPuller("/path/to/custom/config.json")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	img := config.ImageConfig{
		Name: "test.local/nonexistent",
		Tag:  "latest",
	}

	err = puller.Pull(ctx, img, "")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
