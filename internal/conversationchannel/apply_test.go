package conversationchannel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
)

// --- fixtures (the fakeFetcher / fakeChannel / fakeWriter fakes are shared with
// parkpost_test.go and approval_test.go — one fake per collaborator, package-wide).

const (
	testRunID = "c360.semdev.agent.chain.execution.run-1"
	testRef   = "c360studio/semdev-fixture#7"
)

// gatedRun is a run at the change-approval gate carrying a classified intent and
// NEITHER gate fact — the state the routing rules dispatch on. The apply consumer
// takes ONE snapshot of it, so the gate-still-open check and the intent it acts on
// are always coherent.
func gatedRun(intent conversationintent.Intent, author string) *graph.EntityState {
	return entityWith(testRunID, map[string]string{
		"run.issue.ref":                          testRef,
		conversationintent.IntentValuePredicate:  string(intent),
		conversationintent.IntentAuthorPredicate: author,
	})
}

// oneRun wraps a single run snapshot as the fetcher's entity map.
func oneRun(e *graph.EntityState) *fakeFetcher {
	return &fakeFetcher{entities: map[string]*graph.EntityState{testRunID: e}}
}

func newTestApply(fetcher admission.EntityFetcher, ch conversation.Channel, checker admission.PermissionChecker, writer *fakeWriter) *applyConsumer {
	cfg := ComponentConfig{Repo: "c360studio/semdev-fixture", Allowlist: []string{"cglusky"}}
	applyConfigDefaults(&cfg)
	adapter := &approvalAdapter{cfg: cfg, writer: writer, logger: slog.Default()}
	return &applyConsumer{
		cfg:     cfg,
		channel: ch,
		checker: checker,
		fetcher: fetcher,
		stamp:   adapter.stampGateFact,
		logger:  slog.Default(),
	}
}

func dispatchPayload(t *testing.T, entityID string) []byte {
	t.Helper()
	b, err := json.Marshal(publishEnvelope{EntityID: entityID})
	if err != nil {
		t.Fatalf("marshal dispatch envelope: %v", err)
	}
	return b
}

// gateStamps returns the gate-fact triples the shared writer recorded.
func gateStamps(w *fakeWriter) []message.Triple {
	var out []message.Triple
	for _, call := range w.calls {
		for _, tr := range call.add {
			if tr.Predicate == admission.ApprovedPredicate || tr.Predicate == admission.RejectedPredicate {
				out = append(out, tr)
			}
		}
	}
	return out
}

