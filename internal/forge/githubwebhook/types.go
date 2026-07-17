// Package githubwebhook owns the GitHub webhook payload types semdev decodes at the
// intake boundary. The framework shipped these in input/github-webhook through
// beta.146; the pre-v1 breaking wave (beta.147) removed that package under the
// ADR-075 framework-package-admission boundary, so semdev now owns the shapes it
// consumes (docs/operations/31 sister-repo cutover checklist — semdev owns the GitHub
// webhook types). The JSON tags MUST match the FLATTENED `github.event.*` payload the
// forge webhook input publishes, byte-for-byte, or Normalize silently decodes zeros.
//
// M0 SCOPE (G9 minimal): only the issue-event cluster is re-homed — the one flow
// intake.Normalize handles (issue `opened`). The PR/comment/review payloads are
// deferred with their flows (normalize.go's D13 upstream asks) and re-home when those
// land, so no dead vocabulary ships ahead of a consumer.
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
