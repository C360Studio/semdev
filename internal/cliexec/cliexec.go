// Package cliexec is the thin exec seam semdev uses to shell out to a local, TRUSTED
// command and read its exit code as data. Its consumers: the OpenSpec CLI
// compatibility oracle — `openspec validate` (the validate step) and, at M1,
// `openspec archive` — and `git apply` (the apply_patch code-authoring seam, group 6,
// which applies a developer's diff to the run's checkout). It exists so those steps
// depend on an interface, not os/exec directly, and can be unit-tested with a scripted
// runner (no CLI, no filesystem) while production runs the real binary.
//
// It is deliberately DISTINCT from the clean-room verification Runner (group 8):
// that seam provisions fresh product-build isolation to PROVE the delivered
// artifact; this one only shells a local, trusted command to read its exit code.
// Keeping them separate avoids conflating "run a trusted local tool" with "prove the
// product in a cold sandbox" — different trust and isolation contracts.
//
// A non-zero exit is DATA, not an error: Run returns the captured Result (with the
// real ExitCode) and a nil error whenever the process ran to completion, so the
// caller reads the oracle's verdict from ExitCode. A non-nil error means the
// command could not be run at all (binary missing, context cancelled) — a
// transport-class failure the caller must not read as "invalid."
package cliexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Result is one command invocation's captured outcome. ExitCode is the real OS
// exit status (the harness-measured verdict, never a model-supplied one — G3);
// TimedOut reports that the context deadline killed the process.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// Runner runs name with args in working directory dir and returns the captured
// Result. A completed process (any exit code) returns a nil error; a non-nil
// error means the process could not be started or was cancelled — the caller
// treats that as a transport failure, not a verdict.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (Result, error)
}

// OSRunner is the production Runner over os/exec.
type OSRunner struct{}

// Run executes the command with CommandContext (so a cancelled/expired context
// kills the process) in dir, capturing stdout and stderr. A non-zero exit is
// returned in Result with a nil error; only a genuine start/cancel failure
// returns a non-nil error.
func (OSRunner) Run(ctx context.Context, dir, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
	}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = cmd.ProcessState.ExitCode()
		return res, nil
	case errors.As(err, &exitErr):
		// The process ran and exited non-zero — a verdict, not a failure to run.
		res.ExitCode = exitErr.ExitCode()
		if res.TimedOut {
			// Killed by the deadline: surface as a transport failure so the
			// caller retries rather than reading it as a validation verdict.
			return res, fmt.Errorf("run %s: timed out: %w", name, ctx.Err())
		}
		return res, nil
	default:
		// Could not start (binary missing, permission, cancelled before start).
		return res, fmt.Errorf("run %s in %q: %w", name, dir, err)
	}
}
