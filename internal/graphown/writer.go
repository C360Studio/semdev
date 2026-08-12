package graphown

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	"github.com/c360studio/semstreams/pkg/projection"
)

// Writer is ONE writer's contract-validated write surface — semdev's call-site
// facade over pkg/projection's mutation client.
//
// It exists to make the (writer, entity class) contract resolution UNAVOIDABLE.
// projection.Reconcile takes a contract NAME, and a call site that hardcodes
// that string silently decouples the write from the classification in entityClass
// — which is the one thing the offline censuses provably cannot check (design
// D3b). Because Replace resolves through ContractFor on every call, a predicate
// classed onto the wrong entity fails AT THE SITE, and the per-tool unit tests
// that already assert which entity each fact lands on go red. Tools therefore take
// *Writer (concrete), never a hand-rolled interface: a test double substituted
// ABOVE this type would bypass the resolution and take that proof with it. Fake
// the framework's projection.PredicateReconciler underneath instead — which also
// lets a test assert the resolved contract and group, something the pre-ADR-056
// writer could not express.
type Writer struct {
	owner      string
	reconciler projection.PredicateReconciler
	reader     projection.AuthoritativeReader
}

// NewWriter binds a write-only surface to one vocab Source. reconciler is the
// shared contract-validating projection.MutationClient (nil in the
// schema-scanning censuses, where the tool registers schema-only and Replace
// fails loudly if ever called).
//
// NOTE for the composition root: a tool that guards on `writer == nil` needs a nil
// *Writer, NOT a Writer wrapping a nil client — the latter passes the guard and
// fails later, at the write. Build it as
// `var w *graphown.Writer; if client != nil { w = graphown.NewWriter(...) }`.
func NewWriter(owner string, reconciler projection.PredicateReconciler) *Writer {
	return &Writer{owner: owner, reconciler: reconciler}
}

// NewReadWriter is NewWriter plus the authoritative read-back, for the two
// shrinking-package sites (check_floors' findings clear, project_tasks'
// immutability gate). One projection.MutationClient satisfies both roles, so the
// same client is normally passed twice.
func NewReadWriter(owner string, reconciler projection.PredicateReconciler, reader projection.AuthoritativeReader) *Writer {
	return &Writer{owner: owner, reconciler: reconciler, reader: reader}
}

// ReadOwnedPredicates returns this writer's predicates on entityID beginning with
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

// replaceAttempts bounds Replace's convergent-kind retry. Three total attempts:
// enough to ride out one rule-write interleave or one graph-ingest blip, small
// enough that sustained contention surfaces to the caller instead of spinning.
const replaceAttempts = 3

