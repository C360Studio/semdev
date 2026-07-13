// Package vocab is semdev's fact vocabulary — the checked-in source of truth the
// G5 (single-writer) and G9 (minimal-vocabulary) conformance pins compare
// against, and that docs project (G10). semspec accreted 224 predicates with
// "sole writer" as a false comment; semdev starts from zero and every predicate
// carries, in code, its single writer and the OpenSpec change that introduced it.
//
// Adding a predicate here is the deliberate act G9 requires: a new fact must be
// named by the change that needs it. Product code and rules reference these
// entries; the census tests in test/conformance enforce the invariants.
package vocab

import "strings"

// Predicate is one fact predicate: its name, the single writer allowed to stamp
// it (G5), the capability that owns it, and the OpenSpec change slug that
// introduced it (G9). A trailing ".*" in Name denotes a writer-owned namespace
// (every predicate under that prefix has the same single writer).
type Predicate struct {
	Name         string
	Writer       string
	Capability   string
	IntroducedBy string
}

// m0 is the change slug that introduces semdev's founding vocabulary.
const m0 = "m0-walking-skeleton-spine"

// sandbox is the change slug that introduces the containerized sandbox / provision-
// and-prove-cold vocabulary (the sandbox capability).
const sandbox = "containerized-sandbox-dev-loop"