// TestApplyConsumerGateStillOpenReAuthorizeStamps is the group-5 core pin (task
// 5.1, design D6). The deterministic apply consumer, on a dispatch whose entity IS
// the run:
//
//	(a) re-checks the gate is OPEN — a run already carrying EITHER gate fact is a
//	    NO-OP (H4a: a second racing classification cannot double-release);
//	(b) reads the cited author from conversation.intent.author (HARNESS-bound by
//	    classify_intent, H4b — never the overwrite-prone pending slot) and RE-RUNS
//	    admission.Authorize on it; an unauthorized cited author writes NOTHING;
//	(c) POSTS the transparency comment naming the author it acted on;
//	(d) stamps the gate fact through the ONE shared approval-adapter writer.
func TestApplyConsumerGateStillOpenReAuthorizeStamps(t *testing.T) {
	ctx := context.Background()

	t.Run("approve releases the gate through the shared writer", func(t *testing.T) {
		w := &fakeWriter{}
		ch := &fakeChannel{}
		a := newTestApply(oneRun(gatedRun(conversationintent.Approve, "cglusky")), ch, admission.AllowlistOnlyChecker{}, w)

		if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
			t.Fatalf("handleDispatch: %v", err)
		}
		stamps := gateStamps(w)
		if len(stamps) != 1 {
			t.Fatalf("want exactly ONE gate stamp, got %d (%v)", len(stamps), stamps)
		}
		if stamps[0].Predicate != admission.ApprovedPredicate {
			t.Errorf("stamped %q, want %q", stamps[0].Predicate, admission.ApprovedPredicate)
		}
		if stamps[0].Source != ApprovedSource {
			t.Errorf("gate fact Source = %q, want the ONE sanctioned writer %q (G5/D11)", stamps[0].Source, ApprovedSource)
		}
		if w.calls[0].entityID != testRunID {
			t.Errorf("stamped on %q, want the RUN %q", w.calls[0].entityID, testRunID)
		}
		// Transparency BEFORE effect, naming the author acted on (D6 step 3).
		if len(ch.posts) != 1 {
			t.Fatalf("want exactly ONE transparency post, got %d", len(ch.posts))
		}
		if !strings.Contains(ch.posts[0].body, "cglusky") {
			t.Errorf("transparency post %q must name the author it acted on", ch.posts[0].body)
		}
	})

	t.Run("reject stamps the rejection fact", func(t *testing.T) {
		w := &fakeWriter{}
		a := newTestApply(oneRun(gatedRun(conversationintent.Reject, "cglusky")), &fakeChannel{}, admission.AllowlistOnlyChecker{}, w)

		if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
			t.Fatalf("handleDispatch: %v", err)
		}
		stamps := gateStamps(w)
		if len(stamps) != 1 || stamps[0].Predicate != admission.RejectedPredicate {
			t.Fatalf("want exactly one %s stamp, got %v", admission.RejectedPredicate, stamps)
		}
	})

	// (a) The gate-still-open guard — the H4a serializer for the publish→stamp
	// window. Either fact present means a decision already landed.
	for _, landed := range []string{admission.ApprovedPredicate, admission.RejectedPredicate} {
		t.Run("already-decided gate is a no-op ("+landed+" present)", func(t *testing.T) {
			w := &fakeWriter{}
			ch := &fakeChannel{}
			run := gatedRun(conversationintent.Approve, "cglusky")
			run.Triples = append(run.Triples, message.Triple{Predicate: landed, Object: "true"})
			a := newTestApply(oneRun(run), ch, admission.AllowlistOnlyChecker{}, w)

			if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
				t.Fatalf("handleDispatch: %v", err)
			}
			if len(w.calls) != 0 {
				t.Errorf("a run already carrying %s must not be re-stamped (H4a): %v", landed, w.calls)
			}
			if len(ch.posts) != 0 {
				t.Errorf("a closed gate must not post: %v", ch.posts)
			}
		})
	}

	// (b) The cited author is RE-authorized. The classifier's judgment is never
	// trusted for authorization — an unauthorized cited author writes NOTHING.
	t.Run("unauthorized cited author writes nothing", func(t *testing.T) {
		w := &fakeWriter{}
		ch := &fakeChannel{}
		a := newTestApply(oneRun(gatedRun(conversationintent.Approve, "drive-by")), ch, admission.AllowlistOnlyChecker{}, w)

		if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
			t.Fatalf("an unauthorized author is DEFINITIVE (acked), got error: %v", err)
		}
		if len(w.calls) != 0 || len(ch.posts) != 0 {
			t.Errorf("unauthorized cited author must produce ZERO writes and ZERO posts: writes=%v posts=%v", w.calls, ch.posts)
		}
	})

	// The intent facts are read from the run at CONSUME time (grp4-review M6: the
	// routes thread no snapshot). A run whose intent is `none` — the classifier
	// flipped it after the route fired — applies NOTHING.
	t.Run("intent none at consume time applies nothing", func(t *testing.T) {
		w := &fakeWriter{}
		a := newTestApply(oneRun(gatedRun(conversationintent.None, "cglusky")), &fakeChannel{}, admission.AllowlistOnlyChecker{}, w)

		if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
			t.Fatalf("handleDispatch: %v", err)
		}
		if len(w.calls) != 0 {
			t.Errorf("a `none` intent must release no gate: %v", w.calls)
		}
	})

}

// TestApplyPostFailureBlocksStamp pins the transparency-before-effect ordering
// (task 5.2, design D6 / semstreams MEDIUM-6): a Channel.Post failure returns a
// TRANSIENT error and the gate fact is NOT stamped, so redelivery re-Posts until
// the human can actually see what semdev is about to do. Guard 4 (visibility) must
// precede the effect — a stamped gate with a lost post is a silent release.
func TestApplyPostFailureBlocksStamp(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	ch := &fakeChannel{postErr: errors.New("forge 503")}
	a := newTestApply(oneRun(gatedRun(conversationintent.Approve, "cglusky")), ch, admission.AllowlistOnlyChecker{}, w)

	err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{})
	if err == nil {
		t.Fatal("a Post failure must return a TRANSIENT error (redelivery re-Posts), got nil")
	}
	if len(w.calls) != 0 {
		t.Errorf("the gate fact must NOT be stamped when the transparency post failed: %v", w.calls)
	}
}

// TestApplyMalformedDispatchIsDefinitive: an envelope semdev's own rule engine did
// not shape is acked, never redelivered forever (the park-post precedent).
func TestApplyMalformedDispatchIsDefinitive(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	a := newTestApply(oneRun(gatedRun(conversationintent.Approve, "cglusky")), &fakeChannel{}, admission.AllowlistOnlyChecker{}, w)

	for _, payload := range [][]byte{[]byte("not json"), dispatchPayload(t, "")} {
		if err := a.handleDispatch(ctx, payload, &applyAttempt{}); err != nil {
			t.Errorf("malformed dispatch %q must be DEFINITIVE (acked), got: %v", payload, err)
		}
	}
	if len(w.calls) != 0 {
		t.Errorf("a malformed dispatch must write nothing: %v", w.calls)
	}
}

