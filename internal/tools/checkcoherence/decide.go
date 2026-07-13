package checkcoherence

import (
	"fmt"
	"strings"
)

// Decision is the coherence gate's route for a run about to deliver.
type Decision string

const (
	// DecisionCoherent clears the run to open a PR: every delivery signal is green.
	DecisionCoherent Decision = "coherent"
	// DecisionBlocked parks the run toward the human: a delivery signal is missing or not
	// green (fail closed — an absent verdict/validation/verify never opens a PR).
	DecisionBlocked Decision = "blocked"
)

// Inputs is the harness-read delivery evidence the gate rolls up. Every field is a fact
// the harness stamped (the model supplied none of them). A projected task with no verdict
// is a MISSING verdict, which blocks (the architect's D16 ruling: absent = not approved).
type Inputs struct {
	VerifyResult   string            // verify.result — the clean-room cold verdict (pass/fail/retry/"")
	Validated      string            // openspec.validated — non-empty iff the OpenSpec CLI passed
	ProjectedTasks []string          // task.spec.<i> indices present (the tasks that must each be approved)
	Verdicts       map[string]string // task index → review.verdict.<i> (approved/changes_requested)
}

// Decide rolls the delivery signals into one fail-closed route (the m0 open_pr coherence
// gate, D16): coherent iff the clean-room verify PASSED, the change VALIDATED, at least one
// task was projected, and EVERY projected task carries an approved review verdict. Anything
// else blocks toward the human — a non-pass/absent verify, an unvalidated change, no
// projected task, or ANY projected task without an approved verdict. Fail-closed is the
// whole point: SB5 forbids encoding "couldn't prove" as a pass, so a missing signal never
// opens a PR. This is the whole gate policy, isolated as a pure function so the roll-up is
// pinned offline over every combination.
func Decide(in Inputs) (Decision, string) {
	if in.VerifyResult != "pass" {
		return DecisionBlocked, fmt.Sprintf("the clean-room cold verify is not pass (verify.result=%s) — the committed artifact is not proven reproducible; blocking delivery", orMissing(in.VerifyResult))
	}
	if in.Validated == "" {
		return DecisionBlocked, "openspec.validated is absent — the change did not pass the OpenSpec compatibility oracle; blocking delivery"
	}
	if len(in.ProjectedTasks) == 0 {
		return DecisionBlocked, "no projected task.spec on the run — there is nothing reviewed to ship; blocking delivery"
	}
	var unapproved []string
	for _, id := range in.ProjectedTasks {
		if in.Verdicts[id] != reviewApproved {
			unapproved = append(unapproved, "task "+id+" ("+orMissing(in.Verdicts[id])+")")
		}
	}
	if len(unapproved) > 0 {
		return DecisionBlocked, fmt.Sprintf("projected tasks without an approved review verdict: %s — blocking delivery (an absent verdict is not approval, D16)", strings.Join(unapproved, ", "))
	}
	return DecisionCoherent, "the clean-room verify passed, the change validated, and every projected task is approved — coherent, open the PR"
}

// reviewApproved mirrors submitreview.VerdictApproved. It is duplicated as a package const
// (rather than importing the review tool) to keep the pure decision dependency-free and
// offline-pinnable; TestCoherenceLiteralsMatchSources cross-checks the two do not drift.
const reviewApproved = "approved"

// RequiredVerdict returns the review verdict the roll-up requires for approval — exported
// so the conformance drift-check can cross-verify it against submitreview.VerdictApproved
// without exposing the const or coupling the pure package to the review tool.
func RequiredVerdict() string { return reviewApproved }

func orMissing(s string) string {
	if s == "" {
		return "<missing>"
	}
	return s
}
