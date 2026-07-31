package conversationchannel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/c360studio/semdev/internal/graphown"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
)

// The APPROVAL ADAPTER (conversation-channel-seam D8, re-homed from issue-intake):
// the change-approval human gate, operable from the conversation thread. v1 signal
// = the comment command `/semdev approve` by an AUTHORIZED actor. Re-homed behind
// the channel-neutral Channel port: it reads a neutral Message (via the GitHub
// impl's contained normalize) — NOT a githubwebhook.CommentEvent — so a second
// channel would drive the SAME approval logic. The adapter writes EXACTLY the fact
// the journeys' stand-in wrote — run.change.decision="approve", Source approval-adapter,
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
// counterpart to approve. It records the reject DECISION with zero model turns
// (nl-conversation-intent D4); a phase-guarded run-lifecycle rule cancels the
// gated run on it.
const RejectCommandVerb = "reject"

// ApprovedSource is the vocab-declared writer of run.change.decision — the exact
// stand-in Source the journeys proved. The PREDICATE lives in the shared admission
// core (the resolver reads it too).
const ApprovedSource = "approval-adapter"

// approvalAdapter consumes github.event.comment payloads (via the Channel's neutral
// normalize) and releases the change-approval gate.
type approvalAdapter struct {
	cfg      ComponentConfig
	checker  admission.PermissionChecker
	resolver admission.RunResolver
	// TWO owners, two clients: approval-adapter stamps the gate decision on the
	// run, conversation-adapter stamps the pending-message slot. ADR-056 binds one
	// owner per client, and these are distinct vocab Sources (G5).
	writer        *graphown.Writer
	pendingWriter *graphown.Writer
	reader        changefacts.Reader
	logger        *slog.Logger

	ignored int64
	granted int64
	pending int64
}

func newApprovalAdapter(client *natsclient.Client, clients *graphown.Clients, cfg ComponentConfig, checker admission.PermissionChecker, platform component.PlatformMeta, logger *slog.Logger) *approvalAdapter {
	a := &approvalAdapter{cfg: cfg, checker: checker, logger: logger}
	if client != nil {
		a.resolver = admission.NewRunResolver(client, platform.Org, platform.Platform)
		a.writer = clients.Writer(ApprovedSource)
		a.pendingWriter = clients.Writer(conversationintent.AdapterSource)
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
//     to the pre-NL path — records the approve decision, zero model turns);
//   - an exact `/semdev reject` → the deterministic reject fast-path (stamps
//     the reject decision, zero model turns);
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
		return a.releaseGate(ctx, msg, thread, admission.DecisionApprove)
	case hasRejectCommand(a.cfg.OptInCommand, msg.Body):
		return a.releaseGate(ctx, msg, thread, admission.DecisionReject)
	default:
		return a.bridgeNonCommand(ctx, msg, thread)
	}
}

