package delivery

import (
	"context"
	"errors"
	"log/slog"
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

func TestHandleStampsPRRefOnFiringEntity(t *testing.T) {
	w := &fakeWriter{}
	h := &handler{writer: w, logger: slog.Default()}
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
	h := &handler{writer: &fakeWriter{err: errors.New("graph down")}, logger: slog.Default()}
	// A write fault returns an error (logged + metered by the generic Component);
	// the station stamped no pr.ref, so the run does not reach delivered and parks.
	if err := h.Handle(context.Background(), station.Request{EntityID: "run-7"}); err == nil {
		t.Error("Handle must return an error on a write fault (fail closed — no false delivery)")
	}
}
