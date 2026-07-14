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

// Deliver stamps exactly one pr.ref triple (an M0 local delivery stub) on the run with the
// open-pr Source, and returns that ref.
func TestDeliverStampsRef(t *testing.T) {
	w := &fakeWriter{}
	ref, err := Deliver(context.Background(), w, runEntity)
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
	_, err := Deliver(context.Background(), w, runEntity)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Deliver error = %v, want %v", err, wantErr)
	}
}