// releaseGate is the deterministic exact-command gate write shared by the approve
// and reject fast-paths (D4): build the admission Event from the neutral Message +
// the thread's resolved code-host scope (owner/repo from SplitRef) → Authorize the
// author → resolve the run → stamp the decision via the ONE shared writer. decision is
// admission.DecisionApprove or admission.DecisionReject — two VALUES of one
// single-valued fact (D13), not two facts. This keeps Authorize + the approve write
// byte-identical to the pre-NL path (grp2-review carry-forward a).
//
// An ALREADY-DECIDED run is a no-op for BOTH verbs (D13 layer 1): replaying the same
// decision is idempotent, and the OPPOSITE verb is REFUSED — H1 (an approval is
// irreversible once landed) generalized symmetrically, so a rejected-and-cancelled run
// is not resurrected either. The refusal is what closes the wedge the two-boolean design
// left open (external review #2): the old code consulted only "already approved" and so
// happily stamped the opposite fact alongside it.
func (a *approvalAdapter) releaseGate(ctx context.Context, msg conversation.Message, thread conversation.ThreadRef, decision string) error {
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

	run, err := a.resolver.ResolveRunByRef(ctx, string(thread))
	if err != nil {
		return fmt.Errorf("approval: resolve run for %s: %w", string(thread), err)
	}
	if run.EntityID == "" {
		// The run may not be minted yet (the approval raced the wake), the graph read
		// may lag the mint, or the ref never admitted. Redeliver a bounded number of
		// times; MaxDeliver exhausts loud in the consumer log.
		return fmt.Errorf("approval: no run carries run.issue.ref=%s yet; redelivering", string(thread))
	}

	// PHASE GUARD — the exact command decides ONLY a run actually AT the gate.
	//
	// This is load-bearing in two directions, and its absence was a BLOCKING defect:
	//
	//  1. It stops a PRE-GATE decision from wedging the run permanently. run-lifecycle/01
	//     is the ONLY rule that transitions a run INTO awaiting_approval, and it requires
	//     the gate undecided. So a `/semdev reject` typed while the run was still
	//     `executing` used to stamp a decision that made the gate unreachable — and with
	//     the gate unreachable the phase-guarded cancel rule could never fire either, and
	//     a follow-up `/semdev approve` was refused by first-decision-wins. Unrecoverable,
	//     with no park and no operator surface: the exact class D13 exists to remove,
	//     reintroduced one rule over.
	//  2. It is the uniform rule D12 already decided on. Pre-approval (an approve typed
	//     before the proposal exists) does NOT decide the gate — the same rule the
	//     watermark applies. One rule, explainable on a thread, enforced in one place.
	//
	// A command on a non-gated run is DEFINITIVE (acked): redelivering cannot make the
	// run gated, and a bounded retry that happened to span the gate opening would make
	// pre-approval nondeterministically work — worse than not working at all.
	if run.Phase != admission.PhaseAwaitingApproval {
		atomic.AddInt64(&a.ignored, 1)
		a.logger.Info("approval: exact command on a run that is not at the change-approval gate; ignored",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor),
			slog.String("run", run.EntityID), slog.String("phase", run.Phase),
			slog.String("decision", decision))
		return nil
	}

	// D12a — the message must have been written FOR this gate.
	if !a.freshForGate(msg, run, thread) {
		atomic.AddInt64(&a.ignored, 1)
		return nil
	}

	landed, err := a.stampDecision(ctx, run.EntityID, decision)
	if err != nil {
		return fmt.Errorf("approval: stamp %s on %s: %w", decision, run.EntityID, err)
	}
	if landed != decision {
		// REFUSED by first-decision-wins: the gate was already decided (a replay of the
		// same verb, or the opposite verb on a decided run). Counted as ignored, NOT
		// granted — the counter and the log must not assert a decision that did not
		// happen (G7).
		atomic.AddInt64(&a.ignored, 1)
		a.logger.Info("approval: run already decided; command refused (first decision wins, D13)",
			slog.String("ref", string(thread)), slog.String("actor", ev.Actor),
			slog.String("run", run.EntityID), slog.String("stands", landed),
			slog.String("refused", decision))
		return nil
	}
	atomic.AddInt64(&a.granted, 1)
	a.logger.Info("approval: authorized command decided the change gate",
		slog.String("ref", string(thread)), slog.String("actor", ev.Actor),
		slog.String("run", run.EntityID), slog.String("decision", decision))
	return nil
}

// gateWatermarkSkew is how far BEFORE the gate opened a message may still be honored.
//
// It exists because the two transports timestamp a message from DIFFERENT CLOCKS. The poll
// path carries the code host's `created_at` (GitHub's clock); the webhook path carries
// semdev's own receive time. The watermark compares that against a gate-open moment stamped
// by semdev, so the poll path is a CROSS-CLOCK comparison and a strict `>` would drop a
// legitimate approval whenever semdev's clock ran slightly ahead — silently, since a dropped
// message gets no reply.
//
// The tolerance is safe against what the watermark actually defends: a comment written for a
// DIFFERENT, EARLIER gate. Those are separated by at least a full run arc (author → validate
// → gate, several model turns), which is orders of magnitude more than this margin, while
// realistic NTP skew is sub-second. Deliberately a named constant, not a config knob — it is
// a property of clock reality, not of a deployment.
const gateWatermarkSkew = 30 * time.Second

