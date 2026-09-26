// Package scheduler drives the periodic digest-check loop for Tower. On each
// tick it resolves the current digest of every configured image, and when a
// newer digest is found it pulls and redeploys. Concurrency across (possibly
// overlapping) cycles is bounded by a shared semaphore, and shutdown is
// graceful: in-flight pulls and deploys are allowed to finish within a grace
// period rather than being killed mid-flight.
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

// Puller fetches the image identified by img for the given expected digest.
// The expectedDigest is passed so an implementation can verify it pulls exactly
// what the resolver reported before running any command.
type Puller interface {
	Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error
}

// Deps bundles every dependency the Scheduler needs. Constructing a Scheduler
// via New is the only supported way to wire these in.
type Deps struct {
	Config   *config.Config
	Resolver registry.DigestResolver
	Runner   runner.CommandRunner
	State    *state.State
	Logger   *slog.Logger
	Puller   Puller
}

// Scheduler owns the check loop and all per-image update logic. The running
// sync.Map tracks image references that currently have an active job so a slow
// image is never scheduled twice (for example when a long pull overlaps the
// next cycle's tick).
type Scheduler struct {
	cfg      *config.Config
	resolver registry.DigestResolver
	runner   runner.CommandRunner
	state    *state.State
	logger   *slog.Logger
	puller   Puller
	running  sync.Map
}

// New builds a Scheduler from its dependencies. It performs no I/O; the loop
// only starts when Run is called.
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

// Run drives the periodic check loop until ctx is cancelled.
//
// Shutdown is graceful: cancelling ctx stops scheduling NEW work but does NOT
// abort in-flight pulls/deploys. Running jobs are given up to
// cfg.ShutdownTimeout to finish; only after that grace period are they
// force-cancelled. This prevents half-applied deployments on SIGTERM/SIGINT.
func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.CheckInterval)
	defer ticker.Stop()

	s.logger.Info("scheduler started",
		"interval", s.cfg.CheckInterval.String(),
		"concurrency", s.cfg.Concurrency,
		"images", len(s.cfg.Images),
	)

	// runCtx is deliberately decoupled from ctx so a shutdown signal does not
	// abort work already in progress. It is cancelled only after the grace
	// period elapses (or when Run returns).
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	// Shared across cycles so total parallelism is bounded by Concurrency even
	// when cycles overlap.
	sem := make(chan struct{}, s.cfg.Concurrency)
	var wg sync.WaitGroup

	s.checkAll(runCtx, sem, &wg)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("shutdown signal received, draining in-flight work",
				"grace", s.cfg.ShutdownTimeout.String(),
			)
			s.drain(&wg, cancelRun)
			s.logger.Info("scheduler stopped")
			return nil
		case <-ticker.C:
			s.checkAll(runCtx, sem, &wg)
		}
	}
}

// drain waits for in-flight jobs to finish, force-cancelling them once the
// configured grace period elapses.
func (s *Scheduler) drain(wg *sync.WaitGroup, cancelRun context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("all in-flight work completed before shutdown")
	case <-time.After(s.cfg.ShutdownTimeout):
		s.logger.Warn("shutdown grace period elapsed, cancelling remaining work")
		cancelRun()
		<-done // wait for stragglers to observe the cancellation and stop
	}
}

// checkAll schedules one job per image. It returns without waiting so the main
// loop stays responsive to shutdown; completion is tracked via wg.
func (s *Scheduler) checkAll(runCtx context.Context, sem chan struct{}, wg *sync.WaitGroup) {
	cycleID := fmt.Sprintf("cycle-%d", time.Now().UnixMilli())
	s.logger.Debug("check cycle started", "cycle_id", cycleID)

	for _, img := range s.cfg.Images {
		wg.Add(1)
		go func(img config.ImageConfig) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				s.processImage(runCtx, img, cycleID)
			case <-runCtx.Done():
				return
			}
		}(img)
	}
}

// processImage handles a single image for one cycle: it resolves the current
// digest, short-circuits if nothing changed, and otherwise pulls then runs the
// configured command. It is invoked once per image per cycle; the running map
// (see Scheduler) ensures two invocations for the same image never overlap.
func (s *Scheduler) processImage(ctx context.Context, img config.ImageConfig, cycleID string) {
	imageRef := fmt.Sprintf("%s:%s", img.Name, img.Tag)
	runID := fmt.Sprintf("%s-%s", cycleID, imageRef)

	// LoadOrStore claims this image for the current run. If a prior job for the
	// same image is still active (loaded == true), we skip rather than run it
	// concurrently and risk a double pull/deploy.
	if _, loaded := s.running.LoadOrStore(imageRef, struct{}{}); loaded {
		s.logger.Debug("skipping image, previous run still active", "image", imageRef, "run_id", runID)
		return
	}
	defer s.running.Delete(imageRef)

	logger := s.logger.With("image", imageRef, "run_id", runID)

	digest, err := s.resolver.Resolve(ctx, img)
	if err != nil {
		logger.Error("failed to resolve digest from registry", "error", err)
		return
	}

	existing, _ := s.state.Get(imageRef)

	if existing.Digest == digest {
		logger.Debug("no update available", "digest", digest)
		if err := s.state.UpdateChecked(imageRef, digest); err != nil {
			logger.Error("failed to update state checked", "error", err)
		}
		return
	}

	logger.Info("update detected, pulling...",
		"old_digest", existing.Digest,
		"new_digest", digest,
	)

	pullStart := time.Now()
	if err := s.puller.Pull(ctx, img, digest); err != nil {
		logger.Error("pull failed", "error", err, "duration", time.Since(pullStart).Round(time.Millisecond).String())
		return
	}
	logger.Info("pull succeeded, executing command...",
		"duration", time.Since(pullStart).Round(time.Millisecond).String(),
		"command", img.Command,
	)

	res, err := s.runner.Run(ctx, img.Command)
	if err != nil {
		logger.Error("command failed",
			"command", img.Command,
			"exit_code", res.ExitCode,
			"error", err,
			"stderr", res.Stderr,
		)
		return
	}

	// Persist the new digest BEFORE declaring success. If we logged success
	// first and then crashed before the write, the next cycle would see the old
	// digest again and re-pull/re-deploy the same image. Writing state first
	// makes the update idempotent across restarts.
	if err := s.state.UpdateUpdated(imageRef, digest); err != nil {
		logger.Error("failed to persist state update", "error", err)
		return
	}

	logger.Info("image updated successfully",
		"old_digest", existing.Digest,
		"new_digest", digest,
	)
}
