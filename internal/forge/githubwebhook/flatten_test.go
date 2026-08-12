package githubwebhook

import (
	"testing"
	"time"
)

// rawIssuesPayload is a trimmed REAL-shape GitHub "issues" webhook payload
// (nested user/labels objects, the fields the flattener maps plus noise it
// must ignore).
const rawIssuesPayload = `{
  "action": "opened",
  "issue": {
    "number": 7,
    "title": "Health boundary misclassifies at the warning threshold",
    "body": "/semdev Classify(0.80, 0.10) returns ok but the spec says warning.",
    "state": "open",
    "labels": [{"name": "semdev", "color": "00ff00"}, {"name": "bug"}],
    "user": {"login": "cglusky", "id": 43158},
    "html_url": "https://github.com/c360studio/semdev-fixture/issues/7",
    "assignees": []
  },
  "repository": {
    "name": "semdev-fixture",
    "full_name": "c360studio/semdev-fixture",
    "owner": {"login": "c360studio", "id": 1},
    "private": true
  },
  "sender": {"login": "cglusky", "id": 43158}
}`

// rawCommentPayload is a trimmed REAL-shape "issue_comment" payload. The
// nested issue.number is the field the retired framework flattener dropped.
const rawCommentPayload = `{
  "action": "created",
  "issue": {
    "number": 7,
    "title": "Health boundary misclassifies at the warning threshold",
    "state": "open",
    "user": {"login": "cglusky"}
  },
  "comment": {
    "body": "/semdev approve",
    "user": {"login": "cglusky"},
    "id": 99001
  },
  "repository": {
    "name": "semdev-fixture",
    "full_name": "c360studio/semdev-fixture",
    "owner": {"login": "c360studio"}
  },
  "sender": {"login": "cglusky"}
}`

func TestFlattenIssueEventMapsTheRealShape(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ev, err := FlattenIssueEvent([]byte(rawIssuesPayload), "delivery-guid-1", now)
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if ev.EventType != "issues" || ev.Action != "opened" {
		t.Errorf("header = %s/%s, want issues/opened", ev.EventType, ev.Action)
	}
	if ev.Sender != "cglusky" || ev.Issue.Author != "cglusky" {
		t.Errorf("sender/author = %q/%q, want cglusky/cglusky (nested user.login flattened)", ev.Sender, ev.Issue.Author)
	}
	if ev.Repository.FullName != "c360studio/semdev-fixture" || ev.Repository.Owner != "c360studio" {
		t.Errorf("repo = %+v, want c360studio/semdev-fixture", ev.Repository)
	}
	if ev.Issue.Number != 7 || ev.Issue.Body == "" {
		t.Errorf("issue = number %d body %q, want 7 + the authored body", ev.Issue.Number, ev.Issue.Body)
	}
	if len(ev.Issue.Labels) != 2 || ev.Issue.Labels[0] != "semdev" {
		t.Errorf("labels = %v, want the label NAMES [semdev bug]", ev.Issue.Labels)
	}
	if ev.DeliveryID != "delivery-guid-1" {
		t.Errorf("DeliveryID = %q, want the X-GitHub-Delivery GUID (the idempotency key)", ev.DeliveryID)
	}
	if !ev.ReceivedAt.Equal(now) {
		t.Errorf("ReceivedAt = %v, want the receiver clock %v", ev.ReceivedAt, now)
	}
}

// TestFlattenCommentEventCarriesTheIssueNumber pins the CLOSED gap: the
// framework's retired flattener dropped the comment's parent issue number
// (semdev backlog #2), which deferred every comment flow. semdev's flattener
// must carry it — the approval lane binds a command to its issue through it.
func TestFlattenCommentEventCarriesTheIssueNumber(t *testing.T) {
	ev, err := FlattenCommentEvent([]byte(rawCommentPayload), "delivery-guid-2", time.Now())
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if ev.IssueNumber != 7 {
		t.Fatalf("IssueNumber = %d, want 7 — the parent-issue binding is the whole point of owning the flattener", ev.IssueNumber)
	}
	if ev.Comment.Body != "/semdev approve" || ev.Comment.Author != "cglusky" {
		t.Errorf("comment = %+v, want the body + author (the attribution pair)", ev.Comment)
	}
	if ev.Sender != "cglusky" {
		t.Errorf("Sender = %q, want cglusky (the actor the Event invariant binds to)", ev.Sender)
	}
	if ev.EventType != "issue_comment" || ev.Action != "created" {
		t.Errorf("header = %s/%s, want issue_comment/created", ev.EventType, ev.Action)
	}
}

func TestFlattenRejectsMalformedPayloads(t *testing.T) {
	if _, err := FlattenIssueEvent([]byte(`{not json`), "", time.Now()); err == nil {
		t.Error("malformed issues payload must error (the receiver logs + drops, never publishes garbage)")
	}
	if _, err := FlattenCommentEvent([]byte(`{not json`), "", time.Now()); err == nil {
		t.Error("malformed comment payload must error")
	}
}
