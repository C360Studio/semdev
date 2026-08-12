package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/vocab"
	"github.com/c360studio/semstreams/agentic/agentrun"
)

func runLifecycleRules(t *testing.T) map[string]ruleFile {
	t.Helper()
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	byID := make(map[string]ruleFile)
	for _, r := range rules {
		byID[r.ID] = r
	}
	return byID
}

// condition looks up the first condition on a field, if any.
func (r ruleFile) condition(field string) (ruleCondition, bool) {
	for _, c := range r.Conditions {
		if c.Field == field {
			return c, true
		}
	}
	return ruleCondition{}, false
}

func (r ruleFile) hasTriple(predicate string) bool {
	for _, a := range r.OnEnter {
		if a.Type == "add_triple" && a.Predicate == predicate {
			return true
		}
	}
	return false
}

// stampObjectContains reports whether the rule has an add_triple for the given predicate whose
// Object contains substr — used to assert a substituted token (e.g. a .value reason) is carried
// in a stamped fact's object.
func (r ruleFile) stampObjectContains(predicate, substr string) bool {
	for _, a := range r.OnEnter {
		if a.Type == "add_triple" && a.Predicate == predicate && strings.Contains(a.Object, substr) {
			return true
		}
	}
	return false
}

// markerBeforePublish reports whether the fired-once marker add_triple precedes the
// FIRST publish_agent in on_enter — the SB7 restart-safety ordering: the marker must
// be stamped BEFORE the (non-idempotent) spawn so a publish failure leaves the run
// stuck-toward-human rather than duplicated. False if either action is absent.
func (r ruleFile) markerBeforePublish(marker string) bool {
	markerIdx, publishIdx := -1, -1
	for i, a := range r.OnEnter {
		if markerIdx == -1 && a.Type == "add_triple" && a.Predicate == marker {
			markerIdx = i
		}
		if publishIdx == -1 && a.Type == "publish_agent" {
			publishIdx = i
		}
	}
	return markerIdx != -1 && publishIdx != -1 && markerIdx < publishIdx
}

func (r ruleFile) firesTransition() bool {
	for _, a := range r.OnEnter {
		if a.Type == "lifecycle_transition" {
			return true
		}
	}
	return false
}

// publishesTo reports whether the rule has a plain `publish` action (the R6
// deterministic-station dispatch mechanism — distinct from publish_agent, which
// spawns a model loop) to the given subject.
func (r ruleFile) publishesTo(subject string) bool {
	for _, a := range r.OnEnter {
		if a.Type == "publish" && a.Subject == subject {
			return true
		}
	}
	return false
}

// publishesToWithProps reports whether the rule has a `publish` to subject that
// carries EVERY named property — the R6 station property-name contract (a station
// reads its refs, e.g. run_entity_id/slug, from these properties, so a typo would
// fail closed and only surface in the e2e without this offline pin, G6).
func (r ruleFile) publishesToWithProps(subject string, props ...string) bool {
	for _, a := range r.OnEnter {
		if a.Type != "publish" || a.Subject != subject {
			continue
		}
		for _, p := range props {
			if _, ok := a.Properties[p]; !ok {
				return false
			}
		}
		return true
	}
	return false
}

// markerBeforeStationPublish reports whether the fired-once marker add_triple
// precedes the FIRST `publish` action — the same SB7 restart-safety ordering as
// markerBeforePublish, but for the R6 publish→component station path (a publish
// failure must leave the run stuck-toward-human, not duplicable).
func (r ruleFile) markerBeforeStationPublish(marker string) bool {
	markerIdx, publishIdx := -1, -1
	for i, a := range r.OnEnter {
		if markerIdx == -1 && a.Type == "add_triple" && a.Predicate == marker {
			markerIdx = i
		}
		if publishIdx == -1 && a.Type == "publish" {
			publishIdx = i
		}
	}
	return markerIdx != -1 && publishIdx != -1 && markerIdx < publishIdx
}

// clearsPredicate reports whether the rule removes a predicate (the resume-from-
// park rule clears run.awaiting_human, exempting it from the park-exclusion pin).
func (r ruleFile) clearsPredicate(predicate string) bool {
	for _, a := range r.OnEnter {
		if a.Type == "remove_triple" && a.Predicate == predicate {
			return true
		}
	}
	return false
}

// hasAbsenceGuard reports whether the rule requires a predicate to be absent
// (length_eq 0) — the "fact not present" guard.
func (r ruleFile) hasAbsenceGuard(field string) bool {
	for _, c := range r.Conditions {
		if c.Field == field && c.Operator == "length_eq" {
			if f, ok := c.Value.(float64); ok && f == 0 {
				return true
			}
		}
	}
	return false
}

// spawnsNewRun reports whether the rule mints a run (a publish_agent with
// run_scope "new") — the action that can put a fresh run anchor on the firing
// coordinator, and (transitively) an inherited anchor on the spawned child.
func (r ruleFile) spawnsNewRun() bool {
	for _, a := range r.OnEnter {
		if a.Type == "publish_agent" && a.RunScope == "new" {
			return true
		}
	}
	return false
}

// handlesDualAnchor reports whether the rule is a dual-anchor handoff (semteams
// agent-run/01b): it targets a coordinator carrying MORE than one run anchor,
// detected by a length_gt guard on agent.run.entity_id. The single-anchor
// handoff (01) uses length_eq 1 and silently stops matching once a coordinator
// accumulates two anchors, so 01b is what keeps the self-minted run's handoff
// firing in that case.
func (r ruleFile) handlesDualAnchor() bool {
	for _, c := range r.Conditions {
		if c.Field == "agent.run.entity-id" && c.Operator == "length_gt" {
			return true
		}
	}
	return false
}

// dualAnchorGuardViolation returns a non-empty message when the rule set can put
// a coordinator into a two-anchor state (more than one run-minting rule) without
// a dual-anchor handoff to keep that coordinator's handoff firing. Empty when
// safe. Pure over the parsed rules so the pin can be exercised with synthetic
// input.
func dualAnchorGuardViolation(rules []ruleFile) string {
	minters, dualAnchor := 0, false
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if r.spawnsNewRun() {
			minters++
		}
		if r.handlesDualAnchor() {
			dualAnchor = true
		}
	}
	if minters > 1 && !dualAnchor {
		return fmt.Sprintf("%d rules mint a run (run_scope=new) but no dual-anchor handoff rule is present "+
			"(a length_gt guard on agent.run.entity_id) — a coordinator that inherits one anchor and mints a second "+
			"makes the single-anchor handoff (length_eq 1) silently stop matching, wedging the self-minted run in "+
			"dispatched. Port semteams agent-run/01b (or land the framework replace-on-mint fix) in the same change "+
			"that adds the second run_scope=new rule", minters)
	}
	return ""
}

// G2 / 3.4 — every lifecycle_transition rule targets a VALID agent-run edge. The
// source phase comes from the rule's agent.run.phase guard, the target from the
// action; the pair must be a real transition in the framework's agent-run table.
// This ties the rules to the actual workflow so an illegal edge (which would log
// ErrInvalidTransition at runtime) fails the build instead.
func TestLifecycleTransitionsTargetValidAgentRunEdges(t *testing.T) {
	transitions := agentrun.WorkflowDeclaration().Transitions
	saw := 0
	for _, r := range runLifecycleRules(t) {
		for _, a := range r.OnEnter {
			if a.Type != "lifecycle_transition" {
				continue
			}
			saw++
			if a.Workflow != agentrun.WorkflowName {
				t.Errorf("rule %s: lifecycle_transition workflow = %q, want %q", r.ID, a.Workflow, agentrun.WorkflowName)
			}
			guard, ok := r.condition("agent.run.phase")
			if !ok {
				t.Errorf("rule %s: lifecycle_transition has no agent.run.phase guard — the illegal edge would be attempted at runtime", r.ID)
				continue
			}
			source, _ := guard.Value.(string)
			if !slices.Contains(transitions[source], a.Phase) {
				t.Errorf("rule %s: transition %q → %q is not a valid agent-run edge (valid: %v)", r.ID, source, a.Phase, transitions[source])
			}
		}
	}
	if saw == 0 {
		t.Fatal("no lifecycle_transition rules found; the edge pin would pass vacuously")
	}
}

// 3.7 / 3.5 — the change-approval gate ordering. The offer-approval rule requires
// the change to be OpenSpec-validated (openspec.validated present) and not yet
// approved; the resume rule requires the approval. Together: validate → approve →
// resume, and the dev loop cannot start before approval.
func TestChangeApprovalGateOrdering(t *testing.T) {
	rules := runLifecycleRules(t)

	offer, ok := rules["run_offer_change_approval"]
	if !ok {
		t.Fatal("missing run_offer_change_approval rule")
	}
	if c, ok := offer.condition("openspec.change.validated"); !ok || c.Operator != "ne" {
		t.Error("offer-approval must require openspec.validated present (ne \"\") — validate-before-approval ordering (3.7)")
	}
	if c, ok := offer.condition("run.change.decision"); !ok || c.Operator != "length_eq" {
		t.Error("offer-approval must require the gate UNDECIDED (run.change.decision length_eq 0)")
	}

	resume, ok := rules["run_resume_after_change_approval"]
	if !ok {
		t.Fatal("missing run_resume_after_change_approval rule")
	}
	if c, ok := resume.condition("run.change.decision"); !ok || c.Operator != "eq" || c.Value != "approve" {
		t.Error("resume must require run.change.decision == \"approve\" — the gate holds until approval (3.5)")
	}
}

// D15 forward-contract #0 — the change-approval gate's content-freshness is
// DEFERRED, and this tripwire keeps the deferral honest. Ideally the gate would
// require openspec.validated to equal the run's current content revision so a
// rework re-author cannot reach the human gate on a stale validation. That needs a
// slug-independent field-to-field compare, but the only engine form
// ($entity.triple.<pred> in a condition value) floods a "likely silent-pass bug"
// WARN on every entity lacking the predicate (semstreams #519). At M0 the hazard is
// UNREACHABLE (no live path re-authors while the run is executing), so the gate is
// presence-only and the REACHABLE guard lives in project_tasks (Go, warn-free:
// TestProjectRejectsReauthoredUnrevalidatedChange). This pin asserts (a) the gate
// keeps its presence guard, and (b) it does NOT carry the warn-flooding
// $entity.triple value form — so re-introducing it (before #519 is fixed) fails
// here, forcing the author to also update this contract. When a rework path lands,
// the gate MUST adopt the freshness compare via a warn-free form (the #519 fix or
// the .triples form) and this pin is updated in the same change.
func TestChangeApprovalGateFreshnessForwardContract(t *testing.T) {
	offer, ok := runLifecycleRules(t)["run_offer_change_approval"]
	if !ok {
		t.Fatal("missing run_offer_change_approval rule")
	}
	if c, ok := offer.condition("openspec.change.validated"); !ok || c.Operator != "ne" {
		t.Error("offer-approval must still require openspec.validated present (ne \"\") — the validate-before-approval floor")
	}
	for _, c := range offer.Conditions {
		if s, ok := c.Value.(string); ok && strings.Contains(s, "$entity.triple.") {
			t.Errorf("gate condition %q=%v uses the warn-flooding $entity.triple value form — deferred pending semstreams #519; freshness lives in project_tasks until a warn-free form + a rework path land (D15 #0)", c.Field, c.Value)
		}
	}
}

