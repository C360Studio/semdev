package admission

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// EntityFetcher resolves one graph entity by ID — implemented over the
// framework's exact-entity authority read; faked in unit pins. The park-post
// lane reads a run entity through it; the shared admission core is its home (a
// reusable graph-read adapter beside the RunResolver).
type EntityFetcher interface {
	Entity(ctx context.Context, entityID string) (*graph.EntityState, error)
}

// fetchTimeout bounds one entity read round-trip.
const fetchTimeout = 5 * time.Second

// NewNATSEntityFetcher builds the graph-backed single-entity fetcher over a live
// NATS client.
func NewNATSEntityFetcher(client *natsclient.Client) EntityFetcher {
	if client == nil {
		return &natsEntityFetcher{}
	}
	return &natsEntityFetcher{reader: graph.NewExactEntityReader(client, fetchTimeout)}
}

// NewExactEntityFetcher builds the fetcher over an already-constructed
// exact-entity reader — the seam the offline envelope pins drive.
func NewExactEntityFetcher(reader graph.ExactEntityReader) EntityFetcher {
	return &natsEntityFetcher{reader: reader}
}

// natsEntityFetcher reads one entity through graph.ExactEntityReader. The wire
// (the beta.160 {entity, kvRevision} envelope) lives entirely in the framework
// reader — semdev's previous hand-rolled decode read that envelope as an EMPTY
// EntityState, which this lane's ID=="" branch then collapsed to "not found":
// every park-post resolve failed on a live entity.
type natsEntityFetcher struct {
	reader graph.ExactEntityReader
}

func (n *natsEntityFetcher) Entity(ctx context.Context, entityID string) (*graph.EntityState, error) {
	if n.reader == nil {
		return nil, fmt.Errorf("entity query %s: no graph reader bound", entityID)
	}
	exact, err := n.reader.ReadExactEntity(ctx, entityID)
	if err != nil {
		// A MISSING entity is a classified entity_not_found ERROR on this lane;
		// this fetcher's contract collapses it to nil (the caller treats absence
		// as a definitive ack, never a redelivery-to-exhaustion).
		var ce *errs.ClassifiedError
		if errors.As(err, &ce) && ce.Code == graph.ErrorCodeEntityNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("entity query %s: %w", entityID, err)
	}
	if exact == nil || exact.Entity == nil {
		return nil, nil
	}
	return exact.Entity, nil
}
