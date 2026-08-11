package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/intake/admission"
)

// This file is the GitHub v1 implementation of the channel-neutral Channel port
// (conversation-channel-seam D1–D3). It dissolves the three GitHub coupling points
// AT THE SEAM so the arc/component boundary handles a neutral Message + ThreadRef,
// never GitHub's CreateComment / CommentEvent / owner-repo-number shape:
//
//   - POST — Post resolves the ThreadRef ("owner/repo#number") to a GitHub
//     issue/PR coordinate and calls the forge client's CreateComment (the old
//     parkpost.Commenter path, re-homed).
//   - THREAD — ResolveThread is identity: a GitHub comment lives ON the work
//     object, so the work reference IS the thread reference (D1).
//   - NORMALIZE — the flattened webhook comment payload is decoded and mapped to a
//     neutral Message + ThreadRef by normalizeComment (UNEXPORTED, host-specific,
//     contained here). NormalizeInboundComment is the []byte boundary the
//     conversation component calls, so a CommentEvent never reaches it.
//
// SplitRef lives in the shared internal/intake/admission core (the front-door
// coordinate parser both components + the launch driver use).

// conversationClient is the narrow forge surface the GitHub channel needs:
// CreateComment (Post) and ListComments (Read). github.Client satisfies it — the
// same client the park lane posts through and the poll transport reads through.
type conversationClient interface {
	CreateComment(ctx context.Context, owner, repo string, number int, body string) error
	ListComments(ctx context.Context, owner, repo string, number int) ([]github.Comment, error)
}

// GitHubChannel is the GitHub issue/PR-comment implementation of Channel. It is
// the SOLE v1 conversation impl; a second channel (Slack, …) is a new type behind
// the same port, not a change to any caller.
type GitHubChannel struct {
	client conversationClient
}

var _ Channel = (*GitHubChannel)(nil)

// NewGitHubChannel builds the GitHub conversation channel over a forge client
// (github.Client). The no-forge-token deployment (allowlist-only boots, e2e
// journeys) decides the graph-only degrade at the CONSUMER — exactly as park-post
// does today: it skips the post and leaves the durable fact graph-only. It must NOT
// construct a channel with a nil client and expect Post/Read to swallow the call. A
// nil client reaching Post/Read is therefore a WIRING error, guarded defensively
// below, not a runtime deployment path.
func NewGitHubChannel(c conversationClient) *GitHubChannel {
	return &GitHubChannel{client: c}
}

// ResolveThread maps a host-neutral work reference to its ThreadRef. For GitHub v1
// this is identity — comments live on the work object, so the work reference IS the
// thread reference (D1). A future non-identity channel (Slack) resolves here.
func (c *GitHubChannel) ResolveThread(_ context.Context, workRef string) (ThreadRef, error) {
	return ThreadRef(workRef), nil
}

// Post publishes body to the thread. It resolves the ThreadRef's "owner/repo#number"
// coordinate and calls CreateComment. It fails closed: a malformed thread or a
// transport blip returns an error. The caller owns how to treat that error against
// its durable fact — the park-post consumer, as today, retries a transport blip
// bounded and treats a permanent condition (a malformed ref) as a definitive
// graph-only skip; it must not redeliver a permanent failure to exhaustion.
func (c *GitHubChannel) Post(ctx context.Context, thread ThreadRef, body string) error {
	if c.client == nil {
		// Wiring error, not a deployment path: the no-token graph-only degrade is
		// the consumer's to make (it skips Post), never a nil client swallowed here.
		return fmt.Errorf("conversation: github channel has no forge client; cannot post to %q (wiring error)", string(thread))
	}
	owner, repo, number, err := admission.SplitRef(string(thread))
	if err != nil {
		return fmt.Errorf("conversation: resolve thread %q: %w", string(thread), err)
	}
	return c.client.CreateComment(ctx, owner, repo, number, body)
}