// freshForGate reports whether msg was written FOR the gate that is currently open on run —
// the D12a watermark (external review #1).
//
// THE DEFECT IT CLOSES: the poll cursor is in-memory and starts EMPTY, so any restart
// re-reads a thread from the top. Dedup for the NL lane reads a ledger ON THE RUN, and the
// exact path's decided-check is per-run too — so a SECOND run on a reused issue begins with
// an empty ledger and an undecided gate, and a months-old "ship it" (or an old
// `/semdev approve`) would decide a proposal the human never saw. D12b made that WORSE
// before this landed: resolving deterministically to the active gated run turned a
// probabilistic mis-target into a certain one.
//
// The rule is uniform across the NL bridge AND the exact command (the D12 decision): a
// message authored at or before the gate opened does not decide that gate. It is DEFINITIVE
// — redelivery cannot make a message newer — and it is LOUD, because a silently dropped
// human instruction is the failure mode this project keeps paying for.
//
// It FAILS CLOSED when no watermark can be established. A gated run always carries the
// framework's last-transition-at, so a zero here means something is wrong with the run's
// audit facts — and a missing lower bound must never read as "no lower bound", which would
// honor every historical comment on the thread.
func (a *approvalAdapter) freshForGate(msg conversation.Message, run admission.RunState, thread conversation.ThreadRef) bool {
	if run.GateOpenedAt.IsZero() {
		a.logger.Error("approval: gated run carries no readable gate-open time; REFUSING the message (fail closed)",
			slog.String("ref", string(thread)), slog.String("run", run.EntityID),
			slog.String("predicate", admission.LastTransitionAtPredicate))
		return false
	}
	if msg.At.IsZero() {
		a.logger.Error("approval: message carries no timestamp; REFUSING it (the watermark cannot be applied)",
			slog.String("ref", string(thread)), slog.String("run", run.EntityID),
			slog.String("message_id", msg.ID))
		return false
	}
	if !msg.At.After(run.GateOpenedAt.Add(-gateWatermarkSkew)) {
		a.logger.Warn("approval: message predates the gate it would decide; ignored (D12a watermark)",
			slog.String("ref", string(thread)), slog.String("run", run.EntityID),
			slog.String("message_id", msg.ID), slog.String("author", msg.Author),
			slog.Time("message_at", msg.At), slog.Time("gate_opened_at", run.GateOpenedAt))
		return false
	}
	return true
}

