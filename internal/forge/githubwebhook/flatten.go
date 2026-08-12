package githubwebhook

import (
	"encoding/json"
	"fmt"
	"time"
)

// This file is the RAW→FLATTENED mapping half of semdev's webhook receiver
// (forge-io-real-lanes): the framework retired its github-webhook input in the
// beta.147 boundary wave, so semdev owns both the flattened shapes (types.go)
// and the flattening itself. The raw structs decode ONLY the fields the
// flattened shapes carry — GitHub's payload is enormous and everything else is
// deliberately dropped at this boundary (the arc never sees a host payload).

// rawRepository is the nested repository object of a raw GitHub webhook payload.
type rawRepository struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// rawUser is a raw GitHub user object (only the login is mapped).
type rawUser struct {
	Login string `json:"login"`
}

// rawIssue is the nested issue object of a raw issues / issue_comment payload.
type rawIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	User    rawUser `json:"user"`
	HTMLURL string  `json:"html_url"`
}

// rawIssueEvent is the raw "issues" webhook payload (mapped fields only).
type rawIssueEvent struct {
	Action     string        `json:"action"`
	Issue      rawIssue      `json:"issue"`
	Repository rawRepository `json:"repository"`
	Sender     rawUser       `json:"sender"`
}

// rawCommentEvent is the raw "issue_comment" webhook payload (mapped fields only).
type rawCommentEvent struct {
	Action  string   `json:"action"`
	Issue   rawIssue `json:"issue"`
	Comment struct {
		Body string  `json:"body"`
		User rawUser `json:"user"`
	} `json:"comment"`
	Repository rawRepository `json:"repository"`
	Sender     rawUser       `json:"sender"`
}

// FlattenIssueEvent maps a raw GitHub "issues" webhook payload to semdev's
// flattened IssueEvent. deliveryID is the X-GitHub-Delivery header (the
// idempotency key); receivedAt is the receiver's wall clock.
func FlattenIssueEvent(raw []byte, deliveryID string, receivedAt time.Time) (*IssueEvent, error) {
	var r rawIssueEvent
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("githubwebhook: decode raw issues payload: %w", err)
	}
	labels := make([]string, 0, len(r.Issue.Labels))
	for _, l := range r.Issue.Labels {
		labels = append(labels, l.Name)
	}
	return &IssueEvent{
		WebhookEvent: WebhookEvent{
			EventType:  "issues",
			Action:     r.Action,
			Repository: flattenRepo(r.Repository),
			Sender:     r.Sender.Login,
			DeliveryID: deliveryID,
			ReceivedAt: receivedAt.UTC(),
		},
		Issue: IssuePayload{
			Number:  r.Issue.Number,
			Title:   r.Issue.Title,
			Body:    r.Issue.Body,
			State:   r.Issue.State,
			Labels:  labels,
			Author:  r.Issue.User.Login,
			HTMLURL: r.Issue.HTMLURL,
		},
	}, nil
}

// FlattenCommentEvent maps a raw GitHub "issue_comment" webhook payload to
// semdev's flattened CommentEvent — CARRYING the parent issue number (the field
// the framework's retired flattener dropped; semdev backlog #2, closed here).
func FlattenCommentEvent(raw []byte, deliveryID string, receivedAt time.Time) (*CommentEvent, error) {
	var r rawCommentEvent
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("githubwebhook: decode raw issue_comment payload: %w", err)
	}
	return &CommentEvent{
		WebhookEvent: WebhookEvent{
			EventType:  "issue_comment",
			Action:     r.Action,
			Repository: flattenRepo(r.Repository),
			Sender:     r.Sender.Login,
			DeliveryID: deliveryID,
			ReceivedAt: receivedAt.UTC(),
		},
		IssueNumber: r.Issue.Number,
		Comment: CommentPayload{
			Body:   r.Comment.Body,
			Author: r.Comment.User.Login,
		},
	}, nil
}

func flattenRepo(r rawRepository) Repository {
	full := r.FullName
	if full == "" && r.Owner.Login != "" && r.Name != "" {
		full = r.Owner.Login + "/" + r.Name
	}
	return Repository{Owner: r.Owner.Login, Name: r.Name, FullName: full}
}
