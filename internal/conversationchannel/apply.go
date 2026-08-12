package conversationchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semstreams/graph"
)

// The DETERMINISTIC APPLY CONSUMER (nl-conversation-intent D6) — the Go half of
// the NL intent lane. The classifier stamps a ROUTING fact (conversation.intent.*)
// and the group-4 routing rules (conversation/03a, 03b) publish here; NOTHING the
// model produced ever becomes a gate fact directly. This consumer re-derives the
// decision deterministically from the run's own triples and only then releases the
// change-approval gate under the ONE sanctioned writer (G5/D11 — the same
// stampGateFact the exact-command fast-path calls).
//
// The four guards, in order (design D6/D8 — each is a hard stop, not advice):
//
//  1. GATE STILL OPEN — run.change.decision is not yet
//     present. A second racing classification cannot double-release (H4a), and a
//     duplicate dispatch is a no-op.
//  2. HARNESS-BOUND AUTHOR RE-AUTHORIZED — the cited author is read from
//     conversation.intent.author, which classify_intent copied from the pending
//     slot at classification time (never model-supplied, never the overwrite-prone
//     live pending slot — H4b), and admission.Authorize is RE-RUN on it. The
//     classifier's judgment is never trusted for authorization.
//  3. TRANSPARENCY POSTED — the human sees which author's message semdev acted on
//     BEFORE the effect lands. Honest visibility, not an undo promise (the D8
//     backstop is the downstream PR merge).
//  4. THEN the gate fact is stamped.
//
// Guard 3 preceding guard 4 is load-bearing (semstreams MEDIUM-6): a Post failure
// returns TRANSIENT and BLOCKS the stamp, so the retry re-Posts rather than
// silently releasing a gate no human was told about. Because guard 3 IS the whole
// visibility case for a decision with no harness floor (D8.4), a deployment with
// NO channel (no forge token) does not "degrade to graph-only" the way the park
// lane does — it REFUSES to apply (grp5-review H2). A park post is a courtesy on
// top of a durable fact; here the post is the only thing that makes an inferred
// approval visible, so releasing the gate without it would be a silent release.
//
// TRANSPORT (why JetStream, not the core-NATS station base): by the framework's
// own primitive heuristic (docs/concepts/03-streams-vs-kv-watches.md) this dispatch
// is a REQUEST with real external side effects handled by exactly one consumer, and
// replaying it re-posts a comment to a human's thread — all four tests say
// JetStream stream, not KV and not fire-and-forget. The six core-NATS stations
// (internal/station) do NOT contradict this: their own package doc books that
// transport as named M0 debt ("the crash-in-the-publish→handle-window durability
// the old publish_agent inherited from the AGENT JetStream stream is NOT preserved
// here; restart-safe reconstruction ... is design R8"). This lane declines to
// inherit that debt, because a dispatch dropped in the publish→handle window here
// means an authorized human's approval is silently swallowed and the run sits
// gated forever.
//
// FAILURE POSTURE — THIS LANE MUST NOT PARK (grp5-review B1, the blocking find).
// Every other station routes a retries-exhausted dispatch to station.dispatch.failed,
// which run-lifecycle/05 turns into run.awaiting.human. That is correct for a run
// mid-execution and CATASTROPHIC here, because this run is sitting AT the
// change-approval gate and BOTH release rules require run.awaiting.human ABSENT
// (run-lifecycle/02:17 resume, /07:12 cancel) — and NOTHING in the repo ever
// removes that fact (nine park rules add it, zero remove it; a resume-from-park
// rule is still unbuilt). Parking here would therefore wedge the run PERMANENTLY:
// it could never be approved or cancelled, and even the human's deterministic
// escape hatch would die, because /semdev approve would stamp a fact no rule
// would ever consume. This is the identical hazard conversation/05 was written to
// avoid for the fault note; the apply lane's own park had walked straight into it.
//
// So on exhaustion this lane POSTS A NOTE and LEAVES THE GATE OPEN: the human is
// told the automatic path failed and pointed at the exact command, which still
// works because nothing was stamped. Fail toward the human WITHOUT taking away
// their controls. Pinned by TestApplyLaneNeverParksTheGatedRun.

