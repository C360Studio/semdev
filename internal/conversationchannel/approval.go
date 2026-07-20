package conversationchannel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// The APPROVAL ADAPTER (conversation-channel-seam D8, re-homed from issue-intake):
// the change-approval human gate, operable from the conversation thread. v1 signal
// = the comment command `/semdev approve` by an AUTHORIZED actor. Re-homed behind
// the channel-neutral Channel port: it reads a neutral Message (via the GitHub
// impl's contained normalize) — NOT a githubwebhook.CommentEvent — so a second
// channel would drive the SAME approval logic. The adapter writes EXACTLY the fact
// the journeys' stand-in wrote — run.change.approved="true", Source approval-adapter,
// on the RUN — and the untouched resume rule (run-lifecycle/02) does the rest (G2:
// the transition stays rule-owned).
//
// Event invariant: the command counts ONLY when the message is attributable to its
// author (the Channel's normalize enforces sender==author before emitting a
// Message), and the author must be authorized (allowlist or push-capable
// collaborator — the same admission.Authorize the intake gate runs). An
// unauthorized command is ignored with a log line and zero writes.

// ApprovalCommandVerb is the second token of the approval command (the first
// is the configured opt-in command, default "/semdev").
const ApprovalCommandVerb = "approve"

// RejectCommandVerb is the second token of the exact reject command
// (`<command> reject`, default "/semdev reject") — the deterministic
// counterpart to approve. It stamps run.change.rejected with zero model turns
// (nl-conversation-intent D4); a phase-guarded run-lifecycle rule cancels the
// gated run on it.
const RejectCommandVerb = "reject"

// ApprovedSource is the vocab-declared writer of run.change.approved — the exact
// stand-in Source the journeys proved. The PREDICATE lives in the shared admission
// core (the resolver reads it too).
const ApprovedSource = "approval-adapter"

// approvalAdapter consumes github.event.comment payloads (via the Channel's neutral
// normalize) and releases the change-approval gate.
type approvalAdapter struct {
	cfg      ComponentConfig
	checker  admission.PermissionChecker
	resolver admission.RunResolver
	writer   agentictools.OwnedFactWriter
	reader   changefacts.Reader
	logger   *slog.Logger

	ignored int64
	granted int64
	pending int64
}

func newApprovalAdapter(client *natsclient.Client, cfg ComponentConfig, checker admission.PermissionChecker, platform component.PlatformMeta, logger *slog.Logger) *approvalAdapter {
	a := &approvalAdapter{cfg: cfg, checker: checker, logger: logger}
	if client != nil {
		a.resolver = admission.NewRunResolver(client, platform.Org, platform.Platform)
		a.writer = agentictools.NewNATSOwnedFactWriter(client)
		a.reader = changefacts.NewNATSReader(client)
	}
	return a
}

// handleCommentEvent processes one flattened WEBHOOK comment event through the
// Channel's neutral normalize, then hands the Message to the shared approval core
// (handleMessage). It is the webhook transport's entry point; the poller (grp3)
// calls handleMessage directly with a Message it Read (D3 — one core, two
// transports). nil = definitive (acked); error = transient (redelivered). A DECODE
// failure is definitive (acked) — the receiver only publishes shapes it flattened
// itself (grp2-review carry-forward b). A non-signal comment (not created, not
// issue-bound, or not attributable to its sender) is skipped without a write.
func (a *approvalAdapter) handleCommentEvent(ctx context.Context, payload []byte) error {
	msg, thread, ok, err := conversation.NormalizeInboundComment(payload)
	if err != nil {
		a.logger.Error("approval: malformed comment event; skipping", slog.Any("error", err))
		return nil
	}
	// ok folds the old Relevant && Attributable: a created, issue-bound comment
	// whose author is its sender. A non-signal comment is skipped without a write.
	if !ok {
		return nil
	}
	return a.handleMessage(ctx, msg, thread)
}

