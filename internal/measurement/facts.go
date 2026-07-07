package measurement

// This file names the GRAPH PROJECTION of a measurement Result — the owned fact
// package the measure_task tool (the executing harness) stamps on the run entity,
// and that the review gate later reads back to reconstruct []Result for CanApprove.
// The pure core (measurement.go) computes the Result; these consts fix how it lands
// on the graph. Sharing them between the writer (measure_task) and the future reader
// (the reviewer) is the same anti-drift discipline devtask.Fact* applies to the
// task.spec seam: one declaration, two consumers, no silent divergence.
//
// The keying is PER-TASK on purpose. The graph merges replace-per-(subject,
// predicate) (graph.MergeTriples), so a single exact predicate could hold only the
// LAST task's measurement — a second task would clobber the first. Keying the task
// index into the predicate (measurement.result.<i>.<field>) gives each task its own
// owned sub-package, upserted independently: re-measuring task i replaces task i's
// facts and leaves the others untouched, which is exactly the "latest per task"
// shape CanApprove expects (one measurement per required task). It mirrors the
// task.spec.* namespace (owned, per-task, replace) rather than the append-evidence
// shape, because a measurement is a task's CURRENT outcome, not an accumulating log
// (attempt history is task.attempt's job, a separate writer).

// ResultPrefix is the owned namespace measurement facts live under on the run
// entity: measurement.result.<taskIndex>.<field>. It anchors to the
// measurement.result.* vocab namespace (writer measurement-harness, G5). The task
// index in the middle is the same devtask task index task.spec.<i> is keyed by.
const ResultPrefix = "measurement.result."

// Fact-key suffixes for one task's measurement, under
// measurement.result.<taskIndex>.<suffix>. Passed is the harness-DERIVED headline
// (G3 — computed here from the real exit status, never a caller-supplied outcome);
// Ran/ExitCode/TimedOut are the raw evidence a reader re-derives the pass from
// (ignoring a possibly-stale Passed), and Command records exactly what ran (G7).
const (
	FactCommand  = "command"
	FactRan      = "ran"
	FactExitCode = "exit_code"
	FactTimedOut = "timed_out"
	FactPassed   = "passed"
)
