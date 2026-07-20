// Package conversation is semdev's channel-neutral conversation seam: the
// ConversationChannel port and the neutral Message it carries. semdev's human
// interface is a message thread — today GitHub issue/PR comments, tomorrow (each
// composed behind this same port) other channels. The arc already consumes only
// normalized facts (run.change.approved, human.opt.signal), never a host payload;
// this package makes the plumbing BENEATH those facts channel-neutral too, so a
// new channel is a new impl behind the port, not a re-hardcoding of GitHub's
// CreateComment / CommentEvent / owner-repo-number shape at every human-message
// lane the arc grows.
//
// G1 (framework-alignment note): this introduces NO new primitive. It re-homes
// the existing forge-io conversation adapters (parkpost's CreateComment, the
// webhook CommentEvent normalize) behind one Go port — the framework-house shape
// "one component owns the fact, adapters plug in behind it." The GitHub v1 impl
// (Post → github.Client.CreateComment; the CommentEvent → Message normalize
// CONTAINED and unexported) lives beside this file in the same package, so the
// package's EXPORTED surface stays host-neutral (a conformance pin enforces it).
package conversation

import (
	"context"
	"time"
)

// ThreadRef is an opaque, host-neutral handle to the conversation thread a
// message lives on. Only the ConversationChannel impl parses it. For GitHub v1
// it is the host-neutral "owner/repo#number" work coordinate (comments live on
// the work object, so ResolveThread is identity); a future Slack impl would
// resolve a work ref to a "slack:channel/ts" thread (non-identity — a binding
// fact, out of scope here). Callers treat it as a value they never inspect.
type ThreadRef string

// Message is the channel-neutral unit the conversation component processes — the
// generalization of the GitHub-specific CommentSignal. The arc/component boundary
// sees this, never a githubwebhook.CommentEvent. Attribution (which identity Body
// is credited to) is resolved INSIDE the impl before a Message is emitted; by the
// time a Message exists, Author is the trustworthy author of Body. It deliberately
// carries NO host coordinate (owner/repo/number/issueRef) — a channel binds a
// Message to its thread through the transport, not through a field on the unit.
type Message struct {
	// ID is the channel-native, stable message identifier (a comment ID on
	// GitHub) — the dedup key a poll transport keys its idempotent re-read on.
	ID string
	// Author is the actor credited with Body — resolved by the impl's attribution
	// guard (GitHub's sender==comment-author check) before the Message is emitted.
	Author string
	// Body is the message text.
	Body string
	// At is the message's channel-native timestamp.
	At time.Time
}

// Cursor is an opaque, impl-defined read position on a thread. "" reads from the
// top (the first poll / a restart). Only the Channel impl encodes and parses it —
// the poller stores the returned Cursor and passes it back next time, never
// inspecting it. For GitHub v1 it is the last-seen comment ID (comment IDs are
// monotonic), so it is deliberately opaque to keep that host detail off the caller.
type Cursor string

// Channel is the channel-neutral conversation seam (the ConversationChannel port
// of the design): post a message to a thread, resolve a host-neutral work
// reference to its thread, and read a thread's messages after a cursor. Read is the
// third verb the conversation-channel-seam carve deferred and pull-first-transport
// gave a caller (the poller): a webhook-unreachable deployment drives the approval
// gate by polling each active thread instead of consuming pushed comment events.
type Channel interface {
	// Post publishes body to thread. It fails closed — a transport blip returns an
	// error the caller retries bounded, and NEVER blocks a durable fact (the
	// park-post contract: the park is already durable before Post is attempted).
	Post(ctx context.Context, thread ThreadRef, body string) error
	// ResolveThread maps a host-neutral work reference ("owner/repo#number") to the
	// ThreadRef its conversation lives on. For GitHub v1 this is identity (comments
	// live on the work object).
	ResolveThread(ctx context.Context, workRef string) (ThreadRef, error)
	// Read returns the messages on thread AFTER cursor (in channel-native order),
	// plus the cursor to pass next time. An empty cursor reads the whole thread. It
	// fails closed (returns err) — the poller retries next interval and never blocks;
	// on an error, and on an empty successful read, it returns the INPUT cursor
	// unchanged (no re-read storm), so the caller keeps its place.
	Read(ctx context.Context, thread ThreadRef, cursor Cursor) (msgs []Message, next Cursor, err error)
}