// handleMessage is the SHARED, transport-neutral approval core (pull-first-transport
// D3; nl-conversation-intent D4). It repo-scope-checks the thread, then DISPATCHES:
//
//   - an exact `/semdev approve` → the deterministic approve fast-path (byte-identical
//     to the pre-NL path — stamps run.change.approved, zero model turns);
//   - an exact `/semdev reject` → the deterministic reject fast-path (stamps
//     run.change.rejected, zero model turns);
//   - ANY OTHER message → the NL bridge (bridgeNonCommand): an authorized author's
//     non-command message on a run at awaiting_approval, not already classified, is
//     stamped as conversation.pending.* for a classifier to read; everything else
//     (chatter, unauthorized authors, non-gated runs) stamps nothing, zero model turns.
//
// BOTH transports feed it: the webhook consumer via handleCommentEvent (after
// NormalizeInboundComment's sender==author guard), the poller via Channel.Read. It
// authorizes Message.Author either way (H-1: a poll read carries one identity, the
// comment author).
//
// nil = definitive (acked); error = transient (redelivered): an authorization
// lookup fault, a graph read/write blip, or (exact-command only) the run not existing
// YET (the wake→mint race — the approval may beat the mint by seconds; redelivery
// retries it).
func (a *approvalAdapter) handleMessage(ctx context.Context, msg conversation.Message, thread conversation.ThreadRef) error {
	if a.cfg.Repo != "" && !strings.EqualFold(strings.TrimSpace(a.cfg.Repo), refRepo(string(thread))) {
		return nil
	}
	switch {
	case hasApprovalCommand(a.cfg.OptInCommand, msg.Body):
		return a.releaseGate(ctx, msg, thread, admission.ApprovedPredicate)
	case hasRejectCommand(a.cfg.OptInCommand, msg.Body):
		return a.releaseGate(ctx, msg, thread, admission.RejectedPredicate)
	default:
		return a.bridgeNonCommand(ctx, msg, thread)
	}
}

// releaseGate is the deterministic exact-command gate write shared by the approve
// and reject fast-paths (D4): build the admission Event from the neutral Message +
// the thread's resolved code-host scope (owner/repo from SplitRef) → Authorize the
// author → resolve the run → stamp the gate fact via the ONE shared writer. predicate
// is admission.ApprovedPredicate or admission.RejectedPredicate; both carry Source
// ApprovedSource (one G5 writer, two facts — the D11 census). This keeps Authorize +
// the approve write byte-identical to the pre-NL path (grp2-review carry-forward a).
//
// An already-approved run is a no-op for BOTH verbs: replaying approve is idempotent,
// and rejecting an approved run is REFUSED (H1 — an approval is irreversible once
// landed; the transparency post is visibility, the PR merge the downstream human stop).
func (a *approvalAdapter) releaseGate(ctx context.Context, msg conversation.Message, thread conversation.ThreadRef, predicate string) error {
	owner, repo, _, err := admission.SplitRef(string(thread))
	if err != nil {
		// A real thread always carries a parseable owner/repo#number (the webhook
		// flattener reconstructs FullName; the poller enumerates run.issue.ref refs),
		// so an unparseable thread is unreachable in practice. Fail CLOSED if it ever
		// occurs (review L2: a deliberate, safe divergence).
		a.logger.Error("approval: unparseable thread; skipping",
			slog.String("thread", string(thread)), slog.Any("error", err))
		return nil
	}
	ev := admission.Event{Actor: msg.Author, Owner: owner, Repo: repo, AuthoredText: msg.Body}

	// Authorization — the SAME gate admission runs, fail-closed with retry.
	authorized, err := admission.Authorize(ctx, a.admissionConfig(), ev, a.checker)
	if err != nil {
		a.logger.Warn("approval: authorization undetermined; redelivering",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor), slog.Any("error", err))
		return err
	}
	if !authorized {
		atomic.AddInt64(&a.ignored, 1)
		a.logger.Info("approval: unauthorized actor's command ignored (Event invariant)",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor))
		return nil
	}

	runEntityID, alreadyApproved, _, err := a.resolver.ResolveRunByRef(ctx, string(thread))
	if err != nil {
		return fmt.Errorf("approval: resolve run for %s: %w", string(thread), err)
	}
	if runEntityID == "" {
		// The run may not be minted yet (the approval raced the wake) — or the
		// ref never admitted. Redeliver a bounded number of times; MaxDeliver
		// exhausts loud in the consumer log.
		return fmt.Errorf("approval: no run carries run.issue.ref=%s yet; redelivering", string(thread))
	}
	if alreadyApproved {
		a.logger.Info("approval: run already approved; command is a no-op (approval is irreversible, H1)",
			slog.String("ref", string(thread)), slog.String("run", runEntityID), slog.String("predicate", predicate))
		return nil
	}

	if err := a.stampGateFact(ctx, runEntityID, predicate); err != nil {
		return fmt.Errorf("approval: stamp %s on %s: %w", predicate, runEntityID, err)
	}
	atomic.AddInt64(&a.granted, 1)
	a.logger.Info("approval: authorized command released the change gate",
		slog.String("ref", string(thread)), slog.String("actor", ev.Actor),
		slog.String("run", runEntityID), slog.String("predicate", predicate))
	return nil
}

