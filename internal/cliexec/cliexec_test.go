package cliexec

import (
	"context"
	"strings"
	"testing"
)

// A completed process returns its real exit code with a nil error (a non-zero
// exit is a verdict, not a failure to run) and captures stdout/stderr.
func TestOSRunnerCapturesExitAndStreams(t *testing.T) {
	r := OSRunner{}

	ok, err := r.Run(context.Background(), "", "sh", "-c", "echo out; echo err 1>&2; exit 0")
	if err != nil {
		t.Fatalf("zero-exit run errored: %v", err)
	}
	if ok.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", ok.ExitCode)
	}
	if strings.TrimSpace(ok.Stdout) != "out" || strings.TrimSpace(ok.Stderr) != "err" {
		t.Errorf("streams not captured: stdout=%q stderr=%q", ok.Stdout, ok.Stderr)
	}

	bad, err := r.Run(context.Background(), "", "sh", "-c", "exit 3")
	if err != nil {
		t.Fatalf("non-zero exit must be a nil-error verdict, got: %v", err)
	}
	if bad.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", bad.ExitCode)
	}
}

// Run in a working directory.
func TestOSRunnerRunsInDir(t *testing.T) {
	dir := t.TempDir()
	res, err := OSRunner{}.Run(context.Background(), dir, "pwd")
	if err != nil {
		t.Fatalf("pwd: %v", err)
	}
	// macOS resolves the temp dir through /private; suffix-match is enough.
	if !strings.HasSuffix(strings.TrimSpace(res.Stdout), strings.TrimPrefix(dir, "/private")) &&
		!strings.Contains(strings.TrimSpace(res.Stdout), dir) {
		t.Errorf("pwd = %q, want it under %q", strings.TrimSpace(res.Stdout), dir)
	}
}

// A missing binary is a start failure (non-nil error), not a zero-value success.
func TestOSRunnerMissingBinaryErrors(t *testing.T) {
	if _, err := (OSRunner{}).Run(context.Background(), "", "definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("expected an error running a missing binary")
	}
}
