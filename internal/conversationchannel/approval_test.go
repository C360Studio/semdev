package conversationchannel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/message"
)

// --- fakes ---

type fakeResolver struct {
	runID    string
	approved bool
	err      error
}

func (f *fakeResolver) ResolveRunByRef(context.Context, string) (string, bool, error) {
	return f.runID, f.approved, f.err
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
	return &approvalAdapter{cfg: cfg, checker: checker, resolver: resolver, writer: writer, logger: slog.Default()}
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
