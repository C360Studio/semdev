// Package openpr is the shared DELIVERY CORE (group 8D, R6): the run's DELIVERY step. When
// the coherence gate (check_coherence) has cleared a run — the clean-room verify passed, the
// change validated, and every task was approved — Deliver records pr.ref, the reference to
// the delivered PR. It is the terminal of the m0 issue→PR arc. The delivery-station component
// (internal/station/delivery) calls Deliver off a rule publish (R6); it is not a model tool.
//
// M0 HONESTY (design): the arc terminates here; the real forge-io PR (a live GitHub/GitLab
// PR with a URL) lands at M2 behind the forge-io adapter. So at M0 pr.ref is a DETERMINISTIC
// LOCAL delivery reference, not a live PR URL. This is NOT semspec's placeholder-pass: the
// gate that got here GENUINELY passed (a real cold-container verify=pass, a real approved
// verdict, a real openspec.validated) — only the delivery TARGET is a local stub, a declared
// M0 non-goal. semspec faked the verify OUTCOME (a placeholder that WAS the pass); here the
// outcome is real and only the transport is stubbed.
//
// G3: delivery takes no model-supplied arguments — the harness forms the ref, never the
// model. G2: Deliver stamps pr.ref and fires no lifecycle transition (a rule closing the run
// reads pr.ref). Single G5 writer of pr.ref (open-pr).
package openpr

import (
	"context"
	"fmt"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// Source is stamped on the pr.ref triple. It MUST equal the writer declared for pr.ref in
// internal/vocab (G5) — a conformance pin cross-checks it. (The vocab writer was reconciled
// from the placeholder pr-delivery-adapter to this real Source at 8D.)
const Source = "open-pr"

// RefPredicate is the terminal delivery fact this package owns on the run entity. Exact
// predicate → latest-wins.
const RefPredicate = "pr.ref"

// localStubPrefix marks pr.ref as an M0 LOCAL delivery reference, not a live forge PR URL —
// so a reader (or a human) can tell an M0 stub from an M2 real PR at a glance. The forge-io
// adapter replaces this with the live PR URL at M2.
const localStubPrefix = "local-delivery:"

// Deliver records pr.ref (the M0 deterministic LOCAL delivery reference) on the run
// entity and returns the ref. It is the shared delivery core: the delivery STATION
// component (R6, internal/station/delivery) calls it off a rule publish — the single
// writer of pr.ref (G5, Source == open-pr), the one place the M0 stub form lives. It
// fires no lifecycle transition (G2); a rule reading pr.ref closes the run.
//
// Idempotent delivery (R8): Deliver reads the run's existing pr.ref BEFORE creating
// one, so a replay — a restart mid-arc, a re-fired delivery rule, a retried station
// Handle — returns the SAME ref and never opens a second PR. At M0 the create step is
// a KV upsert (already idempotent, latest-wins) so the guard is belt-and-suspenders;
// its real payoff is M2, where create becomes a side-effecting forge "open PR" call
// and a double fire would open two PRs. Reading the STORED ref (via changefacts.Reader,
// the value lane) rather than reconstructing it is what makes the guard M2-correct: a
// live PR URL is not deterministic from the run, so a replay must return what was
// recorded. The run entity always exists by delivery time; a read fault fails closed
// (returned, not swallowed — never a false or duplicate delivery).
func Deliver(ctx context.Context, reader changefacts.Reader, writer agentictools.OwnedFactWriter, runEntityID string) (string, error) {
	existing, err := reader.ReadFacts(ctx, runEntityID, RefPredicate)
	if err != nil {
		return "", fmt.Errorf("open-pr: read existing delivery on %s: %w", runEntityID, err)
	}
	for _, tr := range existing {
		// ReadFacts is prefix-scoped; require the EXACT predicate. The short-circuit is
		// PRESENCE-based: any pr.ref on the run means this run was already delivered, so
		// never reach the create branch (at M2 that would open a duplicate forge PR). A
		// present pr.ref whose object is not a string is a corrupt delivery fact — fail
		// CLOSED (the invariant is "never a false OR duplicate delivery") rather than fall
		// through to create or return a stringified non-ref.
		if tr.Predicate != RefPredicate {
			continue
		}
		ref, ok := tr.Object.(string)
		if !ok {
			return "", fmt.Errorf("open-pr: existing %s on %s has a non-string object %T — "+
				"cannot confirm the recorded delivery reference; failing closed rather than re-opening",
				RefPredicate, runEntityID, tr.Object)
		}
		return ref, nil
	}

	// First delivery: form the M0 deterministic local reference and stamp it. The M2
	// forge-io adapter replaces this create branch with a live PR "open" call; the
	// read-existing guard above stays, so the M2 swap cannot regress idempotency. NOTE
	// (group 8): the guard closes the SEQUENTIAL replay window (restart, re-fired rule,
	// retried Handle), not a CONCURRENT double-fire — two Delivers can both read "absent"
	// and both create. Harmless at M0 (deterministic ref + latest-wins ReplaceTriples
	// converge on one triple); at M2 the side-effecting create must add forge-level
	// idempotency (an idempotency key or query-existing-PR-by-head-branch) on top of this
	// guard. Tracked in design R8.
	ref := localStubPrefix + runEntityID
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  RefPredicate,
		Object:     ref,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil); err != nil {
		return "", fmt.Errorf("open-pr: stamp %s on %s: %w", RefPredicate, runEntityID, err)
	}
	return ref, nil
}
