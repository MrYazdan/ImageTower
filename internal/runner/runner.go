package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var ErrTimeout = errors.New("command timed out")

type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type CommandRunner interface {
	Run(ctx context.Context, command string) (Result, error)
}

type ExecRunner struct {
	Timeout time.Duration
}

func New(timeout time.Duration) *ExecRunner {
	return &ExecRunner{Timeout: timeout}
}

func (r *ExecRunner) Run(ctx context.Context, command string) (Result, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	res := Result{
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}

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
			return res, fmt.Errorf("%w after %s: %s", ErrTimeout, r.Timeout, res.Stderr)
		}
		return res, fmt.Errorf("command failed (exit %d): %s", res.ExitCode, res.Stderr)
	}

	return res, nil
}
