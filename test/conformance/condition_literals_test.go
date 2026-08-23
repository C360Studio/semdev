package conformance

import (
	"sort"
	"testing"

	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/tools/submitreview"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/agentic/agentrun"
)

// agentRunPhases derives the legal run-phase vocabulary from the framework's own
// agent-run workflow declaration — every phase that is the source or the target of
// a declared transition. Sourced, never typed: a phase renamed upstream fails this
// pin at the next bump rather than silently wedging a gate whose guard no longer
// matches anything.
func agentRunPhases() []string {
	seen := map[string]bool{}
	for from, tos := range agentrun.WorkflowDeclaration().Transitions {
		seen[from] = true
		for _, to := range tos {
			seen[to] = true
		}
	}
	out := make([]string, 0, len(seen))
	for phase := range seen {
		out = append(out, phase)
	}
	sort.Strings(out)
	return out
}

// spacesForRepo builds the census from the writers' own constants plus the two
// derived sets.
func spacesForRepo(t *testing.T, rules []ruleFile) map[string]valueSpace {
	t.Helper()
	return conditionValueSpaces(
		agentRunPhases(),
		spawnRolesInPack(rules),
		[]string{submitreview.VerdictApproved, submitreview.VerdictChangesRequested},
		[]string{admission.DecisionApprove, admission.DecisionReject},
		[]string{string(conversationintent.Approve), string(conversationintent.Reject), string(conversationintent.None)},
		[]string{string(verify.OutcomePass), string(verify.OutcomeFail), string(verify.OutcomeRetry)},
	)
}

// Every literal a rule matches on comes from the vocabulary its writer produces.
// This is semdev's guard against the silently-false condition: a declared
// predicate, a real writer, and a literal the writer never stamps. The engine
// already rejects an undeclared FIELD at rule load; nothing checks the VALUE.
func TestConditionLiteralsComeFromWriterVocabularies(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	spaces := spacesForRepo(t, rules)
	violations, checked := conditionLiteralViolations(rules, spaces)
	for _, v := range violations {
		t.Error(v)
	}
	if checked == 0 {
		t.Fatal("no value-matched conditions were examined; the census would pass vacuously " +
			"(a broken loader and a clean pack look identical from here)")
	}
	t.Logf("census examined %d value-matched conditions across %d censused fields", checked, len(spaces))

	// A census entry no rule exercises is either cruft or a field that quietly left
	// the pack. Both want a decision, not a silent carry.
	exercised := map[string]bool{}
	for _, r := range rules {
		for _, c := range r.Conditions {
			if lit, ok := c.Value.(string); ok && lit != "" && (c.Operator == "eq" || c.Operator == "ne") {
				exercised[c.Field] = true
			}
		}
	}
	for field := range spaces {
		if !exercised[field] {
			t.Errorf("censused field %q is matched by no rule — remove the entry, or move it to "+
				"uncensusedValueFields with the reason it is kept", field)
		}
	}
}

// Red-first: the census must fire on an illegal literal and on a value-matched
// field nobody censused, and must stay silent on the legal and the excused. A pin
// that has never been observed failing proves nothing about the property it names —
// three defects this repo shipped had passing tests that agreed with the mistake.
func TestConditionLiteralCensusCatchesViolations(t *testing.T) {
	// A pack that dispatches one developer, so agent.loop.role has a derived set.
	pack := []ruleFile{{
		ID:      "spawns-developer",
		OnEnter: []ruleAction{{Type: "publish_agent", Role: "developer"}},
	}}
	spaces := spacesForRepo(t, pack)

	cases := []struct {
		name  string
		rule  ruleFile
		want  int
		match string
	}{
		{
			name: "illegal literal on a censused field",
			rule: ruleFile{ID: "bad-verdict", Conditions: []ruleCondition{
				{Field: "route.review.verdict", Operator: "eq", Value: "passed"}}},
			want:  1,
			match: "is not in the vocabulary its writer produces",
		},
		{
			name: "legal literal on a censused field",
			rule: ruleFile{ID: "good-verdict", Conditions: []ruleCondition{
				{Field: "route.review.verdict", Operator: "eq", Value: submitreview.VerdictApproved}}},
			want: 0,
		},
		{
			name: "role no spawn action dispatches",
			rule: ruleFile{ID: "ghost-role", Conditions: []ruleCondition{
				{Field: "agent.loop.role", Operator: "eq", Value: "archivist"}}},
			want:  1,
			match: "is not in the vocabulary its writer produces",
		},
		{
			name: "value-matched field nobody censused",
			rule: ruleFile{ID: "uncensused", Conditions: []ruleCondition{
				{Field: "delivery.pr.ref", Operator: "eq", Value: "anything"}}},
			want:  1,
			match: "neither censused in conditionValueSpaces nor listed in uncensusedValueFields",
		},
		{
			name: "explicitly excused field",
			rule: ruleFile{ID: "excused", Conditions: []ruleCondition{
				{Field: "agent.loop.outcome", Operator: "ne", Value: "cancelled"}}},
			want: 0,
		},
		{
			name: "presence test, not a vocabulary match",
			rule: ruleFile{ID: "presence", Conditions: []ruleCondition{
				{Field: "route.attempt.routed", Operator: "eq", Value: ""}}},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := conditionLiteralViolations([]ruleFile{tc.rule}, spaces)
			if len(got) != tc.want {
				t.Fatalf("got %d violations, want %d: %v", len(got), tc.want, got)
			}
			if tc.match != "" && !containsSubstring(got[0], tc.match) {
				t.Errorf("violation %q does not explain the failure (want it to mention %q)", got[0], tc.match)
			}
		})
	}
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