// stampGateFact writes ONE gate fact (predicate=true) on the run under the single
// sanctioned writer Source ApprovedSource — the D11 shared gate writer both the
// exact-command fast-path and the group-5 apply consumer route through (the
// TestOnlySanctionedGateWriters census, group 5). ReplaceTriples is
// replace-by-predicate, so a redelivered stamp is idempotent.
func (a *approvalAdapter) stampGateFact(ctx context.Context, runEntityID, predicate string) error {
	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  predicate,
		Object:     "true",
		Source:     ApprovedSource,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return a.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{tr}, nil)
}

// bridgeNonCommand is the NL bridge (D4/D5): a message that is NOT an exact command,
// from an AUTHORIZED author, on a run at agent.run.phase == awaiting_approval, whose
// id is NOT already in the run's classified ledger, stamps
// conversation.pending.{message-id,author,body} on the run (Source conversation-adapter)
// for the group-4 spawn rule to hand a classifier. Chatter, unauthorized authors,
// non-gated runs, and already-classified ids stamp NOTHING and spend ZERO model turns.
//
// The pending SLOT is single-valued (replace-by-predicate): the latest authorized
// message wins (the accepted latest-wins asymmetry, D5 — the gate-still-open guard,
// the conservative-none persona, and the PR-merge backstop bound the risk). It stamps
// no classifier-spawn marker — that is the group-4 spawn RULE's own fire-once marker
// (the dev.developer.dispatched precedent).
func (a *approvalAdapter) bridgeNonCommand(ctx context.Context, msg conversation.Message, thread conversation.ThreadRef) error {
	if msg.ID == "" {
		// No channel-native id → the message cannot be grounded or deduped; drop it
		// definitively rather than stamp an ungroundable pending (defensive: a real
		// comment always carries an id).
		a.logger.Warn("approval: non-command message has no id; skipping the NL bridge",
			slog.String("ref", string(thread)), slog.String("actor", msg.Author))
		return nil
	}

	// Gate on the INTERNAL phase read FIRST, before the external Authorize call
	// (go-reviewer M1). Resolve is a read-only graph query with no external rate
	// limit; Authorize may hit the code host's permission API. GitHub delivers every
	// issue/PR comment on the bound repo here, so authorizing before the phase gate
	// would spend one permission call per chatter comment even when no gated run is
	// involved. Phase-gating first short-circuits all non-gated chatter with zero
	// external calls. The pending stamp stays behind Authorize, so the security
	// contract is unchanged. The NL bridge fires ONLY at the change-approval gate; a
	// message on a non-gated run (or an issue with no run yet) spends zero model
	// turns, and the poll transport re-reads the thread once it IS gated. (In a
	// webhook-only deployment there is no such re-read, so an NL message typed BEFORE
	// the run reaches the gate is acked-and-lost — an accepted asymmetry: the human
	// replies to the park-post, which lands AT the gate. An exact command survives
	// either way, being phase-agnostic.)
	runEntityID, _, phase, err := a.resolver.ResolveRunByRef(ctx, string(thread))
	if err != nil {
		return fmt.Errorf("approval: resolve run for %s: %w", string(thread), err)
	}
	if runEntityID == "" || phase != admission.PhaseAwaitingApproval {
		return nil
	}

	owner, repo, _, err := admission.SplitRef(string(thread))
	if err != nil {
		a.logger.Error("approval: unparseable thread; skipping",
			slog.String("thread", string(thread)), slog.Any("error", err))
		return nil
	}
	ev := admission.Event{Actor: msg.Author, Owner: owner, Repo: repo, AuthoredText: msg.Body}

	authorized, err := admission.Authorize(ctx, a.admissionConfig(), ev, a.checker)
	if err != nil {
		a.logger.Warn("approval: authorization undetermined; redelivering",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor), slog.Any("error", err))
		return err
	}
	if !authorized {
		atomic.AddInt64(&a.ignored, 1)
		a.logger.Info("approval: unauthorized non-command message ignored (no NL bridge)",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor))
		return nil
	}

	// Dedup against the multi-valued classified ledger (D5): if this message id was
	// already classified, a redelivery / poll re-read must not re-stamp pending and
	// re-spawn a classifier (MEDIUM-4). A genuinely new id proceeds. A read fault is
	// transient (redeliver) — never silently drop an authorized message.
	//
	// The dedup key msg.ID is transport-native: the poll path uses the comment id,
	// the webhook path the delivery GUID (conversation/github.go). These schemes are
	// non-interchangeable, but a deployment runs poll↔webhook XOR (the component's
	// activeConsumerPorts enforces it), so a single lane never mixes them. Even were
	// the XOR violated, the worst case is one bounded re-classification (wasted
	// tokens), never a double gate release — the group-5 apply consumer's
	// gate-still-open guard is the downstream stop.
	classified, err := a.reader.ReadFacts(ctx, runEntityID, conversationintent.IntentClassifiedPredicate)
	if err != nil {
		return fmt.Errorf("approval: read classified ledger on %s: %w", runEntityID, err)
	}
	if containsObject(classified, conversationintent.IntentClassifiedPredicate, msg.ID) {
		a.logger.Info("approval: message already classified; no re-stamp",
			slog.String("ref", string(thread)), slog.String("run", runEntityID), slog.String("message_id", msg.ID))
		return nil
	}

	now := time.Now().UTC()
	mk := func(pred, obj string) message.Triple {
		return message.Triple{Subject: runEntityID, Predicate: pred, Object: obj, Source: conversationintent.AdapterSource, Timestamp: now, Confidence: 1.0}
	}
	pendingTriples := []message.Triple{
		mk(conversationintent.PendingMessageIDPredicate, msg.ID),
		mk(conversationintent.PendingAuthorPredicate, msg.Author),
		mk(conversationintent.PendingBodyPredicate, msg.Body),
	}
	if err := a.writer.ReplaceTriples(ctx, runEntityID, pendingTriples, nil); err != nil {
		return fmt.Errorf("approval: stamp %s on %s: %w", conversationintent.PendingPrefix, runEntityID, err)
	}
	atomic.AddInt64(&a.pending, 1)
	a.logger.Info("approval: authorized non-command message bridged to the classifier",
		slog.String("ref", string(thread)), slog.String("run", runEntityID),
		slog.String("actor", msg.Author), slog.String("message_id", msg.ID))
	return nil
}

