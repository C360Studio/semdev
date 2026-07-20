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
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/message"
)

// --- fakes ---

type fakeResolver struct {
	runID    string
	approved bool
	phase    string
	err      error
}

func (f *fakeResolver) ResolveRunByRef(context.Context, string) (string, bool, string, error) {
	return f.runID, f.approved, f.phase, f.err
}

// fakeIntentReader scripts the classified-ledger read handleMessage's NL bridge
// dedups against (conversation.intent.classified). It filters by prefix exactly as
// the production changefacts reader does.
type fakeIntentReader struct {
	facts []message.Triple
	err   error
	reads int
}

func (f *fakeIntentReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	var out []message.Triple
	for _, tr := range f.facts {
		if strings.HasPrefix(tr.Predicate, prefix) {
			out = append(out, tr)
		}
	}
	return out, nil
}

type replacedFacts struct {
	entityID string
	add      []message.Triple
}

type fakeWriter struct {
	calls []replacedFacts
}

func (f *fakeWriter) ReplaceTriples(_ context.Context, entityID string, add []message.Triple, _ []string) error {
	f.calls = append(f.calls, replacedFacts{entityID: entityID, add: add})
	return nil
}

func (f *fakeWriter) ReadOwnedPredicates(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func newTestApproval(resolver admission.RunResolver, writer *fakeWriter, checker admission.PermissionChecker) *approvalAdapter {
	cfg := ComponentConfig{Repo: "c360studio/semdev-fixture", Allowlist: []string{"cglusky"}}
	applyConfigDefaults(&cfg)
	return &approvalAdapter{cfg: cfg, checker: checker, resolver: resolver, writer: writer, reader: &fakeIntentReader{}, logger: slog.Default()}
}

func flattenedComment(t *testing.T, sender, author, body string) []byte {
	t.Helper()
	ev := githubwebhook.CommentEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issue_comment",
			Action:     "created",
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-fixture", FullName: "c360studio/semdev-fixture"},
			Sender:     sender,
			ReceivedAt: time.Now().UTC(),
		},
		IssueNumber: 7,
		Comment:     githubwebhook.CommentPayload{Body: body, Author: author},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// --- the approval pins (conversation-channel-seam 4.3) ---

// TestApprovalReadsNeutralMessage pins the carve: the approval adapter authorizes
// and releases the gate from a NEUTRAL Message (via conversation.NormalizeInboundComment)
// — NOT a githubwebhook.CommentEvent/CommentSignal — and lands run.change.approved="true"
// (Source approval-adapter) on the resolved run, byte-for-byte the stand-in fact the
// resume rule consumes. The admission Event it authorizes is rebuilt from Message.Author
// + SplitRef(thread), so Authorize is unchanged.
func TestApprovalReadsNeutralMessage(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "c360.semdev-001.agent.chain.execution.r1"}, writer, nil)

	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("handleCommentEvent: %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("ReplaceTriples calls = %d, want 1", len(writer.calls))
	}
	call := writer.calls[0]
	if call.entityID != "c360.semdev-001.agent.chain.execution.r1" {
		t.Errorf("stamped entity = %q, want the resolved run", call.entityID)
	}
	if len(call.add) != 1 {
		t.Fatalf("add = %d triples, want 1", len(call.add))
	}
	tr := call.add[0]
	if tr.Predicate != admission.ApprovedPredicate || tr.Object != "true" || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s) — the exact resume-rule condition",
			tr.Predicate, tr.Object, tr.Source, admission.ApprovedPredicate, ApprovedSource)
	}
}

