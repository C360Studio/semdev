package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
// dropped. (The sibling spawn rules coordinator/02, coordinator/03, and the park
// rule are NOT yet self-extinguishing — tracked in design.md as a pre-production
// hardening carry-forward; when the dev-loop rail adds more publish_agent spawns
// they must follow this pattern.)
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
		t.Errorf("projection spawn must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate projection loop (publish_agent is not idempotent)", marker)
	}
	if !proj.hasTriple(marker) {
		t.Errorf("projection spawn must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !proj.forcesFunction("project_tasks") {
		t.Error("projection spawn must force the project_tasks call (tool_choice mode=function, function_name=project_tasks)")
	}
	// It fires on approval and needs the run anchor (dev-from-task/01) for
	// run_scope=inherit — assert both so the trigger is grounded.
	if c, ok := proj.condition("run.change_approved"); !ok || c.Operator != "eq" || c.Value != "true" {
		t.Error("projection spawn must fire on run.change_approved == true (the spec trigger: approval projects task.spec)")
	}
	if c, ok := proj.condition("agent.run"); !ok || c.Operator != "ne" {
		t.Error("projection spawn must require the agent.run anchor (ne \"\") so run_scope=inherit binds the loop to this run")
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
	// approval — assert the producer exists and forces project_tasks.
	proj, ok := runLifecycleRules(t)["dev_from_task_project_tasks"]
	if !ok || !proj.forcesFunction("project_tasks") {
		t.Error("the projection gate has no producer: dev_from_task_project_tasks must exist and force project_tasks, or task.spec.0.test_command never becomes present and the dev loop deadlocks")
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

// The dispatch station (dev-from-task/04): on a coordinator's dev_from_task decision,
// spawn Amelia to author the fix via apply_patch. Like every publish_agent spawn rule
// it MUST be self-extinguishing — but LOOP-scoped: it fires on the coordinator loop
// carrying the decision (conditions can't read run facts), so the fired-once marker
// dev.dispatched is stamped on that loop, guarded by length_eq 0. Unlike coordinator/02
// (create_change, idempotent-replace, intentionally re-fires), re-dispatching Amelia
// double-authors, so this MUST self-extinguish. It must force the apply_patch call.
func TestDispatchDeveloperIsSelfExtinguishing(t *testing.T) {
	disp, ok := runLifecycleRules(t)["dev_from_task_dispatch_developer"]
	if !ok {
		t.Fatal("missing dev_from_task_dispatch_developer rule")
	}
	const marker = "dev.dispatched"
	if !disp.hasAbsenceGuard(marker) {
		t.Errorf("dispatch-developer must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate developer (publish_agent is not idempotent)", marker)
	}
	if !disp.hasTriple(marker) {
		t.Errorf("dispatch-developer must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !disp.markerBeforePublish(marker) {
		t.Errorf("dispatch-developer must stamp %s BEFORE its publish_agent (SB7) — else a publish failure leaves the run duplicable", marker)
	}
	if !disp.forcesFunction("apply_patch") {
		t.Error("dispatch-developer must force the apply_patch call (tool_choice mode=function, function_name=apply_patch)")
	}
	if c, ok := disp.condition("coordinator.decision.next_action"); !ok || c.Value != "dev_from_task" {
		t.Error("dispatch-developer must fire on the coordinator's dev_from_task decision")
	}
	if c, ok := disp.condition("agent.run.entity_id"); !ok || c.Operator != "ne" {
		t.Error("dispatch-developer must require the run anchor (agent.run.entity_id ne \"\") so run_scope=inherit binds Amelia to the run")
	}
}

// The measure station (dev-from-task/05, group 7B): on the developer loop's
// SUCCESSFUL terminal, measure the task in-container and count the attempt. Like
// every publish_agent spawn rule it MUST be self-extinguishing — LOOP-scoped (it
// fires on the developer loop): a fired-once dev.measured marker stamped BEFORE the
// publish, guarded by length_eq 0. It must fire ONLY on outcome=success (never
// measure a failed/unapplied attempt), force measure_task, and append the per-task
// attempt counter (task.attempt.0). Red-first: drop any of these and this fails.
func TestMeasureTriggerIsSelfExtinguishing(t *testing.T) {
	meas, ok := runLifecycleRules(t)["dev_from_task_measure_developed"]
	if !ok {
		t.Fatal("missing dev_from_task_measure_developed rule")
	}
	const marker = "dev.measured"
	if !meas.hasAbsenceGuard(marker) {
		t.Errorf("measure trigger must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate measure loop (publish_agent is not idempotent)", marker)
	}
	if !meas.hasTriple(marker) {
		t.Errorf("measure trigger must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !meas.markerBeforePublish(marker) {
		t.Errorf("measure trigger must stamp %s BEFORE its publish_agent — else a publish failure leaves the run duplicable", marker)
	}
	if !meas.forcesFunction("measure_task") {
		t.Error("measure trigger must force the measure_task call (tool_choice mode=function, function_name=measure_task)")
	}
	// It must fire on the developer loop's SUCCESSFUL terminal — outcome=success
	// excludes a failed apply (measuring an unapplied checkout is theater).
	if c, ok := meas.condition("agent.loop.role"); !ok || c.Value != "developer" {
		t.Error("measure trigger must fire on a developer loop (agent.loop.role == developer)")
	}
	if c, ok := meas.condition("agent.loop.outcome"); !ok || c.Value != "success" {
		t.Error("measure trigger must fire ONLY on agent.loop.outcome == success — a failed apply must not be measured")
	}
	// It must append the per-task attempt counter so the group-7D budget gate can count it.
	if !meas.hasTriple("task.attempt.0") {
		t.Error("measure trigger must append task.attempt.0 (the attempt counter the budget/retry gate counts against task.spec.0.budget)")
	}
}

// The floors station (dev-from-task/06, group 7C): on the MEASURE loop terminal
// (chained via the dev.measure_done loop marker, warn-free — NOT a field-to-field run
// fact, #519), run the structural floors. Like every publish_agent spawn rule it MUST
// be self-extinguishing — LOOP-scoped: a fired-once dev.floors_dispatched marker
// stamped BEFORE the publish, guarded by length_eq 0. It must fire on the marker (not
// re-derive), force check_floors, and require role=coordinator to distinguish the
// measure loop. Red-first: drop any and this fails.
func TestFloorsTriggerIsSelfExtinguishing(t *testing.T) {
	fl, ok := runLifecycleRules(t)["dev_from_task_check_floors"]
	if !ok {
		t.Fatal("missing dev_from_task_check_floors rule")
	}
	const marker = "dev.floors_dispatched"
	if !fl.hasAbsenceGuard(marker) {
		t.Errorf("floors trigger must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate floors loop (publish_agent is not idempotent)", marker)
	}
	if !fl.hasTriple(marker) {
		t.Errorf("floors trigger must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !fl.markerBeforePublish(marker) {
		t.Errorf("floors trigger must stamp %s BEFORE its publish_agent — else a publish failure leaves the run duplicable", marker)
	}
	if !fl.forcesFunction("check_floors") {
		t.Error("floors trigger must force the check_floors call (tool_choice mode=function, function_name=check_floors)")
	}
	// It fires on the MEASURE loop, distinguished by the tool-stamped dev.measure_done
	// marker (present only on the measure loop) — NOT a field-to-field condition (#519).
	if c, ok := fl.condition("dev.measure_done"); !ok || c.Operator != "ne" {
		t.Error("floors trigger must fire on the measure loop's dev.measure_done marker (ne \"\") — the warn-free loop-marker chain")
	}
	if c, ok := fl.condition("agent.loop.role"); !ok || c.Value != "coordinator" {
		t.Error("floors trigger must require agent.loop.role == coordinator (the measure loop is a coordinator loop; the marker distinguishes it from other coordinator loops)")
	}
	// The trigger is honest only if measure_task actually stamps dev.measure_done. That
	// producer is measure_task (a tool), covered by its own unit pin; here we assert the
	// condition value form stays warn-free (no $entity.triple field-to-field, #519).
	if c, ok := fl.condition("dev.measure_done"); ok {
		if s, isStr := c.Value.(string); isStr && strings.Contains(s, "$entity.triple.") {
			t.Errorf("floors trigger condition on dev.measure_done uses the warn-flooding $entity.triple form (#519) — the loop-marker chain must compare to a literal")
		}
	}
}

// The gate station (dev-from-task/07, group 7D): on the FLOORS loop terminal (chained
// via the dev.floors_done loop marker, warn-free — NOT a field-to-field run fact,
// #519), force check_gate. Like every publish_agent spawn rule it MUST be
// self-extinguishing — LOOP-scoped: a fired-once dev.gate_dispatched marker stamped
// BEFORE the publish, guarded by length_eq 0. It must fire on the marker (not
// re-derive), force check_gate, and require role=coordinator. Red-first: drop any and
// this fails.
func TestGateTriggerIsSelfExtinguishing(t *testing.T) {
	g, ok := runLifecycleRules(t)["dev_from_task_gate_decision"]
	if !ok {
		t.Fatal("missing dev_from_task_gate_decision rule")
	}
	const marker = "dev.gate_dispatched"
	if !g.hasAbsenceGuard(marker) {
		t.Errorf("gate trigger must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate gate loop (publish_agent is not idempotent)", marker)
	}
	if !g.hasTriple(marker) {
		t.Errorf("gate trigger must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !g.markerBeforePublish(marker) {
		t.Errorf("gate trigger must stamp %s BEFORE its publish_agent — else a publish failure leaves the run duplicable", marker)
	}
	if !g.forcesFunction("check_gate") {
		t.Error("gate trigger must force the check_gate call (tool_choice mode=function, function_name=check_gate)")
	}
	// Fires on the FLOORS loop, distinguished by the tool-stamped dev.floors_done marker
	// (present only on the floors loop) — NOT a field-to-field condition (#519).
	if c, ok := g.condition("dev.floors_done"); !ok || c.Operator != "ne" {
		t.Error("gate trigger must fire on the floors loop's dev.floors_done marker (ne \"\") — the warn-free loop-marker chain")
	}
	if c, ok := g.condition("agent.loop.role"); !ok || c.Value != "coordinator" {
		t.Error("gate trigger must require agent.loop.role == coordinator (the floors loop is a coordinator loop; the marker distinguishes it)")
	}
	if c, ok := g.condition("dev.floors_done"); ok {
		if s, isStr := c.Value.(string); isStr && strings.Contains(s, "$entity.triple.") {
			t.Errorf("gate trigger condition on dev.floors_done uses the warn-flooding $entity.triple form (#519) — the loop-marker chain must compare to a literal")
		}
	}
}

// The gate routers (dev-from-task/08a/b/c, group 7D): three rules firing on the
// check_gate loop, mutually exclusive on the single dev.gate_decision value (literal
// eq — snapshot-race-immune, #519-immune). EVERY router must be self-extinguishing via
// the shared LOOP-scoped dev.routed marker (critical for the retry router, whose
// publish_agent is not idempotent — a re-fire spawns a duplicate developer), stamped
// BEFORE any publish. Red-first: drop the guard/marker/eq on any and this fails.
func TestGateRoutersAreSelfExtinguishing(t *testing.T) {
	rules := runLifecycleRules(t)
	const marker = "dev.routed"
	routers := []struct {
		id       string
		decision string
	}{
		{"dev_from_task_route_advance", "advance"},
		{"dev_from_task_route_retry", "retry"},
		{"dev_from_task_route_escalate", "escalate"},
	}
	for _, rt := range routers {
		r, ok := rules[rt.id]
		if !ok {
			t.Fatalf("missing %s rule", rt.id)
		}
		if !r.hasAbsenceGuard(marker) {
			t.Errorf("%s must guard on %s length_eq 0 (fired-once) — else a re-scan re-fires the router (the retry router would spawn a duplicate developer)", rt.id, marker)
		}
		if !r.hasTriple(marker) {
			t.Errorf("%s must add_triple %s in on_enter to extinguish its own trigger", rt.id, marker)
		}
		if !r.markerBeforePublish(marker) && rt.decision == "retry" {
			t.Errorf("%s (a publish_agent router) must stamp %s BEFORE its publish_agent — else a publish failure leaves the run duplicable", rt.id, marker)
		}
		// Each router matches exactly one decision value via a LITERAL eq (no
		// field-to-field #519 form, and mutually exclusive on the single value).
		c, ok := r.condition("dev.gate_decision")
		if !ok || c.Operator != "eq" || c.Value != rt.decision {
			t.Errorf("%s must fire on dev.gate_decision eq %q (literal, #519-immune), got %+v", rt.id, rt.decision, c)
		}
		if s, isStr := c.Value.(string); isStr && strings.Contains(s, "$entity.triple.") {
			t.Errorf("%s condition on dev.gate_decision uses the warn-flooding $entity.triple form (#519)", rt.id)
		}
		if c, ok := r.condition("agent.loop.role"); !ok || c.Value != "coordinator" {
			t.Errorf("%s must require agent.loop.role == coordinator (it fires on the coordinator gate loop)", rt.id)
		}
	}

	// The advance router hands off to clean-room verify; the escalate router parks; the
	// retry router re-dispatches. Assert each router's substantive action.
	if adv, ok := rules["dev_from_task_route_advance"]; ok && !adv.hasTriple("dev.task_cleared.0") {
		t.Error("advance router must stamp dev.task_cleared.0 (the signal the clean-room verify station chains on)")
	}
	if esc, ok := rules["dev_from_task_route_escalate"]; ok {
		if !esc.hasTriple("run.awaiting_human") {
			t.Error("escalate router must stamp run.awaiting_human (park the exhausted task toward the human)")
		}
		if esc.firesTransition() {
			t.Error("escalate router must fire NO lifecycle transition (G2) — it records a fact and posts to the user bus")
		}
	}
	if ret, ok := rules["dev_from_task_route_retry"]; ok && !ret.forcesFunction("apply_patch") {
		t.Error("retry router must re-dispatch a developer forced to apply_patch (re-arm the whole chain)")
	}
}

// The SERIALIZATION INVARIANT (group-7D, load-bearing): "one developer loop in flight
// per task; ONLY dispatch-developer (dev-from-task/04) and the gate's retry branch
// (dev-from-task/08b) may spawn a role=developer loop into a run." Dropping the token
// handshake made this the guarantee that measure and floors evaluated the SAME frozen
// checkout — a third developer-spawner would let two attempts race the shared checkout
// and mis-count the budget. This pin asserts EXACTLY those two spawners exist. Red-first:
// add a role=developer publish_agent to any other rule and this fails.
func TestOnlySanctionedDeveloperSpawners(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	sanctioned := map[string]bool{
		"dev_from_task_dispatch_developer": true, // 04 — the initial dispatch
		"dev_from_task_route_retry":        true, // 08b — the gate's retry branch
	}
	var spawners []string
	for _, r := range rules {
		for _, a := range r.OnEnter {
			// A developer spawn is either an explicit role=developer OR the developer
			// task subject (agent.task.developer) — catch BOTH so a rule that omits the
			// explicit role but targets the developer subject cannot evade the invariant
			// (defense in depth on a load-bearing pin).
			if a.Type != "publish_agent" || (a.Role != "developer" && a.Subject != "agent.task.developer") {
				continue
			}
			spawners = append(spawners, r.ID)
			if !sanctioned[r.ID] {
				t.Errorf("rule %q spawns a developer loop (role=%q subject=%q) but is not a sanctioned spawner — the one-developer-in-flight serialization invariant (which replaced the token handshake) permits ONLY dispatch-developer (04) and the gate retry router (08b)", r.ID, a.Role, a.Subject)
			}
		}
	}
	// And both sanctioned spawners must actually exist (a rename must not silently drop
	// the retry path, leaving the loop unable to retry).
	for id := range sanctioned {
		if !slices.Contains(spawners, id) {
			t.Errorf("sanctioned developer-spawner %q does not spawn a role=developer loop — the retry/dispatch chain is broken", id)
		}
	}
}

// The review station (dev-from-task/09, group 8C): on the gate loop's ADVANCE decision,
// Quinn reviews the cleared task. Like every publish_agent spawn rule it MUST be
// self-extinguishing — LOOP-scoped: a fired-once dev.review_dispatched marker stamped
// BEFORE the publish, guarded by length_eq 0. It must fire on the advance decision (a
// literal eq loop marker), force submit_review, and spawn a role=reviewer loop (Quinn's
// D16 gate). Red-first: drop any and this fails.
func TestReviewTriggerIsSelfExtinguishing(t *testing.T) {
	r, ok := runLifecycleRules(t)["dev_from_task_review_cleared"]
	if !ok {
		t.Fatal("missing dev_from_task_review_cleared rule")
	}
	const marker = "dev.review_dispatched"
	if !r.hasAbsenceGuard(marker) {
		t.Errorf("review trigger must guard on %s length_eq 0 (fired-once) — else a graph replay with RULE_STATE lost re-spawns a duplicate review loop (publish_agent is not idempotent)", marker)
	}
	if !r.hasTriple(marker) {
		t.Errorf("review trigger must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !r.markerBeforePublish(marker) {
		t.Errorf("review trigger must stamp %s BEFORE its publish_agent — else a publish failure leaves the run duplicable", marker)
	}
	if !r.forcesFunction("submit_review") {
		t.Error("review trigger must force the submit_review call (tool_choice mode=function, function_name=submit_review)")
	}
	// It fires on the gate loop's ADVANCE decision (only a cleared task is reviewed).
	if c, ok := r.condition("dev.gate_decision"); !ok || c.Operator != "eq" || c.Value != "advance" {
		t.Errorf("review trigger must fire on dev.gate_decision eq advance (a cleared task), got %+v", c)
	}
	if c, ok := r.condition("agent.loop.role"); !ok || c.Value != "coordinator" {
		t.Error("review trigger must fire on the coordinator gate loop (agent.loop.role == coordinator)")
	}
	// It spawns a role=reviewer loop — Quinn's D16 gate, the one non-coordinator role.
	spawnsReviewer := false
	for _, a := range r.OnEnter {
		if a.Type == "publish_agent" && a.Role == "reviewer" {
			spawnsReviewer = true
		}
	}
	if !spawnsReviewer {
		t.Error("review trigger must spawn a role=reviewer loop (Quinn reviews, D16)")
	}
}

// The verify station (dev-from-task/10, group 8D): on the REVIEW loop terminal (chained
// via the dev.reviewed loop marker, warn-free), run the clean-room cold verify. Like every
// publish_agent spawn rule it MUST be self-extinguishing — LOOP-scoped: a fired-once
// dev.verify_dispatched marker stamped BEFORE the publish, guarded by length_eq 0. It must
// fire on the reviewed marker, force verify_artifact, and require role=reviewer (the review
// loop). Red-first: drop any and this fails.
func TestVerifyTriggerIsSelfExtinguishing(t *testing.T) {
	v, ok := runLifecycleRules(t)["dev_from_task_verify_reviewed"]
	if !ok {
		t.Fatal("missing dev_from_task_verify_reviewed rule")
	}
	const marker = "dev.verify_dispatched"
	if !v.hasAbsenceGuard(marker) {
		t.Errorf("verify trigger must guard on %s length_eq 0 (fired-once) — else a graph replay re-spawns a duplicate verify loop", marker)
	}
	if !v.hasTriple(marker) {
		t.Errorf("verify trigger must add_triple %s in on_enter to extinguish its own trigger", marker)
	}
	if !v.markerBeforePublish(marker) {
		t.Errorf("verify trigger must stamp %s BEFORE its publish_agent", marker)
	}
	if !v.forcesFunction("verify_artifact") {
		t.Error("verify trigger must force the verify_artifact call (tool_choice mode=function, function_name=verify_artifact)")
	}
	// Fires on the REVIEW loop, distinguished by the tool-stamped dev.reviewed marker.
	if c, ok := v.condition("dev.reviewed"); !ok || c.Operator != "ne" {
		t.Error("verify trigger must fire on the review loop's dev.reviewed marker (ne \"\") — the warn-free loop-marker chain")
	}
	if c, ok := v.condition("agent.loop.role"); !ok || c.Value != "reviewer" {
		t.Error("verify trigger must require agent.loop.role == reviewer (the review loop; verify chains off Quinn's review)")
	}
	if c, ok := v.condition("dev.reviewed"); ok {
		if s, isStr := c.Value.(string); isStr && strings.Contains(s, "$entity.triple.") {
			t.Errorf("verify trigger condition on dev.reviewed uses the warn-flooding $entity.triple form (#519)")
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