// Read returns the thread's comments AFTER cursor, mapped to neutral Messages,
// plus the cursor to pass next time (pull-first-transport D7). The GitHub cursor is
// the last-seen comment ID: it lists all comments (ListComments follows pagination)
// and keeps those with id > cursor.
//
// The comparison is NUMERIC, never lexical (review M1): the cursor parses to an
// int64 (empty = 0 = read all) and comments are kept when their id exceeds it — a
// string compare would break at a digit-width boundary ("9" > "10") and still ship
// green on small-id unit tests. When the filtered set is EMPTY, Read returns the
// INPUT cursor unchanged, NOT max-of-empty (review M2): resetting to the top would
// make a still-gated thread (one that never leaves the poller's enumeration) re-read
// and re-authorize its whole history every tick. It fails closed — a wiring error, a
// malformed thread, or a ListComments transport blip returns the INPUT cursor and
// the error, so the poller retries next tick without losing its place. No
// attribution guard: a polled comment has exactly one author (unlike a webhook's
// sender/author pair), so every Message is attributable to its Author.
func (c *GitHubChannel) Read(ctx context.Context, thread ThreadRef, cursor Cursor) ([]Message, Cursor, error) {
	if c.client == nil {
		return nil, cursor, fmt.Errorf("conversation: github channel has no forge client; cannot read %q (wiring error)", string(thread))
	}
	owner, repo, number, err := admission.SplitRef(string(thread))
	if err != nil {
		return nil, cursor, fmt.Errorf("conversation: resolve thread %q: %w", string(thread), err)
	}
	comments, err := c.client.ListComments(ctx, owner, repo, number)
	if err != nil {
		return nil, cursor, fmt.Errorf("conversation: read thread %q: %w", string(thread), err)
	}

	// Numeric cursor (M1). A malformed cursor is never minted by this impl; if one
	// ever arrived, treating it as 0 (read all) is the safe, idempotent choice.
	var cursorInt int64
	if cursor != "" {
		if n, perr := strconv.ParseInt(string(cursor), 10, 64); perr == nil {
			cursorInt = n
		}
	}
	maxID := cursorInt
	var msgs []Message
	for _, cm := range comments {
		if cm.ID <= cursorInt {
			continue
		}
		msgs = append(msgs, Message{
			ID:     strconv.FormatInt(cm.ID, 10),
			Author: cm.Author,
			Body:   cm.Body,
			At:     parseCommentTime(cm.CreatedAt),
		})
		if cm.ID > maxID {
			maxID = cm.ID
		}
	}
	if len(msgs) == 0 {
		// No new comments: keep the INPUT cursor (M2 — no re-read storm).
		return nil, cursor, nil
	}
	return msgs, Cursor(strconv.FormatInt(maxID, 10)), nil
}

// parseCommentTime parses a github.Comment.CreatedAt (RFC3339) best-effort,
// returning the zero time on an unparseable/absent value. At IS load-bearing
// since the D12a gate-open watermark (nl-conversation-intent 8.1a): both
// inbound paths REFUSE an untimestamped message (fail closed), so a zero time
// here means that comment can never decide a gate — the right direction for a
// value the code-host failed to supply. The dedup key remains Message.ID.
func parseCommentTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// NormalizeInboundComment decodes one flattened github.event.comment payload into a
// neutral Message + its ThreadRef. It is the []byte boundary the conversation
// component consumes, so the component sees a Message + ThreadRef and never a
// githubwebhook.CommentEvent (coupling point 2, dissolved at the seam). ok reports
// whether the comment is a usable signal (a created comment, with a parent issue,
// attributable to its author — see normalizeComment); a decode failure is a loud
// error (the receiver only publishes shapes it flattened itself).
func NormalizeInboundComment(payload []byte) (msg Message, thread ThreadRef, ok bool, err error) {
	var e githubwebhook.CommentEvent
	if uerr := json.Unmarshal(payload, &e); uerr != nil {
		return Message{}, "", false, fmt.Errorf("conversation: decode comment event: %w", uerr)
	}
	msg, thread, ok = normalizeComment(e)
	return msg, thread, ok, nil
}

// normalizeComment maps a flattened GitHub comment event to a neutral Message +
// ThreadRef (UNEXPORTED — the one place GitHub's CommentEvent shape is known). ok is
// false for a comment that is not a usable signal:
//
//   - not a `created` comment, or missing its parent issue number (not a signal at
//     all — the `edited`/`deleted` actions and any malformed event); and
//   - NOT ATTRIBUTABLE: the event sender is not the comment's author. A webhook
//     carries two identities; the approval lane acts on a human's words, so a body
//     the sender did not write is never treated as the sender's command (the
//     admission Event invariant, preserved byte-for-byte from the pre-carve
//     NormalizeComment). Because the guard gates ok, a foreign-authored body never
//     reaches the command check downstream.
//
// When ok, Message.Author is the (attributed) author of Body — the authorization
// principal the component checks. The webhook flattened shape carries no per-comment
// ID or timestamp, so ID falls back to the delivery GUID and At to the receive time
// (best-effort; the webhook push path dedups at the JetStream msg-id layer, not via
// Message.ID — that field is the poll transport's dedup key, added by
// pull-first-transport).
func normalizeComment(e githubwebhook.CommentEvent) (Message, ThreadRef, bool) {
	if e.Action != "created" || e.IssueNumber == 0 {
		return Message{}, "", false
	}
	if !strings.EqualFold(strings.TrimSpace(e.Sender), strings.TrimSpace(e.Comment.Author)) {
		return Message{}, "", false
	}
	thread := ThreadRef(fmt.Sprintf("%s#%d", e.Repository.FullName, e.IssueNumber))
	msg := Message{
		ID:     e.DeliveryID,
		Author: e.Comment.Author,
		Body:   e.Comment.Body,
		At:     e.ReceivedAt,
	}
	return msg, thread, true
}