// TestApprovalCoreSharedByWebhookAndPoll pins the pull-first-transport D3 split:
// ONE approval core (handleMessage), TWO transports. The WEBHOOK path
// (handleCommentEvent → NormalizeInboundComment → handleMessage) and the POLL path
// (a neutral Message fed to handleMessage directly) both authorize Message.Author
// and land the IDENTICAL run.change.approved="true" (Source approval-adapter) on the
// resolved run. The webhook path stays byte-identical to today (the migrated
// approval pins are its regression guard); this adds the direct-Message entry point.
func TestApprovalCoreSharedByWebhookAndPoll(t *testing.T) {
	ctx := context.Background()
	const runID = "c360.semdev-001.agent.chain.execution.r1"
	const thread = "c360studio/semdev-fixture#7"

	// Webhook transport: a flattened created comment through the full path.
	wWriter := &fakeWriter{}
	aw := newTestApproval(&fakeResolver{runID: runID}, wWriter, nil)
	if err := aw.handleCommentEvent(ctx, flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("webhook handleCommentEvent: %v", err)
	}

	// Poll transport: a neutral Message (the poll transport's unit — a real comment
	// id, one author) fed to the SAME core directly, no webhook payload.
	pWriter := &fakeWriter{}
	ap := newTestApproval(&fakeResolver{runID: runID}, pWriter, nil)
	msg := conversation.Message{ID: "9876543210", Author: "cglusky", Body: "/semdev approve", At: time.Now().UTC()}
	if err := ap.handleMessage(ctx, msg, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("poll handleMessage: %v", err)
	}

	// Both transports produced the byte-identical write on the same run.
	for name, w := range map[string]*fakeWriter{"webhook": wWriter, "poll": pWriter} {
		if len(w.calls) != 1 {
			t.Fatalf("%s transport: ReplaceTriples calls = %d, want 1", name, len(w.calls))
		}
		call := w.calls[0]
		if call.entityID != runID || len(call.add) != 1 {
			t.Fatalf("%s transport: stamped entity=%q with %d triples, want %q with 1", name, call.entityID, len(call.add), runID)
		}
		tr := call.add[0]
		if tr.Predicate != admission.ApprovedPredicate || tr.Object != "true" || tr.Source != ApprovedSource {
			t.Errorf("%s transport: triple = %s=%v (Source %s), want %s=true (Source %s)",
				name, tr.Predicate, tr.Object, tr.Source, admission.ApprovedPredicate, ApprovedSource)
		}
	}
}

// TestPollAuthorizesMessageAuthorNotAllowlisted — the poll path gates on
// Message.Author exactly as the webhook path gates on the (sender==)author: a
// non-allowlisted author's command is ignored, zero writes (H-1: one identity).
func TestPollAuthorizesMessageAuthorNotAllowlisted(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x"}, writer, admission.AllowlistOnlyChecker{})
	msg := conversation.Message{ID: "1", Author: "mallory", Body: "/semdev approve"}
	if err := a.handleMessage(context.Background(), msg, conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("unauthorized poll message is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unauthorized poll author wrote %d facts, want 0", len(writer.calls))
	}
}

// TestPollHonorsEditedApproveWebhookDoesNot pins the ACCEPTED transport divergence
// (review M3): the poll path reads a comment's CURRENT body, so an approve EDITED
// INTO a comment is honored (attributed to its author, gated by Authorize); the
// webhook path fires only on action=="created" and never sees an edit. Not a
// security hole (Authorize gates either way) and arguably more correct (the thread's
// current state is the truth) — DOCUMENTED, not claimed away.
func TestPollHonorsEditedApproveWebhookDoesNot(t *testing.T) {
	ctx := context.Background()
	const runID = "run-1"

	// Webhook: an EDITED comment carrying "/semdev approve" — normalizeComment
	// rejects a non-created action, so no write ever reaches the core.
	wWriter := &fakeWriter{}
	aw := newTestApproval(&fakeResolver{runID: runID}, wWriter, nil)
	editedEvent := githubwebhook.CommentEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issue_comment",
			Action:     "edited",
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-fixture", FullName: "c360studio/semdev-fixture"},
			Sender:     "cglusky",
			ReceivedAt: time.Now().UTC(),
		},
		IssueNumber: 7,
		Comment:     githubwebhook.CommentPayload{Body: "/semdev approve", Author: "cglusky"},
	}
	editedPayload, err := json.Marshal(editedEvent)
	if err != nil {
		t.Fatalf("marshal edited event: %v", err)
	}
	if err := aw.handleCommentEvent(ctx, editedPayload); err != nil {
		t.Fatalf("webhook edited comment: %v", err)
	}
	if len(wWriter.calls) != 0 {
		t.Errorf("webhook honored an EDITED approve (%d writes); it must fire only on created", len(wWriter.calls))
	}

	// Poll: the SAME edited-in approve, read as a current-body Message, IS honored.
	pWriter := &fakeWriter{}
	ap := newTestApproval(&fakeResolver{runID: runID}, pWriter, nil)
	msg := conversation.Message{ID: "5", Author: "cglusky", Body: "/semdev approve", At: time.Now().UTC()}
	if err := ap.handleMessage(ctx, msg, conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("poll edited-in approve: %v", err)
	}
	if len(pWriter.calls) != 1 {
		t.Errorf("poll did NOT honor an edited-in approve (%d writes); the poll path reads the current body", len(pWriter.calls))
	}
}