// ApplyDispatchSubject is the routing rules' publish target (conversation/03a+03b).
// One const so the rule subject and the consumer's port cannot drift by a typo;
// TestConversationLanePortsMatchConfigs ties it to both shipped configs.
const ApplyDispatchSubject = station.SubjectPrefix + ApplyStationName + "." + station.DispatchLeaf

// ApplyStationName is the dispatch namespace. It matches the group-4
// dispatch-entity census key (test/conformance/rules_test.go) — where this lane is
// recorded as run-fired AND as the one deliberate non-parking exception.
const ApplyStationName = "conversation-apply"

// ApplyStreamName is the durable stream carrying the apply dispatch. Declared in
// the bootstrap `streams` block over component.conversation-apply.> — deliberately
// NARROW: it makes THIS lane restart-safe without silently persisting the other
// six stations' fire-and-forget dispatches (their R8 debt stays explicit).
const ApplyStreamName = "CONVERSATION"

// The in-process retry posture. The budget is deliberately LARGER than the station
// base's 200ms-scaled backoff (grp5-review B1): every attempt here crosses an
// external HTTP dependency (a permission check and a comment POST), and the station
// base's sub-second budget would turn a routine forge 5xx into a terminal outcome.
// Exhaustion is non-destructive (a note, not a park), so a generous budget costs
// only latency.
const (
	applyMaxAttempts  = 3
	applyRetryBackoff = time.Second // scaled by attempt: ~3s total across 3 attempts
	// applyMaxDeliverCap bounds JetStream redelivery. It is a CRASH-LOOP backstop,
	// NOT the retry budget — the in-process loop above owns retries and resolves
	// almost every dispatch to an ack (applied, nothing-to-apply, or note-posted).
	//
	// It matches the comment lane's deliberate 10 rather than the framework default
	// of 3 (grp5-review): ONE exhaustion path cannot notify — when the failure is so
	// early that no thread was ever resolved (a graph outage, or the run not yet
	// materialized), there is nobody to post to, so the dispatch NAKs and relies
	// entirely on redelivery. ConsumeWithHeartbeat naks with a fixed 30s delay, so 3
	// deliveries gave that path only ~66s before JetStream dropped the message into
	// silence; 10 gives it the same ~4.5 minutes the comment lane budgets for
	// exactly this class. Now that exhaustion is non-destructive there is no reason
	// to be stingy — a crash loop still terminates, just later.
	applyMaxDeliverCap = 10
)

// applyExhaustedNote is posted when the deterministic apply could not complete.
// It states plainly that nothing was decided and points at the controls the human
// still has — the gate is untouched, so the exact commands work.
const applyExhaustedNote = "⚠️ I understood your message, but I couldn't complete the approval action (the code host wouldn't respond).\n\n" +
	"**Nothing has been decided** — this run is still waiting. Reply `/semdev approve` or `/semdev reject` to decide it directly."

// applyUnattributableNote is posted when a classified intent cannot be attributed
// to an author the harness bound. Near-unreachable by construction, but silence
// here would be the same human-facing dead-end the fault note exists to close
// (grp5-review M5).
const applyUnattributableNote = "⚠️ I read your message as a decision, but I couldn't confirm who it came from, so I didn't act on it.\n\n" +
	"Reply `/semdev approve` or `/semdev reject` to decide this run directly."

