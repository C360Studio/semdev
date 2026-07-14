package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
		if c.Field == "agent.run.entity_id" && c.Operator == "length_gt" {
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
	if c, ok := offer.condition("openspec.validated"); !ok || c.Operator != "ne" {
		t.Error("offer-approval must require openspec.validated present (ne \"\") — validate-before-approval ordering (3.7)")
	}
	if c, ok := offer.condition("run.change_approved"); !ok || c.Operator != "length_eq" {
		t.Error("offer-approval must require run.change_approved absent (length_eq 0)")
	}

	resume, ok := rules["run_resume_after_change_approval"]
	if !ok {
		t.Fatal("missing run_resume_after_change_approval rule")
	}
	if c, ok := resume.condition("run.change_approved"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Error("resume must require run.change_approved == true — the gate holds until approval (3.5)")
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
	if c, ok := offer.condition("openspec.validated"); !ok || c.Operator != "ne" {
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
	const marker = "run.dev_kickoff"
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
	const marker = "run.projection_kickoff"
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
	if c, ok := proj.condition("run.change_approved"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Error("projection dispatch must fire on run.change_approved == true (the spec trigger: approval projects task.spec)")
	}
	if c, ok := proj.condition("agent.run"); !ok || c.Operator != "ne" {
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
	c, ok := rewake.condition("task.spec.0.test_command")
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

// The provision-and-prove-cold station (sandbox/01-provision) is the approval-
// triggered spawn that stands up the sandbox and cold-proves it. Like every
// publish_agent spawn rule it MUST be self-extinguishing (house restart-safety
// pattern): a fired-once sandbox.provisioned marker stamped in on_enter, guarded by
// length_eq 0 — else an asymmetric RULE_STATE loss re-spawns a duplicate provision
// loop (publish_agent is not idempotent). It must force the provision_sandbox call,
// fire on approval, and require the run anchor for run_scope=inherit.
func TestSandboxProvisionIsSelfExtinguishing(t *testing.T) {
	prov, ok := runLifecycleRules(t)["sandbox_provision"]
	if !ok {
		t.Fatal("missing sandbox_provision rule")
	}
	const marker = "sandbox.provisioned"
	if !prov.hasAbsenceGuard(marker) {
		t.Errorf("provision spawn must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate provision loop (publish_agent is not idempotent)", marker)
	}
	if !prov.hasTriple(marker) {
		t.Errorf("provision spawn must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !prov.markerBeforePublish(marker) {
		t.Errorf("provision spawn must stamp %s BEFORE its publish_agent (SB7) — else a publish failure leaves the run duplicable rather than stuck-toward-human", marker)
	}
	if !prov.forcesFunction("provision_sandbox") {
		t.Error("provision spawn must force the provision_sandbox call (tool_choice mode=function, function_name=provision_sandbox)")
	}
	if c, ok := prov.condition("run.change_approved"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Error("provision spawn must fire on run.change_approved == true (provision the approved run's sandbox)")
	}
	if c, ok := prov.condition("agent.run"); !ok || c.Operator != "ne" {
		t.Error("provision spawn must require the agent.run anchor (ne \"\") so run_scope=inherit binds the loop to this run")
	}
}

// The readiness gate (SB5): the dev loop must NOT proceed onto development without a
// PROVEN sandbox. dev-from-task/02 is gated on sandbox.ready eq true, so an approved,
// projected run whose sandbox was not cold-proved (sandbox.ready never stamped)
// cannot re-wake into dev_from_task — it parks instead (sandbox/02-park-unprovable).
// Red-first: drop the gate and this fails. The gate is honest only if a producer
// actually stamps sandbox.ready, so assert the provision station forces the tool.
func TestDevRewakeGatedOnSandboxReadiness(t *testing.T) {
	rewake, ok := runLifecycleRules(t)["dev_from_task_rewake_coordinator"]
	if !ok {
		t.Fatal("missing dev_from_task_rewake_coordinator rule")
	}
	c, ok := rewake.condition("sandbox.ready")
	if !ok {
		t.Fatal("dev re-wake must gate on a proven sandbox (sandbox.ready) — else the dev loop can proceed over an absent/unproven sandbox (SB5, the semspec disease)")
	}
	if c.Operator != "eq" || c.Value != "true" {
		t.Errorf("dev re-wake readiness gate must be sandbox.ready eq \"true\", got operator=%q value=%v", c.Operator, c.Value)
	}
	prov, ok := runLifecycleRules(t)["sandbox_provision"]
	if !ok || !prov.forcesFunction("provision_sandbox") {
		t.Error("the readiness gate has no producer: sandbox_provision must exist and force provision_sandbox, or sandbox.ready never becomes present and the dev loop deadlocks")
	}
}

// toFloat coerces a JSON-decoded numeric condition value (float64) to float64 for the
// budget-partition arithmetic.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

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
	const marker = "dev.dispatched"
	if !disp.hasAbsenceGuard(marker) || !disp.hasTriple(marker) || !disp.markerBeforePublish(marker) {
		t.Errorf("dispatch-developer must be self-extinguishing (%s guard + add_triple before the publish)", marker)
	}
	if !disp.usesToolChoiceAuto() {
		t.Error("dispatch-developer must spawn a BOUNDED MULTI-TURN loop (tool_choice mode=auto), not a single forced author turn (R2)")
	}
	if !disp.declaresTools() {
		t.Error("dispatch-developer must declare an explicit tools allowlist (the config-lint; Amelia's scoped [read_workspace, apply_patch, measure_task, ask_human])")
	}
	if !disp.hasTriple("task.attempt.0") {
		t.Error("dispatch-developer must append task.attempt.0 AT SPAWN (R3) — the attempt counter the route counts against the budget")
	}
	if c, ok := disp.condition("coordinator.decision.next_action"); !ok || c.Value != "dev_from_task" {
		t.Error("dispatch-developer must fire on the coordinator's dev_from_task decision")
	}
	if c, ok := disp.condition("agent.run.entity_id"); !ok || c.Operator != "ne" {
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
	const marker = "dev.floors_dispatched"
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
//   - 06c retry: route.not_clean=true AND route.attempt.0 length_lt 3 → re-dispatch Amelia.
//   - 06d escalate: route.not_clean=true AND route.attempt.0 length_gt 2 → park.
//
// TOTALITY: advance covers (true,false); not_clean is its exact OR-complement; retry/escalate
// partition the count (length_lt 3 / length_gt 2, no gap). Red-first: break any and this fails.
func TestFloorsRouteTotalityAndSelfExtinguish(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "route.routed"

	adv, ok := rules["dev_from_task_route_advance"]
	if !ok {
		t.Fatal("missing dev_from_task_route_advance rule")
	}
	if c, ok := adv.condition("route.passed"); !ok || c.Operator != "eq" || c.Value != "true" || c.Required {
		t.Errorf("advance must require route.passed eq \"true\" (required:false — a scalar eq with required:true ERRORS on entities lacking the field), got %+v", c)
	}
	if c, ok := adv.condition("route.rejected"); !ok || c.Operator != "eq" || c.Value != "false" || c.Required {
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
		"dev_from_task_route_not_clean_red":   "route.passed",   // measured red
		"dev_from_task_route_not_clean_floor": "route.rejected", // a floor rejected
	}
	for id, signalField := range notCleanRules {
		nc, ok := rules[id]
		if !ok {
			t.Fatalf("missing %s rule (the not-clean OR-collapse is two guarded AND rules)", id)
		}
		if nc.Logic == "or" {
			t.Errorf("%s must be a GUARDED pure-AND rule, NOT logic:or — a guardless OR rule would re-fire and re-append route.not_clean unbounded", id)
		}
		if !nc.hasTriple("route.not_clean") {
			t.Errorf("%s must stamp route.not_clean (the intermediate the retry/escalate rules AND with the budget)", id)
		}
		if !nc.hasAbsenceGuard("route.not_clean") {
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
	if c, ok := ret.condition("route.not_clean"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("retry must require route.not_clean eq \"true\", got %+v", c)
	}
	if c, ok := ret.condition("route.attempt.0"); !ok || c.Operator != "length_lt" {
		t.Errorf("retry must require route.attempt.0 length_lt <budget> (budget remains), got %+v", c)
	}
	if !ret.hasAbsenceGuard(marker) || !ret.markerBeforePublish(marker) || !ret.hasTriple("task.attempt.0") {
		t.Error("retry must be self-extinguishing (route.routed before the developer publish) and append task.attempt.0 at spawn (R3)")
	}

	esc, ok := rules["dev_from_task_route_escalate"]
	if !ok {
		t.Fatal("missing dev_from_task_route_escalate rule")
	}
	if c, ok := esc.condition("route.not_clean"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Errorf("escalate must require route.not_clean eq \"true\", got %+v", c)
	}
	if c, ok := esc.condition("route.attempt.0"); !ok || c.Operator != "length_gt" {
		t.Errorf("escalate must require route.attempt.0 length_gt <budget-1> (fail-closed: catches an over-count, and partitions the count with retry's length_lt), got %+v", c)
	}
	if !esc.hasTriple("run.awaiting_human") || esc.firesTransition() {
		t.Error("escalate must park (run.awaiting_human) with NO lifecycle transition (G2)")
	}
	// The budget literals must partition the count with no gap: retry length_lt N, escalate
	// length_gt N-1. Assert they use the SAME budget so no count falls through both.
	retC, _ := ret.condition("route.attempt.0")
	escC, _ := esc.condition("route.attempt.0")
	retN, escN := toFloat(retC.Value), toFloat(escC.Value)
	if retN != escN+1 {
		t.Errorf("retry length_lt %v and escalate length_gt %v must partition the count with no gap (lt N, gt N-1) — a count could otherwise fall through both or match both", retN, escN)
	}
}

// The REVIEW ROUTE (dev-from-task/07a-d, the reshape group 5): fires on Quinn's review loop
// reading the route.verdict mirror.
//   - 07a approved → spawn verify_artifact.
//   - 07b changes_requested + budget → re-dispatch Amelia (D16 re-entry).
//   - 07c changes_requested + exhausted → park.
//   - 07d no-verdict (terminated, route.verdict absent) → park (fail-closed totality catch).
func TestReviewRouteTotalityAndSelfExtinguish(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "route.routed"

	app, ok := rules["dev_from_task_review_approved"]
	if !ok {
		t.Fatal("missing dev_from_task_review_approved rule")
	}
	if c, ok := app.condition("route.verdict"); !ok || c.Operator != "eq" || c.Value != "approved" || c.Required {
		t.Errorf("review-approved must require route.verdict eq \"approved\" (required:false), got %+v", c)
	}
	if !app.forcesFunction("verify_artifact") || !app.hasAbsenceGuard(marker) || !app.markerBeforePublish(marker) {
		t.Error("review-approved must force verify_artifact and be self-extinguishing (route.routed before the publish)")
	}

	ret, ok := rules["dev_from_task_review_retry"]
	if !ok {
		t.Fatal("missing dev_from_task_review_retry rule (D16 re-entry)")
	}
	if c, ok := ret.condition("route.verdict"); !ok || c.Value != "changes_requested" {
		t.Errorf("review-retry must fire on route.verdict changes_requested, got %+v", c)
	}
	if c, ok := ret.condition("route.attempt.0"); !ok || c.Operator != "length_lt" {
		t.Errorf("review-retry must require route.attempt.0 length_lt <budget> (the SHARED attempt budget, R4), got %+v", c)
	}
	if !ret.hasTriple("task.attempt.0") || !ret.markerBeforePublish(marker) {
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
	if c, ok := park.condition("route.attempt.0"); !ok || c.Operator != "length_gt" {
		t.Errorf("review-park must fire on route.attempt.0 length_gt <budget-1> (exhausted), got %+v", c)
	}
	if !park.hasTriple("run.awaiting_human") || park.firesTransition() {
		t.Error("review-park must park with no transition (G2)")
	}

	nv, ok := rules["dev_from_task_review_no_verdict"]
	if !ok {
		t.Fatal("missing dev_from_task_review_no_verdict rule (fail-closed totality catch)")
	}
	if c, ok := nv.condition("agent.loop.role"); !ok || c.Value != "reviewer" {
		t.Error("review-no-verdict must fire on the reviewer loop (route.verdict is absent, so role is the discriminator)")
	}
	if c, ok := nv.condition("route.verdict"); !ok || c.Operator != "length_eq" {
		t.Error("review-no-verdict must require route.verdict ABSENT (length_eq 0) — the terminated-without-a-verdict case")
	}
	if !nv.hasTriple("run.awaiting_human") {
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
	const marker = "delivery.routed"

	pr, ok := rules["dev_from_task_delivery_open_pr"]
	if !ok {
		t.Fatal("missing dev_from_task_delivery_open_pr rule")
	}
	if c, ok := pr.condition("verify.result"); !ok || c.Operator != "eq" || c.Value != "pass" || c.Required {
		t.Errorf("delivery-open-pr must require verify.result eq \"pass\" (required:false — absent = false, no premature fire), got %+v", c)
	}
	if c, ok := pr.condition("review.verdict.0"); !ok || c.Operator != "eq" || c.Value != "approved" {
		t.Errorf("delivery-open-pr must require review.verdict.0 eq \"approved\" (defense in depth), got %+v", c)
	}
	if c, ok := pr.condition("openspec.validated"); !ok || c.Operator != "length_gt" {
		t.Errorf("delivery-open-pr must require openspec.validated present (length_gt 0; a revision-match eq lands with #519's .value), got %+v", c)
	}
	if !pr.publishesTo("component.delivery-station.dispatch") || !pr.hasAbsenceGuard(marker) || !pr.markerBeforeStationPublish(marker) {
		t.Error("delivery-open-pr must publish the delivery station (component.delivery-station.dispatch, R6) and be self-extinguishing via a RUN-scoped delivery.routed BEFORE the publish (delivery is not idempotent)")
	}

	park, ok := rules["dev_from_task_delivery_park"]
	if !ok {
		t.Fatal("missing dev_from_task_delivery_park rule")
	}
	if c, ok := park.condition("verify.result"); !ok || c.Operator != "eq" || c.Value != "fail" {
		t.Errorf("delivery-park must fire on verify.result eq \"fail\" (mutually exclusive with the coherent route on the verify value; a \"retry\" matches neither), got %+v", c)
	}
	if !park.hasTriple("run.awaiting_human") || park.firesTransition() {
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

// TestDeliveryRoutedWithoutResultIsAKnownGap PINS the R6 station "routed-without-result"
// wedge (G6 — pin the failure shape when a fix is deliberately deferred). The delivery route
// (08a) stamps its self-extinguish delivery.routed marker BEFORE it publishes the
// delivery-station component; if that component then fails persistently (after the base's
// bounded retry), pr.ref never lands. Because the marker is set and rules are EDGE-triggered,
// nothing re-triggers, and no rule reconciles "delivery.routed set ∧ pr.ref absent" into a
// park (08b parks only on verify.result=fail) — the run does not auto-park at M0. This is a
// deliberate deferral to R8/group 8 (restart-safe reconstruction + effect idempotency).
//
// TRIPWIRE: this asserts the gap STILL EXISTS. When group 8 adds the reconciliation rule (a
// park gated on delivery.routed-present ∧ pr.ref-absent), this FAILS — prompting removal of
// the tripwire and of the honest-gap comments in internal/station/station.go, 08a's metadata,
// and internal/vocab/vocab.go.
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
			if c.Field == "delivery.routed" && c.Operator == "length_gt" {
				routedPresent = true
			}
			if c.Field == "pr.ref" && c.Operator == "length_eq" {
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
		"dev_from_task_dispatch_developer": true, // 04 — the initial dispatch
		"dev_from_task_route_retry":        true, // 06c — the floors-route retry
		"dev_from_task_review_retry":       true, // 07b — the review-route retry (D16)
	}
	var spawners []string
	for _, r := range rules {
		for _, a := range r.OnEnter {
			if a.Type != "publish_agent" || (a.Role != "developer" && a.Subject != "agent.task.developer") {
				continue
			}
			spawners = append(spawners, r.ID)
			if !sanctioned[r.ID] {
				t.Errorf("rule %q spawns a developer loop (role=%q subject=%q) but is not a sanctioned spawner — the one-developer-in-flight serialization invariant permits ONLY dispatch-developer (04), the floors-retry route (06c), and the review-retry route (07b)", r.ID, a.Role, a.Subject)
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

// run.awaiting_human is ONE logical park writer (G5) realized by a SANCTIONED SET of rule
// files. Every rule that stamps it must be in the set (rules carry no Source, so the
// tool-Source cross-check can't see them — this pin keeps the realizations honest).
func TestOnlySanctionedParkWriters(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	sanctioned := map[string]bool{
		"run_park_awaiting_human":         true, // run-lifecycle/03 — a coordinator ask_human decision
		"sandbox_park_unprovable":         true, // sandbox/02 — an unprovable sandbox
		"dev_from_task_route_escalate":    true, // 06d — the dev-loop budget exhausted
		"dev_from_task_review_park":       true, // 07c — the review budget exhausted
		"dev_from_task_review_no_verdict": true, // 07d — the reviewer produced no verdict
		"dev_from_task_delivery_park":     true, // 08b — the delivery signals do not cohere
	}
	var writers []string
	for _, r := range rules {
		if r.hasTriple("run.awaiting_human") {
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
	if c, ok := park.condition("sandbox.blocked"); !ok || c.Operator != "ne" {
		t.Error("sandbox park must fire on sandbox.blocked ne \"\" (an unprovable sandbox)")
	}
	if !park.hasTriple("run.awaiting_human") {
		t.Error("sandbox park must stamp run.awaiting_human (the park marker the whole system reads)")
	}
	if !park.hasAbsenceGuard("run.awaiting_human") {
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
	if c, ok := park.condition("coordinator.decision.next_action"); !ok || c.Value != "ask_human" {
		t.Error("park rule must fire on next_action == ask_human")
	}
	if !park.hasTriple("run.awaiting_human") {
		t.Error("park rule must stamp run.awaiting_human (G5 single writer = park-rule)")
	}
	// The run-anchor guard (semteams agent-run/07): without a run anchor the
	// subject-override would not resolve and the marker would be silently dropped.
	if c, ok := park.condition("agent.run.entity_id"); !ok || c.Operator != "ne" {
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
		if r.clearsPredicate("run.awaiting_human") {
			continue // resume-from-park rule: legitimately fires on a parked run to un-park it
		}
		if !r.hasAbsenceGuard("run.awaiting_human") {
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
	if unguarded.hasAbsenceGuard("run.awaiting_human") || unguarded.clearsPredicate("run.awaiting_human") {
		t.Error("unguarded transition rule wrongly treated as guarded/exempt — pin would not fire")
	}

	resume := ruleFile{ID: "resume", OnEnter: []ruleAction{
		{Type: "remove_triple", Predicate: "run.awaiting_human"},
		{Type: "lifecycle_transition", Workflow: "agent-run", Phase: "executing"},
	}}
	if !resume.clearsPredicate("run.awaiting_human") {
		t.Error("resume-from-park rule not recognized as clearing the marker")
	}

	guarded := ruleFile{
		ID:         "guarded",
		Conditions: []ruleCondition{{Field: "run.awaiting_human", Operator: "length_eq", Value: float64(0)}},
		OnEnter:    []ruleAction{{Type: "lifecycle_transition"}},
	}
	if !guarded.hasAbsenceGuard("run.awaiting_human") {
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
		{Field: "agent.run.entity_id", Operator: "length_gt", Value: float64(1)},
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
	if _, ok := archive.condition("openspec.archived"); !ok {
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
	for _, role := range []string{"coordinator", "developer", "reviewer"} {
		dir := filepath.Join(root, "configs", "personas", "fragments", role)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("persona fragment dir for role %q missing at %s", role, dir)
		}
	}
}
