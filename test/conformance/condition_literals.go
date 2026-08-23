package conformance

import (
	"fmt"
	"slices"
	"sort"
)

// The silently-false condition — a rule that is loaded, enabled, wired, and whose
// condition can never match — is the failure class semstreams has now hit three
// times (#1043, #1055, and the shipped architect-editor rule that never fired for
// months). semdev is structurally immune to two thirds of it:
//
//   - an UNDECLARED condition field fails at rule load, in the engine
//     (processor/rule/config_validation.go calls RequireDeclaredPredicate on every
//     condition field), so a typo'd predicate cannot boot;
//   - a rule file that is never loaded is caught by TestEveryRuleFileIsBootstrapped.
//
// The third third is the VALUE. A condition may name a declared predicate that a
// real writer stamps and still match a literal the writer never produces —
// `verify.cleanroom.result eq "passed"` when the writer stamps "pass". Nothing
// rejects it: the predicate is declared, the rule loads, the condition is simply
// false forever. This is semdev's instance of semstreams #1057 (four exposed
// classification fields document a closed vocabulary that nothing validates).
//
// This census closes it, and the load-bearing property is WHERE THE LEGAL VALUES
// COME FROM: every set below is read from the writer's own exported constants, or
// derived from the rule pack itself. None is typed out here. A census that
// restated the literals would agree with any mistake the rules already contain —
// it would assert that "approved" equals "approved" and prove nothing. Rename
// submitreview.VerdictApproved and this pin goes red; that is the whole point.

// valueSpace is the closed vocabulary a condition field's literals must come from.
type valueSpace struct {
	legal []string
	// source names where `legal` was read from, so a failure message points at the
	// authority rather than at this file.
	source string
	// constantBacked is false for the boolean-shaped facts, whose "true"/"false"
	// are written as bare literals with no named constant to bind to. They are
	// still censused — a typo'd "ture" is caught — but a writer that switched to
	// "yes"/"no" would NOT be, and that blind spot is named here rather than left
	// for a reader to discover (G7).
	constantBacked bool
}

// booleanFactSpace is the shared vocabulary of the boolean-shaped route/sandbox
// mirror facts.
func booleanFactSpace(writer string) valueSpace {
	return valueSpace{
		legal:          []string{"true", "false"},
		source:         writer + " (boolean-shaped fact; literal, not a named constant)",
		constantBacked: false,
	}
}

// uncensusedValueFields are value-matched condition fields deliberately outside
// this census, each with the reason. A field that is neither censused nor listed
// here is a VIOLATION — that is what stops a newly added value-matched field from
// joining the rules unpinned, which is how the class arrives in the first place.
var uncensusedValueFields = map[string]string{
	"coordinator.decision.next-action": "already pinned by TestDecisionConditionsUseTheLegalEnum against the " +
		"closed action taxonomy — censusing it here would duplicate that coverage, and two pins on one " +
		"property drift apart",
	"agent.loop.outcome": "FRAMEWORK-owned value space that nothing upstream validates — semstreams #1057 " +
		"records that LoopCompletedEvent.Outcome carries documented constants with no validator, while three " +
		"sibling fields in the same package do validate theirs. semdev cannot source a legal set from a " +
		"vocabulary the framework does not enforce; re-evaluate when #1057 closes",
}

// conditionValueSpaces assembles the census. Callers supply the two sets that are
// derived rather than imported: runPhases from the framework's agent-run workflow
// declaration, and spawnRoles from the rule pack's own spawn actions.
func conditionValueSpaces(runPhases, spawnRoles []string,
	reviewVerdicts, changeDecisions, conversationIntents, cleanroomOutcomes []string) map[string]valueSpace {
	return map[string]valueSpace{
		// Framework-owned but framework-DECLARED: the agent-run workflow's own
		// transition table is the authority, so a phase renamed upstream fails here
		// at the next bump instead of silently wedging a gate.
		"agent.run.phase": {legal: runPhases, source: "agentrun.WorkflowDeclaration().Transitions", constantBacked: true},

		// Rule-internal closure: a role a condition matches must be a role some
		// spawn action in the pack actually sets. A condition on a role nothing
		// dispatches is false forever, and no external constant can catch it
		// because semdev chooses its own roles.
		"agent.loop.role": {legal: spawnRoles, source: "the `role` of the pack's own spawn actions", constantBacked: true},

		"route.review.verdict": {legal: reviewVerdicts, source: "submitreview.Verdict* constants", constantBacked: true},
		"review.verdict.value": {legal: reviewVerdicts, source: "submitreview.Verdict* constants", constantBacked: true},
		"run.change.decision":  {legal: changeDecisions, source: "admission.Decision* constants", constantBacked: true},
		"conversation.intent.value": {legal: conversationIntents,
			source: "conversationintent.Intent constants", constantBacked: true},
		"verify.cleanroom.result": {legal: cleanroomOutcomes, source: "verify.Outcome constants", constantBacked: true},

		"route.attempt.rejected":  booleanFactSpace("route-mirror"),
		"route.attempt.passed":    booleanFactSpace("route-mirror"),
		"route.attempt.transient": booleanFactSpace("route-mirror"),
		"route.attempt.unclean":   booleanFactSpace("route-mirror"),
		"sandbox.provision.ready": booleanFactSpace("sandbox-provisioner"),
	}
}

// spawnRolesInPack returns every distinct role the pack's actions dispatch, sorted.
func spawnRolesInPack(rules []ruleFile) []string {
	seen := map[string]bool{}
	for _, r := range rules {
		for _, a := range append(append(append(append([]ruleAction{}, r.OnEnter...), r.OnExit...), r.WhileTrue...), r.OnRecovery...) {
			if a.Role != "" {
				seen[a.Role] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for role := range seen {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

// conditionLiteralViolations reports every eq/ne condition whose literal is not in
// its field's censused vocabulary, and every value-matched field that is neither
// censused nor explicitly excused. checked counts the literals actually examined,
// so a caller can refuse to pass vacuously. Pure over parsed rules, so the pin can
// be exercised with synthetic input.
func conditionLiteralViolations(rules []ruleFile, spaces map[string]valueSpace) (violations []string, checked int) {
	for _, r := range rules {
		for _, c := range r.Conditions {
			if c.Operator != "eq" && c.Operator != "ne" {
				continue
			}
			literal, ok := c.Value.(string)
			if !ok || literal == "" {
				// A non-string or empty-string comparison is a presence/absence
				// test, not a vocabulary match; the length_* operators and the
				// required:false conventions cover those.
				continue
			}
			space, censused := spaces[c.Field]
			if !censused {
				if _, excused := uncensusedValueFields[c.Field]; excused {
					continue
				}
				violations = append(violations, fmt.Sprintf(
					"rule %q matches a literal on %q, which is neither censused in conditionValueSpaces nor "+
						"listed in uncensusedValueFields. A value-matched field with no legal set is the "+
						"silently-false-condition class: the predicate is declared, the rule loads, and the "+
						"condition can be false forever. Census it against its writer's constants, or excuse "+
						"it with a reason", r.ID, c.Field))
				continue
			}
			checked++
			if !slices.Contains(space.legal, literal) {
				violations = append(violations, fmt.Sprintf(
					"rule %q: condition %s %s %q — %q is not in the vocabulary its writer produces (legal: %v, "+
						"per %s). The rule would load and the condition would be false forever",
					r.ID, c.Field, c.Operator, literal, literal, space.legal, space.source))
			}
		}
	}
	return violations, checked
}
