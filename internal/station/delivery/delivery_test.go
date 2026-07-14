package delivery

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semstreams/message"
)

// fakeWriter captures the triples the delivery core stamps (and can inject a
// write fault) so the handler can be exercised without a live graph.
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

// fakeReader satisfies changefacts.Reader for the idempotency read the delivery core
// performs before creating. It returns seeded triples, prefix-scoped, or a fault.
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

func TestHandleStampsPRRefOnFiringEntity(t *testing.T) {
	w := &fakeWriter{}
	h := &handler{reader: &fakeReader{}, writer: w, logger: slog.Default()}
	// The delivery rule fires on the RUN, so req.EntityID is the run; the station
	// stamps pr.ref on it — no properties needed.
	err := h.Handle(context.Background(), station.Request{EntityID: "run-7"})
	if err != nil {
		t.Fatalf("Handle returned %v, want nil", err)
	}
	if len(w.replaces) != 1 || len(w.replaces[0]) != 1 {
		t.Fatalf("expected exactly one pr.ref triple stamped, got %v", w.replaces)
	}
	tr := w.replaces[0][0]
	if tr.Subject != "run-7" || tr.Predicate != openpr.RefPredicate {
		t.Errorf("stamped %s on %s, want %s on run-7", tr.Predicate, tr.Subject, openpr.RefPredicate)
	}
	if tr.Source != openpr.Source {
		t.Errorf("pr.ref Source = %q, want %q (single writer, G5 — the station and the tool share the core)", tr.Source, openpr.Source)
	}
}

func TestHandleFailsClosedOnWriteFault(t *testing.T) {
	h := &handler{reader: &fakeReader{}, writer: &fakeWriter{err: errors.New("graph down")}, logger: slog.Default()}
	// A write fault returns an error (logged + metered by the generic Component);
	// the station stamped no pr.ref, so the run does not reach delivered and parks.
	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err == nil {
		t.Error("Handle must return an error on a write fault (fail closed — no false delivery)")
	}
}

// A re-fired dispatch on an already-delivered run stamps nothing more (R8, task 7.4):
// the station's reader observes the existing pr.ref and the delivery core short-circuits.
// Proves the reader→handler→Deliver wiring, not just the core in isolation.
func TestHandleIsIdempotentOnRedispatch(t *testing.T) {
	w := &fakeWriter{}
	r := &fakeReader{}
	h := &handler{reader: r, writer: w, logger: slog.Default()}

	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("first Handle stamped %d batches, want 1", len(w.replaces))
	}

	// The run now carries the stamped pr.ref; a redispatch must observe it and no-op.
	r.triples = append(r.triples, w.replaces[0]...)

	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err != nil {
		t.Fatalf("redispatch Handle: %v", err)
	}
	if len(w.replaces) != 1 {
		t.Errorf("redispatch stamped again (%d batches) — an already-delivered run must not re-open", len(w.replaces))
	}
}
