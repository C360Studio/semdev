package measurement

// This file names the GRAPH PROJECTION of a measurement Result — the owned fact
// package the measure_task tool (the executing harness) stamps on the run entity,
// and that the review gate later reads back to reconstruct []Result for CanApprove.
// The pure core (measurement.go) computes the Result; these consts fix how it lands
// on the graph. Sharing them between the writer (measure_task) and the future reader
// (the reviewer) is the same anti-drift discipline devtask.Fact* applies to the
// task.spec seam: one declaration, two consumers, no silent divergence.
//
// Single-task at M0 (reshape R9 / beta.147 D1): the per-task index is OUT of the
// predicate. beta.147's canonical-predicate contract forbids the old
// measurement.result.<i>.<field> shape (>3 segments, a digit-start segment), so a
// task's measurement is now ONE owned package on the run — measurement.result.<field>
// — re-stamped latest-wins each attempt. The M1 multi-task future keys the task into
// the ENTITY ID (a per-task entity), an explicit seam, never back into the predicate.

// ResultPrefix is the owned namespace measurement facts live under on the run
// entity: measurement.result.<field>. It anchors to the measurement.result.* vocab
// family (writer measurement-harness, G5) and is the read prefix for the whole
// single-task measurement package.
const ResultPrefix = "measurement.result."

// Fact-key suffixes for the run's measurement, under measurement.result.<suffix>.
// Passed is the harness-DERIVED headline (G3 — computed here from the real exit
// status, never a caller-supplied outcome); Ran/ExitCode/TimedOut are the raw
// evidence a reader re-derives the pass from (ignoring a possibly-stale Passed), and
// Command records exactly what ran (G7). ExitCode/TimedOut kebab per the canonical
// predicate grammar (no underscore).
const (
	FactCommand  = "command"
	FactRan      = "ran"
	FactExitCode = "exit-code"
	FactTimedOut = "timed-out"
	FactPassed   = "passed"
	// FactCommit binds the measurement to the SNAPSHOT it ran against — the run's
	// attempt.commit.sha at measure time. It is NOT part of the pass derivation (CanApprove
	// ignores it, and ResultsFromFacts collects but does not read it — an extra sub-key is
	// harmless); it lets the floors route treat a green measurement as stale (fail-closed)
	// when a later attempt was re-applied but never re-measured (a different attempt.commit.sha).
	FactCommit = "commit"
)