// applyConsumer is the deterministic apply lane. It holds no state; every decision
// is re-derived from one run snapshot per dispatch.
type applyConsumer struct {
	cfg     ComponentConfig
	channel conversation.Channel
	checker admission.PermissionChecker
	fetcher admission.EntityFetcher

	// stamp is the ONE shared gate writer (approvalAdapter.stampDecision) — the
	// D11 requirement that the fast-path and this consumer cannot drift their
	// Source apart. Injected as a func so the sharing is structural, not a
	// convention two call sites are trusted to honor.
	// It returns the decision that STANDS after the write: a value different from
	// the requested one means the write was REFUSED because the gate was decided
	// during this lane's Authorize+Post window (see the correction path in guard 4).
	stamp func(ctx context.Context, runEntityID, decision string) (string, error)

	// backoff is the inter-attempt delay base; zero means applyRetryBackoff. Only
	// the retry tests set it (to keep a 3-attempt exhaustion pin sub-millisecond
	// instead of ~3s) — production always uses the const.
	backoff time.Duration

	logger *slog.Logger
}

// retryBackoff is the configured base delay, defaulting to the production const.
func (a *applyConsumer) retryBackoff() time.Duration {
	if a.backoff > 0 {
		return a.backoff
	}
	return applyRetryBackoff
}

// applyAttempt carries the little state that must survive across retries of one
// dispatch. `posted` exists so a stamp fault does not re-Post the transparency
// comment on every attempt (grp5-review L1/L8): the human should see the
// announcement once, not once per retry.
type applyAttempt struct {
	// postedFor records WHAT was announced (predicate + author), not merely THAT
	// something was. A bool here would defeat guard 3 in the one window where it
	// matters most (grp5-review NEW-9): predicate and author are deliberately
	// re-read from a fresh snapshot on every attempt (grp4-review M6 — a later
	// classification legitimately wins), so a re-classification inside the retry
	// budget could announce "approving on @alice's decision", then stamp
	// a reject decision on @bob's. The human would have been shown a decision
	// semdev did not take. Keying on the tuple keeps L1's de-duplication (an
	// identical retry never re-announces) while restoring the invariant that the
	// gate fact can never diverge from what was announced.
	postedFor string
	thread    conversation.ThreadRef
	// authorized memoizes a SUCCESSFUL authorization for the cited author, so a
	// 3-attempt exhaustion does not burn three collaborator-permission API calls
	// (grp5-review). Only a positive result is cached: a DENIAL returns
	// immediately (definitive), and an authorization FAULT must be genuinely
	// re-attempted, so neither can be served from here.
	authorized string
}

// handleApplyDispatch is the consumer entry point: bounded in-process retry around
// handleDispatch, then the NON-DESTRUCTIVE terminal path.
//
// Returning nil after posting the exhaustion note is the ack decision: the human
// has been told and holds working controls, so redelivering a doomed dispatch would
// only re-burn the budget. When the failure is so early that not even a thread can
// be resolved (a graph outage), the error is returned instead so JetStream
// redelivers — there is nobody to notify yet and the infrastructure may recover.
//
// NOTE on ctx: this is the per-message context (bounded by MessageTimeout, and also
// cancelled when ConsumeWithHeartbeat's InProgress fails), NOT a component-lifetime
// one. That distinction used to matter because a misclassified cancel skipped the
// park; now that exhaustion is non-destructive, a cancelled context simply returns
// the error and lets redelivery retry — the correct outcome for a shutdown, a
// message timeout, and a heartbeat blip alike.
func (a *applyConsumer) handleApplyDispatch(ctx context.Context, payload []byte) error {
	st := &applyAttempt{}
	var lastErr error
	for attempt := 1; attempt <= applyMaxAttempts; attempt++ {
		err := a.handleDispatch(ctx, payload, st)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return lastErr
		}
		if attempt < applyMaxAttempts {
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(time.Duration(attempt) * a.retryBackoff()):
			}
		}
	}
	// EXHAUSTED — and deliberately NOT a park (B1). Tell the human; leave the gate
	// exactly as open as it was.
	if st.thread == "" {
		a.logger.Error("conversation-apply: dispatch exhausted its retries before a thread could be resolved; redelivering (gate untouched, exact commands still work)",
			slog.Any("handle_error", lastErr))
		return lastErr
	}
	if err := a.postNote(ctx, st.thread, applyExhaustedNote); err != nil {
		a.logger.Error("conversation-apply: dispatch exhausted its retries AND the notice could not be posted; redelivering",
			slog.String("thread", string(st.thread)), slog.Any("post_error", err), slog.Any("handle_error", lastErr))
		return lastErr
	}
	a.logger.Error("conversation-apply: dispatch exhausted its retries; human notified, gate left OPEN (never parked — a park at the gate is unrecoverable, B1)",
		slog.String("thread", string(st.thread)), slog.Any("handle_error", lastErr))
	return nil
}