// RESTART-SAFETY — the dev re-wake must be SELF-EXTINGUISHING. It fires a
// publish_agent (which the framework does NOT dedup — every fire mints a fresh
// loop, actions.go), and its trigger (agent.run ∧ executing ∧ change_approved)
// does NOT self-clear after firing. On a normal restart the durable RULE_STATE
// bucket suppresses re-fire, but under asymmetric bucket loss (RULE_STATE wiped
// while ENTITY_STATES survives) an unguarded rule would re-spawn a DUPLICATE
// coordinator. The fix (semteams agent-run/02 pattern): stamp a fired-once marker
// (run.dev_kickoff) and guard on its absence, so the trigger flips false the
// moment it fires — independent of RULE_STATE. This pin fails if either half is
// dropped. (The sibling spawn rule coordinator/02 and the park rule are NOT yet
// self-extinguishing — tracked in design.md as a pre-production hardening
// carry-forward; when the dev-loop rail adds more publish_agent spawns they must
// follow this pattern. coordinator/03 is no longer in that set — it now `publish`es
// the validation-station component, R6, whose re-dispatch is idempotent, so its
// edge-trigger needs no self-extinguish marker.)
func TestDevRewakeIsSelfExtinguishing(t *testing.T) {
	rewake, ok := runLifecycleRules(t)["dev_from_task_rewake_coordinator"]
	if !ok {
		t.Fatal("missing dev_from_task_rewake_coordinator rule")
	}
	const marker = "run.dev.kickoff"
	if !rewake.hasAbsenceGuard(marker) {
		t.Errorf("dev re-wake must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate coordinator (publish_agent is not idempotent)", marker)
	}
	if !rewake.hasTriple(marker) {
		t.Errorf("dev re-wake must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	// The marker must be stamped by a publish_agent-bearing rule (the spawn is the
	// non-idempotent effect the marker protects) — sanity that we pinned the right rule.
	spawns := false
	for _, a := range rewake.OnEnter {
		if a.Type == "publish_agent" {
			spawns = true
		}
	}
	if !spawns {
		t.Error("dev re-wake pin is on the wrong rule — expected a publish_agent spawn rule")
	}
}

// forcesFunction reports whether the rule's on_enter has a publish_agent that
// forces a specific tool call (tool_choice mode=function, function_name=name).
func (r ruleFile) forcesFunction(name string) bool {
	for _, a := range r.OnEnter {
		if a.Type == "publish_agent" && a.ToolChoice.Mode == "function" && a.ToolChoice.FunctionName == name {
			return true
		}
	}
	return false
}

// The projection station (dev-from-task/03) is the approval-triggered spawn that
// freezes the change's tasks into task.spec. Like every publish_agent spawn rule
// it MUST be self-extinguishing (house restart-safety pattern): a fired-once
// run.projection_kickoff marker stamped in on_enter, guarded by length_eq 0 — else
// an asymmetric RULE_STATE loss re-spawns a duplicate projection loop (publish_agent
// is not idempotent). It must also actually force the project_tasks call.
func TestProjectionSpawnIsSelfExtinguishing(t *testing.T) {
	proj, ok := runLifecycleRules(t)["dev_from_task_project_tasks"]
	if !ok {
		t.Fatal("missing dev_from_task_project_tasks rule")
	}
	const marker = "run.projection.kickoff"
	if !proj.hasAbsenceGuard(marker) {
		t.Errorf("projection dispatch must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-dispatches a duplicate projection (the core-NATS publish is not deduped)", marker)
	}
	if !proj.hasTriple(marker) {
		t.Errorf("projection dispatch must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	// R6: projection is a publish-triggered component now — the rule PUBLISHES the
	// projection station (not a forced project_tasks turn), carrying the change slug as
	// a property (the station reads it), with the marker stamped BEFORE the publish (a
	// publish failure leaves the run stuck, not duplicable).
	if !proj.publishesToWithProps("component.projection-station.dispatch", "slug") || !proj.markerBeforeStationPublish(marker) {
		t.Error("projection dispatch must publish component.projection-station.dispatch (R6) with a slug property and the run.projection_kickoff marker stamped BEFORE the publish")
	}
	// It fires on approval and needs the run anchor (dev-from-task/01) so the run is
	// the dispatch entity_id — assert both so the trigger is grounded.
	if c, ok := proj.condition("run.change.decision"); !ok || c.Operator != "eq" || c.Value != "approve" {
		t.Error("projection dispatch must fire on run.change.decision == \"approve\" (the spec trigger: approval projects task.spec)")
	}
	if c, ok := proj.condition("agent.loop.run"); !ok || c.Operator != "ne" {
		t.Error("projection dispatch must require the agent.run anchor (ne \"\") so the run resolves as the dispatch entity_id")
	}
}

// Codex P1 (9dba14e..f1eed5c): the dev re-wake must NOT fire before task.spec is
// projected — the spec requires approval to project the immutable task surface
// BEFORE the dev loop converges on it, and the coordinator's re-wake prompt reads
// projected task.spec. dev-from-task/02 is therefore gated on projection
// completion (task.spec.0.test_command ne ""), so run.change_approved WITHOUT
// task.spec cannot produce a dev_from_task decision. This also serializes the two
// forced-tool turns so the journey's positional cursor is not raced. Red-first:
// drop the gate and this fails.
func TestDevRewakeGatedOnProjection(t *testing.T) {
	rewake, ok := runLifecycleRules(t)["dev_from_task_rewake_coordinator"]
	if !ok {
		t.Fatal("missing dev_from_task_rewake_coordinator rule")
	}
	c, ok := rewake.condition("task.spec.test-command")
	if !ok {
		t.Fatal("dev re-wake must gate on projected task.spec (task.spec.0.test_command) — else it can decide dev_from_task before the immutable task surface exists (Codex P1)")
	}
	if c.Operator != "ne" || c.Value != "" {
		t.Errorf("dev re-wake projection gate must be task.spec.0.test_command ne \"\" (a present, non-empty projected field), got operator=%q value=%v", c.Operator, c.Value)
	}
	// The gate is only honest if a projection station actually stamps task.spec on
	// approval — assert the producer exists and publishes the projection component.
	proj, ok := runLifecycleRules(t)["dev_from_task_project_tasks"]
	if !ok || !proj.publishesTo("component.projection-station.dispatch") {
		t.Error("the projection gate has no producer: dev_from_task_project_tasks must exist and publish component.projection-station.dispatch (R6), or task.spec.0.test_command never becomes present and the dev loop deadlocks")
	}
}

// The provision-and-prove-cold station (sandbox/01-provision) is the approval-triggered
// publish that stands up the sandbox and cold-proves it — a publish-triggered COMPONENT now
// (R6), not a forced provision_sandbox turn. Like every station-publish rule it MUST be
// self-extinguishing (house restart-safety pattern): a fired-once sandbox.provisioned marker
// stamped in on_enter BEFORE the publish, guarded by length_eq 0 — else an asymmetric
// RULE_STATE loss re-publishes a duplicate provision dispatch (the core-NATS publish is not
// deduped). It must publish the provision station, fire on approval, and require the run
// anchor (the run IS the dispatch entity_id).
func TestSandboxProvisionIsSelfExtinguishing(t *testing.T) {
	prov, ok := runLifecycleRules(t)["sandbox_provision"]
	if !ok {
		t.Fatal("missing sandbox_provision rule")
	}
	const marker = "sandbox.provision.marker"
	if !prov.hasAbsenceGuard(marker) {
		t.Errorf("provision spawn must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-publishes a duplicate provision dispatch (the core-NATS publish is not deduped)", marker)
	}
	if !prov.hasTriple(marker) {
		t.Errorf("provision spawn must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !prov.markerBeforeStationPublish(marker) {
		t.Errorf("provision spawn must stamp %s BEFORE its publish (SB7) — else a publish failure leaves the run duplicable rather than stuck-toward-human", marker)
	}
	// R6: publishes the provision station (fires on the RUN, so the run is the dispatch
	// entity_id — no property). It must NOT force the provision_sandbox tool.
	if !prov.publishesTo("component.provision-station.dispatch") {
		t.Error("provision spawn must publish the provision station (component.provision-station.dispatch, R6)")
	}
	if prov.forcesFunction("provision_sandbox") {
		t.Error("provision spawn must NOT force the provision_sandbox tool — provisioning is a publish-triggered component now (R6)")
	}
	if c, ok := prov.condition("run.change.decision"); !ok || c.Operator != "eq" || c.Value != "approve" {
		t.Error("provision spawn must fire on run.change.decision == \"approve\" (provision the approved run's sandbox)")
	}
	if c, ok := prov.condition("agent.loop.run"); !ok || c.Operator != "ne" {
		t.Error("provision spawn must require the agent.run anchor (ne \"\") — it identifies the run entity the dispatch fires on")
	}
}

// The readiness gate (SB5): the dev loop must NOT proceed onto development without a
// PROVEN sandbox. dev-from-task/02 is gated on sandbox.ready eq true, so an approved,
// projected run whose sandbox was not cold-proved (sandbox.ready never stamped)
// cannot re-wake into dev_from_task — it parks instead (sandbox/02-park-unprovable).
// Red-first: drop the gate and this fails. The gate is honest only if a producer
// actually stamps sandbox.ready, so assert the provision rule publishes the provision station.
func TestDevRewakeGatedOnSandboxReadiness(t *testing.T) {
	rewake, ok := runLifecycleRules(t)["dev_from_task_rewake_coordinator"]
	if !ok {
		t.Fatal("missing dev_from_task_rewake_coordinator rule")
	}
	c, ok := rewake.condition("sandbox.provision.ready")
	if !ok {
		t.Fatal("dev re-wake must gate on a proven sandbox (sandbox.ready) — else the dev loop can proceed over an absent/unproven sandbox (SB5, the semspec disease)")
	}
	if c.Operator != "eq" || c.Value != "true" {
		t.Errorf("dev re-wake readiness gate must be sandbox.ready eq \"true\", got operator=%q value=%v", c.Operator, c.Value)
	}
	prov, ok := runLifecycleRules(t)["sandbox_provision"]
	if !ok || !prov.publishesTo("component.provision-station.dispatch") {
		t.Error("the readiness gate has no producer: sandbox_provision must exist and publish the provision station (R6), or sandbox.ready never becomes present and the dev loop deadlocks")
	}
}

// budgetToken is the #519 scalar-value substitution the retry/escalate routes read for the
// PER-TASK attempt budget: the route-mirror stamps route.task.budget on the firing loop, and
// the routes compare route.attempt.instance against $entity.triple.route.task.budget.value
// (adopt-per-task-routing-budgets, #568). retry = length_lt B, escalate/park = length_gte B,
// both against this same token → {0..B-1} ∪ {B..} partitions every count with no gap/overlap.
const budgetToken = "$entity.triple.route.task.budget.value"

// declaresTools reports whether every publish_agent action in the rule declares a
// non-empty tools allowlist (the config-lint: no allowlist-less model-publishing spawn).
func (r ruleFile) declaresTools() bool {
	sawSpawn := false
	for _, a := range r.OnEnter {
		if a.Type != "publish_agent" {
			continue
		}
		sawSpawn = true
		if len(a.Tools) == 0 {
			return false
		}
	}
	return sawSpawn
}

// usesToolChoiceAuto reports whether a publish_agent uses tool_choice mode=auto.
func (r ruleFile) usesToolChoiceAuto() bool {
	for _, a := range r.OnEnter {
		if a.Type == "publish_agent" && a.ToolChoice.Mode == "auto" {
			return true
		}
	}
	return false
}

// The dispatch station (dev-from-task/04, the reshape group 4): on a coordinator's
// dev_from_task decision, spawn Amelia's BOUNDED MULTI-TURN dev loop — a scoped tool
// allowlist + tool_choice=auto (she reads, patches, measures IN-LOOP, iterates), NOT a
// single forced author turn. It appends task.attempt.0 at SPAWN (R3) and is LOOP-scoped
// self-extinguishing (dev.dispatched). Red-first: drop the allowlist / the auto choice /
// the attempt append / the marker and this fails.
func TestDispatchDeveloperIsMultiTurnAndSelfExtinguishing(t *testing.T) {
	disp, ok := runLifecycleRules(t)["dev_from_task_dispatch_developer"]
	if !ok {
		t.Fatal("missing dev_from_task_dispatch_developer rule")
	}
	const marker = "dev.developer.dispatched"
	if !disp.hasAbsenceGuard(marker) || !disp.hasTriple(marker) || !disp.markerBeforePublish(marker) {
		t.Errorf("dispatch-developer must be self-extinguishing (%s guard + add_triple before the publish)", marker)
	}
	if !disp.usesToolChoiceAuto() {
		t.Error("dispatch-developer must spawn a BOUNDED MULTI-TURN loop (tool_choice mode=auto), not a single forced author turn (R2)")
	}
	if !disp.declaresTools() {
		t.Error("dispatch-developer must declare an explicit tools allowlist (the config-lint; Amelia's scoped [query_entity, read_workspace, apply_patch, measure_task, ask_human])")
	}
	if !disp.hasTriple("task.attempt.instance") {
		t.Error("dispatch-developer must append task.attempt.0 AT SPAWN (R3) — the attempt counter the route counts against the budget")
	}
	if c, ok := disp.condition("coordinator.decision.next-action"); !ok || c.Value != "dev_from_task" {
		t.Error("dispatch-developer must fire on the coordinator's dev_from_task decision")
	}
	if c, ok := disp.condition("agent.run.entity-id"); !ok || c.Operator != "ne" {
		t.Error("dispatch-developer must require the run anchor (agent.run.entity_id ne \"\") so run_scope=inherit binds Amelia to the run")
	}
}

// The floors station (dev-from-task/05, reshape group 5+6, R6 make-or-break): measure moved
// INTO Amelia's loop, so the floors trigger fires on the DEVELOPER loop L_n's TERMINAL
// (role=developer AND outcome ne "" — for both success and failed) and PUBLISHES the floors
// STATION component (not a forced check_floors turn). The component stamps floor.finding on
// the run and the route.* mirror on L_n (= the dispatch entity_id), so the route rules 06a-d
// fire on L_n. run_entity_id + task_index travel as publish properties. LOOP-scoped
// self-extinguishing (dev.floors_dispatched on L_n, before the publish). Red-first: drop any
// and this fails.
func TestFloorsTriggerFiresOnDeveloperTerminal(t *testing.T) {
	fl, ok := runLifecycleRules(t)["dev_from_task_floors_trigger"]
	if !ok {
		t.Fatal("missing dev_from_task_floors_trigger rule")
	}
	const marker = "dev.floors.dispatched"
	if !fl.hasAbsenceGuard(marker) || !fl.hasTriple(marker) || !fl.markerBeforeStationPublish(marker) {
		t.Errorf("floors trigger must be self-extinguishing (%s guard + add_triple before the publish)", marker)
	}
	// R6: publishes the floors station carrying run_entity_id (L_n is the firing entity, so
	// the run travels as a property) + task_index. It must NOT force the check_floors tool.
	if !fl.publishesToWithProps("component.floors-station.dispatch", "run_entity_id", "task_index") {
		t.Error("floors trigger must publish component.floors-station.dispatch (R6) carrying run_entity_id + task_index properties")
	}
	if fl.forcesFunction("check_floors") {
		t.Error("floors trigger must NOT force the check_floors tool — floors is a publish-triggered component now (R6)")
	}
	// Fires on the DEVELOPER loop terminal (measure moved in-loop, so there is no measure
	// loop and no dev.measure_done chain). role=developer + outcome-present is the terminal.
	if c, ok := fl.condition("agent.loop.role"); !ok || c.Value != "developer" {
		t.Error("floors trigger must fire on the DEVELOPER loop (agent.loop.role == developer) — measure moved in-loop")
	}
	if c, ok := fl.condition("agent.loop.outcome"); !ok || c.Operator != "ne" {
		t.Error("floors trigger must fire on the terminal (agent.loop.outcome ne \"\") — for BOTH success and failed; routing reads the harness facts, not the loop outcome")
	}
	if _, ok := fl.condition("dev.measure_done"); ok {
		t.Error("floors trigger must NOT chain off dev.measure_done — measure moved into Amelia's loop; there is no measure loop")
	}
}

// The FLOORS ROUTE (dev-from-task/06a-d, reshape group 5+6): the rule-native replacement
// for check_gate. It fires on the DEVELOPER loop L_n reading the route.* mirror the floors
// STATION (R6) stamps there (pre-reshape this was the check_floors coordinator loop; the
// rules are UNCHANGED — they key on route.*, which the component now mirrors onto L_n).
//   - 06a advance: route.passed=true AND route.rejected=false → spawn Quinn (reviewer).
//   - 06b not_clean: logic:OR (route.passed=false OR route.rejected=true) → route.not_clean.
//   - 06c retry: route.not_clean=true AND route.attempt.instance length_lt B → re-dispatch Amelia.
//   - 06d escalate: route.not_clean=true AND route.attempt.instance length_gte B → park.
//
// TOTALITY: advance covers (true,false); not_clean is its exact OR-complement; retry/escalate
// partition the count against the PER-TASK budget B (length_lt B / length_gte B, no gap/overlap
// for any B). Red-first: break any and this fails.
func TestFloorsRouteTotalityAndSelfExtinguish(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "route.attempt.routed"

	adv, ok := rules["dev_from_task_route_advance"]
	if !ok {
		t.Fatal("missing dev_from_task_route_advance rule")
	}
	if c, ok := adv.condition("route.attempt.passed"); !ok || c.Operator != "eq" || c.Value != "true" || c.Required {
		t.Errorf("advance must require route.passed eq \"true\" (required:false — a scalar eq with required:true ERRORS on entities lacking the field), got %+v", c)
	}
	if c, ok := adv.condition("route.attempt.rejected"); !ok || c.Operator != "eq" || c.Value != "false" || c.Required {
		t.Errorf("advance must require route.rejected eq \"false\" (required:false), got %+v", c)
	}
	if !adv.hasAbsenceGuard(marker) || !adv.hasTriple(marker) || !adv.markerBeforePublish(marker) {
		t.Errorf("advance must be self-extinguishing (%s guard + add_triple before the reviewer publish)", marker)
	}
	spawnsReviewer := false
	for _, a := range adv.OnEnter {
		if a.Type == "publish_agent" && a.Role == "reviewer" {
			spawnsReviewer = true
		}
	}
	if !spawnsReviewer || !adv.usesToolChoiceAuto() || !adv.declaresTools() {
		t.Error("advance must spawn Quinn's bounded multi-turn reviewer loop (role=reviewer, tool_choice=auto, an explicit allowlist)")
	}

	// The not-clean OR-collapse is TWO GUARDED pure-AND rules (not one logic:or rule): a
	// guardless OR rule would re-fire and re-append unbounded (the graph-ingest ADD path
	// bumps the version with no nothing-changed skip). Each branch carries the route.not_clean
	// length_eq 0 self-extinguish guard (which an AND rule can, an OR rule cannot).
	notCleanRules := map[string]string{
		"dev_from_task_route_not_clean_red":   "route.attempt.passed",   // measured red
		"dev_from_task_route_not_clean_floor": "route.attempt.rejected", // a floor rejected
	}
	for id, signalField := range notCleanRules {
		nc, ok := rules[id]
		if !ok {
			t.Fatalf("missing %s rule (the not-clean OR-collapse is two guarded AND rules)", id)
		}
		if nc.Logic == "or" {
			t.Errorf("%s must be a GUARDED pure-AND rule, NOT logic:or — a guardless OR rule would re-fire and re-append route.not_clean unbounded", id)
		}
		if !nc.hasTriple("route.attempt.unclean") {
			t.Errorf("%s must stamp route.not_clean (the intermediate the retry/escalate rules AND with the budget)", id)
		}
		if !nc.hasAbsenceGuard("route.attempt.unclean") {
			t.Errorf("%s must self-extinguish via route.not_clean length_eq 0 — else it re-fires every rescan (unbounded append)", id)
		}
		if _, ok := nc.condition(signalField); !ok {
			t.Errorf("%s must fire on its signal %q", id, signalField)
		}
	}

	ret, ok := rules["dev_from_task_route_retry"]
	if !ok {
		t.Fatal("missing dev_from_task_route_retry rule")
	}
	if c, ok := ret.condition("route.attempt.unclean"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("retry must require route.not_clean eq \"true\", got %+v", c)
	}
	if c, ok := ret.condition("route.attempt.instance"); !ok || c.Operator != "length_lt" || c.Value != budgetToken {
		t.Errorf("retry must require route.attempt.instance length_lt %s (the PER-TASK budget mirror, #519/#568 — not the old constant 3), got %+v", budgetToken, c)
	}
	// The transient exclusion (adopt-reason-aware-escalate): a TRANSIENT loop failure is diverted
	// to the transient-retry route (06f) and must NOT fire the convergence retry. The exclusion
	// MUST be eq "false" against check_floors' ALWAYS-STAMPED atomic mirror flag — NOT an absence
	// guard against a rule-stamped collapse: a sibling rule's stamp lands in its own KV revision,
	// so an absence exclusion reads a pass where unclean is visible but the stamp is not and
	// double-dispatches (06c + 06f — the race TestBridgeProofTransientGraceRetries caught live).
	if c, ok := ret.condition("route.attempt.transient"); !ok || c.Operator != "eq" || c.Value != "false" {
		t.Errorf("retry must exclude a transient failure with route.attempt.transient eq \"false\" (the atomic mirror flag — an absence guard against a rule-stamped collapse races and double-dispatches), got %+v", c)
	}
	if !ret.hasAbsenceGuard(marker) || !ret.markerBeforePublish(marker) || !ret.hasTriple("task.attempt.instance") {
		t.Error("retry must be self-extinguishing (route.routed before the developer publish) and append task.attempt.0 at spawn (R3)")
	}

	esc, ok := rules["dev_from_task_route_escalate"]
	if !ok {
		t.Fatal("missing dev_from_task_route_escalate rule")
	}
	if c, ok := esc.condition("route.attempt.unclean"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("escalate must require route.not_clean eq \"true\", got %+v", c)
	}
	if c, ok := esc.condition("route.attempt.instance"); !ok || c.Operator != "length_gte" || c.Value != budgetToken {
		t.Errorf("escalate must require route.attempt.instance length_gte %s (fail-closed: count ≥ B catches an over-count, and partitions the count with retry's length_lt B — #519/#568, replaces the old length_gt 2), got %+v", budgetToken, c)
	}
	if c, ok := esc.condition("route.attempt.transient"); !ok || c.Operator != "eq" || c.Value != "false" {
		t.Errorf("escalate must exclude a transient failure with route.attempt.transient eq \"false\" (the atomic mirror flag; a transient loop failure parks via 06g, not the convergence escalate), got %+v", c)
	}
	if !esc.hasTriple("run.awaiting.human") || esc.firesTransition() {
		t.Error("escalate must park (run.awaiting_human) with NO lifecycle transition (G2)")
	}
	// Reason-aware park (adopt-reason-aware-escalate, #569): the escalate's run.awaiting.human
	// object carries the loop's classified terminal reason via the .value substitution.
	if !esc.stampObjectContains("run.awaiting.human", "agent.loop.terminal-reason.value") {
		t.Error("escalate must carry the classified terminal reason in its run.awaiting.human message ($entity.triple.agent.loop.terminal-reason.value) — the reason-aware park (#569)")
	}
	// The partition now uses the PER-TASK budget substitution, not literals: retry length_lt B,
	// escalate length_gte B against the SAME route.task.budget.value token → {0..B-1} ∪ {B..}
	// covers every count with no gap and no overlap, for any B in [1,5]. Assert both read the
	// SAME token (a divergent token would reintroduce a gap the old literal lt-N/gt-(N-1) pin
	// caught by arithmetic).
	retC, _ := ret.condition("route.attempt.instance")
	escC, _ := esc.condition("route.attempt.instance")
	if retC.Value != budgetToken || escC.Value != budgetToken {
		t.Errorf("retry (%v) and escalate (%v) must both compare against the SAME budget token %s — a divergent boundary reintroduces a gap/overlap", retC.Value, escC.Value, budgetToken)
	}
}

// The TRANSIENT ROUTE (dev-from-task/06f-06g, adopt-reason-aware-escalate #529/#569): a developer
// loop that FAILED for a transient reason (agent.loop.terminal-reason model_error/handler_error)
// gets bounded grace OUTSIDE the convergence budget.
//   - route.attempt.transient is check_floors' ATOMIC-MIRROR classification of the loop's
//     terminal reason — ALWAYS stamped "true"/"false" in the SAME ReplaceTriples as
//     route.attempt.passed. NO RULE may stamp it: the engine writes each rule action as its own
//     KV revision and evaluates per debounce-flush, so a rule-stamped collapse races the
//     convergence routes' exclusion (the double-dispatch TestBridgeProofTransientGraceRetries
//     caught live on docker). Route mutual exclusion must be a condition PARTITION over the one
//     atomic snapshot: transient eq "true" (06f/06g) vs eq "false" (06c/06d).
//   - 06f transient-retry: transient=true AND passed=false AND route.transient.instance
//     length_lt CAP → re-dispatch Amelia appending task.transient.instance (NOT
//     task.attempt.instance — budget untouched).
//   - 06g transient-park: transient=true AND passed=false AND route.transient.instance
//     length_gte CAP → park (reason-aware). length_lt CAP / length_gte CAP partition the
//     transient count with no gap. Red-first.
func TestTransientRouteTotalityAndSelfExtinguish(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "route.attempt.routed"

	// NO rule stamps route.attempt.transient — it is the check_floors mirror's fact (G5 writer
	// route-mirror). A rule stamping it re-creates the racy collapse: its stamp lands in its own
	// KV revision, and any exclusion reading it double-dispatches against the stamping pass.
	for id, r := range rules {
		if r.hasTriple("route.attempt.transient") {
			t.Errorf("rule %q stamps route.attempt.transient — the transient flag is check_floors' ATOMIC mirror fact (G5: route-mirror); a rule-stamped sibling lands in a separate KV revision and races every exclusion that reads it (the pinned double-dispatch)", id)
		}
	}

	// 06f transient-retry: bounded by the transient cap, re-dispatches a developer WITHOUT
	// consuming the convergence budget (appends task.transient.instance, NOT task.attempt.instance).
	ret, ok := rules["dev_from_task_route_transient_retry"]
	if !ok {
		t.Fatal("missing dev_from_task_route_transient_retry rule (06f)")
	}
	if c, ok := ret.condition("route.attempt.transient"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("transient-retry must fire on route.attempt.transient eq true (the atomic mirror flag), got %+v", c)
	}
	// The shared trigger is UNCLEAN, not passed=false: the mirror stamps passed/rejected/
	// transient independently, so passed=true ∧ rejected=true ∧ transient=true is reachable
	// (measured green, floor rejected, final model call died transiently) — a passed=false
	// gate leaves that cell UNROUTED (06a needs rejected=false; 06c/06d are transient-
	// excluded) = a permanent silent stall. The 8-cell census below proves totality.
	if c, ok := ret.condition("route.attempt.unclean"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("transient-retry must gate on route.attempt.unclean eq true (a passed=false gate strands the passed∧rejected∧transient cell; a measured-green rejected=false loop still advances via 06a because unclean is never stamped), got %+v", c)
	}
	if !ret.hasAbsenceGuard(marker) {
		t.Error("transient-retry must self-extinguish via route.attempt.routed length_eq 0")
	}
	retC, ok := ret.condition("route.transient.instance")
	if !ok || retC.Operator != "length_lt" {
		t.Errorf("transient-retry must require route.transient.instance length_lt <cap>, got %+v", retC)
	}
	if !ret.markerBeforePublish(marker) || !ret.hasTriple("task.transient.instance") {
		t.Error("transient-retry must self-extinguish before the developer publish and append task.transient.instance at spawn")
	}
	if ret.hasTriple("task.attempt.instance") {
		t.Error("transient-retry must NOT append task.attempt.instance — a transient re-dispatch does not consume the convergence budget")
	}
	spawnsDev := false
	for _, a := range ret.OnEnter {
		if a.Type == "publish_agent" && a.Role == "developer" {
			spawnsDev = true
		}
	}
	if !spawnsDev {
		t.Error("transient-retry must spawn a fresh role=developer loop")
	}

	// 06g transient-park: cap reached → park, reason-aware, no transition.
	park, ok := rules["dev_from_task_route_transient_park"]
	if !ok {
		t.Fatal("missing dev_from_task_route_transient_park rule (06g)")
	}
	if c, ok := park.condition("route.attempt.transient"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("transient-park must fire on route.attempt.transient eq true (the atomic mirror flag), got %+v", c)
	}
	if c, ok := park.condition("route.attempt.unclean"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("transient-park must gate on route.attempt.unclean eq true (the shared not-clean trigger — see the retry's totality rationale), got %+v", c)
	}
	parkC, ok := park.condition("route.transient.instance")
	if !ok || parkC.Operator != "length_gte" {
		t.Errorf("transient-park must fire on route.transient.instance length_gte <cap> (exhausted), got %+v", parkC)
	}
	if !park.hasTriple("run.awaiting.human") || park.firesTransition() {
		t.Error("transient-park must park (run.awaiting_human) with NO lifecycle transition (G2)")
	}
	if !park.stampObjectContains("run.awaiting.human", "agent.loop.terminal-reason.value") {
		t.Error("transient-park must carry the transient terminal reason in its run.awaiting.human message (#569)")
	}
	if !park.hasAbsenceGuard(marker) {
		t.Error("transient-park must self-extinguish via route.attempt.routed length_eq 0")
	}
	// The transient partition: retry length_lt CAP, park length_gte CAP against the SAME literal cap
	// → {0..CAP-1} ∪ {CAP..} covers every transient count with no gap and no overlap. The type
	// assertions must SUCCEED: if the caps drifted to strings (or a substitution token), both
	// would coerce to 0 and the equality would pass vacuously while the boundary fails open.
	rc, rok := retC.Value.(float64)
	pc, pok := parkC.Value.(float64)
	if !rok || !pok {
		t.Fatalf("transient caps must be numeric LITERALS (retry %T=%v, park %T=%v) — a string or substitution token coerces to the engine's fail-open empty case", retC.Value, retC.Value, parkC.Value, parkC.Value)
	}
	if rc != pc {
		t.Errorf("transient retry length_lt %v and park length_gte %v must use the SAME cap — a divergent boundary reintroduces a gap/overlap", retC.Value, parkC.Value)
	}
}

// THE FLOORS-ROUTE CELL-TOTALITY CENSUS (adopt-reason-aware-escalate — the offline red-first
// pin for the UNROUTED-CELL defect class). The mirror stamps passed/rejected/transient
// INDEPENDENTLY, so all 8 boolean cells are reachable — including passed=true ∧ rejected=true ∧
// transient=true (measured green, a structural floor rejected the artifact, then the loop's
// final model call died transiently), the cell adversarial review caught UNROUTED when the
// transient routes gated on passed=false: no route fires, no park, and the rail has no backstop
// (B7) — a permanent silent stall. This census derives route.attempt.unclean exactly as the
// 06b/06e OR-collapse stamps it, then statically evaluates every terminal route's conditions
// with the engine's semantics (eq on an absent field → false; length_* over seeded counts;
// required does not change matching, only error loudness) across each count regime, asserting
// EXACTLY ONE route owns every cell — zero is a stall, two is a double dispatch.
func TestFloorsRouteCellTotalityCensus(t *testing.T) {
	rules := runLifecycleRules(t)
	routeIDs := []string{
		"dev_from_task_route_advance",         // 06a
		"dev_from_task_route_retry",           // 06c
		"dev_from_task_route_escalate",        // 06d
		"dev_from_task_route_transient_retry", // 06f
		"dev_from_task_route_transient_park",  // 06g
	}
	const budget = 3
	// evalCond mirrors the engine: scalar eq compares the seeded string (absent → false);
	// length_lt/length_gte/length_eq count the seeded multiset (absent → 0); the budget
	// substitution token resolves to the seeded route.task.budget.
	evalCond := func(c ruleCondition, scalars map[string]string, counts map[string]int) bool {
		switch c.Operator {
		case "eq":
			v, present := scalars[c.Field]
			want, _ := c.Value.(string)
			return present && v == want
		case "length_eq", "length_lt", "length_gte":
			n := counts[c.Field]
			var boundary int
			switch v := c.Value.(type) {
			case float64:
				boundary = int(v)
			case string:
				if v != "$entity.triple.route.task.budget.value" {
					t.Fatalf("census: unexpected substitution token %q in %s %s", v, c.Field, c.Operator)
				}
				boundary = budget
			default:
				t.Fatalf("census: unexpected condition value %T for %s", c.Value, c.Field)
			}
			switch c.Operator {
			case "length_eq":
				return n == boundary
			case "length_lt":
				return n < boundary
			default:
				return n >= boundary
			}
		default:
			t.Fatalf("census: route condition uses unmodeled operator %q on %s — extend the census", c.Operator, c.Field)
			return false
		}
	}
	for _, passed := range []string{"true", "false"} {
		for _, rejected := range []string{"true", "false"} {
			for _, transient := range []string{"true", "false"} {
				for _, attempts := range []int{1, budget} { // within-budget vs exhausted
					for _, transients := range []int{0, 2} { // grace remains vs cap reached
						scalars := map[string]string{
							"route.attempt.passed":    passed,
							"route.attempt.rejected":  rejected,
							"route.attempt.transient": transient,
						}
						// The 06b/06e OR-collapse: unclean stamped iff not-clean; absent otherwise.
						if passed == "false" || rejected == "true" {
							scalars["route.attempt.unclean"] = "true"
						}
						counts := map[string]int{
							"route.attempt.instance":   attempts,
							"route.transient.instance": transients,
							"route.attempt.routed":     0, // fresh loop, not yet routed
						}
						var fired []string
						for _, id := range routeIDs {
							r, ok := rules[id]
							if !ok {
								t.Fatalf("census: missing route rule %s", id)
							}
							if r.Logic == "or" {
								t.Fatalf("census: %s is logic:or — the census models pure-AND routes", id)
							}
							all := true
							for _, c := range r.Conditions {
								if !evalCond(c, scalars, counts) {
									all = false
									break
								}
							}
							if all {
								fired = append(fired, id)
							}
						}
						if len(fired) != 1 {
							t.Errorf("cell passed=%s rejected=%s transient=%s attempts=%d transients=%d: %d routes fire (%v) — every cell must be owned by EXACTLY ONE route (zero = permanent silent stall, no backstop by design; two = double dispatch breaking one-in-flight)",
								passed, rejected, transient, attempts, transients, len(fired), fired)
						}
					}
				}
			}
		}
	}
}

// The REVIEW ROUTE (dev-from-task/07a-d, the reshape group 5): fires on Quinn's review loop
// reading the route.verdict mirror.
//   - 07a approved → publish the verify station (R6 component, no model turn).
//   - 07b changes_requested + budget → re-dispatch Amelia (D16 re-entry).
//   - 07c changes_requested + exhausted → park.
//   - 07d no-verdict (terminated, route.verdict absent) → park (fail-closed totality catch).
func TestReviewRouteTotalityAndSelfExtinguish(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "route.attempt.routed"

	app, ok := rules["dev_from_task_review_approved"]
	if !ok {
		t.Fatal("missing dev_from_task_review_approved rule")
	}
	if c, ok := app.condition("route.review.verdict"); !ok || c.Operator != "eq" || c.Value != "approved" || c.Required {
		t.Errorf("review-approved must require route.verdict eq \"approved\" (required:false), got %+v", c)
	}
	// R6: publishes the verify STATION carrying run_entity_id (Q_n is the firing entity, so
	// the run travels as a property) and is self-extinguishing (route.routed before the
	// publish). It must NOT force the verify_artifact tool.
	if !app.publishesToWithProps("component.verify-station.dispatch", "run_entity_id") || !app.hasAbsenceGuard(marker) || !app.markerBeforeStationPublish(marker) {
		t.Error("review-approved must publish the verify station (component.verify-station.dispatch, R6) carrying run_entity_id and be self-extinguishing (route.routed before the publish)")
	}
	if app.forcesFunction("verify_artifact") {
		t.Error("review-approved must NOT force the verify_artifact tool — verify is a publish-triggered component now (R6)")
	}

	ret, ok := rules["dev_from_task_review_retry"]
	if !ok {
		t.Fatal("missing dev_from_task_review_retry rule (D16 re-entry)")
	}
	if c, ok := ret.condition("route.review.verdict"); !ok || c.Value != "changes_requested" {
		t.Errorf("review-retry must fire on route.verdict changes_requested, got %+v", c)
	}
	if c, ok := ret.condition("route.attempt.instance"); !ok || c.Operator != "length_lt" || c.Value != budgetToken {
		t.Errorf("review-retry must require route.attempt.instance length_lt %s (the SHARED per-task attempt budget, R4/#519/#568 — not the old constant 3), got %+v", budgetToken, c)
	}
	if !ret.hasTriple("task.attempt.instance") || !ret.markerBeforePublish(marker) {
		t.Error("review-retry must append task.attempt.0 at spawn (shared budget) and self-extinguish before the developer publish")
	}
	// D16: it must re-enter DEVELOPMENT (spawn a developer), not verify.
	spawnsDev := false
	for _, a := range ret.OnEnter {
		if a.Type == "publish_agent" && a.Role == "developer" {
			spawnsDev = true
		}
	}
	if !spawnsDev {
		t.Error("review-retry must re-enter DEVELOPMENT (spawn a role=developer loop) — D16: rejection re-enters dev, not verify→park")
	}

	park, ok := rules["dev_from_task_review_park"]
	if !ok {
		t.Fatal("missing dev_from_task_review_park rule")
	}
	if c, ok := park.condition("route.attempt.instance"); !ok || c.Operator != "length_gte" || c.Value != budgetToken {
		t.Errorf("review-park must fire on route.attempt.instance length_gte %s (exhausted, count ≥ B; #519/#568, replaces the old length_gt 2), got %+v", budgetToken, c)
	}
	if !park.hasTriple("run.awaiting.human") || park.firesTransition() {
		t.Error("review-park must park with no transition (G2)")
	}

	nv, ok := rules["dev_from_task_review_no_verdict"]
	if !ok {
		t.Fatal("missing dev_from_task_review_no_verdict rule (fail-closed totality catch)")
	}
	if c, ok := nv.condition("agent.loop.role"); !ok || c.Value != "reviewer" {
		t.Error("review-no-verdict must fire on the reviewer loop (route.verdict is absent, so role is the discriminator)")
	}
	if c, ok := nv.condition("route.review.verdict"); !ok || c.Operator != "length_eq" {
		t.Error("review-no-verdict must require route.verdict ABSENT (length_eq 0) — the terminated-without-a-verdict case")
	}
	if !nv.hasTriple("run.awaiting.human") {
		t.Error("review-no-verdict must park (no verdict is not approval, fail-closed SB5)")
	}
}

// The DELIVERY ROUTE (dev-from-task/08a/08b, the reshape group 5): the rule-native
// replacement for check_coherence. It fires on the RUN (delivery is terminal, so a
// run-scoped delivery.routed guard is correct — no per-attempt reset, no mirror).
//   - 08a coherent: verify.result=pass AND review.verdict.0=approved AND openspec.validated
//     present → open_pr.
//   - 08b blocked: verify.result=fail → park.
//
// A verify.result of "retry" matches NEITHER (a flake never triggers delivery).
func TestDeliveryRouteTotalityAndSelfExtinguish(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "delivery.route.routed"

	pr, ok := rules["dev_from_task_delivery_open_pr"]
	if !ok {
		t.Fatal("missing dev_from_task_delivery_open_pr rule")
	}
	if c, ok := pr.condition("verify.cleanroom.result"); !ok || c.Operator != "eq" || c.Value != "pass" || c.Required {
		t.Errorf("delivery-open-pr must require verify.result eq \"pass\" (required:false — absent = false, no premature fire), got %+v", c)
	}
	if c, ok := pr.condition("review.verdict.value"); !ok || c.Operator != "eq" || c.Value != "approved" {
		t.Errorf("delivery-open-pr must require review.verdict.0 eq \"approved\" (defense in depth), got %+v", c)
	}
	if c, ok := pr.condition("openspec.change.validated"); !ok || c.Operator != "length_gt" {
		t.Errorf("delivery-open-pr must require openspec.validated present (length_gt 0; a revision-match eq lands with #519's .value), got %+v", c)
	}
	if !pr.publishesTo("component.delivery-station.dispatch") || !pr.hasAbsenceGuard(marker) || !pr.markerBeforeStationPublish(marker) {
		t.Error("delivery-open-pr must publish the delivery station (component.delivery-station.dispatch, R6) and be self-extinguishing via a RUN-scoped delivery.routed BEFORE the publish (delivery is not idempotent)")
	}

	park, ok := rules["dev_from_task_delivery_park"]
	if !ok {
		t.Fatal("missing dev_from_task_delivery_park rule")
	}
	if c, ok := park.condition("verify.cleanroom.result"); !ok || c.Operator != "eq" || c.Value != "fail" {
		t.Errorf("delivery-park must fire on verify.result eq \"fail\" (mutually exclusive with the coherent route on the verify value; a \"retry\" matches neither), got %+v", c)
	}
	if !park.hasTriple("run.awaiting.human") || park.firesTransition() {
		t.Error("delivery-park must park with no transition (G2)")
	}
	if !park.hasAbsenceGuard(marker) {
		t.Errorf("delivery-park must self-extinguish via %s (shared with the coherent route; delivery is terminal so a run-scoped guard is correct)", marker)
	}
}

// TestValidationStationPublishWiring pins the validate rule's R6 publish contract (G6:
// the property-name seam offline, not only via the e2e). coordinator/03 fires on the
// authoring LOOP (that is where openspec.change.authored lives), so the run is NOT the
// dispatch entity_id — the rule MUST thread run_entity_id AND slug as publish properties,
// which the validation station reads (a typo would fail closed: the station rejects the
// dispatch, openspec.validated never stamps, the run never reaches awaiting_approval).
func TestValidationStationPublishWiring(t *testing.T) {
	rules := runLifecycleRules(t)
	v, ok := rules["coordinator_validate_authored_change"]
	if !ok {
		t.Fatal("missing coordinator_validate_authored_change rule")
	}
	if !v.publishesToWithProps("component.validation-station.dispatch", "run_entity_id", "slug") {
		t.Error("the validate rule must publish component.validation-station.dispatch (R6) carrying BOTH run_entity_id and slug properties — it fires on the authoring loop, so the run + slug travel as properties, not the firing entity")
	}
	// It must fire on the authored marker (the loop signal) and NOT force a model tool.
	if c, ok := v.condition("openspec.change.authored"); !ok || c.Operator != "ne" {
		t.Error("the validate rule must fire on openspec.change.authored ne \"\" (the authoring-loop marker)")
	}
	if v.forcesFunction("validate_change") {
		t.Error("the validate rule must NOT force the validate_change tool — validation is a publish-triggered component now (R6)")
	}
}

// TestDeliveryRoutedWithoutResultIsAKnownGap PINS what remains of the R6 station
// "routed-without-result" wedge (G6 — pin the failure shape when a fix is deliberately
// deferred). The wedge NARROWED with station-failure-parks: a station that fails
// PERSISTENTLY IN-PROCESS now stamps station.dispatch.failed on retries-exhausted and the
// park rules (run-lifecycle/05/06) route the run to a human — that half is closed and
// journey-pinned. What this tripwire still pins is the RESTART half: a crash in the
// publish→handle window loses the in-flight dispatch — the marker is set, no failure fact
// ever stamps (a dead process stamps nothing; a shutdown-aborted handler deliberately
// stamps nothing), rules are EDGE-triggered, and no rule reconciles "delivery.routed set ∧
// pr.ref absent" into a park after the restart. That reconciliation is R8/group 8
// (upstream-blocked on on_recovery routing).
//
// TRIPWIRE: this asserts the restart gap STILL EXISTS. When group 8 adds the reconciliation
// rule (a park gated on delivery.routed-present ∧ pr.ref-absent), this FAILS — prompting
// removal of the tripwire and of the honest-gap comments in internal/station/station.go,
// 08a's metadata, and internal/vocab/vocab.go.
func TestDeliveryRoutedWithoutResultIsAKnownGap(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		routedPresent, prAbsent := false, false
		for _, c := range r.Conditions {
			if c.Field == "delivery.route.routed" && c.Operator == "length_gt" {
				routedPresent = true
			}
			if c.Field == "delivery.pr.ref" && c.Operator == "length_eq" {
				prAbsent = true
			}
		}
		if routedPresent && prAbsent {
			t.Errorf("rule %q reconciles delivery.routed-set ∧ pr.ref-absent — the R8/group-8 routed-without-result fix appears to have LANDED; remove this known-gap tripwire and the honest-gap comments in internal/station/station.go, configs/rules/dev-from-task/08a-delivery-open-pr.json, and internal/vocab/vocab.go", r.ID)
		}
	}
}

// The SERIALIZATION INVARIANT (the reshape, load-bearing): "one developer loop in flight
// per task; ONLY dispatch-developer (04), the floors-retry route (06c), and the review-retry
// route (07b) may spawn a role=developer loop." This is what guarantees floors evaluated the
// SAME frozen checkout the attempt measured. Red-first: add a role=developer publish_agent to
// any other rule and this fails.
func TestOnlySanctionedDeveloperSpawners(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	sanctioned := map[string]bool{
		"dev_from_task_dispatch_developer":    true, // 04 — the initial dispatch
		"dev_from_task_route_retry":           true, // 06c — the floors-route retry
		"dev_from_task_review_retry":          true, // 07b — the review-route retry (D16)
		"dev_from_task_route_transient_retry": true, // 06f — the transient-retry route (adopt-reason-aware-escalate)
		// The semsource-condition VARIANT siblings (integrate-semsource-ab-harness, D2):
		// byte-identical to their baselines but for the appended semsource read tools
		// (the parity pin), and NEVER loaded alongside them (the mutual-exclusion pin +
		// boot's file SUBSTITUTION) — so the one-developer-in-flight invariant holds in
		// either condition: exactly one of each pair exists in any loaded rule set.
		"dev_from_task_dispatch_developer_semsource": true,
		"dev_from_task_route_retry_semsource":        true,
		"dev_from_task_review_retry_semsource":       true,
	}
	var spawners []string
	for _, r := range rules {
		for _, a := range r.OnEnter {
			if a.Type != "publish_agent" || (a.Role != "developer" && a.Subject != "agent.task.developer") {
				continue
			}
			spawners = append(spawners, r.ID)
			if !sanctioned[r.ID] {
				t.Errorf("rule %q spawns a developer loop (role=%q subject=%q) but is not a sanctioned spawner — the one-developer-in-flight serialization invariant permits ONLY dispatch-developer (04), the floors-retry route (06c), the review-retry route (07b), and the transient-retry route (06f)", r.ID, a.Role, a.Subject)
			}
		}
	}
	for id := range sanctioned {
		if !slices.Contains(spawners, id) {
			t.Errorf("sanctioned developer-spawner %q does not spawn a role=developer loop — the dispatch/retry chain is broken", id)
		}
	}
}

// Delivery is NON-IDEMPOTENT (a second dispatch = a duplicate PR at M2), so ONLY the
// coherent delivery route (dev-from-task/08a) may trigger it — and since delivery is a
// publish-triggered COMPONENT now (R6), no rule may force the open_pr TOOL as a model
// turn. This pin covers both: exactly one publisher of the delivery station, and zero
// forced open_pr turns.
func TestOnlySanctionedDeliveryPublishers(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	const deliveryStationSubject = "component.delivery-station.dispatch"
	var publishers []string
	for _, r := range rules {
		if r.forcesFunction("open_pr") {
			t.Errorf("rule %q forces the open_pr tool as a model turn — delivery is a deterministic publish-triggered component (R6); no rule may spawn a model call to deliver", r.ID)
		}
		if r.publishesTo(deliveryStationSubject) {
			publishers = append(publishers, r.ID)
			if r.ID != "dev_from_task_delivery_open_pr" {
				t.Errorf("rule %q publishes the delivery station but is not the sanctioned publisher — delivery is non-idempotent; ONLY the coherent delivery route (08a) may publish it", r.ID)
			}
		}
	}
	if !slices.Contains(publishers, "dev_from_task_delivery_open_pr") {
		t.Error("the coherent delivery route (08a) does not publish the delivery station — the delivery path is broken")
	}
}

// convertedStationTools are the six deterministic stations converted from a forced
// single-turn coordinator loop into a publish-triggered COMPONENT in group 6 (R6). Their
// tool executors are DELETED (6E); each station calls the surviving shared core off a plain
// `publish`. No rule may force any of them as a model turn ever again.
var convertedStationTools = []string{
	"validate_change",   // → validation-station
	"project_tasks",     // → projection-station
	"check_floors",      // → floors-station
	"verify_artifact",   // → verify-station
	"open_pr",           // → delivery-station
	"provision_sandbox", // → provision-station
}

// The GROUP-6 MODEL-TURN CENSUS (R6, task 6.5): every deterministic station — change
// validation, task projection, structural floors, clean-room verify, delivery, and sandbox
// provisioning — was converted from a forced coordinator turn (a publish_agent with
// tool_choice=function calling a harness tool, or that tool sitting in a spawn's allowlist)
// into a publish-triggered COMPONENT. Under a real LLM each such forced turn was a paid model
// call that decided nothing (the outcome is harness-derived, G3), and the executors are now
// deleted (6E). This pin makes their re-growth fail the build: NO rule may force ANY of the
// six converted tools as a model turn, nor advertise one in a spawn's tools allowlist (which
// global discovery + tool_choice=auto could otherwise let a model call). The forced model
// turns that legitimately REMAIN are the PERSONA turns — the coordinator's decide/create_change
// (Sarah authors the change), Amelia's developer loop, Quinn's reviewer loop — plus measure_task,
// which STAYS a tool by design (R6: Amelia's in-loop feedback channel, not a deterministic station).
func TestNoRuleForcesAConvertedStationTool(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no rules loaded; the model-turn census would pass vacuously")
	}
	for _, r := range rules {
		for _, name := range convertedStationTools {
			if r.forcesFunction(name) {
				t.Errorf("rule %q forces the %q tool as a model turn — it was converted to a publish-triggered component in group 6 (R6) and its executor deleted (6E); a deterministic station fires via `publish`, never a paid forced turn (the outcome is harness-derived, G3)", r.ID, name)
			}
		}
		for _, a := range r.OnEnter {
			if a.Type != "publish_agent" {
				continue
			}
			for _, tool := range a.Tools {
				if slices.Contains(convertedStationTools, tool) {
					t.Errorf("rule %q advertises the converted deterministic-station tool %q in a publish_agent tools allowlist — those tools are components now (R6) with deleted executors (6E); a model must not be able to call one", r.ID, tool)
				}
			}
		}
	}
}

// run.awaiting_human is ONE logical park writer (G5) realized by a SANCTIONED SET of rule
// files. Every rule that stamps it must be in the set (rules carry no Source, so the
// tool-Source cross-check can't see them — this pin keeps the realizations honest).
func TestOnlySanctionedParkWriters(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	sanctioned := map[string]bool{
		"run_park_awaiting_human":            true, // run-lifecycle/03 — a coordinator ask_human decision
		"sandbox_park_unprovable":            true, // sandbox/02 — an unprovable sandbox
		"dev_from_task_route_escalate":       true, // 06d — the dev-loop budget exhausted
		"dev_from_task_review_park":          true, // 07c — the review budget exhausted
		"dev_from_task_review_no_verdict":    true, // 07d — the reviewer produced no verdict
		"dev_from_task_delivery_park":        true, // 08b — the delivery signals do not cohere
		"dev_from_task_route_transient_park": true, // 06g — the transient-retry cap reached (adopt-reason-aware-escalate)
		"run_park_station_failure_run":       true, // run-lifecycle/05 — a RUN-dispatched station exhausted its retries (station-failure-parks)
		"run_park_station_failure_loop":      true, // run-lifecycle/06 — a LOOP-dispatched station exhausted its retries (station-failure-parks)
	}
	var writers []string
	for _, r := range rules {
		if r.hasTriple("run.awaiting.human") {
			writers = append(writers, r.ID)
			if !sanctioned[r.ID] {
				t.Errorf("rule %q stamps run.awaiting_human but is not a sanctioned park writer — run.awaiting_human is ONE logical writer (G5); a new park realization must be added to the sanctioned set deliberately (and mean the identical thing: this run awaits a human)", r.ID)
			}
		}
	}
	for id := range sanctioned {
		if !slices.Contains(writers, id) {
			t.Errorf("sanctioned park writer %q does not stamp run.awaiting_human — a park path was silently dropped", id)
		}
	}
}

// The DISPATCH-ENTITY CENSUS (station-failure-parks task 2.1, D2): for every rule that
// publishes a station dispatch (component.<station>.dispatch), pin which ENTITY KIND the
// dispatch fires on — because that is where the harness stamps station.dispatch.failed on
// retries-exhausted, and the two park rules (run-lifecycle/05 run-fired, /06 loop-fired)
// partition exactly on it. The witness is LOAD-BEARING, not stylistic: a LOOP-fired
// dispatch must thread the run anchor as a publish property substituted from the FIRING
// entity's own triple (`run_entity_id: $entity.triple.agent.run.entity-id`) — which is
// only possible when the firing entity carries agent.run.entity-id, i.e. it is a loop —
// and that same triple is the binding the loop-fired park rule uses to reach the run. A
// RUN-fired dispatch never threads it (the firing entity IS the run). NOTE the census
// CORRECTED the design's expected table: validation is LOOP-fired (coordinator/03 fires
// on the coordinator loop, `agent.loop.role eq coordinator`), not run-fired as D2's
// pre-census sketch guessed — exactly why the design demanded verification from source.
func TestStationDispatchEntityCensus(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}

	// The pinned split. A new station MUST be added here deliberately, deciding
	// which park half covers it.
	expected := map[string]string{
		"projection-station": "run",  // dev-from-task/03 fires on the run
		"provision-station":  "run",  // sandbox/01 fires on the run
		"delivery-station":   "run",  // dev-from-task/08a fires on the run
		"validation-station": "loop", // coordinator/03 fires on the coordinator loop
		"floors-station":     "loop", // dev-from-task/05 fires on the developer loop
		"verify-station":     "loop", // dev-from-task/07a fires on the review loop
		// conversation/03a+03b fire on the RUN (the classifier subject-overrides
		// conversation.intent.* there). NOTE this lane is the ONE DELIBERATE
		// NON-PARKING station (nl-conversation-intent grp5-review B1): the pre-impl
		// sketch assumed a retries-exhausted apply dispatch would stamp
		// station.dispatch.failed and let run-lifecycle/05 park it, but that is
		// UNRECOVERABLE here — this run is at the change-approval gate, and BOTH
		// release rules (run-lifecycle/02 resume, /07 cancel) require
		// run.awaiting.human ABSENT, which nothing in the repo ever removes. The
		// run would be wedged forever and even /semdev approve would die. So the
		// apply consumer notifies the human and leaves the gate OPEN instead; this
		// entry records the firing ENTITY (still the run) for census completeness,
		// and TestApplyLaneIsTheDeliberateNonParkingStation enforces the opt-out.
		"conversation-apply": "run",
	}

	const runAnchorSubstitution = "$entity.triple.agent.run.entity-id"
	found := map[string]string{}
	for _, r := range rules {
		for _, a := range r.OnEnter {
			if a.Type != "publish" || !strings.HasPrefix(a.Subject, "component.") {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(a.Subject, "component."), ".dispatch")
			kind := "run"
			if a.Properties["run_entity_id"] == runAnchorSubstitution {
				kind = "loop"
			}
			if prev, dup := found[name]; dup && prev != kind {
				t.Errorf("station %q is dispatched by rules with CONFLICTING firing-entity kinds (%s vs %s) — the park split cannot cover both from one fact location", name, prev, kind)
			}
			found[name] = kind
		}
	}

	for name, wantKind := range expected {
		gotKind, ok := found[name]
		if !ok {
			t.Errorf("no rule publishes component.%s.dispatch — a station lost its dispatch rule (or the census's subject parsing drifted)", name)
			continue
		}
		if gotKind != wantKind {
			t.Errorf("station %q dispatch fires on a %s entity, census pins %s — the D2 split moved; re-point the park rules (run-lifecycle/05 vs 06) and update this table deliberately", name, gotKind, wantKind)
		}
	}
	for name := range found {
		if _, ok := expected[name]; !ok {
			t.Errorf("rule pack dispatches unknown station %q — add it to the dispatch-entity census (deciding which park half covers it) before it can strand a terminal failure", name)
		}
	}

	// The park rules must partition the entity space along the SAME line the
	// census pins: run-fired parks on the chain grammar, loop-fired on the
	// agentic-loop grammar. This is what makes the two halves mutually
	// exclusive by construction (no racy cross-entity discrimination).
	wantPatterns := map[string]string{
		"run_park_station_failure_run":  "*.*.agent.chain.execution.*",
		"run_park_station_failure_loop": "*.*.agent.agentic-loop.execution.*",
	}
	for _, r := range rules {
		if want, ok := wantPatterns[r.ID]; ok {
			delete(wantPatterns, r.ID)
			if r.Entity.Pattern != want {
				t.Errorf("park rule %q entity pattern = %q, want %q — the pattern IS the run/loop discrimination; a wildcard would fire the run-fired half on loop entities (its absence guards pass vacuously there)", r.ID, r.Entity.Pattern, want)
			}
		}
	}
	for id := range wantPatterns {
		t.Errorf("park rule %q not found in the rule packs", id)
	}
}

// The CONFIG LINT (the reshape, task 4.6): every model-publishing spawn declares an
// explicit tools allowlist — no allowlist-less spawn advertises the whole tool surface to
// the model. And every declared tool is in the agentic-tools allowed_tools gate. Red-first:
// drop the tools from any spawn rule and this fails.
func TestEveryModelSpawnDeclaresToolsAllowlist(t *testing.T) {
	root := repoRoot(t)
	rules, err := loadRules(root)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	allowed := make(map[string]bool)
	for _, tool := range cfg.Components.AgenticTools.Config.AllowedTools {
		allowed[tool] = true
	}
	if len(allowed) == 0 {
		t.Fatal("allowed_tools is empty — the per-spawn allowlists have no gate to be a subset of (the config-lint would pass vacuously)")
	}
	sawSpawn := false
	for _, r := range rules {
		for _, a := range r.OnEnter {
			if a.Type != "publish_agent" {
				continue
			}
			sawSpawn = true
			if len(a.Tools) == 0 {
				t.Errorf("rule %q has a model-publishing spawn with NO tools allowlist — every spawn must scope its tools (task 4.6), so a real model is not handed the whole tool surface", r.ID)
			}
			for _, tool := range a.Tools {
				if !allowed[tool] {
					t.Errorf("rule %q spawns tool %q not in agentic-tools allowed_tools", r.ID, tool)
				}
			}
		}
	}
	if !sawSpawn {
		t.Fatal("no publish_agent spawns found; the config-lint would pass vacuously")
	}
}

// The PROMPT⊆ADVERTISED config-lint (integrate-semsource-ab group 0, semstreams #551): every
// tool a spawn's PROMPT instructs the model to CALL must be in that spawn's tools allowlist.
// Post-#551 the per-loop executor rejects a call outside the advertised set (ToolErrorPermission),
// so a prompt that says "read the task contract with query_entity" while query_entity is
// unadvertised makes every REAL-LLM attempt open with a rejected not_advertised call — invisible
// under the mock (which never calls query_entity) but fatal on the first real token. Red-first:
// this FAILS if any spawn prompt names an unadvertised tool.
//
// Matching: a tool is "instructed" if its name appears as a whole word in the prompt AFTER
// stripping double-quoted string literals. The strip drops the coordinator's decide-action
// TAXONOMY values (e.g. decide(action="ask_human")) — which name a ROUTE DECISION, not a tool call
// — while keeping bare tool instructions (query_entity(...), "with query_entity", "with
// read_workspace"). The tool universe is the agentic-tools allowed_tools gate, so a word that is
// not a registered tool is prose, not a missing advertisement.
func TestSpawnPromptToolsAreAdvertised(t *testing.T) {
	root := repoRoot(t)
	rules, err := loadRules(root)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	known := cfg.Components.AgenticTools.Config.AllowedTools
	if len(known) == 0 {
		t.Fatal("allowed_tools is empty — the prompt⊆advertised lint would pass vacuously")
	}
	toolRe := make(map[string]*regexp.Regexp, len(known))
	for _, tool := range known {
		toolRe[tool] = regexp.MustCompile(`\b` + regexp.QuoteMeta(tool) + `\b`)
	}
	quoted := regexp.MustCompile(`"[^"]*"`)
	sawSpawn := false
	for _, r := range rules {
		for _, a := range r.OnEnter {
			if a.Type != "publish_agent" || a.Prompt == "" {
				continue
			}
			sawSpawn = true
			advertised := make(map[string]bool, len(a.Tools))
			for _, tool := range a.Tools {
				advertised[tool] = true
			}
			// Strip quoted literals so a decide-action taxonomy value is not read as a tool call.
			prose := quoted.ReplaceAllString(a.Prompt, "")
			for _, tool := range known {
				if advertised[tool] || !toolRe[tool].MatchString(prose) {
					continue
				}
				t.Errorf("rule %q (role %q) prompt instructs tool %q but does NOT advertise it in the spawn tools allowlist %v — post-#551 the executor rejects the unadvertised call (ToolErrorPermission), so the FIRST real-LLM attempt opens with a rejected not_advertised call (invisible under the mock). Add %q to the allowlist or drop it from the prompt.", r.ID, a.Role, tool, a.Tools, tool)
			}
		}
	}
	if !sawSpawn {
		t.Fatal("no publish_agent spawns with prompts found; the prompt⊆advertised lint would pass vacuously")
	}
}

// G2 WIDENED (the reshape, task 5.6): zero Go-DERIVED ROUTING TOKENS. The route-token layer
// (check_gate/check_coherence and their dev.gate_decision/dev.coherence_decided/pr.coherence
// decision facts) is DELETED — routing is now rules over harness-stamped facts (R1). This
// pin fails if a route-DECISION predicate (a Go-derived advance/retry/escalate/coherent/
// blocked token consumed by a rule dispatch condition) reappears in the vocabulary OR a rule
// condition, or if the deleted decider packages return. The raw route.* mirror facts (passed/
// rejected/verdict) are NOT decisions — they are harness copies the RULES compose the route
// from — so they are allowed; only a single-fact DECISION token is banned.
func TestNoGoDerivedRoutingTokens(t *testing.T) {
	// Banned route-DECISION predicates: a Go tool that stamps one of these, consumed by a
	// rule's dispatch condition, is exactly the check_gate/check_coherence pattern (B1).
	banned := []string{"dev.gate_decision", "dev.coherence_decided", "pr.coherence.decision", "dev.gate.0.decision"}
	for _, name := range banned {
		if _, ok := vocab.WriterOf(name); ok {
			t.Errorf("vocabulary declares a route-decision token %q — the reshape deleted the Go route-token layer (check_gate/check_coherence); routing is rules over raw facts (R1/G2)", name)
		}
	}
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	for _, r := range rules {
		for _, c := range r.Conditions {
			for _, name := range banned {
				if c.Field == name {
					t.Errorf("rule %q dispatches on a Go-derived route-decision token %q — routing must compose from RAW harness facts (route.passed/route.rejected/route.verdict/verify.result), never a single derived decision (G2, the widened census)", r.ID, name)
				}
			}
		}
	}
	// The decider packages themselves must be gone.
	for _, pkg := range []string{"internal/tools/checkgate", "internal/tools/checkcoherence"} {
		if _, err := os.Stat(filepath.Join(repoRoot(t), pkg)); err == nil {
			t.Errorf("the Go route-decider package %s still exists — the reshape deletes check_gate/check_coherence; routing is rule-native (R1)", pkg)
		}
	}
}

// The fail-closed park (SB5): an unprovable sandbox (provision_sandbox stamped
// sandbox.blocked) parks the run toward the human — it stamps run.awaiting_human and
// posts to the user bus, and it does NOT fire a lifecycle transition (G2). Fire-once
// via the run.awaiting_human absence guard so it does not re-post on every re-scan.
func TestSandboxParkOnUnprovable(t *testing.T) {
	park, ok := runLifecycleRules(t)["sandbox_park_unprovable"]
	if !ok {
		t.Fatal("missing sandbox_park_unprovable rule")
	}
	if c, ok := park.condition("sandbox.provision.blocked"); !ok || c.Operator != "ne" {
		t.Error("sandbox park must fire on sandbox.blocked ne \"\" (an unprovable sandbox)")
	}
	if !park.hasTriple("run.awaiting.human") {
		t.Error("sandbox park must stamp run.awaiting_human (the park marker the whole system reads)")
	}
	if !park.hasAbsenceGuard("run.awaiting.human") {
		t.Error("sandbox park must guard on run.awaiting_human length_eq 0 (fire once, don't re-post to the user bus each re-scan)")
	}
	if park.firesTransition() {
		t.Error("sandbox park must fire NO lifecycle transition (G2) — it records a fact and posts to the user bus")
	}
}

// 3.6 — the park rule stamps run.awaiting_human (its single writer, G5) on
// ask_human and posts to the user bus. No Go reconciler advances a parked run.
func TestParkRuleStampsAwaitingHuman(t *testing.T) {
	park, ok := runLifecycleRules(t)["run_park_awaiting_human"]
	if !ok {
		t.Fatal("missing run_park_awaiting_human rule")
	}
	if c, ok := park.condition("coordinator.decision.next-action"); !ok || c.Value != "ask_human" {
		t.Error("park rule must fire on next_action == ask_human")
	}
	if !park.hasTriple("run.awaiting.human") {
		t.Error("park rule must stamp run.awaiting_human (G5 single writer = park-rule)")
	}
	// The run-anchor guard (semteams agent-run/07): without a run anchor the
	// subject-override would not resolve and the marker would be silently dropped.
	if c, ok := park.condition("agent.run.entity-id"); !ok || c.Operator != "ne" {
		t.Error("park rule must carry the agent.run.entity_id run-anchor guard (ne \"\")")
	}
}

// D15 park-exclusion — every active lifecycle_transition rule MUST exclude parked
// runs (run.awaiting_human absent), or a parked run gets swept through a gate
// before the group-5 human-resume path clears the marker. The only exemption is a
// rule that itself clears run.awaiting_human (the resume-from-park rule). This is
// the guard codex flagged: rules 01/02 advance the run and must carry it.
func TestLifecycleTransitionRulesExcludeParkedRuns(t *testing.T) {
	saw := 0
	for _, r := range runLifecycleRules(t) {
		if !r.firesTransition() {
			continue
		}
		saw++
		if r.clearsPredicate("run.awaiting.human") {
			continue // resume-from-park rule: legitimately fires on a parked run to un-park it
		}
		if !r.hasAbsenceGuard("run.awaiting.human") {
			t.Errorf("rule %s fires a lifecycle_transition but does not exclude parked runs (run.awaiting_human length_eq 0) — a parked run would be swept through the gate (design D15)", r.ID)
		}
	}
	if saw == 0 {
		t.Fatal("no lifecycle_transition rules found; the park-exclusion pin would pass vacuously")
	}
}

// Red-first: the park-exclusion helpers must classify correctly — an unguarded
// transition rule is caught, a marker-clearing rule is exempt, a guarded rule
// passes.
func TestParkExclusionPinLogic(t *testing.T) {
	unguarded := ruleFile{ID: "unguarded", OnEnter: []ruleAction{{Type: "lifecycle_transition", Workflow: "agent-run", Phase: "executing"}}}
	if !unguarded.firesTransition() {
		t.Fatal("unguarded rule not seen as a transition")
	}
	if unguarded.hasAbsenceGuard("run.awaiting.human") || unguarded.clearsPredicate("run.awaiting.human") {
		t.Error("unguarded transition rule wrongly treated as guarded/exempt — pin would not fire")
	}

	resume := ruleFile{ID: "resume", OnEnter: []ruleAction{
		{Type: "remove_triple", Predicate: "run.awaiting.human"},
		{Type: "lifecycle_transition", Workflow: "agent-run", Phase: "executing"},
	}}
	if !resume.clearsPredicate("run.awaiting.human") {
		t.Error("resume-from-park rule not recognized as clearing the marker")
	}

	guarded := ruleFile{
		ID:         "guarded",
		Conditions: []ruleCondition{{Field: "run.awaiting.human", Operator: "length_eq", Value: float64(0)}},
		OnEnter:    []ruleAction{{Type: "lifecycle_transition"}},
	}
	if !guarded.hasAbsenceGuard("run.awaiting.human") {
		t.Error("guarded rule not recognized as carrying the absence guard")
	}
}

// The dual-anchor forward guard. At M0 exactly one rule mints a run
// (coordinator/01-issue-intake-mint-run, guarded so a coordinator never
// accumulates two anchors), so the single-anchor handoff (agent-run/01,
// length_eq 1) suffices and semteams' 01b is intentionally not ported. This pin
// fails the moment a SECOND run_scope=new rule lands (create_change, recovery
// re-dispatch, …) without a dual-anchor handoff — the exact change at which a
// coordinator can reach a two-anchor state and the length_eq 1 guard would
// silently wedge the self-minted run in dispatched. It forces 01b (or the
// framework fix) to land in the same change, not silently after.
func TestDualAnchorHandoffPresentWhenMultipleRunScopeNew(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if msg := dualAnchorGuardViolation(rules); msg != "" {
		t.Error(msg)
	}
}

// Red-first: the dual-anchor guard helper must classify correctly — one minter
// is safe, two minters without a dual-anchor handoff is a violation, and adding
// the handoff clears it.
func TestDualAnchorGuardLogic(t *testing.T) {
	mint := ruleFile{ID: "mint", Enabled: true, OnEnter: []ruleAction{{Type: "publish_agent", RunScope: "new"}}}
	dualAnchor := ruleFile{ID: "handoff-01b", Enabled: true, Conditions: []ruleCondition{
		{Field: "agent.run.entity-id", Operator: "length_gt", Value: float64(1)},
	}}

	if msg := dualAnchorGuardViolation([]ruleFile{mint}); msg != "" {
		t.Errorf("one minter should be safe, got violation: %s", msg)
	}
	if msg := dualAnchorGuardViolation([]ruleFile{mint, mint}); msg == "" {
		t.Error("two minters without a dual-anchor handoff should violate, got none")
	}
	if msg := dualAnchorGuardViolation([]ruleFile{mint, mint, dualAnchor}); msg != "" {
		t.Errorf("two minters WITH a dual-anchor handoff should be safe, got violation: %s", msg)
	}
	// A disabled second minter does not arm the guard.
	disabledMint := ruleFile{ID: "mint-off", Enabled: false, OnEnter: []ruleAction{{Type: "publish_agent", RunScope: "new"}}}
	if msg := dualAnchorGuardViolation([]ruleFile{mint, disabledMint}); msg != "" {
		t.Errorf("a disabled second minter should not arm the guard, got violation: %s", msg)
	}
}

// 3.9 — the archive_change loop-closer is declared but disabled at M0; it
// references openspec.archived and is wired to a merge trigger at M1.
func TestArchiveChangeIsDeclaredAndDisabled(t *testing.T) {
	archive, ok := runLifecycleRules(t)["run_archive_change_on_merge"]
	if !ok {
		t.Fatal("missing run_archive_change_on_merge rule")
	}
	if archive.Enabled {
		t.Error("archive_change loop-closer must be disabled at M0 (merge trigger + live archive land at M1)")
	}
	if _, ok := archive.condition("openspec.change.archived"); !ok {
		t.Error("archive loop-closer must reference openspec.archived (the fact its harness stamps)")
	}
}

// Every rules_files path in the bootstrap config resolves to a real rule file —
// a listed-but-missing path boots a processor that silently drops a rule (the
// swallowed-rule-load class). rules_files are authored relative to the config
// file's own directory (configs/), which boot.resolveRulePackPaths turns into
// absolute paths at load so the rule engine is CWD-independent; this pin resolves
// them the same way.
func TestRulesFilesResolve(t *testing.T) {
	root := repoRoot(t)
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	if len(cfg.Components.Rule.Config.RulesFiles) == 0 {
		t.Fatal("bootstrap lists no rules_files")
	}
	configDir := filepath.Join(root, "configs")
	for _, rel := range cfg.Components.Rule.Config.RulesFiles {
		if _, err := os.Stat(filepath.Join(configDir, rel)); err != nil {
			t.Errorf("rules_files entry %q does not resolve relative to the config dir: %v", rel, err)
		}
	}
}

// The REVERSE census: every rule file on disk is LISTED in the bootstrap rules_files.
// A file-on-disk the runtime never loads passes every conformance census green (loadRules
// walks the disk) while the rule silently never fires — exactly how the transient routes
// sat inert mid-WIP until configs/semdev-bootstrap.json gained their entries
// (adopt-reason-aware-escalate). The offline pin for that failure shape.
func TestEveryRuleFileIsBootstrapped(t *testing.T) {
	root := repoRoot(t)
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	listed := make(map[string]bool)
	for _, rel := range cfg.Components.Rule.Config.RulesFiles {
		listed[filepath.ToSlash(rel)] = true
	}
	rulesDir := filepath.Join(root, "configs", "rules")
	err = filepath.WalkDir(rulesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		rel, relErr := filepath.Rel(filepath.Join(root, "configs"), path)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if listed[slash] {
			return nil
		}
		// The semsource-condition VARIANT convention (integrate-semsource-ab-harness,
		// D2): an `X-semsource.json` sibling is loaded by boot SUBSTITUTING it for its
		// listed baseline `X.json` when the experiment condition is declared — so a
		// variant is "bootstrapped" iff its baseline is listed. An orphan variant
		// (baseline not listed) is dead and fails below like any unlisted file.
		if strings.HasSuffix(slash, "-semsource.json") {
			if listed[strings.TrimSuffix(slash, "-semsource.json")+".json"] {
				return nil
			}
		}
		t.Errorf("rule file %q exists on disk but is NOT in the bootstrap rules_files (nor a -semsource variant of a listed baseline) — the runtime never loads it, so the rule silently never fires while every disk-walking census stays green (add it to configs/semdev-bootstrap.json, or delete the file)", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk configs/rules: %v", err)
	}
}

// Every tool a rule's publish_agent spawns must be in the agentic-tools
// allowlist, else the loop boots and fails at runtime with "tool X not allowed".
// Vacuous at M0 (the run-lifecycle rules dispatch no agents), load-bearing as
// spawn rules land.
func TestRuleToolsSubsetOfAllowed(t *testing.T) {
	root := repoRoot(t)
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	allowed := make(map[string]bool)
	for _, tool := range cfg.Components.AgenticTools.Config.AllowedTools {
		allowed[tool] = true
	}
	rules, err := loadRules(root)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	for _, r := range rules {
		for _, a := range r.OnEnter {
			for _, tool := range a.Tools {
				if !allowed[tool] {
					t.Errorf("rule %s spawns tool %q not in allowed_tools", r.ID, tool)
				}
			}
		}
	}
}

// Every persona role a rule or the roster relies on has a fragment directory
// (the role name is the fragment binding key).
func TestPersonaRoleDirsExist(t *testing.T) {
	root := repoRoot(t)
	for _, role := range []string{"coordinator", "developer", "reviewer", "conversation"} {
		dir := filepath.Join(root, "configs", "personas", "fragments", role)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("persona fragment dir for role %q missing at %s", role, dir)
		}
	}
}

// TestRejectCancelsGatedRunOnly pins the reject lane (nl-conversation-intent task
// 5.3, design D7). Two guards, both LOAD-BEARING:
//
// PHASE GUARD (architect H1): the agent-run state machine allows a LEGAL
// executing→cancelled edge, so an unguarded reject rule would kill an already
// approved, already executing run — destroying work the human explicitly approved.
// The rule fires ONLY at awaiting_approval, mirroring the resume rule.
//
// CELL-SPACE PARTITION (grp3-review M2): the exact-command fast-paths bypass the
// routing rules' both-facts-absent guard, so an authorized `/semdev reject` then
// `/semdev approve` on a still-gated run can leave BOTH gate facts present. Full
// mutual exclusion is the RULES' job (G2) and must be evaluated over ONE atomic
// mirror snapshot, so BOTH lifecycle rules carry the other fact's absence guard: a
// run holding both facts transitions to NEITHER, never to both. Without the paired
// guards the run would resume AND cancel. NOTE that cell is an UNSURFACED STALL, not
// a park — nothing stamps run.awaiting.human, nothing posts, no operator surface
// flags it — and parking it is NOT available as a fix, because a park at this gate
// is unrecoverable (both release rules require run.awaiting.human absent and nothing
// ever removes it; grp5-review B1). Named in the design's Risks.
func TestRejectCancelsGatedRunOnly(t *testing.T) {
	rules := runLifecycleRules(t)

	cancel, ok := rules["run_cancel_after_change_rejection"]
	if !ok {
		t.Fatal("missing run_cancel_after_change_rejection rule — the NL reject lane records a reject decision with nothing to act on it (the run stays gated forever)")
	}
	if c, ok := cancel.condition("run.change.decision"); !ok || c.Operator != "eq" || c.Value != "reject" {
		t.Error("cancel must require run.change.decision == \"reject\"")
	}
	// H1 — the phase guard.
	if c, ok := cancel.condition("agent.run.phase"); !ok || c.Operator != "eq" || c.Value != "awaiting_approval" {
		t.Error("cancel MUST be phase-guarded to awaiting_approval (H1) — the legal executing→cancelled edge would otherwise kill an approved, executing run")
	}
	// D13 (group 8): the cell-space partition the two-boolean design needed is GONE,
	// because a single-valued decision cannot contradict itself. The old cross-guards
	// (cancel requiring approved-absent, resume requiring rejected-absent) existed only
	// to stop a both-facts run from both resuming AND cancelling; a run can no longer
	// hold both. Mutual exclusion is now asserted where it lives — on the VALUE.

	var cancelled bool
	for _, a := range cancel.OnEnter {
		if a.Type == "lifecycle_transition" && a.Phase == "cancelled" {
			cancelled = true
		}
	}
	if !cancelled {
		t.Error("cancel must fire the rule-owned awaiting_approval → cancelled transition (G2: no Go fires it)")
	}

	// The OTHER half of the exclusion: resume keys on the OPPOSITE value of the SAME
	// single-valued fact, so exactly one of the two rules can ever match a given run.
	resume, ok := rules["run_resume_after_change_approval"]
	if !ok {
		t.Fatal("missing run_resume_after_change_approval rule")
	}
	rc, rok := resume.condition("run.change.decision")
	cc, cok := cancel.condition("run.change.decision")
	if !rok || !cok || rc.Value == cc.Value {
		t.Errorf("resume and cancel must key on OPPOSITE values of the one decision fact "+
			"(resume=%v cancel=%v) — that is what makes them mutually exclusive by construction (D13)", rc.Value, cc.Value)
	}
}

// TestNoRuleReadsARetiredGateFact is the group-8 D13 half-migration guard.
//
// run.change.approved and run.change.rejected were RETIRED in favour of the single-valued
// run.change.decision. A rule left behind reading a retired predicate does not error, does
// not fail to load, and does not show up in any other census — it simply NEVER FIRES,
// because nothing writes that predicate any more. That is the silently-dead-rule shape this
// repo has been bitten by before (the inert transient routes), and for a rule on the
// approval gate it means a run that can never resume or never cancel.
//
// The scan is over the RAW file bytes rather than parsed conditions, so a retired
// predicate hiding in an action, a substitution token, or a prompt is caught too. Prose
// mentions in `description` are legitimate — the D13 rationale has to be able to name what
// it replaced — so descriptions are stripped before scanning.
func TestNoRuleReadsARetiredGateFact(t *testing.T) {
	retired := []string{"run.change.approved", "run.change.rejected"}
	root := repoRoot(t)
	rulesDir := filepath.Join(root, "configs", "rules")

	err := filepath.WalkDir(rulesDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var doc map[string]any
		if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil {
			return fmt.Errorf("%s: %w", path, jsonErr)
		}
		// Descriptions may name the retired facts (the D13 rationale); everything
		// else may not.
		delete(doc, "description")
		scanned, marshalErr := json.Marshal(doc)
		if marshalErr != nil {
			return marshalErr
		}
		for _, pred := range retired {
			if strings.Contains(string(scanned), pred) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s references the RETIRED gate fact %q outside its description — "+
					"nothing writes it any more, so this rule can never fire (D13: use run.change.decision "+
					"with value %q or %q)", rel, pred, "approve", "reject")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk rules: %v", err)
	}
}

// TestDecisionConditionsUseTheLegalEnum is the D13 companion census — and the half the
// retired-NAME census structurally cannot see.
//
// D13 replaced a predicate whose domain was one universally-familiar literal ("true") with
// a two-word English enum. Nothing else in the repo checks that a rule keying on
// run.change.decision uses a value the writer can ever produce. A typo does not fail to
// load, does not error, and does not trip any other pin — the rule simply never fires.
//
// This is not hypothetical: mutating dev-from-task/02-rewake-coordinator-dev.json from
// "approve" to "approved" left the ENTIRE conformance suite green, and "approved" is a live
// value elsewhere in this repo (dev-from-task/08a keys on review.verdict.value == "approved").
// That rule is the dev-loop kickoff, so the run would approve, resume, provision and project
// tasks — then never rewake the coordinator. Executing forever, no park, no surface.
//
// The legal shapes are exactly two: an absence guard (length_eq 0) or an equality against a
// value the single sanctioned writer can actually stamp.
func TestDecisionConditionsUseTheLegalEnum(t *testing.T) {
	legal := map[string]bool{admission.DecisionApprove: true, admission.DecisionReject: true}
	root := repoRoot(t)
	checked := 0

	err := filepath.WalkDir(filepath.Join(root, "configs", "rules"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var doc struct {
			Conditions []struct {
				Field    string `json:"field"`
				Operator string `json:"operator"`
				Value    any    `json:"value"`
			} `json:"conditions"`
		}
		if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil {
			return fmt.Errorf("%s: %w", path, jsonErr)
		}
		rel, _ := filepath.Rel(root, path)
		for _, c := range doc.Conditions {
			if c.Field != admission.DecisionPredicate {
				continue
			}
			checked++
			switch c.Operator {
			case "length_eq", "length_gte", "length_lte":
				// An absence/length guard does not name a value.
			case "eq", "ne":
				s, ok := c.Value.(string)
				if !ok || !legal[s] {
					t.Errorf("%s: condition on %s uses value %#v, which the sanctioned writer "+
						"NEVER stamps — this rule can never fire. Legal values are %q and %q.",
						rel, admission.DecisionPredicate, c.Value, admission.DecisionApprove, admission.DecisionReject)
				}
			default:
				t.Errorf("%s: condition on %s uses operator %q — a single-valued enum fact "+
					"supports only equality and length guards", rel, admission.DecisionPredicate, c.Operator)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk rules: %v", err)
	}
	// Vacuity guard: this census is worthless if it silently matches nothing.
	if checked == 0 {
		t.Fatal("census matched ZERO conditions on run.change.decision — the scan is broken, " +
			"not the ruleset (the gate rules all key on it)")
	}
	t.Logf("checked %d run.change.decision conditions", checked)
}
