// Package measurement is the G3 boundary of semdev's harness-measurement gate: it
// turns the OS-level outcome of running a task's declared test_command into a
// measurement fact, and gates semantic review on those facts rather than on any
// model claim. It is pure and offline — the whole point of G3 is that the outcome
// is DERIVED from the real exit code here, so a model asserting "all tests pass"
// cannot alter what gets recorded (spec: "a failing command records failure
// regardless of model text").
//
// The executing harness (the measurement tool, a later live increment) runs the
// command via the cliexec seam and calls Measure; the reviewer persona (Quinn)
// reads the resulting facts and CanApprove is the deterministic floor under its
// verdict — a false success claim cannot be approved (7.4). This package writes no
// facts and takes no model input; measurement.result and the review gate stay
// harness-truth.
package measurement

import "github.com/c360studio/semdev/internal/cliexec"

// Result is the measurement fact the executing harness stamps: the OS-level
// outcome of one test_command run. Passed is DERIVED — never a field a model or a
// tool schema supplies (G3) — so a non-zero exit records failure no matter what
// the command's stdout claims.
type Result struct {
	// Ran is true only when the command ran to completion. False means it could
	// not be launched or was cancelled (a missing binary, a permission error): the
	// "did it complete" bit lives in the runner's error, NOT in an exit code, so it
	// is folded into the derivation here rather than left to a caller to remember.
	Ran      bool
	ExitCode int
	TimedOut bool
	// Passed is true only when the command ran to completion, exited 0, and was not
	// killed by the timeout. A start failure or a timeout is never a pass, even
	// though a process that never started reports a zero exit code.
	Passed bool
	Stdout string
	Stderr string
	// RunError is the runner's failure detail when !Ran (harness-observed, never a
	// model claim).
	RunError string
}

// passed is the single derivation of a passing measurement, used both to stamp
// Result.Passed and to re-derive the review gate — a command that ran to
// completion, exited zero, and was not killed by the timeout.
func passed(ran bool, exitCode int, timedOut bool) bool {
	return ran && exitCode == 0 && !timedOut
}

// Measure derives a measurement Result from a completed command execution and the
// error the runner returned alongside it. This is the G3 stamp point: Passed is
// computed from the real exit status, timeout, AND completion (runErr), so neither
// a model-supplied outcome nor a command that never started can enter the record
// as a pass. A start failure yields Ran=false / Passed=false with RunError set.
func Measure(r cliexec.Result, runErr error) Result {
	ran := runErr == nil
	res := Result{
		Ran:      ran,
		ExitCode: r.ExitCode,
		TimedOut: r.TimedOut,
		Passed:   passed(ran, r.ExitCode, r.TimedOut),
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
	}
	if !ran {
		res.RunError = runErr.Error()
	}
	return res
}

// CanApprove is the deterministic floor under a semantic review verdict (7.4): a
// review may record an approving verdict ONLY when every harness measurement
// passed. It RE-DERIVES the pass condition from each Result's raw evidence rather
// than trusting the Passed field, so the gate is authoritative no matter how a
// Result was constructed. A model claiming success over a failing measurement
// cannot be approved, and an EMPTY set cannot be approved either — an approval with
// no harness evidence is the canonical false-green. Review findings are additive
// constraints on top of this floor; they never relax it.
func CanApprove(results []Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if !passed(r.Ran, r.ExitCode, r.TimedOut) {
			return false
		}
	}
	return true
}
