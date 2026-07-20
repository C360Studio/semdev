package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// commenter is the narrow posting surface Post needs — github.Client satisfies it
// (the same CreateComment the park lane used before the carve).
type commenter interface {
	CreateComment(ctx context.Context, owner, repo string, number int, body string) error
}

// GitHubChannel is the GitHub issue/PR-comment implementation of Channel. It is
// the SOLE v1 conversation impl; a second channel (Slack, …) is a new type behind
// the same port, not a change to any caller.
type GitHubChannel struct {
	commenter commenter
}

var _ Channel = (*GitHubChannel)(nil)

// NewGitHubChannel builds the GitHub conversation channel over a posting client
// (github.Client). The no-forge-token deployment (allowlist-only boots, e2e
// journeys) decides the graph-only degrade at the CONSUMER — exactly as park-post
// does today: it skips the post and leaves the durable fact graph-only. It must NOT
// construct a channel with a nil client and expect Post to swallow the message. A
// nil commenter reaching Post is therefore a WIRING error, guarded defensively
// below, not a runtime deployment path.
func NewGitHubChannel(c commenter) *GitHubChannel {
	return &GitHubChannel{commenter: c}
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
	if c.commenter == nil {
		// Wiring error, not a deployment path: the no-token graph-only degrade is
		// the consumer's to make (it skips Post), never a nil client swallowed here.
		return fmt.Errorf("conversation: github channel has no posting client; cannot post to %q (wiring error)", string(thread))
	}
	owner, repo, number, err := admission.SplitRef(string(thread))
	if err != nil {
		return fmt.Errorf("conversation: resolve thread %q: %w", string(thread), err)
	}
	return c.commenter.CreateComment(ctx, owner, repo, number, body)
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
