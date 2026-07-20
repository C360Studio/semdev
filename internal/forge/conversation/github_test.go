package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
)

// fakeCommenter records the last CreateComment call (and can fail on demand). It
// also serves ListComments for the Read pin — the GitHub channel's client surface
// is now CreateComment + ListComments (pull-first-transport widened it).
type fakeCommenter struct {
	owner, repo, body string
	number            int
	calls             int
	err               error

	// listComments is the thread ListComments returns; listErr fails the read.
	listComments []github.Comment
	listErr      error
	listCalls    int
}

func (f *fakeCommenter) CreateComment(_ context.Context, owner, repo string, number int, body string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.owner, f.repo, f.number, f.body = owner, repo, number, body
	return nil
}

func (f *fakeCommenter) ListComments(_ context.Context, _, _ string, _ int) ([]github.Comment, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listComments, nil
}

// TestGitHubChannelReadFiltersByCursor pins the poll Read (pull-first-transport
// D1/D7): each github.Comment maps to a neutral Message{ID=itoa(id),Author,Body,At};
// the cursor filter is NUMERIC (parse to int64 — a lexical compare breaks at a
// digit-width boundary, "9" > "10"); an empty cursor returns ALL; a no-new-comments
// read returns the INPUT cursor unchanged (M2 — no re-read storm).
func TestGitHubChannelReadFiltersByCursor(t *testing.T) {
	at := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// Deliberately SHUFFLED (not ascending) so the next-cursor assertions prove the
	// impl takes the true MAX of the returned set, not merely the last element —
	// order-independence of the maxID accumulation (go-review hardening).
	fake := &fakeCommenter{listComments: []github.Comment{
		{ID: 11, Author: "cara", Body: "/semdev approve", CreatedAt: at.Format(time.RFC3339)},
		{ID: 9, Author: "alice", Body: "nine", CreatedAt: at.Format(time.RFC3339)},
		{ID: 10, Author: "bob", Body: "ten", CreatedAt: at.Format(time.RFC3339)},
	}}
	ch := NewGitHubChannel(fake)
	ctx := context.Background()

	// Empty cursor → ALL comments, mapped to neutral Messages; next cursor = max id
	// (11, though it is the FIRST element — proving max, not last).
	msgs, next, err := ch.Read(ctx, ThreadRef("acme/widgets#7"), "")
	if err != nil {
		t.Fatalf("Read(empty cursor): %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("empty-cursor Read returned %d messages, want all 3", len(msgs))
	}
	if msgs[0].ID != "11" || msgs[0].Author != "cara" || !msgs[0].At.Equal(at) {
		t.Errorf("Message[0] = %+v, want the first listed comment {ID:11, cara, %v}", msgs[0], at)
	}
	if next != Cursor("11") {
		t.Errorf("next cursor = %q, want 11 (the MAX comment id, regardless of list order)", next)
	}

	// Digit-width boundary (M1): cursor "9" must return the SET {10, 11} NUMERICALLY
	// — a lexical compare would wrongly keep only ids that sort after "9" (none). The
	// impl preserves source order (it does not sort), so assert the set, not a fixed
	// sequence.
	msgs, next, err = ch.Read(ctx, ThreadRef("acme/widgets#7"), Cursor("9"))
	if err != nil {
		t.Fatalf("Read(cursor 9): %v", err)
	}
	gotIDs := map[string]bool{}
	for _, m := range msgs {
		gotIDs[m.ID] = true
	}
	if len(msgs) != 2 || !gotIDs["10"] || !gotIDs["11"] {
		t.Fatalf("cursor-9 Read returned %+v, want the set {10, 11} (numeric compare, excludes the equal-id 9)", msgs)
	}
	if next != Cursor("11") {
		t.Errorf("next cursor = %q, want 11", next)
	}

	// No-new-comments (cursor at the max): empty result, INPUT cursor unchanged
	// (M2 — max-of-empty would reset to the top and re-read every tick).
	msgs, next, err = ch.Read(ctx, ThreadRef("acme/widgets#7"), Cursor("11"))
	if err != nil {
		t.Fatalf("Read(cursor 11): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("cursor-11 Read returned %d messages, want 0 (all seen)", len(msgs))
	}
	if next != Cursor("11") {
		t.Errorf("empty-read next cursor = %q, want the INPUT cursor 11 unchanged (no re-read storm)", next)
	}

	// A transport error fails closed (propagated), returning the INPUT cursor so the
	// poller retries next tick without losing its place.
	failing := NewGitHubChannel(&fakeCommenter{listErr: errors.New("502 bad gateway")})
	if _, nc, rerr := failing.Read(ctx, ThreadRef("acme/widgets#7"), Cursor("5")); rerr == nil {
		t.Error("Read did not propagate the ListComments transport error")
	} else if nc != Cursor("5") {
		t.Errorf("failed-read next cursor = %q, want the INPUT cursor 5 unchanged", nc)
	}

	// A malformed thread fails closed before any list call.
	if _, _, rerr := ch.Read(ctx, ThreadRef("not-a-ref"), ""); rerr == nil {
		t.Error("Read of a malformed thread returned nil; want a fail-closed error")
	}
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