// TestApplyGraphReadFaultIsTransient: the run snapshot read is the consumer's only
// view of the gate. A read fault must REDELIVER, never be conflated with an open
// gate (which would skip the H4a guard) or a closed one (which would drop the
// human's decision).
func TestApplyGraphReadFaultIsTransient(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	a := newTestApply(&fakeFetcher{err: errors.New("graph classified fault")}, &fakeChannel{}, admission.AllowlistOnlyChecker{}, w)

	if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err == nil {
		t.Fatal("a graph read fault must be TRANSIENT (redelivered), got nil")
	}
	if len(w.calls) != 0 {
		t.Errorf("a faulted read must write nothing: %v", w.calls)
	}
}

// --- the retry / terminal-path layer (grp5-review H3 + semstreams HIGH-2) ---

// failingFetcher fails a bounded number of times, then succeeds — so a test can
// distinguish "retried and recovered" from "gave up".
type failingFetcher struct {
	fails  int
	calls  int
	entity *graph.EntityState
}

func (f *failingFetcher) Entity(context.Context, string) (*graph.EntityState, error) {
	f.calls++
	if f.calls <= f.fails {
		return nil, errors.New("graph transient fault")
	}
	return f.entity, nil
}

func newRetryApply(fetcher admission.EntityFetcher, ch conversation.Channel, w *fakeWriter) *applyConsumer {
	a := newTestApply(fetcher, ch, admission.AllowlistOnlyChecker{}, w)
	a.backoff = time.Microsecond // keep the exhaustion pin sub-millisecond
	return a
}

// TestApplyLaneNeverParksTheGatedRun is the B1 regression guard — the single most
// important pin in group 5.
//
// Every OTHER station routes a retries-exhausted dispatch to
// station.dispatch.failed, which run-lifecycle/05 converts into run.awaiting.human.
// Doing that HERE would be unrecoverable: this run sits AT the change-approval
// gate, and BOTH release rules (run-lifecycle/02 resume, /07 cancel) require
// run.awaiting.human ABSENT — and nothing in the repo ever removes it. The run
// could then never be approved or cancelled, and even `/semdev approve` would die,
// stamping a fact no rule would consume.
//
// So exhaustion here must be NON-DESTRUCTIVE: notify the human, write NOTHING,
// leave the gate exactly as open as it was. This pin fails if anyone reintroduces
// a park (or any other write) on the terminal path.
func TestApplyLaneNeverParksTheGatedRun(t *testing.T) {
	ctx := context.Background()
	ch := &fakeChannel{}
	// The writer FAILS every write, so the gate stamp never lands and the lane
	// exhausts. Crucially the writer stays WIRED (a.stamp is the production
	// stampGateFact, not a stub), so w records every write the lane attempts —
	// which is what keeps the assertions below non-vacuous. An earlier version of
	// this pin overrode a.stamp, severing the only link to w and making its
	// headline assertion incapable of failing (grp5-review).
	w := &fakeWriter{err: errors.New("graph write fault")}
	a := newRetryApply(oneRun(gatedRun(conversationintent.Approve, "cglusky")), ch, w)

	if err := a.handleApplyDispatch(ctx, dispatchPayload(t, testRunID)); err != nil {
		t.Fatalf("an exhausted dispatch that notified the human must ACK (nil), got: %v", err)
	}

	// ANTI-VACUITY: prove the writer is reachable before asserting what it did NOT
	// receive. If this is empty the test is inspecting a disconnected fake.
	if len(w.calls) == 0 {
		t.Fatal("the writer recorded NOTHING — the lane never reached its write path, so the assertions below would pass vacuously")
	}

	// THE GUARD: the only thing this lane may ever write is a GATE fact. A park
	// (station.dispatch.failed, or run.awaiting.human directly) is the wedge:
	// run-lifecycle/02 and /07 both require run.awaiting.human absent, and nothing
	// in the repo removes it, so the run could never be approved OR cancelled and
	// even /semdev approve would die.
	for _, call := range w.calls {
		for _, tr := range call.add {
			switch tr.Predicate {
			case admission.ApprovedPredicate, admission.RejectedPredicate:
				// the gate fact itself — the one sanctioned write
			default:
				t.Errorf("the apply lane wrote %q — it may ONLY ever write a gate fact; a park predicate here wedges the gate FOREVER", tr.Predicate)
			}
		}
	}

	// And it is not silent: the human learns nothing was decided.
	if len(ch.posts) == 0 || !strings.Contains(ch.posts[len(ch.posts)-1].body, "Nothing has been decided") {
		t.Errorf("exhaustion must notify the human that nothing was decided, got %v", ch.posts)
	}
}

