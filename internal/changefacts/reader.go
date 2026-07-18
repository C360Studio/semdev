// Package changefacts is the READ side of the openspec-io graph seam: it reads a
// run entity's authored change DOCUMENT off the graph (beta.147 D3: one scalar under
// openspec.change.document, not a triple tree) and deserializes the openspec.Change it
// carries. It is the mirror of the create_change author tool's write side — that tool
// serializes the change to the blob (OwnedFactWriter.ReplaceTriples), this reads and
// deserializes it so a tool can render or write it.
//
// It is the shared substrate under the hydrate tool (renders the change to
// markdown), the WriteChange-to-workspace tool (materializes the folder for the
// PR), and the CLI-validate step (materializes to a temp dir for the oracle) —
// all three need "read the run's change, reconstruct the Change" (via Hydrate). The
// projector needs the execution-rich per-task fields too and reads the whole document
// via HydrateDocument. Keeping the read in one place means the graph-query wiring and
// the document decode cannot drift between them.
//
// The Reader surface is deliberately narrow (read-only, prefix-scoped) and is the
// read analogue of agentictools.OwnedFactWriter. Both drive the same
// graph.ingest.query.entity lane; this returns the full triples so the document blob's
// object can be decoded.
package changefacts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
)

// Reader reads the full triples currently on an already-born entity whose
// predicate begins with prefix. prefix MUST be non-empty and scope the read to
// the caller's fact package (e.g. "openspec.change.<slug>."); an empty prefix is
// rejected, mirroring OwnedFactWriter.ReadOwnedPredicates — an unscoped read on a
// shared entity would pull every owner's facts.
//
// Unlike the write lane, this is a QUERY: the NATS-backed implementation uses the
// non-retrying classified request (a hung query is a responder problem, not
// latency to paper over). A never-created entity surfaces as a classified
// entity_not_found error, not an empty result.
type Reader interface {
	ReadFacts(ctx context.Context, entityID, prefix string) ([]message.Triple, error)
}

// Hydrate reads the run's authored change document off the run entity and returns the
// openspec.Change it carries (beta.147 D3: the change is ONE scalar under
// DocumentPredicate, not a triple tree). A run that never authored a change yields a
// Change with all-nil artifacts (the absent-is-nil contract), NOT an error — the caller
// decides whether an empty change is a failure for its step. The render/write/validate
// tools use this; the projector uses HydrateDocument (it needs the rich task fields too).
func Hydrate(ctx context.Context, r Reader, entityID, slug string) (*openspec.Change, error) {
	doc, err := HydrateDocument(ctx, r, entityID, slug)
	if err != nil {
		return nil, err
	}
	if doc.Change == nil {
		return &openspec.Change{Slug: slug}, nil
	}
	return doc.Change, nil
}

// HydrateDocument reads the full change document — the openspec.Change PLUS the
// execution-rich, graph-only per-task fields — off the run entity. The projector needs
// the rich fields; the render tools take only the Change (via Hydrate). An absent
// document yields the zero ChangeDocument (nil Change), not an error.
func HydrateDocument(ctx context.Context, r Reader, entityID, slug string) (ChangeDocument, error) {
	// slug is validated non-empty but does NOT scope the read: the document is a single
	// run-level fact under DocumentPredicate (single-change at M0, beta.147 D1), so the
	// read is by that fixed predicate, not a slug-scoped prefix. The M1 multi-change future
	// keys the change into the entity ID, an explicit seam.
	if slug == "" {
		return ChangeDocument{}, fmt.Errorf("hydrate change: slug is required")
	}
	triples, err := r.ReadFacts(ctx, entityID, DocumentPredicate)
	if err != nil {
		return ChangeDocument{}, fmt.Errorf("read %q on %s: %w", DocumentPredicate, entityID, err)
	}
	var raw string
	for _, t := range triples {
		if t.Predicate == DocumentPredicate {
			raw = objectString(t.Object)
			break
		}
	}
	return UnmarshalDocument(raw)
}

// ReadErrorKind classifies a read failure for a tool result: a handler-classified
// error (entity_not_found or another graph-side failure — an ordering/internal
// fault, not transport) maps to ToolErrorInternal; anything else is treated as a
// transport failure worth a network-kind result. The read-side tools share this
// so their error classification cannot drift. Mirrors the write side's
// writeErrKind in the create_change tool.
func ReadErrorKind(err error) agentic.ToolErrorKind {
	var ce *errs.ClassifiedError
	if errors.As(err, &ce) {
		return agentic.ToolErrorInternal
	}
	return agentic.ToolErrorNetwork
}

func objectString(o any) string {
	if s, ok := o.(string); ok {
		return s
	}
	if o == nil {
		return ""
	}
	return fmt.Sprint(o)
}

// filterByPrefix returns the triples whose predicate begins with prefix. The
// graph.ingest.query.entity handler returns the WHOLE entity (every owner's
// facts), so the NATS reader scopes to the caller's package here — the same
// prefix filter OwnedFactWriter applies to its predicate read-back.
func filterByPrefix(triples []message.Triple, prefix string) []message.Triple {
	out := make([]message.Triple, 0, len(triples))
	for _, t := range triples {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out
}