// stampDecision writes the run's SINGLE-VALUED change-approval decision under the one
// sanctioned writer Source ApprovedSource — the D11 shared gate writer that BOTH the
// exact-command fast-path and the group-5 apply consumer route through (the
// TestOnlySanctionedGateWriters census). ReplaceTriples is replace-by-predicate, so a
// redelivered stamp is idempotent.
//
// FIRST DECISION WINS (D13 layer 1). It READS the run's current decision and refuses to
// change a decided run: approve-on-rejected and reject-on-approved are both no-ops. This
// is the guard whose absence was the wedge — the old code read only "already approved"
// and stamped the opposite fact beside it, leaving a run that fired NEITHER lifecycle
// rule and could never be approved OR cancelled again.
//
// The read-then-write is NOT atomic and there is no durable CAS in the framework, so a
// true interleave of two opposite authorized decisions can still land the second write.
// That is bounded and deliberately accepted (design D13 / Risks): the fact is
// single-valued, so the loser is OVERWRITTEN rather than coexisting — the run always
// holds ONE valid decision, and the phase-guarded lifecycle rules make a decision that
// arrives after the run left the gate inert. The failure mode is "which of two things the
// human actually asked for wins is undefined", never a permanent wedge. Do NOT "fix" this
// with an in-process mutex: it would read as a guarantee while silently not holding across
// a second component instance.
//
// IT RETURNS THE DECISION THAT STANDS, which is how a caller detects a refusal. A refusal
// MUST NOT be silent: the apply consumer POSTS its transparency comment BEFORE calling
// this (guard 3 precedes guard 4, deliberately), and between its gate-still-open read and
// this write it makes up to three external round-trips — ResolveThread, Authorize, Post.
// A competing decision landing in that window means the human has ALREADY been shown one
// outcome while a different one stands. Returning `nil` there let the consumer log a
// success that never happened and leave the thread asserting the opposite of reality (G7).
// Callers compare landed != requested and correct the record.
func (a *approvalAdapter) stampDecision(ctx context.Context, runEntityID, decision string) (string, error) {
	current, err := a.currentDecision(ctx, runEntityID)
	if err != nil {
		// A read fault is TRANSIENT — never decide a gate whose current state is
		// unknown (fail closed; redelivery retries).
		return "", fmt.Errorf("read current decision on %s: %w", runEntityID, err)
	}
	if current != "" {
		// Already decided: a same-value replay is idempotent, the opposite verb is
		// refused (H1 generalized). Either way what STANDS is `current`.
		return current, nil
	}

	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  admission.DecisionPredicate,
		Object:     decision,
		Source:     ApprovedSource,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := a.writer.Replace(ctx, runEntityID, []message.Triple{tr}); err != nil {
		return "", err
	}
	return decision, nil
}

// currentDecision reads the run's decision fact fresh from the graph ("" = undecided).
// It reads at STAMP time rather than trusting the resolver's earlier snapshot — the
// resolve happens before an external Authorize round-trip, which is exactly the window a
// competing decision lands in.
func (a *approvalAdapter) currentDecision(ctx context.Context, runEntityID string) (string, error) {
	if a.reader == nil {
		// No graph reader (a nil NATS client — the schema-only/registration wiring).
		// The gate's current state is UNKNOWABLE here, so refuse rather than write
		// blind: deciding a gate without being able to see whether it is already
		// decided is exactly the read the wedge came from. Fail CLOSED.
		return "", fmt.Errorf("no fact reader wired; refusing to decide a gate whose state cannot be read")
	}
	facts, err := a.reader.ReadFacts(ctx, runEntityID, admission.DecisionPredicate)
	if err != nil {
		return "", err
	}
	for _, tr := range facts {
		if tr.Predicate == admission.DecisionPredicate {
			if s, ok := tr.Object.(string); ok && s != "" {
				return s, nil
			}
		}
	}
	return "", nil
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
	run, err := a.resolver.ResolveRunByRef(ctx, string(thread))
	if err != nil {
		return fmt.Errorf("approval: resolve run for %s: %w", string(thread), err)
	}
	if run.EntityID == "" || run.Phase != admission.PhaseAwaitingApproval {
		return nil
	}
	if run.Decided() {
		// Gated but ALREADY DECIDED — the window between the decision landing and the
		// lifecycle rule firing the transition. The run is still awaiting_approval, so
		// the phase check above passes, but the routing rules require the gate
		// UNDECIDED, so any classification produced here could never route. Bridging
		// would spend a real model turn on a guaranteed-dead result.
		a.logger.Info("approval: run is gated but already decided; no NL bridge (the classification could not route)",
			slog.String("ref", string(thread)), slog.String("run", run.EntityID),
			slog.String("decision", run.Decision))
		return nil
	}
	// D12a — the same watermark the exact command carries. Applied BEFORE Authorize so a
	// replayed thread costs zero code-host permission calls, matching the phase gate's
	// rationale (grp3-review M1).
	if !a.freshForGate(msg, run, thread) {
		atomic.AddInt64(&a.ignored, 1)
		return nil
	}
	runEntityID := run.EntityID

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
	if err := a.pendingWriter.Replace(ctx, runEntityID, pendingTriples); err != nil {
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