// TestUnauthorizedApprovalIsIgnored — the Event invariant: no authorization,
// no write, definitive ack.
func TestUnauthorizedApprovalIsIgnored(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x"}, writer, admission.AllowlistOnlyChecker{})
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "mallory", "mallory", "/semdev approve")); err != nil {
		t.Fatalf("unauthorized is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unauthorized command wrote %d facts, want 0", len(writer.calls))
	}
}

// TestUnattributableCommandIsNoSignal — sender ≠ comment author means the body is
// NOT the sender's words; the neutral normalize drops it (ok == false), so the
// command never counts (privilege confusion otherwise).
func TestUnattributableCommandIsNoSignal(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x"}, writer, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "mallory", "/semdev approve")); err != nil {
		t.Fatalf("unattributable is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unattributable command wrote %d facts, want 0", len(writer.calls))
	}
}

// TestApprovalReplayIsIdempotent — an already-approved run gets no second write
// (and no error: the replay acks).
func TestApprovalReplayIsIdempotent(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x", approved: true}, writer, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err != nil {
		t.Fatalf("replay must ack: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("replay wrote %d facts, want 0", len(writer.calls))
	}
}

// TestApprovalBeforeRunExistsRedelivers — the approval racing the mint is a
// TRANSIENT state: the handler errors so the consumer redelivers (bounded).
func TestApprovalBeforeRunExistsRedelivers(t *testing.T) {
	a := newTestApproval(&fakeResolver{runID: ""}, &fakeWriter{}, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err == nil {
		t.Fatal("no-run-yet must return an error (redeliver) — dropping an authorized approval strands the gate")
	}
}

// TestApprovalResolverFaultRedelivers — a transport fault is transient.
func TestApprovalResolverFaultRedelivers(t *testing.T) {
	a := newTestApproval(&fakeResolver{err: errors.New("nats blip")}, &fakeWriter{}, nil)
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", "/semdev approve")); err == nil {
		t.Fatal("resolver fault must redeliver")
	}
}

// TestDecodeErrorIsDefinitiveAck — a malformed payload is logged + ACKED, never
// redelivered (grp2-review carry-forward b: the receiver only publishes shapes it
// flattened itself).
func TestDecodeErrorIsDefinitiveAck(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x"}, writer, nil)
	if err := a.handleCommentEvent(context.Background(), []byte(`{not json`)); err != nil {
		t.Fatalf("a decode error must ack (nil), got %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("a malformed payload wrote %d facts, want 0", len(writer.calls))
	}
}

// TestNonCommandCommentsAreIgnored — ordinary conversation never touches the graph.
func TestNonCommandCommentsAreIgnored(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x"}, writer, nil)
	for _, body := range []string{
		"looks good to me",
		"/semdevil approve",       // prefix-collision guard
		"about /semdev approvals", // "approvals" ≠ the verb token
		"approve /semdev",         // wrong token order
	} {
		if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "cglusky", "cglusky", body)); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
	}
	if len(writer.calls) != 0 {
		t.Errorf("non-command comments wrote %d facts, want 0", len(writer.calls))
	}
}

