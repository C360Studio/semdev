// Package changefacts is the READ side of the openspec-io graph seam: it reads a
// run entity's openspec.change.<slug>.* facts back off the graph and rebuilds the
// openspec.Change they project. It is the mirror of the create_change author
// tool's write side — that tool stamps the facts (OwnedFactWriter.ReplaceTriples),
// this reads them and hydrates the Change so a tool can render or write it.
//
// It is the shared substrate under the hydrate tool (renders the change to
// markdown), the WriteChange-to-workspace tool (materializes the folder for the
// PR), and the CLI-validate step (materializes to a temp dir for the oracle) —
// all three need "read the run's change facts, reconstruct the Change." Keeping
// the read in one place means the graph-query wiring and the triple→Fact
// conversion cannot drift between them.
//
// The Reader surface is deliberately narrow (read-only, prefix-scoped) and is the
// read analogue of agentictools.OwnedFactWriter: the writer exposes
// ReadOwnedPredicates (predicate NAMES only, for the replace-clear set), which is
// not enough to reconstruct content — hydration needs the objects too. Both drive
// the same graph.ingest.query.entity lane; this returns the full triples.
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

// Hydrate reads slug's openspec.change.* facts off the run entity and rebuilds the
// Change they project. It is the inverse of the create_change author write: read
// the owned package, convert triples to subject-less openspec.Facts, then
// openspec.ChangeFromFacts. A run that never authored the change yields a Change
// with all-nil artifacts (ChangeFromFacts's absent-is-nil contract), NOT an error
// — the caller decides whether an empty change is a failure for its step.
func Hydrate(ctx context.Context, r Reader, entityID, slug string) (*openspec.Change, error) {
	if slug == "" {
		return nil, fmt.Errorf("hydrate change: slug is required")
	}
	prefix := openspec.ChangeEntityPrefix(slug)
	triples, err := r.ReadFacts(ctx, entityID, prefix)
	if err != nil {
		return nil, fmt.Errorf("read %q facts on %s: %w", prefix, entityID, err)
	}
	return openspec.ChangeFromFacts(slug, triplesToFacts(triples)), nil
}

// triplesToFacts drops each triple to the subject-less {Predicate, Object} pair
// the format layer consumes. Object is written as its string form by the engine
// (prose verbatim, scalars via strconv, arrays JSON-encoded), so it round-trips
// as a Go string; a non-string object (a hand-written or legacy fact) is
// formatted rather than silently dropped, so it still reaches ChangeFromFacts.
func triplesToFacts(triples []message.Triple) []openspec.Fact {
	facts := make([]openspec.Fact, 0, len(triples))
	for _, t := range triples {
		facts = append(facts, openspec.Fact{Predicate: t.Predicate, Object: objectString(t.Object)})
	}
	return facts
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
