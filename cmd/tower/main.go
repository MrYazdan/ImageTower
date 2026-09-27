package main

// tower is the daemon binary that watches configured Docker images for
// registry digest changes, pulls updated images via the Docker CLI, and runs
// the configured deployment commands. This file only handles process
// bootstrapping; the watching/updating logic lives in internal/scheduler.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tower/internal/config"
	"tower/internal/docker"
	"tower/internal/registry"
	"tower/internal/rotator"
	"tower/internal/runner"
	"tower/internal/scheduler"
	"tower/internal/state"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// If default config.yaml does not exist in current directory, check /etc/tower/config.yaml
	targetConfig := *configPath
	if targetConfig == "config.yaml" {
		if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
			if _, err := os.Stat("/etc/tower/config.yaml"); err == nil {
				targetConfig = "/etc/tower/config.yaml"
			}
		}
	}

	cfg, err := config.Load(targetConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config from %s: %v\n", targetConfig, err)
		os.Exit(1)
	}

	// Map the human-readable log level from config onto the slog.Level values
	// used to filter records; fall back to info for anything unrecognized.
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var outWriter io.Writer = os.Stdout
	if cfg.LogFile != "" {
		rotWriter, err := rotator.New(cfg.LogFile, cfg.LogMaxSizeMB, cfg.LogMaxBackups)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to initialize log file %s: %v\n", cfg.LogFile, err)
			os.Exit(1)
		}
		defer rotWriter.Close()
		// MultiWriter mirrors every log line to both stdout (for container
		// log collection) and the rotating file (for on-disk persistence),
		// without changing the rest of the logging code paths.
		outWriter = io.MultiWriter(os.Stdout, rotWriter)
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(outWriter, opts)
	} else {
		handler = slog.NewTextHandler(outWriter, opts)
	}

	logger := slog.New(handler)
	// Install as the process-wide default so library code and the rest of
	// tower can log through slog without threading a logger everywhere.
	slog.SetDefault(logger)

	// Rehydrate previously observed digests so tower doesn't treat every
	// image as "changed" on the first run after a restart. A missing/corrupt
	// state file is non-fatal: we simply start with an empty slate.
	st, err := state.Load(cfg.StateFile)
	if err != nil {
		logger.Warn("failed to load state, starting fresh", "error", err)
	}

	// Build the Docker puller (shells out to the docker CLI). This is an
	// early hard dependency - without it tower can't apply any update.
	puller, err := docker.NewDockerPuller(cfg.DockerConfigPath)
	if err != nil {
		logger.Error("failed to create docker puller", "error", err)
		os.Exit(1)
	}
	defer puller.Close()

	// Wire the scheduler's collaborators: the registry resolver (digest
	// lookup), the command runner (deployment hooks), persisted state, the
	// logger, and the puller. Keeping these as explicit dependencies makes
	// the scheduler's behavior easy to reason about and test.
	sched := scheduler.New(scheduler.Deps{
		Config:   cfg,
		Resolver: registry.NewHTTPResolver(cfg.DockerConfigPath, cfg.InsecureSkipVerify),
		Runner:   runner.New(cfg.CommandTimeout),
		State:    st,
		Logger:   logger,
		Puller:   puller,
	})

	// Derive a context that is cancelled on SIGINT/SIGTERM. This gives the
	// scheduler a single, cooperative shutdown signal instead of each
	// subsystem installing its own signal handler.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("shutdown signal received")
	}()

	if err := sched.Run(ctx); err != nil {
		logger.Error("scheduler error", "error", err)
		os.Exit(1)
	}

	logger.Info("tower stopped")
}