// Replace reconciles this writer's COMPLETE owned group on entityID to desired.
//
// The group is the blast radius: every predicate in the writer's group for this
// entity class that desired does not re-supply is DELETED (design D3a). Callers
// that previously passed an explicit remove list pass the resulting full set as
// desired; callers that cleared a package pass nil.
//
// Reconcile is revision-fenced inside the client (it reads the entity, then
// writes against that exact revision) and performs ONE request — the client
// retries nothing. The seam owns the bounded retry for the three kinds a later
// attempt converges on:
//
//   - revision-conflict: another writer bumped the entity between the client's
//     read and its write. G5 gives every predicate one writer, so on a semdev
//     write this is interleaving noise (typically a rule action landing on the
//     same entity), never a semantic collision on the group; re-entering
//     Reconcile re-reads and re-fences.
//   - unavailable: no responder took the request — the write did not land.
//   - commit-unknown: delivery without a valid reply — the write MAY have
//     landed, and re-issuing converges because a reconcile is a full-group set.
//
// Everything else (invalid, not-found, conflict, internal) is bug-shaped and
// surfaces immediately. Exhaustion returns the classified error unchanged so the
// caller's retry/park routing owns it — the seam never converts exhaustion into
// silence.
//
// Mutation metadata is left ZERO deliberately. Setting Metadata.Source would make
// the client REJECT any triple carrying a different Source, and semdev's triples
// already stamp their own writer (G5); a zero Timestamp likewise lets each triple
// keep its own.
func (w *Writer) Replace(ctx context.Context, entityID string, desired []message.Triple) error {
	if w == nil || w.reconciler == nil {
		return fmt.Errorf("graphown: no bound mutation client for owner %q — a fact write was attempted on a schema-only registration", w.Owner())
	}
	contract, err := ContractFor(w.owner, entityID)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= replaceAttempts; attempt++ {
		_, lastErr = w.reconciler.Reconcile(ctx, projection.ReconcileMutation{
			Contract: contract,
			Group:    OwnedGroup,
			EntityID: entityID,
			Desired:  desired,
		})
		if lastErr == nil {
			return nil
		}
		if attempt == replaceAttempts || !convergentWriteKind(lastErr) {
			break
		}
		if delay := writeRetryBackoff(lastErr, attempt); delay > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("reconcile %q on %s: %w", contract, entityID, ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("reconcile %q on %s: %w", contract, entityID, lastErr)
}

// convergentWriteKind reports whether a later identical attempt can succeed.
func convergentWriteKind(err error) bool {
	var me *projection.MutationError
	if !errors.As(err, &me) {
		return false
	}
	switch me.Kind {
	case projection.MutationRevisionConflict, projection.MutationUnavailable, projection.MutationCommitUnknown:
		return true
	default:
		return false
	}
}

// writeRetryBackoff: a revision conflict retries immediately — the fix is the
// fresh fence Reconcile's internal re-read provides, and waiting under rule-write
// churn only widens the race window. The transport kinds back off (the deleted
// framework retry's initial delay, scaled per attempt) so a restarting
// graph-ingest gets a beat to resubscribe.
func writeRetryBackoff(err error, attempt int) time.Duration {
	var me *projection.MutationError
	if errors.As(err, &me) && me.Kind == projection.MutationRevisionConflict {
		return 0
	}
	return time.Duration(attempt) * 100 * time.Millisecond
}

// ReadOwnedPredicates returns the DISTINCT predicates present on entityID whose
// name begins with ownedPrefix, sorted — the read half of the shrinking-package
// pattern.
//
// The prefix filter is applied LOCALLY because ReadAuthoritative returns the WHOLE
// entity (every writer's facts). Dropping it does not merely widen a result — it
// inverts callers that gate on emptiness (projecttasks' task.spec immutability
// check would then see triples on every run and refuse universally). An empty
// prefix is REJECTED for the same reason: on a shared entity an unscoped read
// returns predicates the caller does not own.
//
// A MISSING entity maps to an EMPTY read, not an error: the beta.160 authority
// read classifies absence as not-found where the old surface returned an empty
// entity, and the emptiness-gating callers above treat "not born yet" as "nothing
// owned on it". Every other read failure stays loud.
func ReadOwnedPredicates(ctx context.Context, r projection.AuthoritativeReader, entityID, ownedPrefix string) ([]string, error) {
	if r == nil {
		return nil, errors.New("graphown: no bound authoritative reader — an owned-predicate read was attempted on a schema-only registration")
	}
	if ownedPrefix == "" {
		return nil, errors.New("graphown: read owned predicates: ownedPrefix must be non-empty (an unscoped read returns predicates you do not own)")
	}
	exact, err := r.ReadAuthoritative(ctx, entityID)
	if err != nil {
		var me *projection.MutationError
		if errors.As(err, &me) && me.Kind == projection.MutationNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("read authoritative %s: %w", entityID, err)
	}
	if exact == nil || exact.Entity == nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	for _, t := range exact.Entity.Triples {
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
		case projection.MutationInvalid, projection.MutationInternal,
			projection.MutationNotFound, projection.MutationConflict:
			// Not-found on a write means the target entity was never minted —
			// semdev writes land after dispatch on framework-minted entities, so
			// this is wiring, not timing. Conflict is a strict-create collision,
			// unreachable on the reconcile lane.
			return agentic.ToolErrorInternal
		default:
			// Revision-conflict past Replace's bounded retry is sustained
			// contention; unavailable/commit-unknown are transport-shaped. All
			// three converge on a later attempt (a reconcile is a full-group
			// set, hence idempotent), so the loop may retry them.
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
