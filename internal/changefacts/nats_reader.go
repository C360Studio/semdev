package changefacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

const (
	// queryEntitySubject is the single-entity read-back lane. It MUST match
	// graphingest query.go — the same subject OwnedFactWriter.ReadOwnedPredicates
	// and agentic-loop's todos reader use.
	queryEntitySubject = "graph.ingest.query.entity"
	// readTimeout bounds one read round-trip. Matches the 5s budget the sibling
	// owned-fact query uses — a fast KV op.
	readTimeout = 5 * time.Second
)

// entityQueryRequest is the wire shape graph.ingest.query.entity expects
// ({"id": "..."}). Kept local: graph exposes the handler, not a request type
// (owned_fact_writer.go keeps the same local copy).
type entityQueryRequest struct {
	ID string `json:"id"`
}

// natsReader adapts natsclient.Client to Reader, routing the read through
// graph.ingest.query.entity and scoping the returned entity's triples to the
// caller's owned prefix.
type natsReader struct {
	client *natsclient.Client
}

// NewNATSReader builds a Reader backed by the shared graph query surface. Wire
// this into read-side openspec-io tools (hydrate, WriteChange-to-workspace,
// CLI-validate). A nil client is a programming error surfaced at read time, not
// here — tools register schema-only without a client and fail loudly if executed.
func NewNATSReader(client *natsclient.Client) Reader {
	return &natsReader{client: client}
}

func (r *natsReader) ReadFacts(ctx context.Context, entityID, prefix string) ([]message.Triple, error) {
	if prefix == "" {
		return nil, fmt.Errorf("read facts: prefix must be non-empty (an unscoped read pulls every owner's facts off a shared entity)")
	}
	reqData, err := json.Marshal(entityQueryRequest{ID: entityID})
	if err != nil {
		return nil, fmt.Errorf("marshal entity query: %w", err)
	}
	// RequestClassified (NOT the retry variant): this is a QUERY — retrying a
	// hung query masks a responder problem as latency (natsclient
	// mutation-vs-query rule). Handler failures (entity_not_found) still arrive
	// classified, so a never-created entity is a loud error, not an empty read.
	respData, err := r.client.RequestClassified(ctx, queryEntitySubject, reqData, readTimeout)
	if err != nil {
		var ce *errs.ClassifiedError
		if errors.As(err, &ce) && ce.Code != "" {
			return nil, fmt.Errorf("read facts failed [%s]: %w", ce.Code, err)
		}
		return nil, fmt.Errorf("request %s: %w", queryEntitySubject, err)
	}
	var entity graph.EntityState
	if err := json.Unmarshal(respData, &entity); err != nil {
		return nil, fmt.Errorf("unmarshal entity query: %w", err)
	}
	return filterByPrefix(entity.Triples, prefix), nil
}
