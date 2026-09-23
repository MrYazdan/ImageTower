package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunnerSuccess(t *testing.T) {
	r := New(5 * time.Second)
	res, err := r.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", res.ExitCode)
	}
	if res.Stdout != "hello" {
		t.Fatalf("stdout: got %q, want 'hello'", res.Stdout)
	}
}

func TestRunnerFailure(t *testing.T) {
	r := New(5 * time.Second)
	res, err := r.Run(context.Background(), "echo fail >&2 && exit 3")
	if err == nil {
		t.Fatal("expected error")
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code: got %d, want 3", res.ExitCode)
	}
	if res.Stderr == "" {
		t.Fatal("expected non-empty stderr")
	}
	if !strings.Contains(res.Stderr, "fail") {
		t.Fatalf("stderr should contain 'fail': %q", res.Stderr)
	}
	// A non-timeout failure must not be reported as a timeout.
	if errors.Is(err, ErrTimeout) {
		t.Fatal("failure should not be classified as timeout")
	}
}

func TestRunnerTimeout(t *testing.T) {
	r := New(50 * time.Millisecond)
	start := time.Now()
	res, err := r.Run(context.Background(), "sleep 5")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("expected non-zero exit code on timeout")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

// TestRunnerTimeoutKillsProcessGroup verifies the runner remains usable after a
// timeout that had to SIGKILL a spawned child process group.
func TestRunnerTimeoutKillsProcessGroup(t *testing.T) {
	r := New(100 * time.Millisecond)
	// Start a background child that would outlive the shell.
	cmd := "sleep 30 & wait"

	_, err := r.Run(context.Background(), cmd)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}

	// Give the killed group a short grace to be reaped.
	time.Sleep(200 * time.Millisecond)

	// Re-run a quick command to ensure the runner is still usable.
	res, err := r.Run(context.Background(), "echo ok")
	if err != nil || res.Stdout != "ok" {
		t.Fatalf("runner unusable after timeout: res=%+v err=%v", res, err)
	}
}

func TestRunnerEmptyCommand(t *testing.T) {
	r := New(5 * time.Second)
	res, err := r.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("empty command should succeed (shell no-op): %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code: got %d", res.ExitCode)
	}
}

func TestRunnerContextCancel(t *testing.T) {
	r := New(30 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := r.Run(ctx, "sleep 5")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	// Parent cancellation must NOT be reported as our timeout.
	if errors.Is(err, ErrTimeout) {
		t.Fatal("parent cancellation should not be classified as timeout")
	}
}

func TestRunnerMultiWordCommand(t *testing.T) {
	r := New(5 * time.Second)
	res, err := r.Run(context.Background(), `echo "multi word output"`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "multi word output" {
		t.Fatalf("stdout: got %q", res.Stdout)
	}
}

func TestRunnerPipeCommand(t *testing.T) {
	r := New(5 * time.Second)
	res, err := r.Run(context.Background(), "echo 'a b c' | tr ' ' '\n' | wc -l")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "3" {
		t.Fatalf("stdout: got %q, want '3'", res.Stdout)
	}
}

func TestRunnerCommandNotFound(t *testing.T) {
	r := New(5 * time.Second)
	res, err := r.Run(context.Background(), "nonexistentcommand12345")
	if err == nil {
		t.Fatal("expected error")
	}
	if res.ExitCode == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if res.Stderr == "" {
		t.Fatal("expected stderr")
	}
}
