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
	"time"

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

// Deliver stamps pr.ref (the M0 deterministic LOCAL delivery reference) on the run
// entity and returns the ref. It is the shared delivery core: the delivery STATION
// component (R6, internal/station/delivery) calls it off a rule publish — the single
// writer of pr.ref (G5, Source == open-pr), the one place the M0 stub form lives. It
// fires no lifecycle transition (G2); a rule reading pr.ref closes the run.
func Deliver(ctx context.Context, writer agentictools.OwnedFactWriter, runEntityID string) (string, error) {
	// The M0 delivery reference is deterministic from the run — a stub proving the coherent
	// run reached delivery; the M2 forge-io adapter replaces it with a live PR URL.
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
		return "", err
	}
	return ref, nil
}
