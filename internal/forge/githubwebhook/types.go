// Package githubwebhook owns the GitHub webhook payload types semdev decodes at the
// intake boundary. The framework shipped these in input/github-webhook through
// beta.146; the pre-v1 breaking wave (beta.147) removed that package under the
// ADR-075 framework-package-admission boundary, so semdev now owns the shapes it
// consumes (docs/operations/31 sister-repo cutover checklist — semdev owns the GitHub
// webhook types). The JSON tags MUST match the FLATTENED `github.event.*` payload the
// forge webhook input publishes, byte-for-byte, or Normalize silently decodes zeros.
//
// SCOPE: the issue-event cluster (intake's `opened` flow) and — since
// forge-io-real-lanes — the COMMENT-event cluster (the approval lane) plus the
// RAW→FLATTENED mapping itself (flatten.go): the framework retired the whole
// receiver, so semdev owns the flattener and with it the two payload gaps the
// old flattener had (transferred backlog #2 comment parent number — CLOSED by
// CommentEvent.IssueNumber; #3 specific added/removed label — still open, the
// `labeled` opt-in flow stays deferred). PR/review payloads re-home when their
// flows land, so no dead vocabulary ships ahead of a consumer.
package githubwebhook

import "time"

// WebhookEvent is the common header present in every GitHub webhook event.
// Specific event types embed this struct and add their own domain-specific fields.
type WebhookEvent struct {
	// EventType is the value of the X-GitHub-Event header (e.g. "issues").
	EventType string `json:"event_type"`

	// Action is the fine-grained sub-action within the event type
	// (e.g. "opened", "edited", "closed").
	Action string `json:"action"`

	// Repository identifies the repository that generated the event.
	Repository Repository `json:"repository"`

	// Sender is the GitHub login of the user who triggered the event.
	Sender string `json:"sender"`

	// DeliveryID is GitHub's per-delivery GUID (the X-GitHub-Delivery header),
	// stamped by semdev's receiver (forge-io-real-lanes — semdev owns the
	// flattener now). It is the natural idempotency key: a webhook REDELIVERY
	// carries the same GUID, so the consumer's content-derived admission record
	// collapses duplicates instead of double-waking the arc. Empty when the
	// event entered without a receiver (an e2e journey publishing the flattened
	// shape straight onto the stream).
	DeliveryID string `json:"delivery_id,omitempty"`

	// ReceivedAt is the UTC timestamp at which this process received the event.
	ReceivedAt time.Time `json:"received_at"`
}

// Repository holds the identifying fields of a GitHub repository.
type Repository struct {
	// Owner is the organisation or user that owns the repository.
	Owner string `json:"owner"`

	// Name is the repository name without the owner prefix.
	Name string `json:"name"`

	// FullName is the canonical "owner/name" identifier.
	FullName string `json:"full_name"`
}

// IssueEvent represents a GitHub "issues" webhook event.
type IssueEvent struct {
	WebhookEvent
	Issue IssuePayload `json:"issue"`
}

// IssuePayload contains the fields extracted from a GitHub issue object.
type IssuePayload struct {
	// Number is the repository-scoped issue number.
	Number int `json:"number"`

	// Title is the issue title.
	Title string `json:"title"`

	// Body is the issue body text (may be empty).
	Body string `json:"body"`

	// State is the issue state: "open" or "closed".
	State string `json:"state"`

	// Labels is the list of label names applied to the issue.
	Labels []string `json:"labels"`

	// Author is the GitHub login of the issue author.
	Author string `json:"author"`

	// HTMLURL is the canonical browser URL for the issue.
	HTMLURL string `json:"html_url"`
}

// CommentEvent represents a GitHub "issue_comment" webhook event in semdev's
// OWN flattened shape (forge-io-real-lanes). The framework's retired flattener
// dropped the parent issue number — the documented gap that deferred every
// comment-driven flow (transferred to semdev as backlog #2 when the receiver
// moved in-tree). semdev's flattener CLOSES it: the comment event carries the
// parent issue number, so an approval command can bind to both its issue AND
// its actor (the admission Event invariant).
type CommentEvent struct {
	WebhookEvent

	// IssueNumber is the parent issue's repository-scoped number — the field
	// whose absence deferred the comment flows. Always populated by semdev's
	// receiver from the raw payload's issue.number.
	IssueNumber int `json:"issue_number"`

	// Comment is the comment body cluster.
	Comment CommentPayload `json:"comment"`
}

// CommentPayload contains the fields extracted from a GitHub comment object.
type CommentPayload struct {
	// Body is the comment text.
	Body string `json:"body"`

	// Author is the GitHub login of the comment author. The approval lane
	// admits a command ONLY when Author equals the event Sender (the same
	// attribution guard the issue-opened lane uses).
	Author string `json:"author"`
}
