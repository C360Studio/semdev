package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// D15 forward-contract #0 — the change-approval gate must require openspec.validated
// to EQUAL the run's current content revision (openspec.change.revision), not merely
// be present. Without this field-to-field guard, a rework re-author (which bumps the
// revision) could reach the human gate on the PRIOR version's validation — the
// stale-same-slug false-green. The value must be the $entity.triple substitution of
// the run-level, slug-INDEPENDENT revision (the gate is slug-blind). Red-first:
// dropping the condition, or pointing it at a slug-scoped/literal value, fails.
func TestChangeApprovalGateRequiresContentRevisionMatch(t *testing.T) {
	offer, ok := runLifecycleRules(t)["run_offer_change_approval"]
	if !ok {
		t.Fatal("missing run_offer_change_approval rule")
	}
	const wantValue = "$entity.triple.openspec.change.revision"
	found := false
	for _, c := range offer.Conditions {
		if c.Field == "openspec.validated" && c.Operator == "eq" {
			if c.Value != wantValue {
				t.Errorf("content-revision guard value = %v, want %q (slug-independent, so the slug-blind gate can compare it)", c.Value, wantValue)
			}
			found = true
		}
	}
	if !found {
		t.Error("offer-approval must require openspec.validated eq $entity.triple.openspec.change.revision — else a re-authored change reaches the gate on a stale validation (D15 #0)")
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
