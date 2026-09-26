package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tower/internal/config"
	"tower/internal/runner"
	"tower/internal/state"
)

type mockResolver struct {
	mu        sync.Mutex
	digests   map[string]string
	calls     int64
	shouldErr bool
}

func (m *mockResolver) Resolve(ctx context.Context, img config.ImageConfig) (string, error) {
	atomic.AddInt64(&m.calls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shouldErr {
		return "", &mockError{msg: "resolver error"}
	}
	return m.digests[img.Name+":"+img.Tag], nil
}

type mockRunner struct {
	mu         sync.Mutex
	shouldFail bool
	calls      int64
	commands   []string
	delay      time.Duration
}

func (m *mockRunner) Run(ctx context.Context, cmd string) (runner.Result, error) {
	atomic.AddInt64(&m.calls, 1)
	m.mu.Lock()
	m.commands = append(m.commands, cmd)
	m.mu.Unlock()
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return runner.Result{ExitCode: -1, Stderr: "cancelled"}, ctx.Err()
		}
	}
	if m.shouldFail {
		return runner.Result{ExitCode: 1, Stderr: "mock stderr"}, &mockError{msg: "command failed"}
	}
	return runner.Result{ExitCode: 0}, nil
}

type mockError struct{ msg string }

func (e *mockError) Error() string { return e.msg }

type mockPuller struct {
	mu              sync.Mutex
	shouldFail      bool
	calls           int64
	expectedDigests []string
}

func (m *mockPuller) Pull(ctx context.Context, img config.ImageConfig, expectedDigest string) error {
	atomic.AddInt64(&m.calls, 1)
	m.mu.Lock()
	m.expectedDigests = append(m.expectedDigests, expectedDigest)
	m.mu.Unlock()
	if m.shouldFail {
		return &mockError{msg: "pull failed"}
	}
	return nil
}

type discardWriter struct{}

