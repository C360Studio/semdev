package changefacts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// readTimeout bounds one read round-trip. Matches the 5s budget the sibling
// owned-fact query uses — a fast KV op.
const readTimeout = 5 * time.Second

// natsReader adapts the framework's exact-entity authority read to Reader,
// scoping the returned entity's triples to the caller's owned prefix.
//
// The wire lives ENTIRELY in graph.ExactEntityReader — beta.160 changed the
// graph.ingest.query.entity response to the {entity, kvRevision} envelope, and
// semdev's previous hand-rolled decode read that envelope as an EMPTY
// EntityState: every read silently returned zero triples and the validation
// station refused every authored change ("no change document"). Owning the
// decode again would reopen exactly that class; the framework reader is the
// same code its own consumers decode with.
type natsReader struct {
	reader graph.ExactEntityReader
}

// NewNATSReader builds a Reader backed by the shared graph query surface. Wire
// this into read-side openspec-io tools (hydrate, WriteChange-to-workspace,
// CLI-validate). A nil client yields a reader that fails loudly at read time,
// not here — tools register schema-only without a client.
func NewNATSReader(client *natsclient.Client) Reader {
	if client == nil {
		return &natsReader{}
	}
	return &natsReader{reader: graph.NewExactEntityReader(client, readTimeout)}
}

// NewExactReader builds a Reader over an already-constructed exact-entity
// reader — the seam the offline envelope pins drive with a fake requester.
func NewExactReader(reader graph.ExactEntityReader) Reader {
	return &natsReader{reader: reader}
}

func (r *natsReader) ReadFacts(ctx context.Context, entityID, prefix string) ([]message.Triple, error) {
	if prefix == "" {
		return nil, fmt.Errorf("read facts: prefix must be non-empty (an unscoped read pulls every owner's facts off a shared entity)")
	}
	if r.reader == nil {
		return nil, fmt.Errorf("read facts: no graph reader bound — a fact read was attempted on a schema-only registration")
	}
	// The reader performs ONE classified request (a QUERY — retrying a hung
	// query masks a responder problem as latency). Handler failures
	// (entity_not_found) arrive classified, so a never-created entity is a loud
	// error, not an empty read.
	exact, err := r.reader.ReadExactEntity(ctx, entityID)
	if err != nil {
		var ce *errs.ClassifiedError
		if errors.As(err, &ce) && ce.Code != "" {
			return nil, fmt.Errorf("read facts failed [%s]: %w", ce.Code, err)
		}
		return nil, fmt.Errorf("read entity %s: %w", entityID, err)
	}
	if exact == nil || exact.Entity == nil {
		return nil, fmt.Errorf("read entity %s: empty authority response", entityID)
	}
	return filterByPrefix(exact.Entity.Triples, prefix), nil
}
