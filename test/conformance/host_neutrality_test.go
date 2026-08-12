package conformance

import "testing"

// Host-neutrality (T7, forge-io 5.8) — the arc reacts only to normalized facts, so
// NO rule may name a code host in a predicate position (a condition field or an
// action subject/predicate). Host-awareness lives in the Go forge adapter; a host
// name in a rule couples the arc to GitHub and breaks the "a second adapter
// requires no arc change" contract. This build-failing pin guards that boundary as
// the forge-io rules land.
func TestNoArcRuleReferencesHostSpecificField(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no rules loaded; the host-neutrality pin would pass vacuously")
	}
	for _, v := range hostSpecificRuleRefs(rules) {
		t.Error(v)
	}
}

// Red-first: the census must fire on a rule that names a host in a condition field
// and on one that names a host in an action subject/predicate, so a real coupling
// cannot slip through — while a normalized-fact rule stays clean.
func TestHostNeutralityCensusCatchesHostRefs(t *testing.T) {
	bad := []ruleFile{
		{ID: "reads-host-field", Conditions: []ruleCondition{{Field: "github.issue.state", Operator: "eq", Value: "open"}}},
		{ID: "writes-host-subject", OnEnter: []ruleAction{{Type: "add_triple", Subject: "org.github.repo.x.issue.1", Predicate: "run.issue.ref", Object: "x#1"}}},
		{ID: "writes-host-predicate", OnEnter: []ruleAction{{Type: "add_triple", Subject: "$entity.id", Predicate: "github.issue.title", Object: "t"}}},
		{ID: "dispatches-host-tool", OnEnter: []ruleAction{{Type: "publish_agent", Tools: []string{"decide", "github_list_comments"}}}},
	}
	if got := hostSpecificRuleRefs(bad); len(got) != 4 {
		t.Errorf("census caught %d of 4 planted host refs: %v", len(got), got)
	}

	clean := []ruleFile{{
		ID:         "neutral",
		Conditions: []ruleCondition{{Field: "coordinator.decision.next-action", Operator: "eq", Value: "issue_intake"}},
		OnEnter:    []ruleAction{{Type: "add_triple", Subject: "$entity.triple.agent.run.entity_id", Predicate: "run.issue.ref", Object: "$entity.triple.intake.issue_ref"}},
	}}
	if got := hostSpecificRuleRefs(clean); len(got) != 0 {
		t.Errorf("census flagged a host-neutral rule: %v", got)
	}
}
