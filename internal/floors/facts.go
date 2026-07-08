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
