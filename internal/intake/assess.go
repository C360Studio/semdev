package intake

import "context"

// Outcome is the full result of assessing one code-host event: the normalized
// intake and the admission decision. The adapter component acts on it with two
// effects, BOTH gated on Admitted — stamp intake.actor/intake.admitted and spawn
// the coordinator. A non-admitted or non-relevant Outcome produces NEITHER: no
// fact is written and no coordinator is spawned, so a rejected event creates no
// run and spends no token (the zero-cost rejection, G6).
type Outcome struct {
	Intake   *Intake
	Decision Decision
	// Admitted is the single gate the adapter acts on: true → stamp + spawn;
	// false → drop (a non-relevant event is never admitted).
	Admitted bool
}

// Assess runs the whole zero-token intake chain for one event — normalize the host
// payload, then (for a relevant event) decide admission — WITHOUT any side effect.
// It is the seam the adapter component drives: on a nil-error Outcome the component
// stamps and spawns iff Admitted, and simply drops otherwise (zero cost). A
// non-nil error is a fail-closed, retryable authorization-lookup failure (the
// permission check errored); the component leaves the event un-acked for
// redelivery rather than admitting or permanently rejecting.
//
// No LLM token is ever spent here: normalization is pure and the decision makes at
// most one GitHub permission call (skipped for an allowlisted actor). An
// unauthorized or un-opted-in event is rejected before any run or model exists.
func Assess(ctx context.Context, cfg Config, subject string, payload []byte, checker PermissionChecker) (*Outcome, error) {
	in, err := Normalize(subject, payload)
	if err != nil {
		return nil, err
	}
	if !in.Relevant {
		// Not an M0 intake trigger — no decision, no effect.
		return &Outcome{Intake: in}, nil
	}
	d, err := Decide(ctx, cfg, in.Event, checker)
	if err != nil {
		// Fail closed and retryable: do not admit, surface for redelivery.
		return &Outcome{Intake: in, Decision: d}, err
	}
	return &Outcome{Intake: in, Decision: d, Admitted: d.Admitted}, nil
}