// TestApplyExhaustionWithoutThreadRedelivers: when the failure is so early that no
// thread was ever resolved, there is nobody to notify — so the dispatch must
// REDELIVER (return the error) rather than ack into silence.
func TestApplyExhaustionWithoutThreadRedelivers(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	a := newRetryApply(&failingFetcher{fails: 1 << 30}, &fakeChannel{}, w)

	if err := a.handleApplyDispatch(ctx, dispatchPayload(t, testRunID)); err == nil {
		t.Fatal("an exhausted dispatch with NO notifiable thread must return its error (redeliver), not ack into silence")
	}
	if len(w.calls) != 0 {
		t.Errorf("still zero writes on this path: %v", w.calls)
	}
}

// TestApplyRetriesTransientThenSucceeds: the in-process retry is real — a
// transient graph fault that clears is applied, not surfaced to the human. This is
// what makes the generous backoff worth having (a routine forge/graph blip must
// not become a terminal outcome).
func TestApplyRetriesTransientThenSucceeds(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	ch := &fakeChannel{}
	f := &failingFetcher{fails: 1, entity: gatedRun(conversationintent.Approve, "cglusky")}
	a := newRetryApply(f, ch, w)

	if err := a.handleApplyDispatch(ctx, dispatchPayload(t, testRunID)); err != nil {
		t.Fatalf("a transient fault that clears must succeed, got: %v", err)
	}
	if f.calls != 2 {
		t.Errorf("fetcher called %d times, want 2 (one fault + one success)", f.calls)
	}
	stamps := gateStamps(w)
	if len(stamps) != 1 || stamps[0].Predicate != admission.ApprovedPredicate {
		t.Errorf("the recovered attempt must release the gate exactly once, got %v", stamps)
	}
	// And the human sees ONE announcement, not one per attempt.
	if len(ch.posts) != 1 {
		t.Errorf("want exactly ONE transparency post across the retry, got %d", len(ch.posts))
	}
}

// TestApplyStampFaultDoesNotRePost pins the retry de-duplication (grp5-review
// L1/L8): if the transparency post succeeds and the STAMP faults, the retry must
// re-attempt the stamp WITHOUT re-announcing. Otherwise one dispatch spams the
// human's thread with identical comments.
func TestApplyStampFaultDoesNotRePost(t *testing.T) {
	ctx := context.Background()
	ch := &fakeChannel{}
	a := newRetryApply(oneRun(gatedRun(conversationintent.Approve, "cglusky")), ch, &fakeWriter{})
	var stampCalls int
	a.stamp = func(context.Context, string, string) error {
		stampCalls++
		return errors.New("graph write fault")
	}

	if err := a.handleApplyDispatch(ctx, dispatchPayload(t, testRunID)); err != nil {
		t.Fatalf("handleApplyDispatch: %v", err)
	}
	if stampCalls != applyMaxAttempts {
		t.Errorf("stamp attempted %d times, want %d (the retry must re-try the stamp)", stampCalls, applyMaxAttempts)
	}
	// ONE transparency post + ONE exhaustion notice — never one announcement per attempt.
	if len(ch.posts) != 2 {
		t.Fatalf("want 2 posts (one transparency + one exhaustion notice), got %d: %v", len(ch.posts), ch.posts)
	}
	if !strings.Contains(ch.posts[0].body, "cglusky") {
		t.Errorf("first post should be the transparency announcement, got %q", ch.posts[0].body)
	}
	if !strings.Contains(ch.posts[1].body, "Nothing has been decided") {
		t.Errorf("second post should be the exhaustion notice, got %q", ch.posts[1].body)
	}
}

// TestApplyRefusesWithoutChannel pins H2: the transparency post is the ENTIRE
// visibility guard for a decision with no harness floor, so a deployment with no
// channel must REFUSE to apply rather than release the gate silently. (Contrast
// the park lane, whose post is a courtesy on top of an already-durable fact.)
func TestApplyRefusesWithoutChannel(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	a := newTestApply(oneRun(gatedRun(conversationintent.Approve, "cglusky")), nil, admission.AllowlistOnlyChecker{}, w)
	a.channel = nil

	if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
		t.Fatalf("a channel-less deployment is definitive (acked), got: %v", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("without a channel the gate must NOT be released — a silent NL approval is exactly what the transparency guard exists to prevent: %v", w.calls)
	}
}

