package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/forge/githubwebhook"
)

// fakeCommenter records the last CreateComment call (and can fail on demand).
type fakeCommenter struct {
	owner, repo, body string
	number            int
	calls             int
	err               error
}

func (f *fakeCommenter) CreateComment(_ context.Context, owner, repo string, number int, body string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.owner, f.repo, f.number, f.body = owner, repo, number, body
	return nil
}

// Post resolves the ThreadRef's work coordinate via SplitRef and posts through the
// forge client's CreateComment; ResolveThread is identity (the work reference IS the
// thread reference for GitHub). (conversation-channel-seam 2.1)
func TestGitHubChannelPostResolvesThreadAndComments(t *testing.T) {
	fake := &fakeCommenter{}
	ch := NewGitHubChannel(fake)
	ctx := context.Background()

	// ResolveThread is identity — no host parsing leaks to the caller.
	thread, err := ch.ResolveThread(ctx, "acme/widgets#7")
	if err != nil {
		t.Fatalf("ResolveThread: %v", err)
	}
	if thread != ThreadRef("acme/widgets#7") {
		t.Fatalf("ResolveThread = %q, want identity %q", thread, "acme/widgets#7")
	}

	if err := ch.Post(ctx, thread, "hello world"); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("CreateComment called %d times, want 1", fake.calls)
	}
	if fake.owner != "acme" || fake.repo != "widgets" || fake.number != 7 || fake.body != "hello world" {
		t.Errorf("CreateComment got (%q,%q,%d,%q), want (acme,widgets,7,hello world)",
			fake.owner, fake.repo, fake.number, fake.body)
	}

	// A malformed thread fails closed BEFORE any post (never a silent drop).
	if err := ch.Post(ctx, ThreadRef("not-a-ref"), "x"); err == nil {
		t.Error("Post to a malformed thread returned nil; want a fail-closed error")
	}
	if fake.calls != 1 {
		t.Errorf("a malformed-thread Post still called CreateComment (%d calls); it must fail before posting", fake.calls)
	}

	// A transport blip propagates (wrapped) — the caller retries it bounded.
	boom := errors.New("502 bad gateway")
	failing := NewGitHubChannel(&fakeCommenter{err: boom})
	if err := failing.Post(ctx, ThreadRef("acme/widgets#7"), "x"); !errors.Is(err, boom) {
		t.Errorf("Post did not propagate the commenter transport error; got %v", err)
	}

	// A nil commenter is a defensive WIRING guard (not the no-token degrade — that is
	// the consumer's graph-only skip). It errors before any post rather than panic.
	if err := NewGitHubChannel(nil).Post(ctx, ThreadRef("acme/widgets#7"), "x"); err == nil {
		t.Error("nil-commenter Post returned nil; want a defensive wiring error")
	}
}

// The contained (unexported) normalize maps a githubwebhook.CommentEvent to a
// neutral Message + ThreadRef, preserving the sender==author attribution guard; the
// []byte boundary (NormalizeInboundComment) means the downstream consumer sees a
// Message, never a CommentEvent. (conversation-channel-seam 2.2)
func TestGitHubChannelNormalizeContainsCommentEvent(t *testing.T) {
	at := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ev := githubwebhook.CommentEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issue_comment",
			Action:     "created",
			Sender:     "alice",
			DeliveryID: "guid-123",
			ReceivedAt: at,
			Repository: githubwebhook.Repository{Owner: "acme", Name: "widgets", FullName: "acme/widgets"},
		},
		IssueNumber: 7,
		Comment:     githubwebhook.CommentPayload{Body: "/semdev approve", Author: "alice"},
	}

	// The unexported normalize (the contained host-shape mapping).
	msg, thread, ok := normalizeComment(ev)
	if !ok {
		t.Fatal("normalizeComment(created, attributable) not ok; want ok")
	}
	if thread != ThreadRef("acme/widgets#7") {
		t.Errorf("thread = %q, want acme/widgets#7", thread)
	}
	if msg.Author != "alice" || msg.Body != "/semdev approve" {
		t.Errorf("Message = {Author:%q, Body:%q}, want {alice, /semdev approve}", msg.Author, msg.Body)
	}
	if msg.ID != "guid-123" || !msg.At.Equal(at) {
		t.Errorf("Message = {ID:%q, At:%v}, want {guid-123, %v} (best-effort webhook identity)", msg.ID, msg.At, at)
	}

	// The []byte boundary the component consumes — never a CommentEvent.
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg2, thread2, ok2, err := NormalizeInboundComment(payload)
	if err != nil {
		t.Fatalf("NormalizeInboundComment: %v", err)
	}
	// Compare field-wise (a time.Time struct-== is monotonic/zone-fragile across a
	// JSON round-trip); At uses .Equal.
	if !ok2 || thread2 != thread || msg2.ID != msg.ID || msg2.Author != msg.Author ||
		msg2.Body != msg.Body || !msg2.At.Equal(msg.At) {
		t.Errorf("NormalizeInboundComment = (%+v, %q, %v), want it to equal the direct normalize (%+v, %q)", msg2, thread2, ok2, msg, thread)
	}

	// Attribution guard: sender != comment author → not a usable signal (a body the
	// sender did not write is never the sender's command).
	foreign := ev
	foreign.Comment.Author = "mallory"
	if _, _, ok := normalizeComment(foreign); ok {
		t.Error("normalizeComment admitted a comment whose author != sender; the attribution guard regressed")
	}

	// The guard is EqualFold + TrimSpace (GitHub logins are case-insensitive), NOT
	// ==: a case-differing sender/author is the SAME actor and stays a usable signal.
	casey := ev
	casey.Sender = "Alice"
	casey.Comment.Author = "alice"
	if _, _, ok := normalizeComment(casey); !ok {
		t.Error("normalizeComment rejected a case-differing sender/author; the guard must be EqualFold, not ==")
	}

	// A non-`created` action is not a signal.
	edited := ev
	edited.Action = "edited"
	if _, _, ok := normalizeComment(edited); ok {
		t.Error("normalizeComment admitted an `edited` comment; only `created` is a signal candidate")
	}

	// A comment with no parent issue number is not a signal (the pre-carve gap).
	orphan := ev
	orphan.IssueNumber = 0
	if _, _, ok := normalizeComment(orphan); ok {
		t.Error("normalizeComment admitted a comment with no parent issue number")
	}
}