// handleDispatch applies ONE dispatch. nil = definitive (acked: applied, or
// nothing to apply); error = transient (retried, then the human is notified).
func (a *applyConsumer) handleDispatch(ctx context.Context, payload []byte, st *applyAttempt) error {
	runEntityID, ok := a.decodeRun(payload)
	if !ok {
		return nil
	}

	// The transparency post IS the visibility guard for a floorless decision (D8.4),
	// so no channel means no NL apply — fail closed rather than release a gate the
	// human is never told about (grp5-review H2). The exact-command lane is
	// unaffected and still works.
	// NOTE the guard relies on the field being a ZERO-VALUE interface, which is what
	// NewProcessor leaves when no forge token is set. A typed-nil channel
	// ((*GitHubChannel)(nil)) would make this false and silently restore the silent
	// release — worth knowing, given this one line is the whole H2 fix.
	if a.channel == nil {
		a.logger.Warn("conversation-apply: no conversation channel configured; REFUSING to apply an NL decision (the transparency post is the visibility guard). Use /semdev approve or /semdev reject.",
			slog.String("run", runEntityID))
		return nil
	}

	// ONE snapshot: the gate facts, the intent, and the thread ref are all read
	// from a single EntityState, so the gate-open check and the intent acted on
	// are coherent by construction rather than by two racing reads.
	run, err := a.fetcher.Entity(ctx, runEntityID)
	if err != nil {
		return fmt.Errorf("conversation-apply: read run %s: %w", runEntityID, err)
	}
	if run == nil {
		// The dispatch raced the run's own materialization. Transient — but never
		// let the terminal path stamp anything onto an id that does not exist.
		return fmt.Errorf("conversation-apply: run %s not found yet", runEntityID)
	}

	// GUARD 1 — the gate must still be OPEN (H4a).
	if landed := decidedGate(run); landed != "" {
		a.logger.Info("conversation-apply: gate already decided; dispatch is a no-op (H4a)",
			slog.String("run", runEntityID), slog.String("decided", landed))
		return nil
	}

	// The routing fact, read at CONSUME time (grp4-review M6: the routes thread no
	// snapshot, so a later classification legitimately wins). `none` or absent
	// means there is nothing to apply.
	decision, ok := decisionFor(entityTriple(run, conversationintent.IntentValuePredicate))
	if !ok {
		a.logger.Info("conversation-apply: run carries no directive intent; nothing to apply",
			slog.String("run", runEntityID))
		return nil
	}

	// The thread coordinate — needed for authorization scope, the transparency
	// post, and any notice. Resolve it before the guards that might need to notify.
	ref := entityTriple(run, "run.issue.ref")
	owner, repo, _, err := admission.SplitRef(ref)
	if err != nil {
		// Unparseable ref: there is no thread to post to, so this is the one
		// dead-end that can only be a loud log (grp5-review M5, honestly bounded).
		a.logger.Error("conversation-apply: run has no parseable run.issue.ref; cannot authorize, post, or notify",
			slog.String("run", runEntityID), slog.String("ref", ref), slog.Any("error", err))
		return nil
	}
	// st.thread is cached across retries while owner/repo are re-derived fresh from
	// each attempt's snapshot. That asymmetry is safe ONLY because run.issue.ref is
	// stamped once by coordinator/04 and is the run's identity — it never moves
	// mid-run. If semdev ever re-stamps ref (GitHub does support issue transfer),
	// this lane would authorize against the NEW repo while posting to the OLD
	// thread, silently. Re-resolve per attempt if that day comes.
	if st.thread == "" {
		thread, err := a.channel.ResolveThread(ctx, ref)
		if err != nil {
			return fmt.Errorf("conversation-apply: resolve thread for %s: %w", ref, err)
		}
		st.thread = thread
	}

	// GUARD 2 — the HARNESS-BOUND cited author, re-authorized (H4b).
	author := entityTriple(run, conversationintent.IntentAuthorPredicate)
	if author == "" {
		// Fail CLOSED rather than fall back to the live pending slot: an intent
		// with no bound author cannot be attributed, so it cannot be authorized.
		// Surface it — a silent stop here reads to the human as being ignored.
		a.logger.Warn("conversation-apply: classified intent carries no harness-bound author; refusing to apply",
			slog.String("run", runEntityID))
		if err := a.postNote(ctx, st.thread, applyUnattributableNote); err != nil {
			return fmt.Errorf("conversation-apply: notify unattributable intent on %s: %w", ref, err)
		}
		return nil
	}
	if st.authorized != author {
		authorized, err := admission.Authorize(ctx, a.admissionConfig(), admission.Event{Actor: author, Owner: owner, Repo: repo}, a.checker)
		if err != nil {
			return fmt.Errorf("conversation-apply: authorize %s on %s: %w", author, ref, err)
		}
		if authorized {
			st.authorized = author
		}
	}
	if st.authorized != author {
		// Deliberately SILENT: an unauthorized author must not be able to make
		// semdev post to the thread on demand.
		a.logger.Info("conversation-apply: cited author is not authorized; zero writes",
			slog.String("run", runEntityID), slog.String("author", author))
		return nil
	}

	// GUARD 3 — transparency BEFORE the effect. De-duplicated on WHAT is being
	// announced, so an identical retry does not re-announce, but a retry whose
	// intent or author MOVED re-announces before stamping the new decision. The
	// gate fact must never diverge from what the human was shown.
	announcement := decision + "\x00" + author
	if st.postedFor != announcement {
		if err := a.channel.Post(ctx, st.thread, transparencyBody(decision, author)); err != nil {
			return fmt.Errorf("conversation-apply: post transparency for %s: %w", ref, err)
		}
		st.postedFor = announcement
	}

	// GUARD 4 — the effect, through the ONE shared gate writer (D11).
	landed, err := a.stamp(ctx, runEntityID, decision)
	if err != nil {
		return fmt.Errorf("conversation-apply: stamp %s on %s: %w", decision, runEntityID, err)
	}
	if landed != decision {
		// REFUSED — the gate was decided by something else (an exact command, or a
		// racing classification) AFTER guard 1 read it open. Guard 1's read and this
		// write are separated by up to three external round-trips (ResolveThread,
		// Authorize, Post), so this window is seconds wide, not the sub-millisecond
		// interleave the two-exact-command case bounds.
		//
		// The human has ALREADY been shown `decision` by guard 3. Staying silent here
		// would leave the thread asserting an outcome that never took effect, and the
		// run would then follow `landed` — an approval announced, a cancellation
		// delivered. So the record is CORRECTED on the thread. Best-effort by design:
		// the durable decision is already correct and this lane must not redeliver
		// (guard 1 would short-circuit the retry and the correction would never post),
		// so a Post failure is a loud log, not an error — the park-post courtesy
		// posture, on a fact that is already durable.
		a.logger.Warn("conversation-apply: gate was decided during this dispatch; the announcement is being corrected",
			slog.String("run", runEntityID), slog.String("author", author),
			slog.String("announced", decision), slog.String("stands", landed))
		if perr := a.postNote(ctx, st.thread, decisionCorrectionBody(decision, landed)); perr != nil {
			a.logger.Error("conversation-apply: could not correct the announcement; the thread still shows the superseded decision",
				slog.String("run", runEntityID), slog.String("announced", decision),
				slog.String("stands", landed), slog.Any("error", perr))
		}
		return nil
	}
	a.logger.Info("conversation-apply: classified intent decided the change gate",
		slog.String("run", runEntityID), slog.String("author", author), slog.String("decision", decision))
	return nil
}

