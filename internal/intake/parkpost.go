package intake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// The PARK-MESSAGE POSTING lane (forge-io-real-lanes D4): every park rule
// already publishes `user.response.<instance>` on the USER stream — this
// consumer (the intake component's second durable consumer, G1-minimality over
// a separate comms component) turns that publish into an ISSUE COMMENT via the
// forge client, so a parked run's `run.awaiting.human` message reaches the
// human where they live. Posting is DOWNSTREAM of the park: the park fact is
// already durable, a posting failure is retried bounded (consumer redelivery)
// and NEVER blocks the park itself.

// UserResponseSubject is the park lane's publish namespace (the USER stream).
const UserResponseSubject = "user.response.>"

// Commenter is the narrow posting surface — github.Client satisfies it.
type Commenter interface {
	CreateComment(ctx context.Context, owner, repo string, number int, body string) error
}

// EntityFetcher resolves one graph entity by ID — implemented over
// graph.ingest.query.entity; faked in unit pins.
type EntityFetcher interface {
	Entity(ctx context.Context, entityID string) (*graph.EntityState, error)
}

// parkPoster consumes user.response.* publishes and posts park comments.
type parkPoster struct {
	commenter Commenter
	fetcher   EntityFetcher
	logger    *slog.Logger
}

// publishEnvelope is the rule engine's executePublish payload (the station
// dispatch envelope shape — entity_id is the FIRING entity of the park rule).
type publishEnvelope struct {
	EntityID string `json:"entity_id"`
}

// handleUserResponse posts the parked run's message as an issue comment.
// nil = definitive ack (posted, or nothing postable); error = transient
// (redelivered bounded — a forge blip must not lose the human's notification).
func (p *parkPoster) handleUserResponse(ctx context.Context, payload []byte) error {
	if p.commenter == nil {
		// No forge token — a legitimate deployment shape (journeys, allowlist-
		// only boots). The park is already durable + visible on the graph.
		p.logger.Debug("park-post: no forge client; park message stays graph-only")
		return nil
	}
	var env publishEnvelope
	if err := json.Unmarshal(payload, &env); err != nil || env.EntityID == "" {
		p.logger.Error("park-post: malformed user.response envelope; skipping", slog.Any("error", err))
		return nil
	}

	runEntityID, err := p.resolveRun(ctx, env.EntityID)
	if err != nil {
		return fmt.Errorf("park-post: resolve run from %s: %w", env.EntityID, err)
	}
	run, err := p.fetcher.Entity(ctx, runEntityID)
	if err != nil {
		return fmt.Errorf("park-post: read run %s: %w", runEntityID, err)
	}
	if run == nil {
		p.logger.Warn("park-post: run entity absent; nothing to post", slog.String("run", runEntityID))
		return nil
	}
	message := entityTriple(run, "run.awaiting.human")
	ref := entityTriple(run, "run.issue.ref")
	if message == "" {
		// The publish raced the park add (per-action revisions) — redeliver so
		// the comment carries the actual message.
		return fmt.Errorf("park-post: run %s carries no run.awaiting.human yet; redelivering", runEntityID)
	}
	if ref == "" {
		// A run without an issue binding (a journey-driven run pre-webhook, or
		// ask_human outside an issue) has nowhere to post — the park stays
		// graph-only, stated loud.
		p.logger.Warn("park-post: run has no run.issue.ref; park message stays graph-only",
			slog.String("run", runEntityID), slog.String("message", message))
		return nil
	}
	owner, repo, number, err := SplitRef(ref)
	if err != nil {
		p.logger.Error("park-post: unparseable run.issue.ref; park message stays graph-only",
			slog.String("ref", ref), slog.Any("error", err))
		return nil
	}

	body := fmt.Sprintf("⏸️ **semdev parked this run — a human decision is needed.**\n\n> %s\n\nRun: `%s`\n\nReply `/semdev approve` (for the change gate) or resolve the named blocker and re-trigger.",
		message, runEntityID)
	if err := p.commenter.CreateComment(ctx, owner, repo, number, body); err != nil {
		return fmt.Errorf("park-post: comment on %s: %w", ref, err)
	}
	p.logger.Info("park-post: park message posted to the issue",
		slog.String("ref", ref), slog.String("run", runEntityID))
	return nil
}

// resolveRun maps the park publish's firing entity to its run: a chain entity
// IS the run; a loop entity carries the agent.run.entity-id anchor.
func (p *parkPoster) resolveRun(ctx context.Context, entityID string) (string, error) {
	if strings.Contains(entityID, ".agent.chain.execution.") {
		return entityID, nil
	}
	e, err := p.fetcher.Entity(ctx, entityID)
	if err != nil {
		return "", err
	}
	if e == nil {
		return "", fmt.Errorf("firing entity %s not found", entityID)
	}
	if anchor := entityTriple(e, "agent.run.entity-id"); anchor != "" {
		return anchor, nil
	}
	return "", fmt.Errorf("firing entity %s carries no agent.run.entity-id anchor", entityID)
}

// SplitRef parses a host-neutral "owner/repo#number" coordinate into its parts. Exported for
// the operator launch driver (which needs the issue number to read the issue's content).
func SplitRef(ref string) (owner, repo string, number int, err error) {
	hash := strings.LastIndexByte(ref, '#')
	slash := strings.IndexByte(ref, '/')
	if hash <= 0 || slash <= 0 || slash > hash {
		return "", "", 0, fmt.Errorf("ref %q is not owner/repo#number", ref)
	}
	n, err := strconv.Atoi(ref[hash+1:])
	if err != nil || n <= 0 {
		return "", "", 0, fmt.Errorf("ref %q has no issue number", ref)
	}
	return ref[:slash], ref[slash+1 : hash], n, nil
}

// entityTriple returns the first string object of the exact predicate.
func entityTriple(e *graph.EntityState, predicate string) string {
	for _, tr := range e.Triples {
		if tr.Predicate != predicate {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			return s
		}
	}
	return ""
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
