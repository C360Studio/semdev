package conformance

import (
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
// a listed-but-missing path boots a processor that silently drops a rule.
func TestRulesFilesResolve(t *testing.T) {
	root := repoRoot(t)
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	if len(cfg.Components.Rule.Config.RulesFiles) == 0 {
		t.Fatal("bootstrap lists no rules_files")
	}
	for _, rel := range cfg.Components.Rule.Config.RulesFiles {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("rules_files entry %q does not resolve: %v", rel, err)
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
