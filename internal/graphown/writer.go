package graphown

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	"github.com/c360studio/semstreams/pkg/projection"
)

// Writer is ONE owner's bound write surface — the replacement for the deleted
// agentictools.OwnedFactWriter at semdev's call sites.
//
// It exists to make the (owner, entity class) contract resolution UNAVOIDABLE.
// projection.ReplaceOwned takes a contract NAME, and a call site that hardcodes
// that string silently decouples the write from the classification in entityClass
// — which is the one thing the offline censuses provably cannot check (design
// D3b). Because Replace resolves through ContractFor on every call, a predicate
// classed onto the wrong entity fails AT THE SITE, and the per-tool unit tests
// that already assert which entity each fact lands on go red. Tools therefore take
// *Writer (concrete), never a hand-rolled interface: a test double substituted
// ABOVE this type would bypass the resolution and take that proof with it. Fake
// the framework's projection.OwnedReplacer underneath instead — which also lets a
// test assert the resolved contract and group, something the old writer could not
// express.
type Writer struct {
	owner    string
	replacer projection.OwnedReplacer
	reader   projection.AuthoritativeReader
}

// NewWriter binds a write-only surface to one vocab Source. replacer is that
// owner's contract-bound projection.MutationClient (nil in the schema-scanning
// censuses, where the tool registers schema-only and Replace fails loudly if ever
// called).
//
// NOTE for the composition root: a tool that guards on `writer == nil` needs a nil
// *Writer, NOT a Writer wrapping a nil client — the latter passes the guard and
// fails later, at the write. Build it as
// `var w *graphown.Writer; if client != nil { w = graphown.NewWriter(...) }`.
func NewWriter(owner string, replacer projection.OwnedReplacer) *Writer {
	return &Writer{owner: owner, replacer: replacer}
}

// NewReadWriter is NewWriter plus the authoritative read-back, for the two
// shrinking-package sites (check_floors' findings clear, project_tasks'
// immutability gate). One projection.MutationClient satisfies both roles, so the
// same client is normally passed twice.
func NewReadWriter(owner string, replacer projection.OwnedReplacer, reader projection.AuthoritativeReader) *Writer {
	return &Writer{owner: owner, replacer: replacer, reader: reader}
}

// ReadOwnedPredicates returns this owner's predicates on entityID beginning with
// ownedPrefix, sorted — the read half of the shrinking-package pattern. Only a
// writer built by NewReadWriter can serve it.
func (w *Writer) ReadOwnedPredicates(ctx context.Context, entityID, ownedPrefix string) ([]string, error) {
	if w == nil || w.reader == nil {
		return nil, fmt.Errorf("graphown: owner %q has no authoritative reader bound — build it with NewReadWriter", w.Owner())
	}
	return ReadOwnedPredicates(ctx, w.reader, entityID, ownedPrefix)
}

// Owner reports the vocab Source this writer stamps as.
func (w *Writer) Owner() string {
	if w == nil {
		return ""
	}
	return w.owner
}

// Replace reconciles this owner's COMPLETE owned group on entityID to desired.
//
// It is NOT the old ReplaceTriples(add, removePredicates). The group is the blast
// radius: every predicate in the owner's group for this entity class that desired
// does not re-supply is DELETED (design D3a). Callers that previously passed an
// explicit remove list pass the resulting full set as desired; callers that cleared
// a package pass nil.
//
// Mutation metadata is left ZERO deliberately. Setting Metadata.Source would make
// the client REJECT any triple carrying a different Source, and semdev's triples
// already stamp their own writer (G5); a zero Timestamp likewise lets each triple
// keep its own. That keeps the recorded fact byte-identical to what the old writer
// wrote — the whole point of the conservative migration.
func (w *Writer) Replace(ctx context.Context, entityID string, desired []message.Triple) error {
	if w == nil || w.replacer == nil {
		return fmt.Errorf("graphown: no bound mutation client for owner %q — a fact write was attempted on a schema-only registration", w.Owner())
	}
	contract, err := ContractFor(w.owner, entityID)
	if err != nil {
		return err
	}
	_, err = w.replacer.ReplaceOwned(ctx, projection.ReplaceOwnedMutation{
		Contract: contract,
		Group:    OwnedGroup,
		EntityID: entityID,
		Desired:  desired,
	})
	if err != nil {
		return fmt.Errorf("replace owned %q on %s: %w", contract, entityID, err)
	}
	return nil
}

