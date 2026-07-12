package floors

// This file names the GRAPH PROJECTION of a Finding — the owned fact package the
// check_floors tool (the floor-tools wrapper) stamps on the run entity, and that the
// loop gate reads to route on. The pure floors compute the Finding; these consts fix
// how it lands on the graph, shared between the writer and any future reader so they
// cannot drift.
//
// Keying is per (task, floor): floor.finding.<taskIndex>.<floorName>.<field>. The
// graph merges replace-per-(subject,predicate), so a single exact predicate would
// hold only ONE finding; keying the task index AND the floor name into the predicate
// gives each floor its own sub-package, re-stamped each attempt (latest-attempt-wins)
// so a task's current floor verdicts never clobber another task's. Mirrors the
// measurement.result.* / task.spec.* namespaces rather than an append log — the
// current attempt's verdict is what the gate reads; attempt history is task.attempt.

// FindingPrefix is the owned namespace floor findings live under on the run entity:
// floor.finding.<taskIndex>.<floorName>.<field>. It anchors to the floor.finding.*
// vocab namespace (writer floor-tools, G5).
const FindingPrefix = "floor.finding."

// Fact-key suffixes for one floor's finding. Passed is the harness-DERIVED verdict
// (the floor is deterministic Go, not a model claim — G3); Detail is the reason,
// kept so a park-toward-human on a rejecting floor is legible (G7).
const (
	FactPassed = "passed"
	FactDetail = "detail"
)

// FactAttempt is the per-task sub-key holding the attempt identity (AttemptID) the
// finding set evaluated: floor.finding.<taskIndex>.attempt. It binds the whole
// finding set to a specific attempt so a gate can require the findings belong to the
// run's CURRENT attempt before reading them as a pass — without it, a stale earlier
// pass is indistinguishable from a current one. It is a sibling of the per-floor
// sub-keys (no floor is named "attempt"), a scalar at the task level.
const FactAttempt = "attempt"

// FactRejected is the per-task AGGREGATE verdict sub-key: floor.finding.<taskIndex>.
// rejected, "true" iff ANY floor rejected (floors.AnyRejected). It is the single bool
// the dev-loop gate reads to route (advance vs retry) rather than re-deriving it from
// the six per-floor sub-keys — the gate stays a simple literal read. A sibling of the
// per-floor sub-keys and of attempt (no floor is named "rejected"), scalar at the task
// level. The per-floor findings stay stamped alongside it for the human-legible reason.
const FactRejected = "rejected"
