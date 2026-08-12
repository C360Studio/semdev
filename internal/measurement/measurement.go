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
// outcome of one task's test_command run, BOUND to the task identity it measured.
// Passed is DERIVED — never a field a model or a tool schema supplies (G3) — so a
// non-zero exit records failure no matter what the command's stdout claims.
type Result struct {
	// TaskID binds this measurement to the task whose test_command it ran, so the
	// review gate can verify the REQUIRED evidence exists (not merely that some
	// provided result passed). Command is the exact test_command that ran (audit).
	TaskID  string
	Command string
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

// Measure derives a measurement Result for a task's command execution from the
// runner's Result and the error it returned alongside it. This is the G3 stamp
// point: Passed is computed from the real exit status, timeout, AND completion
// (runErr), so neither a model-supplied outcome nor a command that never started
// can enter the record as a pass. A start failure yields Ran=false / Passed=false
// with RunError set. taskID/command bind the fact to what it measured.
func Measure(taskID, command string, r cliexec.Result, runErr error) Result {
	ran := runErr == nil
	res := Result{
		TaskID:   taskID,
		Command:  command,
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
// review may record an approving verdict ONLY when EVERY required task has a
// passing measurement. It proves "the required task evidence exists and passed",
// not merely "no provided result failed" — so a run cannot approve by omitting a
// task's measurement entirely. For each required task it demands EXACTLY ONE
// observed measurement (the loop supplies the latest attempt's; a missing one, or
// an ambiguous/stale duplicate, blocks approval) and re-derives its pass from raw
// evidence, ignoring the possibly-hand-set Passed field. An empty required set
// cannot be approved — an approval with no required evidence is the canonical
// false-green.
//
// Accepted M0 limit (carry-forward for the measurement-tool increment): the gate
// proves a required task ran and exited zero, NOT that it ran a non-zero number of
// tests — a command that exits 0 having run zero tests still passes. Closing that
// needs harness-parsed test counts (itself G3-sensitive: counts from the real
// output, never model text), which lands with the tool's stdout parse and its own
// red-first pin.
func CanApprove(requiredTaskIDs []string, observed []Result) bool {
	if len(requiredTaskIDs) == 0 {
		return false
	}
	byTask := map[string][]Result{}
	for _, r := range observed {
		byTask[r.TaskID] = append(byTask[r.TaskID], r)
	}
	for _, id := range requiredTaskIDs {
		rs := byTask[id]
		if len(rs) != 1 {
			return false // missing (0) or ambiguous/stale (>1) evidence for a required task
		}
		if !passed(rs[0].Ran, rs[0].ExitCode, rs[0].TimedOut) {
			return false
		}
	}
	return true
}