func (a *approvalAdapter) admissionConfig() admission.Config {
	return admission.Config{Allowlist: a.cfg.Allowlist, OptInLabel: a.cfg.OptInLabel, OptInCommand: a.cfg.OptInCommand}
}

// hasApprovalCommand reports whether text contains the two-token approval command
// (`<command> approve`, default "/semdev approve") as consecutive whole tokens.
func hasApprovalCommand(command, text string) bool {
	return hasCommandVerb(command, ApprovalCommandVerb, text)
}

// hasRejectCommand reports whether text contains the two-token reject command
// (`<command> reject`, default "/semdev reject") as consecutive whole tokens — the
// same tokenizing discipline as approve.
func hasRejectCommand(command, text string) bool {
	return hasCommandVerb(command, RejectCommandVerb, text)
}

// hasCommandVerb reports whether text contains `<command> <verb>` as consecutive
// whole whitespace-delimited tokens (case-insensitive) — same discipline as the
// opt-in match, so "/semdevil approve" and "prefix/semdev approve" do not match.
func hasCommandVerb(command, verb, text string) bool {
	if command == "" {
		return false
	}
	tokens := strings.Fields(text)
	for i := 0; i+1 < len(tokens); i++ {
		if strings.EqualFold(tokens[i], command) && strings.EqualFold(tokens[i+1], verb) {
			return true
		}
	}
	return false
}

// containsObject reports whether any triple of pred has string object want.
func containsObject(triples []message.Triple, pred, want string) bool {
	for _, tr := range triples {
		if tr.Predicate == pred {
			if s, ok := tr.Object.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// refRepo returns the "owner/repo" half of an "owner/repo#number" thread.
func refRepo(ref string) string {
	if i := strings.LastIndex(ref, "#"); i > 0 {
		return ref[:i]
	}
	return ref
}
