package measurement

import (
	"errors"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
)

// A zero exit that ran to completion with no timeout is the only pass.
func TestMeasurePasses(t *testing.T) {
	r := Measure("task-0", "go test ./...", cliexec.Result{ExitCode: 0, Stdout: "ok"}, nil)
	if !r.Passed || !r.Ran {
		t.Fatalf("exit 0 completed must derive Passed=true, Ran=true; got %+v", r)
	}
	if r.TaskID != "task-0" || r.Command != "go test ./..." {
		t.Errorf("identity not bound: %+v", r)
	}
}

// G3 core (7.2): a non-zero exit records failure regardless of what the command's
// output claims. The model's "ALL TESTS PASSED" text cannot flip the derived fact.
func TestMeasureNonZeroExitFailsRegardlessOfText(t *testing.T) {
	r := Measure("task-0", "go test ./...", cliexec.Result{ExitCode: 1, Stdout: "ALL TESTS PASSED\nOK: 42 assertions"}, nil)
	if r.Passed {
		t.Fatal("a non-zero exit must record failure regardless of stdout claims")
	}
	if r.ExitCode != 1 {
		t.Errorf("exit code not carried through: %d", r.ExitCode)
	}
}

// A timeout is never a pass, even if the killed process happened to report exit 0.
func TestMeasureTimeoutIsNotAPass(t *testing.T) {
	r := Measure("task-0", "go test ./...", cliexec.Result{ExitCode: 0, TimedOut: true}, nil)
	if r.Passed {
		t.Fatal("a timed-out run must never record a pass")
	}
}

// Regression pin (semstreams review HIGH): a command that never STARTED returns a
// zero-value cliexec.Result (ExitCode 0) with a non-nil error. Measure must fold
// that error in — a start failure is Ran=false / Passed=false, never a false-green
// pass off the zero exit code.
func TestMeasureStartFailureIsNotAPass(t *testing.T) {
	r := Measure("task-0", "pytest", cliexec.Result{}, errors.New("exec: \"pytest\": executable file not found in $PATH"))
	if r.Passed {
		t.Fatal("a command that never started must not record a pass (zero-value ExitCode is not success)")
	}
	if r.Ran {
		t.Error("Ran must be false on a start failure")
	}
	if r.RunError == "" {
		t.Error("RunError should record why the command did not run")
	}
}

// A signal-killed process (negative exit code) is not a pass.
func TestMeasureNegativeExitIsNotAPass(t *testing.T) {
	if r := Measure("t", "c", cliexec.Result{ExitCode: -1}, nil); r.Passed {
		t.Fatal("a negative exit code (signal-killed) must not record a pass")
	}
}

func pass(taskID string) Result { return Result{TaskID: taskID, Ran: true, ExitCode: 0} }
func fail(taskID string) Result { return Result{TaskID: taskID, Ran: true, ExitCode: 1} }

func TestCanApprove(t *testing.T) {
	if !CanApprove([]string{"task-0", "task-1"}, []Result{pass("task-0"), pass("task-1")}) {
		t.Error("every required task passing must be approvable")
	}
	if CanApprove([]string{"task-0", "task-1"}, []Result{pass("task-0"), fail("task-1")}) {
		t.Error("any failing required measurement must block approval")
	}
	// An approval with no required evidence is the canonical false-green.
	if CanApprove(nil, []Result{pass("task-0")}) {
		t.Error("an empty required set must not be approvable")
	}
}

// Codex P2: the gate must prove the REQUIRED evidence exists, not just that
// provided results passed. A run with two required tasks but only one passing
// measurement (the other omitted) must NOT approve.
func TestCanApproveRejectsMissingRequiredMeasurement(t *testing.T) {
	if CanApprove([]string{"task-0", "task-1"}, []Result{pass("task-0")}) {
		t.Fatal("a missing required task's measurement must block approval")
	}
}

// Ambiguous/stale evidence — more than one measurement for a required task —
// blocks approval; the loop must supply exactly the latest per task.
func TestCanApproveRejectsDuplicateMeasurement(t *testing.T) {
	if CanApprove([]string{"task-0"}, []Result{pass("task-0"), pass("task-0")}) {
		t.Fatal("duplicate (ambiguous/stale) measurements for a required task must block approval")
	}
}

// An extra measurement for a task that is not required does not affect the
// required proof (it is ignored, not a reason to approve or reject).
func TestCanApproveIgnoresNonRequiredExtras(t *testing.T) {
	if !CanApprove([]string{"task-0"}, []Result{pass("task-0"), pass("task-9")}) {
		t.Error("an extra non-required measurement must not block a satisfied required set")
	}
}

// Defense in depth: CanApprove re-derives from raw evidence, so a Result whose
// Passed field was hand-set inconsistently with a non-zero exit (or a start
// failure) cannot slip through the gate.
func TestCanApproveReDerivesAndIgnoresHandSetPassed(t *testing.T) {
	inconsistentExit := Result{TaskID: "task-0", Passed: true, Ran: true, ExitCode: 1}
	if CanApprove([]string{"task-0"}, []Result{inconsistentExit}) {
		t.Error("a hand-set Passed over a non-zero exit must not be approvable")
	}
	neverRan := Result{TaskID: "task-0", Passed: true, Ran: false, ExitCode: 0}
	if CanApprove([]string{"task-0"}, []Result{neverRan}) {
		t.Error("a hand-set Passed over a start failure must not be approvable")
	}
}

// The end-to-end false-success pin (7.4): a measurement derived from a non-zero
// exit — no matter its stdout — cannot be approved, so a model's false success
// claim cannot reach open_pr.
func TestFalseSuccessClaimCannotBeApproved(t *testing.T) {
	m := Measure("task-0", "go test ./...", cliexec.Result{ExitCode: 2, Stdout: "PASS"}, nil)
	if CanApprove([]string{"task-0"}, []Result{m}) {
		t.Fatal("a false success claim (non-zero exit) must not be approvable")
	}
}
