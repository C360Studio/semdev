package changefacts_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semdev/internal/changefacts"
)

// envelopeRequester serves the LITERAL beta.160 graph.ingest.query.entity
// response — the {entity, kvRevision} envelope — or a scripted classified
// error, through the same graph.NewExactEntityReader production wires.
type envelopeRequester struct {
	entity *graph.EntityState
	err    error
}

func (f envelopeRequester) RequestClassified(context.Context, string, []byte, time.Duration) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return json.Marshal(graph.ExactEntity{Entity: f.entity, KVRevision: 7})
}

const envelopeRun = "c360.semdev.agent.chain.execution.envelope1"

// TestReadFactsDecodesTheAuthorityEnvelope pins the wire shape that broke the
// beta.160 migration's first docker run: the query lane replies the
// {entity, kvRevision} ENVELOPE, and semdev's previous hand-rolled decode read
// it as an EMPTY EntityState — every read silently returned zero triples, the
// validation station refused every authored change, and all 18 journeys
// parked. The read must travel graph.ExactEntityReader (the framework decodes
// its own envelope); this pin drives the production reader through a fake
// requester serving the literal wire bytes so an envelope regression fails
// OFFLINE, never in docker again.
func TestReadFactsDecodesTheAuthorityEnvelope(t *testing.T) {
	entity := &graph.EntityState{ID: envelopeRun, Triples: []message.Triple{
		{Subject: envelopeRun, Predicate: "openspec.change.document", Object: "{...}", Source: "create-change-author-tool", Timestamp: time.Now().UTC(), Confidence: 1.0},
		{Subject: envelopeRun, Predicate: "measurement.result.passed", Object: "true", Source: "measurement-harness", Timestamp: time.Now().UTC(), Confidence: 1.0},
	}}
	reader := changefacts.NewExactReader(graph.NewExactEntityReader(envelopeRequester{entity: entity}, time.Second))

	got, err := reader.ReadFacts(context.Background(), envelopeRun, "openspec.change.")
	if err != nil {
		t.Fatalf("read facts over the envelope: %v", err)
	}
	if len(got) != 1 || got[0].Predicate != "openspec.change.document" {
		t.Fatalf("got %v, want exactly the one openspec.change.document triple — an empty result here is the silent-empty-decode class that parked every journey", got)
	}
}

// TestReadFactsKeepsNotFoundLoud pins the other half of the contract: a
// never-created entity stays a LOUD classified error, never an empty read (the
// emptiness would count as "nothing authored" and misroute the run).
func TestReadFactsKeepsNotFoundLoud(t *testing.T) {
	notFound := errs.ClassifiedCode(errs.ErrorInvalid, graph.ErrorCodeEntityNotFound, context.DeadlineExceeded)
	reader := changefacts.NewExactReader(graph.NewExactEntityReader(envelopeRequester{err: notFound}, time.Second))

	_, err := reader.ReadFacts(context.Background(), envelopeRun, "openspec.change.")
	if err == nil {
		t.Fatal("a missing entity must be a loud error on this lane, never an empty read")
	}
	if !strings.Contains(err.Error(), graph.ErrorCodeEntityNotFound) {
		t.Errorf("error %q must carry the classified code", err)
	}
}
