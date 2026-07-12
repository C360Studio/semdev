package runspace

import (
	"context"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
)

// newTestSandboxes builds a Sandboxes whose warm containers are the given mock
// runners, consumed one per Provision call in order — so a test drives the whole
// stand-up/resolve/tear-down contract without docker.
func newTestSandboxes(runners ...*cleanroom.MockRunner) *Sandboxes {
	s := NewSandboxes()
	i := 0
	s.newRunner = func(string) cleanroom.Runner {
		r := runners[i]
		i++
		return r
	}
	return s
}

// Provision stands a warm container up (Up), Resolve hands back the SAME runner +
// sandbox measure will Exec through, and Down forgets it (Resolve then fails
// closed). The whole warm-iteration lifecycle, docker-free.
func TestSandboxesProvisionResolveDown(t *testing.T) {
	mock := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 0, Stdout: "ok"}}}}
	s := newTestSandboxes(mock)

	sb, err := s.Provision(context.Background(), "run-1", "img:tag", "/abs/checkout", []string{"GOMODCACHE", "GOCACHE"})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if mock.UpCalls != 1 || mock.UpWorkDir != "/abs/checkout" {
		t.Errorf("Up not called over the checkout root: calls=%d workDir=%q", mock.UpCalls, mock.UpWorkDir)
	}

	runner, got, err := s.Resolve(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if runner != cleanroom.Runner(mock) {
		t.Error("Resolve returned a different runner than Provision stood up")
	}
	if got.WorkDir != sb.WorkDir {
		t.Errorf("Resolve sandbox WorkDir = %q, want the provisioned %q", got.WorkDir, sb.WorkDir)
	}
	// The resolved pair is what measure Execs through.
	if _, err := runner.Exec(context.Background(), got, []string{"sh", "-c", "go test ./..."}); err != nil {
		t.Fatalf("exec through resolved sandbox: %v", err)
	}

	if err := s.Down(context.Background(), "run-1"); err != nil {
		t.Fatalf("down: %v", err)
	}
	if mock.DownCalls != 1 {
		t.Errorf("Down did not tear the container down: DownCalls=%d", mock.DownCalls)
	}
	if _, _, err := s.Resolve(context.Background(), "run-1"); err == nil {
		t.Error("Resolve after Down must fail closed — a torn-down sandbox is not resolvable")
	}
}

// Resolving a run with no provisioned sandbox FAILS CLOSED — measure must park,
// never fall back to a host exec over an unproven environment (SB5).
func TestSandboxesResolveFailsClosed(t *testing.T) {
	s := NewSandboxes()
	if _, _, err := s.Resolve(context.Background(), "unknown-run"); err == nil {
		t.Fatal("Resolve of an unprovisioned run must fail closed")
	}
}

// A provisioning transport fault (Up fails) records nothing and returns the fault —
// the caller parks; Resolve stays closed.
func TestSandboxesProvisionUpFaultRecordsNothing(t *testing.T) {
	mock := &cleanroom.MockRunner{UpErr: cleanroom.ErrDockerUnavailable}
	s := newTestSandboxes(mock)
	if _, err := s.Provision(context.Background(), "run-1", "img", "/abs/checkout", nil); err == nil {
		t.Fatal("a failed Up must return a provisioning fault")
	}
	if _, _, err := s.Resolve(context.Background(), "run-1"); err == nil {
		t.Error("a run whose Up faulted must not resolve (nothing recorded)")
	}
}

// Re-provisioning a run tears down the PRIOR warm container before recording the
// new one — a run never accumulates containers (restart-recovery / re-prove path).
func TestSandboxesReprovisionTearsDownPrior(t *testing.T) {
	first := &cleanroom.MockRunner{}
	second := &cleanroom.MockRunner{}
	s := newTestSandboxes(first, second)

	if _, err := s.Provision(context.Background(), "run-1", "img", "/abs/checkout", nil); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	if _, err := s.Provision(context.Background(), "run-1", "img", "/abs/checkout", nil); err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	if first.DownCalls != 1 {
		t.Errorf("re-provision must tear down the prior container: first.DownCalls=%d, want 1", first.DownCalls)
	}
	runner, _, err := s.Resolve(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("resolve after re-provision: %v", err)
	}
	if runner != cleanroom.Runner(second) {
		t.Error("Resolve must return the re-provisioned (second) runner")
	}
}

// CloseAll (the runtime reaper) tears down every tracked warm sandbox so a
// shutdown leaks no containers.
func TestSandboxesCloseAllReapsEverything(t *testing.T) {
	a := &cleanroom.MockRunner{}
	b := &cleanroom.MockRunner{}
	s := newTestSandboxes(a, b)
	_, _ = s.Provision(context.Background(), "run-a", "img", "/abs/a", nil)
	_, _ = s.Provision(context.Background(), "run-b", "img", "/abs/b", nil)

	if err := s.CloseAll(context.Background()); err != nil {
		t.Fatalf("close all: %v", err)
	}
	if a.DownCalls != 1 || b.DownCalls != 1 {
		t.Errorf("CloseAll must tear down every warm sandbox: a=%d b=%d", a.DownCalls, b.DownCalls)
	}
	if _, _, err := s.Resolve(context.Background(), "run-a"); err == nil {
		t.Error("after CloseAll, no run resolves")
	}
}