// TestHasApprovalCommandTokenizing pins the two-token whole-word match.
func TestHasApprovalCommandTokenizing(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"/semdev approve", true},
		{"please /semdev approve now", true},
		{"/SEMDEV APPROVE", true}, // case-insensitive like the opt-in
		{"/semdev  approve", true},
		{"/semdev\napprove", true},
		{"/semdevapprove", false},
		{"/semdev", false},
		{"approve", false},
		{"", false},
	}
	for _, c := range cases {
		if got := hasApprovalCommand("/semdev", c.text); got != c.want {
			t.Errorf("hasApprovalCommand(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestHasRejectCommandTokenizing pins the reject verb's two-token whole-word match
// (the deterministic counterpart to approve).
func TestHasRejectCommandTokenizing(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"/semdev reject", true},
		{"please /semdev reject now", true},
		{"/SEMDEV REJECT", true},
		{"/semdevreject", false},
		{"/semdev", false},
		{"reject", false},
		{"/semdev approve", false}, // the other verb is not a reject
		{"", false},
	}
	for _, c := range cases {
		if got := hasRejectCommand("/semdev", c.text); got != c.want {
			t.Errorf("hasRejectCommand(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// --- the grp3 pins: hybrid fast-path (exact approve/reject) + the NL bridge ---

// TestExactApproveCommandStillDeterministic — the whole-token /semdev approve
// fast-path stays BYTE-IDENTICAL under the hybrid dispatch: it stamps
// run.change.approved (Source approval-adapter) and NEVER consults the classifier
// ledger (no NL path, zero model turns). The migrated approval pins above are the
// regression guard; this adds the "no classifier read on the fast-path" assertion.
func TestExactApproveCommandStillDeterministic(t *testing.T) {
	writer := &fakeWriter{}
	reader := &fakeIntentReader{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	a.reader = reader

	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "1", Author: "cglusky", Body: "/semdev approve"},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if len(writer.calls) != 1 || len(writer.calls[0].add) != 1 {
		t.Fatalf("exact approve = %d calls, want one 1-triple write", len(writer.calls))
	}
	tr := writer.calls[0].add[0]
	if tr.Predicate != admission.ApprovedPredicate || tr.Object != "true" || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s)",
			tr.Predicate, tr.Object, tr.Source, admission.ApprovedPredicate, ApprovedSource)
	}
	if reader.reads != 0 {
		t.Errorf("exact approve consulted the classifier ledger %d times, want 0 (deterministic fast-path)", reader.reads)
	}
}

// TestExactRejectCommandStampsRejected — a whole-token /semdev reject stamps
// run.change.rejected (Source approval-adapter) deterministically, no model turn,
// no classifier read. (The awaiting_approval→cancelled transition is a rule,
// asserted in group 5.)
func TestExactRejectCommandStampsRejected(t *testing.T) {
	writer := &fakeWriter{}
	reader := &fakeIntentReader{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	a.reader = reader

	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "2", Author: "cglusky", Body: "/semdev reject"},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if len(writer.calls) != 1 || len(writer.calls[0].add) != 1 {
		t.Fatalf("exact reject = %d calls, want one 1-triple write", len(writer.calls))
	}
	tr := writer.calls[0].add[0]
	if tr.Predicate != admission.RejectedPredicate || tr.Object != "true" || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s)",
			tr.Predicate, tr.Object, tr.Source, admission.RejectedPredicate, ApprovedSource)
	}
	if reader.reads != 0 {
		t.Errorf("exact reject consulted the classifier ledger %d times, want 0", reader.reads)
	}
}

// TestExactRejectRefusedOnApprovedRun — H1: an approval is irreversible once
// landed, so /semdev reject on an already-approved run is a no-op (no write).
func TestExactRejectRefusedOnApprovedRun(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-1", approved: true, phase: "executing"}, writer, nil)
	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "3", Author: "cglusky", Body: "/semdev reject"},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("reject on an approved run wrote %d facts, want 0 (H1 — approval irreversible)", len(writer.calls))
	}
}