func (w *discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func newTestDeps(t *testing.T) (*config.Config, *mockResolver, *mockRunner, *mockPuller, *state.State) {
	t.Helper()

	dir := t.TempDir()
	st, _ := state.Load(dir + "/state.json")

	cfg := &config.Config{
		CheckInterval:   time.Hour,
		Concurrency:     2,
		CommandTimeout:  time.Minute,
		ShutdownTimeout: 2 * time.Second,
		Images: []config.ImageConfig{
			{Name: "reg.local/app", Tag: "latest", Command: "echo deploy"},
		},
	}

	resolver := &mockResolver{digests: map[string]string{"reg.local/app:latest": "sha256:new"}}
	cmdRunner := &mockRunner{}
	puller := &mockPuller{}

	return cfg, resolver, cmdRunner, puller, st
}

func newScheduler(cfg *config.Config, resolver *mockResolver, runner *mockRunner, puller *mockPuller, st *state.State) *Scheduler {
	logger := slog.New(slog.NewTextHandler(&discardWriter{}, nil))
	return New(Deps{
		Config:   cfg,
		Resolver: resolver,
		Runner:   runner,
		State:    st,
		Logger:   logger,
		Puller:   puller,
	})
}

func TestSchedulerUpdatesStateOnSuccess(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	entry, ok := st.Get("reg.local/app:latest")
	if !ok {
		t.Fatal("expected state entry")
	}
	if entry.Digest != "sha256:new" {
		t.Fatalf("digest: got %s, want sha256:new", entry.Digest)
	}
	if entry.LastUpdated.IsZero() {
		t.Fatal("LastUpdated should be set")
	}
	if entry.LastChecked.IsZero() {
		t.Fatal("LastChecked should be set")
	}

	if atomic.LoadInt64(&runner.calls) != 1 {
		t.Fatalf("runner calls: got %d, want 1", runner.calls)
	}
	if atomic.LoadInt64(&puller.calls) != 1 {
		t.Fatalf("puller calls: got %d, want 1", puller.calls)
	}
	if len(puller.expectedDigests) != 1 || puller.expectedDigests[0] != "sha256:new" {
		t.Fatalf("expectedDigests: got %v, want [sha256:new]", puller.expectedDigests)
	}
}

func TestSchedulerNoUpdateOnCommandFail(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	runner.shouldFail = true
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	_, ok := st.Get("reg.local/app:latest")
	if ok {
		t.Fatal("state should not be updated on command failure")
	}
	if atomic.LoadInt64(&runner.calls) != 1 {
		t.Fatalf("runner calls: got %d, want 1", runner.calls)
	}
	if atomic.LoadInt64(&puller.calls) != 1 {
		t.Fatalf("puller should still be called once")
	}
}

func TestSchedulerNoUpdateOnPullFail(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	puller.shouldFail = true
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	_, ok := st.Get("reg.local/app:latest")
	if ok {
		t.Fatal("state should not be updated on pull failure")
	}
	if atomic.LoadInt64(&runner.calls) != 0 {
		t.Fatalf("runner should NOT be called on pull failure, got %d calls", runner.calls)
	}
	if atomic.LoadInt64(&puller.calls) != 1 {
		t.Fatalf("puller calls: got %d, want 1", puller.calls)
	}
}

func TestSchedulerNoUpdateOnResolverFail(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	resolver.shouldErr = true
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	_, ok := st.Get("reg.local/app:latest")
	if ok {
		t.Fatal("state should not be updated on resolver failure")
	}
	if atomic.LoadInt64(&puller.calls) != 0 {
		t.Fatalf("puller should NOT be called on resolver failure")
	}
	if atomic.LoadInt64(&runner.calls) != 0 {
		t.Fatalf("runner should NOT be called on resolver failure")
	}
}

func TestSchedulerSameDigestNoUpdate(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	st.Set("reg.local/app:latest", state.ImageState{Digest: "sha256:same"})
	resolver.digests["reg.local/app:latest"] = "sha256:same"
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	if atomic.LoadInt64(&puller.calls) != 0 {
		t.Fatalf("puller should NOT be called when digest unchanged, got %d calls", puller.calls)
	}
	if atomic.LoadInt64(&runner.calls) != 0 {
		t.Fatalf("runner should NOT be called when digest unchanged")
	}

	entry, _ := st.Get("reg.local/app:latest")
	if entry.Digest != "sha256:same" {
		t.Fatalf("digest: got %s, want sha256:same", entry.Digest)
	}
	if entry.LastChecked.IsZero() {
		t.Fatal("LastChecked should still be updated")
	}
}

func TestSchedulerSkipConcurrentSameImage(t *testing.T) {
	cfg, resolver, _, _, st := newTestDeps(t)
	s := newScheduler(cfg, resolver, &mockRunner{}, &mockPuller{}, st)

	imageRef := "reg.local/app:latest"
	s.running.Store(imageRef, struct{}{})

	done := make(chan struct{})
	go func() {
		s.processImage(context.Background(), cfg.Images[0], "test-cycle")
		close(done)
	}()

	select {
	case <-done:
		// Good - skipped immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected skip to return immediately")
	}
}

func TestSchedulerMultipleImages(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	cfg.Images = []config.ImageConfig{
		{Name: "reg.local/app1", Tag: "v1", Command: "echo 1"},
		{Name: "reg.local/app2", Tag: "v2", Command: "echo 2"},
		{Name: "reg.local/app3", Tag: "v3", Command: "echo 3"},
	}
	resolver.digests = map[string]string{
		"reg.local/app1:v1": "sha256:d1",
		"reg.local/app2:v2": "sha256:d2",
		"reg.local/app3:v3": "sha256:d3",
	}

	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	cancel()

	if atomic.LoadInt64(&runner.calls) != 3 {
		t.Fatalf("runner calls: got %d, want 3", runner.calls)
	}
	if atomic.LoadInt64(&puller.calls) != 3 {
		t.Fatalf("puller calls: got %d, want 3", puller.calls)
	}

	for _, ref := range []string{"reg.local/app1:v1", "reg.local/app2:v2", "reg.local/app3:v3"} {
		entry, ok := st.Get(ref)
		if !ok {
			t.Fatalf("missing state for %s", ref)
		}
		if entry.Digest == "" {
			t.Fatalf("digest not set for %s", ref)
		}
	}
}

func TestSchedulerConcurrentImagesRespectConcurrency(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	cfg.Concurrency = 2
	cfg.Images = []config.ImageConfig{
		{Name: "reg.local/a", Tag: "t", Command: "echo a"},
		{Name: "reg.local/b", Tag: "t", Command: "echo b"},
		{Name: "reg.local/c", Tag: "t", Command: "echo c"},
		{Name: "reg.local/d", Tag: "t", Command: "echo d"},
	}
	resolver.digests = map[string]string{
		"reg.local/a:t": "sha256:a",
		"reg.local/b:t": "sha256:b",
		"reg.local/c:t": "sha256:c",
		"reg.local/d:t": "sha256:d",
	}
	runner.delay = 100 * time.Millisecond

	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	time.Sleep(500 * time.Millisecond)
	cancel()

	if atomic.LoadInt64(&runner.calls) != 4 {
		t.Fatalf("runner calls: got %d, want 4", runner.calls)
	}
}

func TestSchedulerCommandIncludesCorrectCommand(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	cfg.Images[0].Command = "docker compose up -d myservice"
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(runner.commands))
	}
	if runner.commands[0] != "docker compose up -d myservice" {
		t.Fatalf("command: got %q", runner.commands[0])
	}
}

// TestSchedulerGracefulShutdown verifies that a shutdown signal does NOT abort
// an in-flight deploy: Run must wait for it to finish and persist state.
func TestSchedulerGracefulShutdown(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	runner.delay = 300 * time.Millisecond // simulate a slow deploy
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Let the first cycle start the slow deploy, then request shutdown.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}

	// The in-flight deploy must have completed and persisted state.
	entry, ok := st.Get("reg.local/app:latest")
	if !ok || entry.Digest != "sha256:new" {
		t.Fatalf("in-flight deploy was aborted; entry=%+v ok=%v", entry, ok)
	}
	if atomic.LoadInt64(&runner.calls) < 1 {
		t.Fatal("expected the deploy to have run")
	}
}

// TestSchedulerShutdownForceCancelsAfterGrace verifies that work exceeding the
// grace period is force-cancelled and does not persist state.
func TestSchedulerShutdownForceCancelsAfterGrace(t *testing.T) {
	cfg, resolver, runner, puller, st := newTestDeps(t)
	cfg.ShutdownTimeout = 100 * time.Millisecond
	runner.delay = 5 * time.Second // far longer than the grace period
	s := newScheduler(cfg, resolver, runner, puller, st)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(50 * time.Millisecond) // deploy starts
	cancel()

	select {
	case <-done:
		// returned after the grace period elapsed
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not force-cancel after grace period")
	}

	if _, ok := st.Get("reg.local/app:latest"); ok {
		t.Fatal("state should not be updated when deploy is force-cancelled")
	}
}
