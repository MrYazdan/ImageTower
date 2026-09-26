package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tower/internal/config"
	"tower/internal/registry"
	"tower/internal/runner"
	"tower/internal/state"
)

type Puller interface {
	Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error
}

type Deps struct {
	Config   *config.Config
	Resolver registry.DigestResolver
	Runner   runner.CommandRunner
	State    *state.State
	Logger   *slog.Logger
	Puller   Puller
}

type Scheduler struct {
	cfg      *config.Config
	resolver registry.DigestResolver
	runner   runner.CommandRunner
	state    *state.State
	logger   *slog.Logger
	puller   Puller
}

func New(deps Deps) *Scheduler {
	return &Scheduler{
		cfg:      deps.Config,
		resolver: deps.Resolver,
		runner:   deps.Runner,
		state:    deps.State,
		logger:   deps.Logger,
		puller:   deps.Puller,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.CheckInterval)
	defer ticker.Stop()

	sem := make(chan struct{}, s.cfg.Concurrency)
	var wg sync.WaitGroup

	s.checkAll(ctx, sem, &wg)

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-ticker.C:
			s.checkAll(ctx, sem, &wg)
		}
	}
}

func (s *Scheduler) checkAll(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup) {
	for _, img := range s.cfg.Images {
		wg.Add(1)
		go func(img config.ImageConfig) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				s.processImage(ctx, img)
			case <-ctx.Done():
				return
			}
		}(img)
	}
}

func (s *Scheduler) processImage(ctx context.Context, img config.ImageConfig) {
	imageRef := fmt.Sprintf("%s:%s", img.Name, img.Tag)
	digest, err := s.resolver.Resolve(ctx, img)
	if err != nil {
		s.logger.Error("failed to resolve digest", "image", imageRef, "error", err)
		return
	}

	existing, _ := s.state.Get(imageRef)
	if existing.Digest == digest {
		_ = s.state.UpdateChecked(imageRef, digest)
		return
	}

	if err := s.puller.Pull(ctx, img, digest); err != nil {
		s.logger.Error("pull failed", "image", imageRef, "error", err)
		return
	}

	res, err := s.runner.Run(ctx, img.Command)
	if err != nil {
		s.logger.Error("command failed", "image", imageRef, "error", err, "stderr", res.Stderr)
		return
	}

	_ = s.state.UpdateUpdated(imageRef, digest)
	s.logger.Info("image updated successfully", "image", imageRef, "digest", digest)
}
