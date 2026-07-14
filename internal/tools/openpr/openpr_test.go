package openpr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeWriter struct {
	replaces [][]message.Triple
	err      error
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	if w.err != nil {
		return w.err
	}
	w.replaces = append(w.replaces, add)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

// fakeReader satisfies changefacts.Reader: it returns its seeded triples, prefix-scoped
// (mirroring the NATS reader's filter), or an injected fault. An empty reader models a
// run that has not yet been delivered.
type fakeReader struct {
	triples []message.Triple
	err     error
}

func (r *fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, t := range r.triples {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Deliver stamps exactly one pr.ref triple (an M0 local delivery stub) on the run with the
// open-pr Source, and returns that ref.
func TestDeliverStampsRef(t *testing.T) {
	w := &fakeWriter{}
	ref, err := Deliver(context.Background(), &fakeReader{}, w, runEntity)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	var triples []message.Triple
	for _, batch := range w.replaces {
		triples = append(triples, batch...)
	}
	if len(triples) != 1 {
		t.Fatalf("Deliver wrote %d triples, want exactly 1: %v", len(triples), triples)
	}
	tr := triples[0]
	if tr.Predicate != RefPredicate {
		t.Errorf("Deliver wrote predicate %q, want %q", tr.Predicate, RefPredicate)
	}
	if tr.Subject != runEntity {
		t.Errorf("pr.ref subject = %q, want run entity", tr.Subject)
	}
	if tr.Source != Source {
		t.Errorf("pr.ref Source = %q, want %q (G5)", tr.Source, Source)
	}

	// M0 honesty: the ref is a LOCAL stub (not a live PR URL), deterministic from the run.
	if !strings.HasPrefix(ref, localStubPrefix) {
		t.Errorf("pr.ref = %q, want the %q M0 local-delivery stub prefix", ref, localStubPrefix)
	}
	if !strings.Contains(ref, runEntity) {
		t.Errorf("pr.ref = %q, want it derived deterministically from the run", ref)
	}
	if got, want := tr.Object.(string), ref; got != want {
		t.Errorf("stamped triple object = %q, want the returned ref %q", got, want)
	}
}

// A writer error propagates to the caller (the delivery station) rather than being
// swallowed — Deliver has no fallback path.
func TestDeliverPropagatesWriterError(t *testing.T) {
	wantErr := errors.New("boom")
	w := &fakeWriter{err: wantErr}
	_, err := Deliver(context.Background(), &fakeReader{}, w, runEntity)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Deliver error = %v, want %v", err, wantErr)
	}
}

// Idempotent delivery (R8, task 7.4): a replay does not open a second PR. Once a run
// carries a pr.ref, a second Deliver returns the SAME ref and writes nothing more —
// the replay pin that at M2 stops a re-fired delivery from opening two forge PRs.
func TestDeliverIsIdempotentOnReplay(t *testing.T) {
	w := &fakeWriter{}
	r := &fakeReader{}

	ref1, err := Deliver(context.Background(), r, w, runEntity)
	if err != nil {
		t.Fatalf("first Deliver: %v", err)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("first Deliver wrote %d batches, want 1", len(w.replaces))
	}

	// The run now carries the pr.ref the first delivery stamped — feed it back to the
	// reader so the replay observes the same graph state a real run would.
	r.triples = append(r.triples, w.replaces[0]...)

	ref2, err := Deliver(context.Background(), r, w, runEntity)
	if err != nil {
		t.Fatalf("replay Deliver: %v", err)
	}
	if ref2 != ref1 {
		t.Errorf("replay ref = %q, want the first ref %q (no double-open)", ref2, ref1)
	}
	if len(w.replaces) != 1 {
		t.Errorf("replay wrote again (%d batches total) — delivery must be idempotent, no second stamp", len(w.replaces))
	}
}

// The short-circuit returns the STORED ref value, it does not reconstruct it. Seeding a
// pr.ref whose value is NOT the deterministic M0 stub (a stand-in for the M2 live PR URL)
// proves Deliver reads what was recorded — the property that makes the guard M2-correct,
// where the ref cannot be re-derived from the run — and writes nothing.
func TestDeliverReturnsStoredRefWithoutRewriting(t *testing.T) {
	const storedRef = "https://forge.example/acme/repo/pull/42"
	w := &fakeWriter{}
	r := &fakeReader{triples: []message.Triple{{
		Subject:   runEntity,
		Predicate: RefPredicate,
		Object:    storedRef,
		Source:    Source,
	}}}

	ref, err := Deliver(context.Background(), r, w, runEntity)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if ref != storedRef {
		t.Errorf("Deliver returned %q, want the stored ref %q (read the recorded value, do not reconstruct)", ref, storedRef)
	}
	if len(w.replaces) != 0 {
		t.Errorf("Deliver wrote %d batches on an already-delivered run, want 0 (no re-create)", len(w.replaces))
	}
}

// A present-but-malformed pr.ref (non-string object) fails closed: Deliver must NOT
// fall through to create (a duplicate delivery at M2) nor return a stringified non-ref.
// The short-circuit is presence-based; an unreadable recorded ref is an error.
func TestDeliverFailsClosedOnNonStringRef(t *testing.T) {
	w := &fakeWriter{}
	r := &fakeReader{triples: []message.Triple{{
		Subject:   runEntity,
		Predicate: RefPredicate,
		Object:    42, // not a string — a corrupt delivery fact
		Source:    Source,
	}}}

	_, err := Deliver(context.Background(), r, w, runEntity)
	if err == nil {
		t.Fatal("Deliver must fail closed on a present non-string pr.ref, not fall through to re-create")
	}
	if len(w.replaces) != 0 {
		t.Errorf("Deliver stamped %d batches on a present (malformed) pr.ref, want 0 (no re-open)", len(w.replaces))
	}
}

// A read fault fails closed: Deliver returns the error and stamps nothing, so a run
// never reaches a false or duplicate delivery when the existence check cannot be made.
func TestDeliverFailsClosedOnReadFault(t *testing.T) {
	readErr := errors.New("graph query down")
	w := &fakeWriter{}
	_, err := Deliver(context.Background(), &fakeReader{err: readErr}, w, runEntity)
	if !errors.Is(err, readErr) {
		t.Fatalf("Deliver error = %v, want %v (fail closed on read fault)", err, readErr)
	}
	if len(w.replaces) != 0 {
		t.Errorf("Deliver stamped %d batches despite a read fault, want 0", len(w.replaces))
	}
}
