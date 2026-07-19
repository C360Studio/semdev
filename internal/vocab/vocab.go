// Package vocab is semdev's fact vocabulary — the checked-in source of truth the
// G5 (single-writer) and G9 (minimal-vocabulary) conformance pins compare
// against, and that docs project (G10). semspec accreted 224 predicates with
// "sole writer" as a false comment; semdev starts from zero and every predicate
// carries, in code, its single writer and the OpenSpec change that introduced it.
//
// Adding a predicate here is the deliberate act G9 requires: a new fact must be
// named by the change that needs it. Product code and rules reference these
// entries; the census tests in test/conformance enforce the invariants.
//
// beta.147: every name is CANONICAL (three lower-kebab segments domain.category.property,
// no digit-start, no underscore). beta.150: the census is now FULLY CONCRETE — the last
// deferred namespace, the brownfield living-spec tree, was flattened from openspec.spec.*
// to the single concrete blob predicate openspec.spec.document (specfacts, mirroring the
// D3 change blob), so Register() declares every entry and the ".*"-namespace machinery is
// retired. Single-task at M0 (beta.147 D1): the per-task/-change index left the predicate
// (it keys the entity ID at M1), so the old task.spec.<i>/measurement.result.<i>/
// openspec.change.<slug> shapes collapsed to flat names.
package vocab

import (
	_ "github.com/c360studio/semstreams/agentic/agentrun" // init() declares agent.run.phase (+ transition-audit predicates) semdev reads
	"github.com/c360studio/semstreams/vocabulary"
	agenticvocab "github.com/c360studio/semstreams/vocabulary/agentic"
)

// Predicate is one fact predicate: its name, the single writer allowed to stamp
// it (G5), the capability that owns it, and the OpenSpec change slug that
// introduced it (G9).
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

// budgets is the change slug that adopts the per-task ATTEMPT budget (semstreams #568
// length_gte): the routing rules read the projected task.spec.budget via a route-mirror
// scalar instead of the hard-coded constant 3.
const budgets = "adopt-per-task-routing-budgets"