// TestNonCommandAuthorizedGatedMessageStampsPending — a non-command message from an
// AUTHORIZED author on an awaiting_approval run stamps
// conversation.pending.{message-id,author,body} (Source conversation-adapter) and
// ACKs; an UNAUTHORIZED author and a NON-gated run each stamp NOTHING.
func TestNonCommandAuthorizedGatedMessageStampsPending(t *testing.T) {
	ctx := context.Background()
	const thread = "c360studio/semdev-fixture#7"

	// Authorized author, gated run, fresh id → three pending triples on the run.
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	msg := conversation.Message{ID: "msg-42", Author: "cglusky", Body: "yes, please ship this"}
	if err := a.handleMessage(ctx, msg, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("authorized gated non-command: %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("pending stamp = %d writer calls, want 1", len(writer.calls))
	}
	got := map[string]string{}
	for _, tr := range writer.calls[0].add {
		if tr.Source != conversationintent.AdapterSource {
			t.Errorf("pending triple %s Source = %q, want %q", tr.Predicate, tr.Source, conversationintent.AdapterSource)
		}
		if s, ok := tr.Object.(string); ok {
			got[tr.Predicate] = s
		}
	}
	if got[conversationintent.PendingMessageIDPredicate] != "msg-42" ||
		got[conversationintent.PendingAuthorPredicate] != "cglusky" ||
		got[conversationintent.PendingBodyPredicate] != "yes, please ship this" {
		t.Errorf("pending triples = %v, want message-id=msg-42 author=cglusky body=<the message>", got)
	}

	// Unauthorized author → zero writes, zero model turns.
	uWriter := &fakeWriter{}
	ua := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, uWriter, admission.AllowlistOnlyChecker{})
	if err := ua.handleMessage(ctx, conversation.Message{ID: "m", Author: "mallory", Body: "approve it"}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("unauthorized non-command: %v", err)
	}
	if len(uWriter.calls) != 0 {
		t.Errorf("unauthorized non-command wrote %d facts, want 0", len(uWriter.calls))
	}

	// Non-gated run (phase != awaiting_approval) → zero writes.
	nWriter := &fakeWriter{}
	na := newTestApproval(&fakeResolver{runID: "run-1", phase: "executing"}, nWriter, nil)
	if err := na.handleMessage(ctx, conversation.Message{ID: "m2", Author: "cglusky", Body: "looks good"}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("non-gated non-command: %v", err)
	}
	if len(nWriter.calls) != 0 {
		t.Errorf("non-command on a non-gated run wrote %d facts, want 0", len(nWriter.calls))
	}
}

// TestPendingDedupByAppendSetLedger — dedup is against the MULTI-VALUED
// conversation.intent.classified ledger: a message id already in the ledger is NOT
// re-stamped (no re-spawn, MEDIUM-4); a genuinely new id stamps.
func TestPendingDedupByAppendSetLedger(t *testing.T) {
	ctx := context.Background()
	const thread = "c360studio/semdev-fixture#7"
	ledger := []message.Triple{
		{Predicate: conversationintent.IntentClassifiedPredicate, Object: "old-1"},
		{Predicate: conversationintent.IntentClassifiedPredicate, Object: "old-2"},
	}

	// An id already in the ledger → no re-stamp.
	dWriter := &fakeWriter{}
	da := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, dWriter, nil)
	da.reader = &fakeIntentReader{facts: ledger}
	if err := da.handleMessage(ctx, conversation.Message{ID: "old-2", Author: "cglusky", Body: "re-read"}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("already-classified message: %v", err)
	}
	if len(dWriter.calls) != 0 {
		t.Errorf("an already-classified id re-stamped pending (%d writes), want 0 (append-set dedup)", len(dWriter.calls))
	}

	// A genuinely new id → stamps.
	nWriter := &fakeWriter{}
	na := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, nWriter, nil)
	na.reader = &fakeIntentReader{facts: ledger}
	if err := na.handleMessage(ctx, conversation.Message{ID: "new-3", Author: "cglusky", Body: "ship it"}, conversation.ThreadRef(thread)); err != nil {
		t.Fatalf("new message: %v", err)
	}
	if len(nWriter.calls) != 1 {
		t.Errorf("a new id did not stamp pending (%d writes), want 1", len(nWriter.calls))
	}
}

// TestNonCommandLedgerReadFaultRedelivers — a classified-ledger read fault is
// TRANSIENT (redeliver), never a silent drop of an authorized message.
func TestNonCommandLedgerReadFaultRedelivers(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-1", phase: "awaiting_approval"}, writer, nil)
	a.reader = &fakeIntentReader{err: errors.New("graph read blip")}
	if err := a.handleMessage(context.Background(),
		conversation.Message{ID: "x", Author: "cglusky", Body: "ship it"},
		conversation.ThreadRef("c360studio/semdev-fixture#7")); err == nil {
		t.Fatal("a ledger read fault must redeliver (return error), not silently drop the message")
	}
	if len(writer.calls) != 0 {
		t.Errorf("a read fault stamped %d facts, want 0", len(writer.calls))
	}
}