// TestApplyUnattributableIntentNotifies pins M5: a classified intent with no
// harness-bound author fails closed AND says so. A silent stop reads to the human
// as being ignored — the D9 dead-end class.
func TestApplyUnattributableIntentNotifies(t *testing.T) {
	ctx := context.Background()
	w := &fakeWriter{}
	ch := &fakeChannel{}
	run := entityWith(testRunID, map[string]string{
		"run.issue.ref":                         testRef,
		conversationintent.IntentValuePredicate: string(conversationintent.Approve),
	})
	a := newTestApply(oneRun(run), ch, admission.AllowlistOnlyChecker{}, w)

	if err := a.handleDispatch(ctx, dispatchPayload(t, testRunID), &applyAttempt{}); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("an author-less intent must fail closed: %v", w.calls)
	}
	if len(ch.posts) != 1 || !strings.Contains(ch.posts[0].body, "couldn't confirm who") {
		t.Errorf("an unattributable intent must be surfaced to the human, got %v", ch.posts)
	}
}

// movingFetcher returns a DIFFERENT run snapshot on each call — the mid-retry
// re-classification the routing rules deliberately allow (grp4-review M6: the
// routes thread no snapshot, so the consumer acts on the CURRENT intent).
type movingFetcher struct {
	snapshots []*graph.EntityState
	calls     int
}

func (f *movingFetcher) Entity(context.Context, string) (*graph.EntityState, error) {
	i := f.calls
	f.calls++
	if i >= len(f.snapshots) {
		i = len(f.snapshots) - 1
	}
	return f.snapshots[i], nil
}

// TestApplyReAnnouncesWhenIntentMovesMidRetry pins guard 3 in the one window where
// it matters most (grp5-review NEW-9 — a defect introduced by the L1 de-duplication
// fix, not by the original design).
//
// predicate and author are re-read from a fresh snapshot on EVERY attempt, by
// design. So if a second classification lands inside the retry budget, a
// de-duplication keyed on a mere "did we post?" bool would announce the FIRST
// decision and then stamp the SECOND — telling the human semdev was approving on
// @alice's word while it cancelled the run on @bob's. Transparency-before-effect
// (D8 guard 4) is one of five guards backing a decision with NO harness floor, so
// the announcement and the gate fact must never diverge.
//
// Keying the memo on (predicate, author) preserves the de-duplication for identical
// retries and forces a fresh announcement when the decision actually moves.
func TestApplyReAnnouncesWhenIntentMovesMidRetry(t *testing.T) {
	ctx := context.Background()
	ch := &fakeChannel{}
	w := &fakeWriter{}
	// Attempt 1 sees approve/alice and fails its stamp; attempt 2 sees reject/bob.
	fetcher := &movingFetcher{snapshots: []*graph.EntityState{
		gatedRun(conversationintent.Approve, "cglusky"),
		gatedRun(conversationintent.Reject, "second-human"),
	}}
	a := newRetryApply(fetcher, ch, w)
	cfg := a.cfg
	cfg.Allowlist = []string{"cglusky", "second-human"}
	a.cfg = cfg

	var stampCalls int
	realStamp := a.stamp
	a.stamp = func(ctx context.Context, runID, predicate string) error {
		stampCalls++
		if stampCalls == 1 {
			return errors.New("graph write fault") // force a second attempt
		}
		return realStamp(ctx, runID, predicate)
	}

	if err := a.handleApplyDispatch(ctx, dispatchPayload(t, testRunID)); err != nil {
		t.Fatalf("handleApplyDispatch: %v", err)
	}

	stamps := gateStamps(w)
	if len(stamps) != 1 {
		t.Fatalf("want exactly one gate stamp, got %v", stamps)
	}
	// The lane acted on the CURRENT intent — reject, by the second author.
	if stamps[0].Predicate != admission.RejectedPredicate {
		t.Fatalf("the consumer must act on the CURRENT intent (reject), stamped %q", stamps[0].Predicate)
	}
	// THE GUARD: the human must have been told about the decision that actually
	// landed, not only about the one that was abandoned.
	if len(ch.posts) < 2 {
		t.Fatalf("the decision MOVED between attempts, so it must be re-announced before it lands; got %d post(s): %v", len(ch.posts), ch.posts)
	}
	last := ch.posts[len(ch.posts)-1].body
	if !strings.Contains(last, "second-human") || !strings.Contains(last, "Cancelling") {
		t.Errorf("the FINAL announcement must describe the decision that landed (cancelling, second-human), got %q", last)
	}
}
