package checkgate

import "fmt"

// Decision is the dev-loop gate's route for one task's attempt.
type Decision string

const (
	// DecisionAdvance clears the task: it measured green and no floor rejected.
	DecisionAdvance Decision = "advance"
	// DecisionRetry mints a fresh developer attempt: the attempt was not clean and
	// the task's iteration budget still has room.
	DecisionRetry Decision = "retry"
	// DecisionEscalate parks toward the human: the attempt was not clean and the
	// budget is exhausted, OR a required gate fact was missing/unparseable (fail
	// closed — never a false retry).
	DecisionEscalate Decision = "escalate"
)

// Inputs is the harness-read evidence the gate routes on. The pointer fields are
// present ONLY when the corresponding fact was found on the run and parsed; a nil
// pointer is a MISSING or unparseable fact. AttemptCount is the number of DISTINCT
// objects recorded under task.attempt.<i> (the developer loop instances) — never
// raw triple cardinality, which the at-least-once append over-counts.
type Inputs struct {
	Passed       *bool // measurement.result.<i>.passed — did the in-container test pass
	Rejected     *bool // floor.finding.<i>.rejected — did any structural floor reject
	Budget       *int  // task.spec.<i>.budget — the clamped iteration ceiling
	AttemptCount int   // distinct objects of task.attempt.<i>
}

// Decide derives the route from the recorded evidence, failing CLOSED. This is the
// whole gate policy, isolated as a pure function so the routing table is pinned
// offline over every combination (the loop only ever exercises the happy path).
//
// Fail-closed first: any missing judgment fact (passed, rejected, or budget) →
// escalate. The gate cannot judge the attempt (no passed/rejected) or bound the loop
// (no budget), so it parks toward the human rather than retrying blind — an unbounded
// retry loop or a verification skipped over absent evidence is the dangerous
// direction, and G3 forbids inventing the missing outcome.
//
// Otherwise: advance iff the attempt measured green AND no floor rejected; else retry
// while attempts remain strictly under budget; else the budget is spent → escalate.
// With budget N this yields exactly N attempts before escalation (the gate runs once
// per counted attempt).
func Decide(in Inputs) (Decision, string) {
	// The fail-closed guard precedes the advance branch UNCONDITIONALLY: a green,
	// unrejected attempt still escalates if Budget is absent. That coupling (advance
	// consults budget-presence) is a conscious choice — it is the safe direction and is
	// unreachable in practice (project_tasks always stamps a clamped task.spec.<i>.budget
	// or fails projection toward the human), so no clean attempt is ever wrongly parked.
	if in.Passed == nil || in.Rejected == nil || in.Budget == nil {
		return DecisionEscalate, fmt.Sprintf(
			"fail-closed: a required gate fact is missing or unreadable (passed=%s, rejected=%s, budget=%s) — the gate cannot judge or bound this task, escalating to the human",
			presentBool(in.Passed), presentBool(in.Rejected), presentInt(in.Budget))
	}
	if *in.Passed && !*in.Rejected {
		return DecisionAdvance, "the attempt measured green (passed=true) and no structural floor rejected — advance the task"
	}
	if in.AttemptCount < *in.Budget {
		return DecisionRetry, fmt.Sprintf(
			"the attempt was not clean (passed=%t, rejected=%t) and the iteration budget still has room (%d of %d used) — retry with a fresh attempt",
			*in.Passed, *in.Rejected, in.AttemptCount, *in.Budget)
	}
	return DecisionEscalate, fmt.Sprintf(
		"the attempt was not clean (passed=%t, rejected=%t) and the iteration budget is exhausted (%d of %d used) — escalating to the human",
		*in.Passed, *in.Rejected, in.AttemptCount, *in.Budget)
}

func presentBool(b *bool) string {
	if b == nil {
		return "<missing>"
	}
	return fmt.Sprintf("%t", *b)
}

func presentInt(i *int) string {
	if i == nil {
		return "<missing>"
	}
	return fmt.Sprintf("%d", *i)
}