// decisionCorrectionBody is posted when a decision this lane ALREADY announced was
// overtaken before it could be recorded. It names both sides plainly: the human saw one
// outcome and the run is following another, and a thread that never mentions the
// divergence is worse than one that does (G7 — the transparency post is the whole
// visibility case for a gate with no harness floor).
func decisionCorrectionBody(announced, stands string) string {
	return fmt.Sprintf("⚠️ Correction: I announced this change as **%s**, but the gate had already been decided as **%s** — that earlier decision stands and is what this run is following.\n\nNo action was taken on the %s.",
		announced, stands, announced)
}

// postNote posts a human-facing notice. Kept separate from the transparency post
// so the two intents read differently at the call sites.
func (a *applyConsumer) postNote(ctx context.Context, thread conversation.ThreadRef, body string) error {
	if a.channel == nil {
		return nil
	}
	return a.channel.Post(ctx, thread, body)
}

// decodeRun extracts the run entity id from the dispatch envelope. The routing
// rules fire on the RUN, so the firing entity IS the run (no property needed) —
// the provision/projection-station shape, pinned by the grp4 dispatch-entity
// census. A malformed envelope is DEFINITIVE: semdev's own rule engine shapes
// this payload, so a shape it did not produce will never parse on redelivery.
func (a *applyConsumer) decodeRun(payload []byte) (string, bool) {
	var env publishEnvelope
	if err := json.Unmarshal(payload, &env); err != nil || env.EntityID == "" {
		a.logger.Error("conversation-apply: malformed dispatch envelope; skipping", slog.Any("error", err))
		return "", false
	}
	return env.EntityID, true
}