// Predicates is the complete semdev-OWNED fact vocabulary (canonical beta.147 names).
// Order is presentational only; the pins treat it as a set keyed by Name. Framework
// predicates semdev merely READS (agent.loop.*, coordinator.decision.*, agent.run.phase,
// and the four frameworkAdjacent below) are NOT here — this census is semdev's product
// vocabulary, one product-owned writer per fact.
var Predicates = []Predicate{
	{"intake.actor.login", "admission-check", "forge-io", m0},
	{"intake.actor.admitted", "admission-check", "forge-io", m0},
	{"run.issue.ref", "issue-intake-adapter", "forge-io", m0},
	{"run.change.approved", "approval-adapter", "forge-io", m0},
	{"human.opt.signal", "comment-adapter", "forge-io", m0},
	{"run.awaiting.human", "park-rule", "run-lifecycle", m0},
	{"run.dev.kickoff", "dev-rewake-rule", "dev-from-task", m0},
	{"run.projection.kickoff", "dev-projection-rule", "dev-from-task", m0},
	// delivery.pr.ref is the delivered PR reference (was pr.ref). The DELIVERY-STATION
	// component stamps it (off the delivery route's publish) via the same openpr.Deliver
	// core the tool used — Source open-pr, so this single-writer declaration holds. An M0
	// local-delivery stub; the M2 forge-io adapter replaces it with a live PR URL.
	{"delivery.pr.ref", "open-pr", "forge-io", m0},

	// openspec-io. beta.147 D3: the change is ONE scalar document + a slug pointer + a
	// content revision, not a deep triple tree — an OpenSpec change is one artifact.
	// openspec.change.authored is the fire-once marker create_change stamps on the
	// authoring LOOP (value = slug) so the validate rule can fire slug-independently.
	{"openspec.change.document", "create-change-author-tool", "openspec-io", m0},
	{"openspec.change.slug", "create-change-author-tool", "openspec-io", m0},
	{"openspec.change.revision", "create-change-author-tool", "openspec-io", m0},
	{"openspec.change.authored", "create-change-author-tool", "openspec-io", m0},
	{"openspec.change.validated", "openspec-validate-harness", "openspec-io", m0},
	{"openspec.change.archived", "openspec-archive-harness", "openspec-io", m0},
	// openspec.spec.document is the brownfield living-spec blob: the brownfield-spec-projector
	// serializes each capability spec to one JSON scalar here (specfacts.SpecDocument), on that
	// capability's spec entity — the canonical (3-part) twin of the openspec.change.document
	// change blob. Concrete, so Register() declares it like any other name.
	{"openspec.spec.document", "brownfield-spec-projector", "openspec-io", m0},

	// task.spec.<field> — the immutable projected task package (flat, single-task M0).
	{"task.spec.goal", "task-projector", "dev-from-task", m0},
	{"task.spec.budget", "task-projector", "dev-from-task", m0},
	{"task.spec.assumptions", "task-projector", "dev-from-task", m0},
	{"task.spec.non-goals", "task-projector", "dev-from-task", m0},
	{"task.spec.target-files", "task-projector", "dev-from-task", m0},
	{"task.spec.test-command", "task-projector", "dev-from-task", m0},

	// task.attempt.instance is the PER-TASK attempt counter: the dispatching rules append
	// one triple per attempt AT SPAWN TIME (R3), object = the spawned developer loop
	// instance (distinct per attempt). length_* over this ONE predicate is the per-attempt
	// count. One logical writer dev-dispatch-rule realized by the three sanctioned spawners
	// (04/06c/07b); rules carry no Source, so the single entry is not drifted.
	{"task.attempt.instance", "dev-dispatch-rule", "dev-from-task", m0},
	// attempt.commit.sha is the immutable-snapshot pointer: the SHA apply_patch commits
	// after each successful apply (latest-wins, writer patch-committer). The cold verify
	// clones this commit and read_diff diffs base..this — so what is verified/reviewed is a
	// committed tree, never the mutable warm checkout (G4/G7, the reshape's P1 fix).
	{"attempt.commit.sha", "patch-committer", "sandbox", reshape},

	// floor.finding.* (flattened, beta.147 D4): the aggregate the route reads, one
	// human-legible detail scalar, and the attempt binding. Writer floor-tools.
	{"floor.finding.rejected", "floor-tools", "dev-from-task", m0},
	{"floor.finding.detail", "floor-tools", "dev-from-task", m0},
	{"floor.finding.attempt", "floor-tools", "dev-from-task", m0},

	// measurement.result.<field> (flat): passed is the harness-DERIVED headline (G3);
	// ran/exit-code/timed-out are the raw evidence a reader re-derives from; command records
	// what ran (G7); commit binds the measurement to the snapshot. Writer measurement-harness.
	{"measurement.result.passed", "measurement-harness", "harness-measurement", m0},
	{"measurement.result.command", "measurement-harness", "harness-measurement", m0},
	{"measurement.result.commit", "measurement-harness", "harness-measurement", m0},
	{"measurement.result.ran", "measurement-harness", "harness-measurement", m0},
	{"measurement.result.exit-code", "measurement-harness", "harness-measurement", m0},
	{"measurement.result.timed-out", "measurement-harness", "harness-measurement", m0},

	{"review.verdict.value", "reviewer-quinn", "harness-measurement", m0},
	// review.findings.value is the reviewer's PROSE findings (the required changes Quinn
	// raised) — model JUDGMENT (G3 restricts measurement outcomes, not review judgment),
	// stamped by submit_review alongside the floored verdict. Same writer as the verdict.
	{"review.findings.value", "reviewer-quinn", "harness-measurement", reshape},
	{"verify.cleanroom.result", "verify-harness", "clean-room-verify", m0},
	{"evidence.ledger.run", "evidence-ledger", "evidence-ledger", m0},

	// sandbox (the provision-and-prove-cold station). The provisioning rule owns the
	// fired-once kickoff marker; the provision_sandbox harness owns the readiness/
	// attestation package it DERIVES from the cold proof (G3) — split so no predicate has
	// two writers (G5). blocked routes an unprovable sandbox to the human (SB5).
	{"sandbox.provision.marker", "sandbox-provision-rule", "sandbox", sandbox},
	{"sandbox.provision.ready", "sandbox-provisioner", "sandbox", sandbox},
	{"sandbox.provision.blocked", "sandbox-provisioner", "sandbox", sandbox},
	{"sandbox.attestation.image", "sandbox-provisioner", "sandbox", sandbox},
	{"sandbox.attestation.tier", "sandbox-provisioner", "sandbox", sandbox},

	// the bounded dev loop + rule-native routing (the reshape, groups 4+5). Routing is
	// RULES over harness-stamped facts (design R1); measure_task runs INSIDE Amelia's
	// multi-turn loop. dev.developer.dispatched is the fired-once marker dispatch-developer
	// (04) stamps on the coordinator loop so a graph replay cannot re-spawn Amelia.
	{"dev.developer.dispatched", "dev-dispatch-rule", "dev-from-task", sandbox},
	// dev.floors.dispatched is the fired-once marker the floors trigger (05) stamps on the
	// DEVELOPER loop before spawning check_floors. A fresh developer loop per retry re-arms it.
	{"dev.floors.dispatched", "dev-floors-rule", "dev-from-task", reshape},

	// THE ROUTING INPUTS mirrored onto the fresh-per-attempt firing loop (route-mirror,
	// design R1). RAW harness copies of measurement/floor verdicts + the attempt-count
	// mirror, never a derived route DECISION (the advance/retry/escalate decision lives in
	// the route RULES, G2). ONE logical writer route-mirror, realized by check_floors
	// (attempt.passed/rejected/instance on its loop) and submit_review (review.verdict/
	// attempt.instance on its loop). route.attempt.passed/rejected are single-valued; the
	// MULTI-valued counter is route.attempt.instance, counted by length_* (exact predicate).
	{"route.attempt.passed", "route-mirror", "dev-from-task", reshape},
	{"route.attempt.rejected", "route-mirror", "dev-from-task", reshape},
	{"route.review.verdict", "route-mirror", "dev-from-task", reshape},
	{"route.attempt.instance", "route-mirror", "dev-from-task", reshape},
	// route.task.budget is the per-task ATTEMPT budget (the projected task.spec.budget,
	// clamped [1,5]) mirrored onto the firing loop so the retry/escalate routes read it via
	// $entity.triple.route.task.budget.value instead of the old constant 3 (adopt-per-task-
	// routing-budgets, semstreams #568 length_gte). A RAW copy of the authored contract value
	// (not a derived decision — like route.attempt.*), stamped by the ONE logical writer
	// route-mirror at its TWO sanctioned sites (check_floors→L_n for 06c/06d AND submit_review→
	// the review loop for 07b/07c — the same one-writer/two-sites precedent as
	// route.attempt.instance). Canonical 3-seg so `.value` arity-disambiguates.
	{"route.task.budget", "route-mirror", "dev-from-task", budgets},

	// THE ROUTE MARKERS (rule-owned, no Source drift — the multi-realized single-writer
	// pattern). route.attempt.unclean is the OR-collapse intermediate (route.attempt.passed
	// eq false OR route.attempt.rejected eq true), stamped single-valued "true" by the pure
	// logic:or floors-route rule and read by the pure-AND retry/escalate rules.
	{"route.attempt.unclean", "dev-route-rule", "dev-from-task", reshape},
	// route.attempt.routed is the shared fired-once self-extinguish marker the floors-route
	// and review-route rules stamp on their firing loop BEFORE any (non-idempotent) publish;
	// a fresh loop per attempt re-arms it, so retries route for free.
	{"route.attempt.routed", "dev-route-rule", "dev-from-task", reshape},
	// delivery.route.routed is the fired-once self-extinguish marker the two delivery routes
	// (coherent→publish / blocked→park) stamp on the RUN — delivery is terminal (never
	// repeats), so a run-scoped guard is correct and load-bearing (real delivery is not
	// idempotent at M2).
	{"delivery.route.routed", "dev-route-rule", "dev-from-task", reshape},
}

