package floors

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/c360studio/semstreams/message"

	semfloors "github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/station"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/tools/checkfloors"
)

// errAttempts fails to resolve the attempt — the fail-closed trigger.
type errAttempts struct{ err error }

func (a errAttempts) Resolve(_ context.Context, _ string, _ int) (semfloors.Attempt, error) {
	return semfloors.Attempt{}, a.err
}

// okWriter has no findings to clear and accepts writes (so RunFloors' fail-closed clear
// path is a no-op and the resolve error surfaces).
type okWriter struct{}

func (okWriter) ReplaceOwned(_ context.Context, _ projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

func (okWriter) ReadAuthoritative(_ context.Context, id string) (*graph.EntityState, error) {
	return &graph.EntityState{ID: id}, nil
}

type nilReader struct{}

func (nilReader) ReadFacts(_ context.Context, _, _ string) ([]message.Triple, error) {
	return nil, nil
}

func TestHandleRejectsMissingRunEntity(t *testing.T) {
	// The floors trigger fires on the developer loop L_n, so the run travels as a
	// property. Without it the station cannot stamp floor.finding on the run — it errors
	// rather than flooring against "".
	h := &handler{attempts: errAttempts{}, reader: nilReader{}, writer: graphown.NewReadWriter(checkfloors.Source, okWriter{}, okWriter{}), logger: slog.Default()}
	err := h.Handle(context.Background(), station.Request{EntityID: "dev-loop-1", Properties: map[string]string{"task_index": "0"}})
	if err == nil {
		t.Error("floors handler must error when the dispatch carries no run_entity_id property")
	}
}

func TestHandleFailsClosedOnResolveError(t *testing.T) {
	// The attempt resolve errors (e.g. the run's checkout is unavailable) → RunFloors
	// clears findings and returns the error, which the handler must propagate: no route
	// mirror lands on L_n, so the floors route never advances a broken attempt.
	h := &handler{attempts: errAttempts{err: errors.New("no checkout")}, reader: nilReader{}, writer: graphown.NewReadWriter(checkfloors.Source, okWriter{}, okWriter{}), logger: slog.Default()}
	err := h.Handle(context.Background(), station.Request{EntityID: "dev-loop-1", Properties: map[string]string{"run_entity_id": "run-1", "task_index": "0"}})
	if err == nil {
		t.Error("floors handler must fail closed when the attempt cannot be resolved")
	}
}

func TestParseTaskIndex(t *testing.T) {
	cases := map[string]int{"": 0, "0": 0, "2": 2, "-1": 0, "notanint": 0}
	for in, want := range cases {
		if got := parseTaskIndex(in); got != want {
			t.Errorf("parseTaskIndex(%q) = %d, want %d", in, got, want)
		}
	}
}