func (a *applyConsumer) admissionConfig() admission.Config {
	return admission.Config{Allowlist: a.cfg.Allowlist, OptInLabel: a.cfg.OptInLabel, OptInCommand: a.cfg.OptInCommand}
}

// decidedGate returns the run's change-approval decision ("" if the gate is still
// open) — the H4a guard. Since D13 the decision is ONE single-valued fact, so this is
// a single read rather than a scan of two mutually-contradictory booleans: approve and
// reject are equally terminal and either closes this lane.
func decidedGate(run *graph.EntityState) string {
	return entityTriple(run, admission.DecisionPredicate)
}

// decisionFor maps a classified intent to the gate DECISION it carries. `none` (and an
// absent/unknown value) maps to NOTHING — the closed taxonomy's deliberate
// no-directive case: the run stays gated (D1).
func decisionFor(intent string) (string, bool) {
	switch conversationintent.Intent(intent) {
	case conversationintent.Approve:
		return admission.DecisionApprove, true
	case conversationintent.Reject:
		return admission.DecisionReject, true
	default:
		return "", false
	}
}

// transparencyBody is the comment posted BEFORE the gate fact lands. It names the
// author whose message drove the decision so the human can see the attribution —
// honest visibility, with NO undo promise (architect H1/BLOCKING-1: an NL approval
// is irreversible once landed; the downstream PR merge is the real human stop).
func transparencyBody(decision, author string) string {
	if decision == admission.DecisionReject {
		return fmt.Sprintf("🛑 Cancelling this run based on @%s's rejection.\n\nThe authored change is abandoned. Re-trigger the issue to start a fresh run.", author)
	}
	// Phrased as the decision being RECORDED, not as work already underway: this
	// post lands BEFORE the gate fact (D6, load-bearing), so on the exhaustion path
	// the human would otherwise read "developing now" immediately followed by
	// "nothing has been decided" (grp5-review, G7).
	return fmt.Sprintf("✅ Approving this change based on @%s's decision — starting development.\n\nThe pull request will still need a human review before anything merges.", author)
}
