package conversationchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/graph"
)

// The PARK-MESSAGE POSTING lane (conversation-channel-seam D8, re-homed from
// issue-intake): every park rule publishes `user.response.<instance>` on the USER
// stream — this consumer turns that publish into a thread message VIA THE CHANNEL
// PORT (not a GitHub CreateComment call), so a parked run's `run.awaiting.human`
// message reaches the human where they live, through whatever channel is wired.
// Posting is DOWNSTREAM of the park: the park fact is already durable, a posting
// failure is retried bounded (consumer redelivery) and NEVER blocks the park itself.

// UserResponseSubject is the park lane's publish namespace (the USER stream).
const UserResponseSubject = "user.response.>"

// parkPoster consumes user.response.* publishes and posts park messages via the
// Channel port. A nil channel is the no-forge-token deployment (allowlist-only
// boots, e2e journeys): the park stays graph-only, exactly as before the carve.
type parkPoster struct {
	channel conversation.Channel
	fetcher admission.EntityFetcher
	logger  *slog.Logger
}

// publishEnvelope is the rule engine's executePublish payload (the station
// dispatch envelope shape — entity_id is the FIRING entity of the park rule).
type publishEnvelope struct {
	EntityID string `json:"entity_id"`
}

// handleUserResponse posts the parked run's message to its thread. nil = definitive
// ack (posted, or nothing postable); error = transient (redelivered bounded — a
// forge blip must not lose the human's notification).
func (p *parkPoster) handleUserResponse(ctx context.Context, payload []byte) error {
	if p.channel == nil {
		// No forge client — a legitimate deployment shape (journeys, allowlist-
		// only boots). The park is already durable + visible on the graph. This
		// graph-only degrade is the consumer's decision (grp2-review carry-forward a).
		p.logger.Debug("park-post: no conversation channel; park message stays graph-only")
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
	msg := entityTriple(run, "run.awaiting.human")
	ref := entityTriple(run, "run.issue.ref")
	if msg == "" {
		// The publish raced the park add (per-action revisions) — redeliver so
		// the comment carries the actual message.
		return fmt.Errorf("park-post: run %s carries no run.awaiting.human yet; redelivering", runEntityID)
	}
	if ref == "" {
		// A run without an issue binding (a journey-driven run pre-webhook, or
		// ask_human outside an issue) has nowhere to post — the park stays
		// graph-only, stated loud.
		p.logger.Warn("park-post: run has no run.issue.ref; park message stays graph-only",
			slog.String("run", runEntityID), slog.String("message", msg))
		return nil
	}
	// Validate the thread coordinate BEFORE posting so a malformed ref stays a
	// graph-only skip (definitive ack), NOT a Post-error redeliver — the Channel's
	// fail-closed Post cannot distinguish a permanent bad-ref from a transient blip
	// (grp2-review carry-forward b).
	if _, _, _, err := admission.SplitRef(ref); err != nil {
		p.logger.Error("park-post: unparseable run.issue.ref; park message stays graph-only",
			slog.String("ref", ref), slog.Any("error", err))
		return nil
	}

	body := fmt.Sprintf("⏸️ **semdev parked this run — a human decision is needed.**\n\n> %s\n\nRun: `%s`\n\nReply `/semdev approve` (for the change gate) or resolve the named blocker and re-trigger.",
		msg, runEntityID)
	thread, err := p.channel.ResolveThread(ctx, ref)
	if err != nil {
		return fmt.Errorf("park-post: resolve thread for %s: %w", ref, err)
	}
	if err := p.channel.Post(ctx, thread, body); err != nil {
		return fmt.Errorf("park-post: post to %s: %w", ref, err)
	}
	p.logger.Info("park-post: park message posted to the thread",
		slog.String("ref", ref), slog.String("run", runEntityID))
	return nil
}

// UserNoteSubject is the fault-note lane's publish namespace. It rides the SAME
// USER stream as the park lane (subjects user.>) but a DISTINCT subject, because
// the two are different things: a park is a lifecycle fact with a message attached,
// a note is only a message. Keeping them apart is load-bearing — see
// handleUserNote.
const UserNoteSubject = "user.note.>"

// faultNoteBody is the fallback note posted when a classifier FAULTS (design D9 /
// semstreams HIGH-3). It names the deterministic escape hatch, because the one
// thing the human must not experience is silence: from their side, a faulted
// classification and a semdev that ignored them look identical.
const faultNoteBody = "🤔 I couldn't read that as an approval or a rejection.\n\n" +
	"Reply `/semdev approve` or `/semdev reject` to decide this change explicitly — or just re-word what you meant and I'll try again."

// handleUserNote posts the classifier fault note to the run's thread
// (conversation/05). It deliberately shares the park lane's resolve-and-post
// machinery but NOT its fact: the note stamps nothing.
//
// WHY THIS IS NOT THE PARK LANE: the park poster reads run.awaiting.human, and
// that predicate is run-lifecycle/02's RESUME BLOCKER. Routing a classifier fault
// through the park would wedge the run — a later successful approval could never
// resume it. So a fault gets a message and nothing else, leaving the gate exactly
// as open as it was.
//
// nil = definitive ack; error = transient (redelivered bounded — a forge blip must
// not lose the human's only signal that their message was not understood).
func (p *parkPoster) handleUserNote(ctx context.Context, payload []byte) error {
	if p.channel == nil {
		p.logger.Warn("fault-note: no conversation channel; the classifier fault is INVISIBLE to the human (the note is its only artifact)")
		return nil
	}
	var env publishEnvelope
	if err := json.Unmarshal(payload, &env); err != nil || env.EntityID == "" {
		p.logger.Error("fault-note: malformed user.note envelope; skipping", slog.Any("error", err))
		return nil
	}
	runEntityID, err := p.resolveRun(ctx, env.EntityID)
	if err != nil {
		return fmt.Errorf("fault-note: resolve run from %s: %w", env.EntityID, err)
	}
	run, err := p.fetcher.Entity(ctx, runEntityID)
	if err != nil {
		return fmt.Errorf("fault-note: read run %s: %w", runEntityID, err)
	}
	if run == nil {
		p.logger.Warn("fault-note: run entity absent; nothing to post", slog.String("run", runEntityID))
		return nil
	}
	// A DECISION ALREADY LANDED → say nothing (grp6-review M3). The note and the
	// apply consumer's transparency post are both human-facing, and together they
	// can contradict: "✅ Approving this change based on @jo's decision" immediately
	// followed by "🤔 I couldn't read that". That pair is worse than noise — the
	// transparency post is the ENTIRE visibility case for a gate with no harness
	// floor (D8 guard 4), so teaching the human those announcements are unreliable
	// undermines the guard itself. Two shapes reach here: a mirror that failed after
	// its classification landed, and a classifier terminal that arrives after the
	// gate was released by something else (rule 05 cannot guard on gate facts —
	// its conditions are loop-scoped). Both are correctly silent: a decision was
	// applied AND announced, so a note about not understanding is simply wrong.
	if landed := decidedGate(run); landed != "" {
		p.logger.Info("fault-note: a gate decision already landed and was announced; suppressing the note",
			slog.String("run", runEntityID), slog.String("decided", landed))
		return nil
	}
	ref := entityTriple(run, "run.issue.ref")
	if ref == "" {
		p.logger.Warn("fault-note: run has no run.issue.ref; the classifier fault stays graph-only",
			slog.String("run", runEntityID))
		return nil
	}
	// Validate the coordinate BEFORE posting so a malformed ref is a definitive
	// skip, not an endless Post-error redeliver (the park lane's posture).
	if _, _, _, err := admission.SplitRef(ref); err != nil {
		p.logger.Error("fault-note: unparseable run.issue.ref; note stays graph-only",
			slog.String("ref", ref), slog.Any("error", err))
		return nil
	}
	thread, err := p.channel.ResolveThread(ctx, ref)
	if err != nil {
		return fmt.Errorf("fault-note: resolve thread for %s: %w", ref, err)
	}
	if err := p.channel.Post(ctx, thread, faultNoteBody); err != nil {
		return fmt.Errorf("fault-note: post to %s: %w", ref, err)
	}
	p.logger.Info("fault-note: classifier fault surfaced to the human",
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
