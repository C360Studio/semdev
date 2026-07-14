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

// reshape is the change slug that reshapes the M0 execution rail (immutable git-backed
// snapshot, rule-native routing, restart-safe provisioning).
const reshape = "simplify-m0-execution-rail"

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
	// pr.ref is the delivered PR reference. Its writer was reconciled at 8D from the
	// placeholder pr-delivery-adapter (which never existed) to open-pr. Since group 6 (R6)
	// the DELIVERY-STATION component stamps it (off the delivery route's publish) via the
	// SAME openpr.Deliver core the tool used — same Source open-pr, so this single-writer
	// declaration is unchanged (an M0 local-delivery stub; the M2 forge-io adapter replaces
	// it with a live PR URL).
	{"pr.ref", "open-pr", "forge-io", m0},
	{"openspec.change.*", "create-change-author-tool", "openspec-io", m0},
	{"openspec.spec.*", "brownfield-spec-projector", "openspec-io", m0},
	{"openspec.validated", "openspec-validate-harness", "openspec-io", m0},
	{"openspec.archived", "openspec-archive-harness", "openspec-io", m0},
	{"task.spec.*", "task-projector", "dev-from-task", m0},
	// task.attempt.* is the PER-TASK attempt counter: the dispatching rules append one
	// triple per attempt AT SPAWN TIME (R3) — dispatch-developer (04) on the first
	// attempt, the floors-retry route (06c) and the review-retry route (07b) on each
	// subsequent attempt — object = the spawned developer loop instance (distinct per
	// attempt). Appended at spawn (not terminal) so a crashed/wedged loop still consumed an
	// attempt (honest accounting, restart-safe). A rule add_triple goes through graph-ingest's
	// UNCONDITIONAL append (Component.AddTriple appends + bumps the entity version with NO
	// dedup — verified against beta.146), so length_* over this family is a per-attempt count.
	// Its robustness rests on the DISTINCT loop-instance object per real attempt (not storage
	// dedup) plus the rule engine's per-entity revision tracker breaking self-feedback re-fire;
	// an at-least-once redelivered mutation can double-append, which the budget route handles
	// FAIL-CLOSED (length_gt 2 escalate catches an over-count → early park, never a false
	// retry-past-budget nor a hollow advance). A NAMESPACE (task.attempt.<i>) so the count is
	// per-task. One logical writer (dev-dispatch-rule) realized by the three sanctioned
	// developer spawners (04/06c/07b); rules carry no Source, so the single entry is not drifted.
	{"task.attempt.*", "dev-dispatch-rule", "dev-from-task", m0},
	// attempt.commit is the immutable-snapshot pointer: the SHA apply_patch commits after
	// each successful apply (latest-wins, one writer patch-committer). The cold verify
	// clones this commit and read_diff diffs base..this — so what is verified and reviewed
	// is a committed tree, never the mutable warm checkout (G4/G7, the reshape's P1 fix).
	{"attempt.commit", "patch-committer", "sandbox", reshape},
	{"floor.finding.*", "floor-tools", "dev-from-task", m0},
	{"measurement.result.*", "measurement-harness", "harness-measurement", m0},
	{"review.verdict.*", "reviewer-quinn", "harness-measurement", m0},
	// review.findings.<i> is the reviewer's per-task PROSE findings (the required changes
	// Quinn raised) — model JUDGMENT (G3 restricts measurement outcomes, not review
	// judgment), stamped by submit_review on the run alongside the floored verdict. A
	// changes_requested re-entry (D16, the review-retry route) tells the fresh Amelia to
	// re-read these off the run and address them. Same single writer as review.verdict.*.
	{"review.findings.*", "reviewer-quinn", "harness-measurement", reshape},
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

	// the bounded dev loop + rule-native routing (the reshape, groups 4+5). The
	// route-token layer (check_gate/check_coherence, dev.gate_decision,
	// dev.coherence_decided, and the ten relay markers) is DELETED — routing is now RULES
	// over harness-stamped facts (design R1). measure_task runs INSIDE Amelia's multi-turn
	// loop, so the separate measure loop + its dev.measured/dev.measure_done markers are gone.
	//
	// dev.dispatched is the fired-once marker dispatch-developer (04) stamps on the
	// coordinator loop whose dev_from_task decision it consumed, so a graph replay cannot
	// re-spawn Amelia (self-extinguishing, loop-scoped).
	{"dev.dispatched", "dev-dispatch-rule", "dev-from-task", sandbox},
	// dev.floors_dispatched is the fired-once marker the floors trigger (05) stamps on the
	// DEVELOPER loop before spawning check_floors — the trigger now fires on the developer
	// loop's terminal (measure moved in-loop), not on a separate measure loop. Self-
	// extinguishing, loop-scoped; a fresh developer loop per retry re-arms floors for free.
	{"dev.floors_dispatched", "dev-floors-rule", "dev-from-task", reshape},

	// THE ROUTING INPUTS mirrored onto the fresh-per-attempt firing loop (route-mirror,
	// design R1). A rule condition reads only the FIRING entity's triples, so the harness
	// copies the run-level routing inputs onto the loop the route rules fire on. These are
	// RAW facts (harness copies of measurement/floor verdicts + the attempt-count mirror),
	// never a derived route DECISION (the advance/retry/escalate decision lives entirely in
	// the route RULES, G2 — that is the whole point of deleting check_gate). ONE logical
	// writer route-mirror, realized by check_floors (route.passed/rejected/attempt on its
	// loop) and submit_review (route.verdict/attempt on its loop).
	//
	// route.passed = check_floors' copy of measurement.result.<i>.passed onto its own loop,
	// fail-closed "false" when the measurement is absent (the "model never measured" case).
	{"route.passed", "route-mirror", "dev-from-task", reshape},
	// route.rejected = check_floors' copy of its own floor.finding.<i>.rejected onto its loop.
	{"route.rejected", "route-mirror", "dev-from-task", reshape},
	// route.verdict = submit_review's copy of review.verdict.<i> onto its own review loop.
	{"route.verdict", "route-mirror", "dev-from-task", reshape},
	// route.attempt.<i> = the tool-written MIRROR of task.attempt.<i>'s distinct objects onto
	// the firing loop (check_floors' floors loop, submit_review's review loop), so the route
	// rules can count the budget via length_* (a rule can't read the run's task.attempt from a
	// loop). Written via ReplaceTriples → MergeTriples FULL-SET-REPLACE per predicate: the tool
	// stamps the COMPLETE current attempt set each run (not append), so it is replay-idempotent
	// (the same set replaces itself). Distinct from the RULE-written task.attempt.* on the run,
	// which is an unconditional APPEND (rule add_triple) — the two use different write paths.
	{"route.attempt.*", "route-mirror", "dev-from-task", reshape},

	// THE ROUTE MARKERS (rule-owned, no Source drift — the multi-realized single-writer
	// pattern, like run.awaiting_human). route.not_clean is the OR-collapse intermediate:
	// the pure logic:or floors-route rule (route.passed eq false OR route.rejected eq true)
	// stamps it single-valued "true", and the pure-AND retry/escalate rules read it (a rule
	// can't AND a disjunction with the budget count, so the disjunction is a separate rule).
	{"route.not_clean", "dev-route-rule", "dev-from-task", reshape},
	// route.routed is the shared fired-once self-extinguish marker the floors-route rules
	// (advance/retry/escalate) and the review-route rules (approved/retry/park/no-verdict)
	// stamp on their firing loop BEFORE any (non-idempotent) publish — a fresh loop per
	// attempt re-arms it, so retries route for free. Loop-scoped, mutually-exclusive routes.
	{"route.routed", "dev-route-rule", "dev-from-task", reshape},
	// delivery.routed is the fired-once self-extinguish marker the two delivery routes
	// (coherent→publish the delivery-station component / blocked→park) stamp on the RUN —
	// delivery is terminal (it never repeats), so a run-scoped guard is correct (no
	// per-attempt reset needed), and it is load-bearing for the coherent route since real
	// delivery is not idempotent at M2. (See the 08a KNOWN M0 GAP: a persistent station
	// fault leaves this set with pr.ref absent and no auto-park — reconciled by R8/group 8.)
	{"delivery.routed", "dev-route-rule", "dev-from-task", reshape},
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
