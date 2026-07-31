package validation

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/station"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/tools/validatechange"
)

// errReader returns a configurable error from ReadFacts (nil = empty result).
type errReader struct{ err error }

func (r errReader) ReadFacts(_ context.Context, _, _ string) ([]message.Triple, error) {
	return nil, r.err
}

type okWriter struct{}

func (okWriter) ReplaceOwned(_ context.Context, _ projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

func (okWriter) ReadAuthoritative(_ context.Context, id string) (*graph.EntityState, error) {
	return &graph.EntityState{ID: id}, nil
}

// nopRunner is never reached in these tests (Validate errors before shelling the CLI).
type nopRunner struct{}

func (nopRunner) Run(_ context.Context, _, _ string, _ ...string) (cliexec.Result, error) {
	return cliexec.Result{}, nil
}

func TestHandleRejectsMissingRunEntity(t *testing.T) {
	// The validate rule fires on the authoring loop, so the run travels as a property.
	// A dispatch without run_entity_id cannot target the run — the handler errors
	// rather than validating against "" (which would misfire the oracle).
	h := &handler{reader: errReader{}, runner: nopRunner{}, writer: graphown.NewWriter(validatechange.Source, okWriter{}), logger: slog.Default()}
	err := h.Handle(context.Background(), station.Request{EntityID: "loop-1", Properties: map[string]string{"slug": "test-change"}})
	if err == nil {
		t.Error("validation handler must error when the dispatch carries no run_entity_id property")
	}
}

func TestHandleFailsClosedOnValidateError(t *testing.T) {
	// The change hydrate (reader.ReadFacts) errors → Validate returns an error and the
	// handler propagates it (fail closed: nothing stamped, so the gate does not advance).
	h := &handler{reader: errReader{err: errors.New("graph down")}, runner: nopRunner{}, writer: graphown.NewWriter(validatechange.Source, okWriter{}), logger: slog.Default()}
	err := h.Handle(context.Background(), station.Request{EntityID: "loop-1", Properties: map[string]string{"run_entity_id": "run-1", "slug": "test-change"}})
	if err == nil {
		t.Error("validation handler must fail closed when Validate errors")
	}
}