// Predicates is the complete fact vocabulary. See design.md D12 — this table is
// that decision made executable. Order is presentational only; the pins treat it
// as a set keyed by Name.
var Predicates = []Predicate{
	{"intake.actor", "admission-check", "forge-io", m0},
	{"intake.admitted", "admission-check", "forge-io", m0},
	{"run.issue_ref", "issue-intake-adapter", "forge-io", m0},
	{"run.change_approved", "approval-adapter", "forge-io", m0},
	{"human.signal", "comment-adapter", "forge-io", m0},
	{"run.awaiting_human", "park-rule", "run-lifecycle", m0},
	{"run.dev_kickoff", "dev-rewake-rule", "dev-from-task", m0},
	{"run.projection_kickoff", "dev-projection-rule", "dev-from-task", m0},
	{"pr.ref", "pr-delivery-adapter", "forge-io", m0},
	{"openspec.change.*", "create-change-author-tool", "openspec-io", m0},
	{"openspec.spec.*", "brownfield-spec-projector", "openspec-io", m0},
	{"openspec.validated", "openspec-validate-harness", "openspec-io", m0},
	{"openspec.archived", "openspec-archive-harness", "openspec-io", m0},
	{"task.spec.*", "task-projector", "dev-from-task", m0},
	// task.attempt.* is the PER-TASK attempt counter: the measure-trigger rule
	// (dev-from-task/05) appends one triple per attempt on the developer-loop terminal,
	// object = the developer loop instance (distinct per attempt). A rule add_triple
	// goes through graph-ingest's UNCONDITIONAL append (no property-replace, no
	// set-dedup — verified against Component.AddTriple), and the mutation is at-least-
	// once (a lost NATS ack retries and double-appends). So the group-7D budget gate
	// MUST count DISTINCT OBJECTS of task.attempt.<i> (the loop instance) against
	// task.spec.<i>.budget — never raw triple cardinality: distinct-object counting is
	// robust to the retry double-append AND counts every real attempt (the dev.measured
	// marker already bounds it to one append per developer loop in the normal path). A
	// NAMESPACE (task.attempt.<i>) so the count is per-task, not global — realized by
	// the sandbox change's dev loop (group 7B); the NAME was reserved in m0's founding
	// dev-loop vocabulary.
	{"task.attempt.*", "dev-measure-rule", "dev-from-task", m0},
	{"floor.finding.*", "floor-tools", "dev-from-task", m0},
	{"measurement.result.*", "measurement-harness", "harness-measurement", m0},
	{"review.verdict.*", "reviewer-quinn", "harness-measurement", m0},
	{"verify.result", "verify-harness", "clean-room-verify", m0},
	{"evidence.run", "evidence-ledger", "evidence-ledger", m0},

	// sandbox (the provision-and-prove-cold station, group 5). The provisioning
	// rule owns the fired-once kickoff marker; the provision_sandbox harness owns
	// the readiness/attestation package it DERIVES from the cold proof (G3) — split
	// so no predicate has two writers (G5). readiness/attestation are proven, not
	// claimed; blocked routes an unprovable sandbox to the human (SB5).
	{"sandbox.provisioned", "sandbox-provision-rule", "sandbox", sandbox},
	{"sandbox.ready", "sandbox-provisioner", "sandbox", sandbox},
	{"sandbox.blocked", "sandbox-provisioner", "sandbox", sandbox},
	{"sandbox.attestation.*", "sandbox-provisioner", "sandbox", sandbox},

	// dev loop in the sandbox (group 7). dev.dispatched is the fired-once marker the
	// dispatch-developer rule stamps on the coordinator loop whose dev_from_task
	// decision it consumed, so a graph replay cannot re-spawn Amelia (self-extinguishing,
	// loop-scoped like the run.*_kickoff markers are run-scoped).
	{"dev.dispatched", "dev-dispatch-rule", "dev-from-task", sandbox},
	// dev.measured is the fired-once marker the measure-trigger rule (dev-from-task/05)
	// stamps on the DEVELOPER loop whose successful terminal it consumed, so a graph
	// replay cannot re-spawn a duplicate measure loop (self-extinguishing, loop-scoped
	// like dev.dispatched). A fresh developer loop per retry carries no marker, so each
	// attempt re-measures for free.
	{"dev.measured", "dev-measure-rule", "dev-from-task", sandbox},
	// dev.measure_done is the CHAINING marker measure_task stamps on ITS OWN measure
	// loop (value = task index) once it has recorded a measurement — the slug-independent
	// "this coordinator loop just measured" signal the floors-trigger (dev-from-task/06)
	// fires on to spawn check_floors and inherit the run. Tool-owned (writer
	// measurement-harness, measure_task's Source — like create_change's openspec.change.
	// authored marker); distinguishes the measure loop from the other coordinator loops
	// that also reach outcome=success.
	{"dev.measure_done", "measurement-harness", "dev-from-task", sandbox},
	// dev.floors_dispatched is the fired-once marker the floors-trigger (dev-from-task/06)
	// stamps on the measure loop before spawning check_floors, so a graph replay cannot
	// re-spawn a duplicate floors loop (self-extinguishing, loop-scoped like dev.dispatched).
	{"dev.floors_dispatched", "dev-floors-rule", "dev-from-task", sandbox},
	// dev.floors_done is the CHAINING marker check_floors stamps on ITS OWN floors loop
	// (value = task index) once it has recorded the findings — the slug-independent "this
	// coordinator loop just ran the floors" signal the gate-trigger (dev-from-task/07)
	// fires on to spawn check_gate and inherit the run. Tool-owned (writer floor-tools,
	// check_floors' Source — the measure→floors precedent, dev.measure_done); distinguishes
	// the floors loop from the other coordinator loops that also reach outcome=success.
	{"dev.floors_done", "floor-tools", "dev-from-task", sandbox},

	// the dev-loop GATE (group 7D). check_gate reads the recorded measurement/floor
	// verdicts and the attempt count vs budget and derives advance/retry/escalate — the
	// harness owns the route (G3). dev.gate.* is the per-task decision EVIDENCE on the
	// run (the human who gets parked, and the verify/PR steps, read it); dev.gate_decision
	// is the loop marker the router rules act on. Both tool-owned (writer gate-tools).
	{"dev.gate.*", "gate-tools", "dev-from-task", sandbox},
	{"dev.gate_decision", "gate-tools", "dev-from-task", sandbox},
	// dev.gate_dispatched is the fired-once marker the gate-trigger (dev-from-task/07)
	// stamps on the floors loop before spawning check_gate, so a graph replay cannot
	// re-spawn a duplicate gate loop (self-extinguishing, loop-scoped like dev.dispatched).
	{"dev.gate_dispatched", "dev-gate-rule", "dev-from-task", sandbox},
	// dev.routed is the fired-once marker the three router rules (dev-from-task/08a/b/c)
	// stamp on the gate loop before acting, so a graph replay cannot re-fire a router
	// (critical for the retry router, whose publish_agent is not idempotent — a re-fire
	// would spawn a duplicate developer, breaking the one-in-flight serialization
	// invariant). One logical writer (dev-route-rule) realized by three mutually-exclusive
	// rule files (like run.awaiting_human's two park realizations); rule add_triple carries
	// no Source, so the single vocab entry is not drifted.
	{"dev.routed", "dev-route-rule", "dev-from-task", sandbox},
	// dev.task_cleared.<i> is the ADVANCE router's output: the per-task signal that its
	// dev loop converged (measured green, no floor rejected, gate advanced). The clean-room
	// verify station (group 8) chains on it. Rule-owned (writer dev-route-rule).
	{"dev.task_cleared.*", "dev-route-rule", "dev-from-task", sandbox},
}

// Names returns every predicate name in declaration order.
func Names() []string {
	names := make([]string, len(Predicates))
	for i, p := range Predicates {
		names[i] = p.Name
	}
	return names
}

// WriterOf returns the single writer declared for a predicate name. It matches
// exactly first, then falls back to a declared namespace: a concrete name like
// "openspec.change.proposal" resolves to the writer of the "openspec.change.*"
// entry. ok is false when the name is in neither.
func WriterOf(name string) (writer string, ok bool) {
	for _, p := range Predicates {
		if p.Name == name {
			return p.Writer, true
		}
	}
	for _, p := range Predicates {
		if prefix, isNS := strings.CutSuffix(p.Name, ".*"); isNS && strings.HasPrefix(name, prefix+".") {
			return p.Writer, true
		}
	}
	return "", false
}
