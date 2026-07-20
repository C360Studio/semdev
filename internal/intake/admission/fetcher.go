package admission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// EntityFetcher resolves one graph entity by ID — implemented over
// graph.ingest.query.entity; faked in unit pins. The park-post lane reads a run
// entity through it; the shared admission core is its home (a reusable graph-read
// adapter beside the RunResolver).
type EntityFetcher interface {
	Entity(ctx context.Context, entityID string) (*graph.EntityState, error)
}

// NewNATSEntityFetcher builds the graph-backed single-entity fetcher over a live
// NATS client.
func NewNATSEntityFetcher(client *natsclient.Client) EntityFetcher {
	return &natsEntityFetcher{client: client}
}

// natsEntityFetcher reads one entity via graph.ingest.query.entity.
type natsEntityFetcher struct {
	client *natsclient.Client
}

func (n *natsEntityFetcher) Entity(ctx context.Context, entityID string) (*graph.EntityState, error) {
	req, err := json.Marshal(map[string]string{"id": entityID})
	if err != nil {
		return nil, err
	}
	respData, err := n.client.RequestClassified(ctx, "graph.ingest.query.entity", req, 5*time.Second)
	if err != nil {
		// A MISSING entity is a classified entity_not_found ERROR on this lane
		// (the framework's own readers collapse it to nil — review finding: the
		// old ID=="" branch was unreachable and absence redelivered to
		// exhaustion instead of the documented definitive ack).
		var ce *errs.ClassifiedError
		if errors.As(err, &ce) && ce.Code == graph.ErrorCodeEntityNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("entity query %s: %w", entityID, err)
	}
	var e graph.EntityState
	if err := json.Unmarshal(respData, &e); err != nil {
		return nil, fmt.Errorf("decode entity %s: %w", entityID, err)
	}
	if e.ID == "" {
		return nil, nil
	}
	return &e, nil
}
