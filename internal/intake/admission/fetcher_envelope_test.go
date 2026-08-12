package admission_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semdev/internal/intake/admission"
)

type envelopeRequester struct {
	entity *graph.EntityState
	err    error
}

func (f envelopeRequester) RequestClassified(context.Context, string, []byte, time.Duration) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return json.Marshal(graph.ExactEntity{Entity: f.entity, KVRevision: 3})
}

const envelopeRun = "c360.semdev.agent.chain.execution.envelope2"

// TestFetcherDecodesTheAuthorityEnvelope pins the beta.160 wire shape for the
// park-post resolve lane: the previous hand-rolled decode read the
// {entity, kvRevision} envelope as an EMPTY EntityState, and this lane's
// absence-collapse then reported every LIVE entity as not-found — the park
// message never reached the human's thread. The read travels
// graph.ExactEntityReader; this pin serves the literal wire bytes so the
// envelope class fails offline.
func TestFetcherDecodesTheAuthorityEnvelope(t *testing.T) {
	entity := &graph.EntityState{ID: envelopeRun, Triples: []message.Triple{
		{Subject: envelopeRun, Predicate: "run.awaiting.human", Object: "park msg", Source: "park-rule", Timestamp: time.Now().UTC(), Confidence: 1.0},
	}}
	f := admission.NewExactEntityFetcher(graph.NewExactEntityReader(envelopeRequester{entity: entity}, time.Second))

	got, err := f.Entity(context.Background(), envelopeRun)
	if err != nil {
		t.Fatalf("fetch over the envelope: %v", err)
	}
	if got == nil || got.ID != envelopeRun || len(got.Triples) != 1 {
		t.Fatalf("got %+v, want the live entity — a nil here is the silent-empty-decode class that killed every park-post resolve", got)
	}
}

// TestFetcherCollapsesNotFoundToNil pins the lane's absence contract: a
// classified entity_not_found collapses to (nil, nil) — a definitive ack for
// the consumer, never a redelivery-to-exhaustion.
func TestFetcherCollapsesNotFoundToNil(t *testing.T) {
	notFound := errs.ClassifiedCode(errs.ErrorInvalid, graph.ErrorCodeEntityNotFound, context.DeadlineExceeded)
	f := admission.NewExactEntityFetcher(graph.NewExactEntityReader(envelopeRequester{err: notFound}, time.Second))

	got, err := f.Entity(context.Background(), envelopeRun)
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil) — absence is a definitive ack on this lane", got, err)
	}
}
