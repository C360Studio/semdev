package intake

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/forge/githubwebhook"
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

func newTestApproval(resolver RunResolver, writer *fakeWriter, checker PermissionChecker) *approvalAdapter {
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

// --- the approval pins (task 3.1) ---

// TestAuthorizedApprovalReleasesTheGate pins the whole authorized path: the
// command lands run.change.approved="true" (Source approval-adapter) on the
// resolved run — byte-for-byte the stand-in fact the resume rule consumes.
func TestAuthorizedApprovalReleasesTheGate(t *testing.T) {
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
	if tr.Predicate != ApprovedPredicate || tr.Object != "true" || tr.Source != ApprovedSource {
		t.Errorf("triple = %s=%v (Source %s), want %s=true (Source %s) — the exact resume-rule condition",
			tr.Predicate, tr.Object, tr.Source, ApprovedPredicate, ApprovedSource)
	}
}

// TestUnauthorizedApprovalIsIgnored — the Event invariant: no authorization,
// no write, definitive ack.
func TestUnauthorizedApprovalIsIgnored(t *testing.T) {
	writer := &fakeWriter{}
	a := newTestApproval(&fakeResolver{runID: "run-x"}, writer, allowlistOnlyChecker{})
	if err := a.handleCommentEvent(context.Background(), flattenedComment(t, "mallory", "mallory", "/semdev approve")); err != nil {
		t.Fatalf("unauthorized is definitive: %v", err)
	}
	if len(writer.calls) != 0 {
		t.Errorf("unauthorized command wrote %d facts, want 0", len(writer.calls))
	}
}

// TestUnattributableCommandIsNoSignal — sender ≠ comment author means the
// body is NOT the sender's words; the command must not count (privilege
// confusion otherwise: an authorized actor's unrelated event inheriting a
// foreign command).
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

// TestApprovalReplayIsIdempotent — an already-approved run gets no second
// write (and no error: the replay acks).
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
// TRANSIENT state: the handler errors so the consumer redelivers (bounded),
// instead of silently dropping an authorized human's approval.
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

// TestNonCommandCommentsAreIgnored — ordinary conversation on the issue never
// touches the graph.
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

// TestNormalizeCommentShapes pins the comment normalization: created+attributable
// carries the body; edited/deleted are irrelevant; sender≠author strips the text.
func TestNormalizeCommentShapes(t *testing.T) {
	attributable := flattenedComment(t, "cglusky", "cglusky", "/semdev approve")
	sig, err := NormalizeComment(attributable)
	if err != nil || !sig.Relevant || !sig.Attributable {
		t.Fatalf("attributable created comment: sig=%+v err=%v", sig, err)
	}
	if sig.IssueRef != "c360studio/semdev-fixture#7" {
		t.Errorf("IssueRef = %q — the parent-issue binding (the closed gap)", sig.IssueRef)
	}
	if !strings.Contains(sig.Event.AuthoredText, "approve") {
		t.Errorf("attributable comment must carry the body")
	}

	foreign := flattenedComment(t, "cglusky", "mallory", "/semdev approve")
	sig, err = NormalizeComment(foreign)
	if err != nil || !sig.Relevant {
		t.Fatalf("foreign-author comment stays relevant: %v", err)
	}
	if sig.Attributable || sig.Event.AuthoredText != "" {
		t.Errorf("foreign-author comment must be unattributable with NO text, got %+v", sig)
	}

	var edited githubwebhook.CommentEvent
	_ = json.Unmarshal(flattenedComment(t, "cglusky", "cglusky", "x"), &edited)
	edited.Action = "edited"
	editedData, _ := json.Marshal(edited)
	sig, err = NormalizeComment(editedData)
	if err != nil || sig.Relevant {
		t.Errorf("an edited comment is not a signal, got %+v (%v)", sig, err)
	}
}
