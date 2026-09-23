// Package runner executes deployment commands via /bin/sh -c under an enforced
// timeout. A command is always launched in its own process group, and on
// cancellation or timeout the whole group is SIGKILLed so that no child or
// grandchild process (for example a `docker compose up` and its helpers) can
// survive the death of the shell.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ErrTimeout is returned (wrapped) when a command exceeds its configured
// timeout. Callers can detect it with errors.Is(err, ErrTimeout) to
// distinguish a timeout from a generic non-zero exit.
var ErrTimeout = errors.New("command timed out")

// Result holds the outcome of a command execution. Stdout and Stderr are
// reported separately so callers never have to guess which stream a value came
// from. ExitCode is the command's exit status (or -1 when the process could not
// be started or was killed by a signal).
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CommandRunner executes shell commands with a timeout. It is the seam that
// lets the scheduler run deployments against a fake runner in tests.
type CommandRunner interface {
	Run(ctx context.Context, command string) (Result, error)
}

// ExecRunner is the production CommandRunner. Its Timeout is applied on top of
// whatever context the caller supplies, so a command is bounded even when the
// caller passes a context with no deadline.
type ExecRunner struct {
	Timeout time.Duration
}

// New builds an ExecRunner whose commands are killed if they run longer than
// timeout. Pass a non-positive timeout to disable the bound (the command then
// lives and dies entirely with the caller's context).
func New(timeout time.Duration) *ExecRunner {
	return &ExecRunner{Timeout: timeout}
}

// Run executes command via /bin/sh -c, enforcing r.Timeout as an independent
// deadline on top of the caller's context.
//
// The command is started in its own process group (Setpgid) and, on
// cancellation or timeout, the entire group is SIGKILLed via cmd.Cancel. This
// prevents orphaned grandchildren (e.g. `docker compose up` spawning helper
// processes) from lingering after the shell itself is killed.
func (r *ExecRunner) Run(ctx context.Context, command string) (Result, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", command)

	// Own process group so we can kill the shell and all of its children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// A negative pid signals the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Give the killed group a bounded window to be reaped before Run returns.
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	res := Result{
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}

	// Our own deadline fired (as opposed to the parent context being cancelled).
	timedOut := errors.Is(cmdCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		if res.Stderr == "" {
			res.Stderr = runErr.Error()
		}
		if timedOut {
			return res, fmt.Errorf("%w after %s (exit %d): %s", ErrTimeout, r.Timeout, res.ExitCode, res.Stderr)
		}
		return res, fmt.Errorf("command failed (exit %d): %s", res.ExitCode, res.Stderr)
	}

	// Defensive: a clean exit while our deadline elapsed should not happen, but
	// surface it as a timeout rather than a silent success.
	if timedOut {
		return res, fmt.Errorf("%w after %s: %s", ErrTimeout, r.Timeout, res.Stderr)
	}

	return res, nil
}
