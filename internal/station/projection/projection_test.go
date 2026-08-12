package projection

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/station"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
)

// errReader returns a configurable error from ReadFacts (nil = empty result).
type errReader struct{ err error }

func (r errReader) ReadFacts(_ context.Context, _, _ string) ([]message.Triple, error) {
	return nil, r.err
}

// okWriter passes the immutability check (no existing task.spec) and accepts writes.
type okWriter struct{}

func (okWriter) Reconcile(_ context.Context, _ projection.ReconcileMutation) (projection.MutationReceipt, error) {
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

func (okWriter) ReadAuthoritative(_ context.Context, id string) (*graph.ExactEntity, error) {
	return &graph.ExactEntity{Entity: &graph.EntityState{ID: id}, KVRevision: 1}, nil
}

func TestHandleFailsClosedOnProjectError(t *testing.T) {
	// Immutability passes (okWriter), then the D15#0 revision read errors → Project
	// returns an error and the handler must propagate it (fail closed: no partial
	// task.spec, so the run does not advance past the projection gate).
	h := &handler{reader: errReader{err: errors.New("graph down")}, writer: graphown.NewReadWriter(projecttasks.Source, okWriter{}, okWriter{}), logger: slog.Default()}
	err := h.Handle(context.Background(), station.Request{EntityID: "run-1", Properties: map[string]string{"slug": "test-change"}})
	if err == nil {
		t.Error("projection handler must fail closed when Project errors")
	}
}

func TestHandleRejectsMissingSlug(t *testing.T) {
	// The projection rule threads the slug as a property; without it Project refuses
	// (slug is required) and the handler errors rather than projecting a null change.
	h := &handler{reader: errReader{}, writer: graphown.NewReadWriter(projecttasks.Source, okWriter{}, okWriter{}), logger: slog.Default()}
	err := h.Handle(context.Background(), station.Request{EntityID: "run-1"})
	if err == nil {
		t.Error("projection handler must error when the dispatch carries no slug property")
	}
}