// ReadOwnedPredicates returns the DISTINCT predicates present on entityID whose
// name begins with ownedPrefix, sorted — the read half of the shrinking-package
// pattern, preserved from the deleted OwnedFactWriter.
//
// The prefix filter is applied LOCALLY because ReadAuthoritative returns the WHOLE
// entity (every owner's facts), where the old ReadOwnedPredicates filtered
// server-side. Dropping it does not merely widen a result — it inverts callers that
// gate on emptiness (projecttasks' task.spec immutability check would then see
// triples on every run and refuse universally). An empty prefix is REJECTED for the
// same reason the old surface rejected it: on a shared entity an unscoped read
// returns predicates the caller does not own.
func ReadOwnedPredicates(ctx context.Context, r projection.AuthoritativeReader, entityID, ownedPrefix string) ([]string, error) {
	if r == nil {
		return nil, errors.New("graphown: no bound authoritative reader — an owned-predicate read was attempted on a schema-only registration")
	}
	if ownedPrefix == "" {
		return nil, errors.New("graphown: read owned predicates: ownedPrefix must be non-empty (an unscoped read returns predicates you do not own)")
	}
	entity, err := r.ReadAuthoritative(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("read authoritative %s: %w", entityID, err)
	}
	if entity == nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	for _, t := range entity.Triples {
		if t.Predicate == "" || !strings.HasPrefix(t.Predicate, ownedPrefix) {
			continue
		}
		seen[t.Predicate] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// WriteErrorKind classifies a mutation failure for a tool result, extending
// changefacts.ReadErrorKind's classified-vs-transport split with the projection
// client's own taxonomy.
//
// The distinction matters for the retry posture: a MutationInvalid (contract
// mismatch, a predicate outside the selected group, an entity outside the
// pattern) is a BUG in the wiring, not a blip. Classified as Network it would be
// retried by the loop until the iteration cap, burning turns on a write that can
// never succeed; classified as Internal it faults loudly on the first attempt.
func WriteErrorKind(err error) agentic.ToolErrorKind {
	var me *projection.MutationError
	if errors.As(err, &me) {
		switch me.Kind {
		case projection.MutationInvalid, projection.MutationInternal, projection.MutationNotFound,
			projection.MutationConflict, projection.MutationRevisionConflict, projection.MutationStaleOwnerToken:
			// MutationStaleOwnerToken is explicitly non-retryable per the framework:
			// "Callers should NOT retry without resolving the ownership conflict"
			// (graph/mutation_responses.go). Conflict/RevisionConflict are unreachable on
			// the ReplaceOwned lane (it never sets ExpectedRevision), so Internal is the
			// safe default for them.
			return agentic.ToolErrorInternal
		case projection.MutationCommittedUnverified:
			// The write COMMITTED but the authoritative readback did not equal Desired
			// (mutation_client.go committedUnverified) — a concurrent writer of the same
			// group, or a readback divergence. Retrying does NOT converge, and the
			// idempotence argument below does not apply: the write already landed. Fault
			// loudly rather than burning the loop's budget re-landing it.
			return agentic.ToolErrorInternal
		default:
			// Unavailable / commit-unknown are genuinely transport-shaped: the write may
			// not have landed, and ReplaceOwned is idempotent, so a retry converges.
			return agentic.ToolErrorNetwork
		}
	}
	var ce *errs.ClassifiedError
	if errors.As(err, &ce) {
		return agentic.ToolErrorInternal
	}
	// A ContractFor failure is a plain error and a wiring bug, not a transport
	// fault — but it is unreachable here only if every caller resolves through
	// Writer.Replace, so keep the conservative Internal default for our own errors.
	if strings.HasPrefix(err.Error(), "graphown:") {
		return agentic.ToolErrorInternal
	}
	return agentic.ToolErrorNetwork
}
