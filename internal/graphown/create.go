package graphown

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/projection"
)

// Creator is one create-owner's birth surface — the strict-Create sibling of
// Writer, with the same unavoidable contract resolution (design D3b): the call
// site supplies only its owner and the entity, and ContractFor derives the
// birth contract, so a misdirected birth fails loudly at the site.
type Creator struct {
	owner   string
	creator projection.EntityCreator
}

// NewCreator binds a birth surface to one create-owner Source. creator is the
// shared contract-validating projection.MutationClient (nil in the
// schema-scanning censuses, where Create fails loudly if ever called).
func NewCreator(owner string, creator projection.EntityCreator) *Creator {
	return &Creator{owner: owner, creator: creator}
}

// Creator returns owner's birth surface, or nil when the owner derives no
// contract (the census path — same posture as Writer).
func (c *Clients) Creator(owner string) *Creator {
	if !c.knows(owner) {
		return nil
	}
	return NewCreator(owner, c.client)
}

// Owner reports the vocab Source this creator births as.
func (c *Creator) Owner() string {
	if c == nil {
		return ""
	}
	return c.owner
}

// Create births entityID with triples under the owner's birth contract — strict
// create-or-conflict. Only a sanctioned create owner may birth: the framework
// client validates predicates against the contract's WHOLE allowed set (groups
// included), so without this gate a reconcile-owner's Creator could strict-
// create a bogus entity carrying its group predicates — an entity the rules
// would then act on. The gate lives HERE, at the write, so misuse fails AT THE
// SITE with the owner named (D3b), exactly as Writer.Replace does.
//
// A CONFLICT surfaces to the caller AS-IS: on a content-derived entity ID it
// is the idempotent-duplicate signal (the event was processed before), and
// swallowing it here would erase exactly the signal the intake lane keys its
// skip-the-wake decision on.
//
// The transport kinds retry bounded like Replace: unavailable never landed, and
// a commit-unknown retry that DID land converges to the conflict signal, which
// the caller's record-then-check-run flow already handles.
//
// Metadata: RequestID is the content-derived entity ID (stable across
// redeliveries — a retried logical create presents the identical request) and
// Source is the owner, matching the Source each triple already stamps (G5).
func (c *Creator) Create(ctx context.Context, entityID string, msgType message.Type, triples []message.Triple) error {
	if c == nil || c.creator == nil {
		return fmt.Errorf("graphown: no bound mutation client for owner %q — a birth write was attempted on a schema-only registration", c.Owner())
	}
	if !createOwners[c.owner] {
		return fmt.Errorf("graphown: owner %q is not a sanctioned create owner — only birth contracts may strict-create an entity (design D3/OQ2)", c.owner)
	}
	contract, err := ContractFor(c.owner, entityID)
	if err != nil {
		return err
	}
	mutation := projection.CreateMutation{
		Contract: contract,
		Entity:   &graph.EntityState{ID: entityID, MessageType: msgType},
		Triples:  triples,
		Metadata: projection.MutationMetadata{RequestID: entityID, Source: c.owner},
	}
	var lastErr error
	for attempt := 1; attempt <= transportAttempts; attempt++ {
		_, lastErr = c.creator.Create(ctx, mutation)
		if lastErr == nil {
			return nil
		}
		if attempt == transportAttempts || !convergentCreateKind(lastErr) {
			break
		}
		select {
		case <-ctx.Done():
			// Keep the classified error visible: on the shutdown-during-retry
			// path it is the one diagnostic fact (unavailable vs commit-unknown).
			return fmt.Errorf("create %q on %s: %w (last attempt: %v)", contract, entityID, ctx.Err(), lastErr)
		case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
		}
	}
	return fmt.Errorf("create %q on %s: %w", contract, entityID, lastErr)
}

// convergentCreateKind: only the transport kinds retry. A revision conflict
// cannot occur on a create, and a strict-create CONFLICT is the caller's
// duplicate signal, never a retry.
func convergentCreateKind(err error) bool {
	var me *projection.MutationError
	if !errors.As(err, &me) {
		return false
	}
	switch me.Kind {
	case projection.MutationUnavailable, projection.MutationCommitUnknown:
		return true
	default:
		return false
	}
}

// IsConflict reports whether err is the strict-create duplicate signal.
func IsConflict(err error) bool {
	var me *projection.MutationError
	return errors.As(err, &me) && me.Kind == projection.MutationConflict
}