// frameworkAdjacent are canonical predicates semdev READS or WRITES that the framework
// leaves UNDECLARED, so beta.147's rule-load predicate-declaration check needs them
// registered. They are deliberately NOT in the G5/G9 census (that is semdev's product
// vocabulary — one product-owned writer per fact): agent.loop.run and agent.run.entity-id
// are framework-written (publish_agent mint/inherit); rule.task.spawned is engine-written;
// agent.run.handoff is the ported semteams handoff marker (an agent-run pack rule writes it).
// Most framework predicates semdev reads ARE declared by agenticvocab.Register()
// (agent.loop.role/outcome/…, coordinator.decision.*) or the agentrun init (agent.run.phase),
// so they are not repeated here — but agent.loop.run and agent.run.entity-id are the two the
// framework's registration OMITS, which is exactly why they appear above.
var frameworkAdjacent = []string{
	"agent.loop.run",      // publish_agent inherit anchor (the dev-from-task/01 rule writes it on the run)
	"agent.run.entity-id", // publish_agent run_scope=new stamps it; semdev reads it
	"rule.task.spawned",   // the rule engine stamps it on a published task; agent-run/01 reads it
	"agent.run.handoff",   // the semteams handoff marker (agent-run pack) — a semdev rule writes+reads it
}

// Register declares every semdev predicate (the census + the framework-adjacent set) with
// the framework vocabulary registry, and pulls in the framework's own agentic declarations
// (agent.loop.role/outcome, coordinator.decision.next-action, …), so beta.147's
// UNCONDITIONAL rule-load predicate-declaration check passes. Boot calls this before the
// rule processor loads its packs. vocabulary.Register PANICS on a non-canonical name, so
// this doubles as a boot-time canonical-shape guard over the whole census.
func Register() {
	agenticvocab.Register()
	for _, p := range Predicates {
		// The census is fully concrete (beta.150 — the last ".*" namespace, openspec.spec.*,
		// was flattened to the concrete openspec.spec.document blob), so every entry declares.
		vocabulary.Register(p.Name)
	}
	for _, name := range frameworkAdjacent {
		vocabulary.Register(name)
	}
}

// Names returns every predicate name in declaration order.
func Names() []string {
	names := make([]string, len(Predicates))
	for i, p := range Predicates {
		names[i] = p.Name
	}
	return names
}

// WriterOf returns the single writer declared for a predicate name. ok is false when the
// name is not in the census. The census is fully CONCRETE (beta.150 retired the last ".*"
// namespace, openspec.spec.*, for the concrete openspec.spec.document blob), so this is an
// exact match — no namespace fallback.
func WriterOf(name string) (writer string, ok bool) {
	for _, p := range Predicates {
		if p.Name == name {
			return p.Writer, true
		}
	}
	return "", false
}
