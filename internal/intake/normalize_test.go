package intake

import (
	"context"
	"encoding/json"
	"testing"

	githubwebhook "github.com/c360studio/semstreams/input/github-webhook"
)

func issuePayload(action, sender string, labels []string, body string, num int) []byte {
	e := githubwebhook.IssueEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issues",
			Action:     action,
			Sender:     sender,
			Repository: githubwebhook.Repository{Owner: "octo", Name: "repo", FullName: "octo/repo"},
		},
		Issue: githubwebhook.IssuePayload{Number: num, Title: "t", Body: body, Labels: labels, Author: sender},
	}
	b, _ := json.Marshal(e)
	return b
}

// An issue `opened` normalizes to a host-neutral Intake: the opener is the actor,
// the initial labels + body are their attributable opt-in signal, and IssueRef is
// the "owner/repo#number" reference.
func TestNormalizeIssueOpened(t *testing.T) {
	in, err := Normalize(SubjectIssue, issuePayload("opened", "alice", []string{"bug", "semdev"}, "please /semdev", 7))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !in.Relevant {
		t.Fatal("an issue opened should be intake-relevant")
	}
	if in.Event.Actor != "alice" || in.Event.Owner != "octo" || in.Event.Repo != "repo" {
		t.Errorf("actor/owner/repo = %+v", in.Event)
	}
	if in.IssueRef != "octo/repo#7" {
		t.Errorf("IssueRef = %q, want octo/repo#7", in.IssueRef)
	}
	if len(in.Event.AppliedLabels) != 2 || in.Event.AuthoredText != "please /semdev" {
		t.Errorf("opener's opt-in signal not captured: %+v", in.Event)
	}
}

// THE MUST-PIN (semstreams-reviewer HIGH): a `labeled` action must NOT surface the
// aggregate labels as the actor's opt-in signal. Even though the issue now bears
// `semdev` (added by someone earlier) and an AUTHORIZED actor triggered this
// `labeled` event, no opt-in is attributable to them → not admitted. This is the
// privilege-confusion hole; Normalize closes it by extracting the signal only for
// `opened`.
func TestNormalizeLabeledDoesNotInheritForeignOptIn(t *testing.T) {
	// An authorized actor triggers a `labeled` event on an issue already bearing
	// the semdev label (aggregate). No opt-in signal should be attributed to them.
	in, err := Normalize(SubjectIssue, issuePayload("labeled", "authorized-dev", []string{"semdev", "bug"}, "unrelated body", 7))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(in.Event.AppliedLabels) != 0 || in.Event.AuthoredText != "" {
		t.Fatalf("a labeled event surfaced an unattributable opt-in signal: %+v", in.Event)
	}
	// End to end: an authorized actor + that non-attributable event is NOT admitted.
	d, _ := Decide(context.Background(), cfg(), in.Event, &fakeChecker{level: "admin"})
	if d.Admitted {
		t.Fatalf("privilege confusion: an authorized actor's labeled event inherited a foreign opt-in and was admitted: %+v", d)
	}
}

// Defense in depth (semstreams-reviewer MEDIUM): on an `opened` event where the
// sender is NOT the issue author, the body/labels are not attributable to the
// actor, so no opt-in signal is surfaced — an authorized sender on someone else's
// authored content is not admitted.
func TestNormalizeOpenedSenderMustBeAuthor(t *testing.T) {
	e := githubwebhook.IssueEvent{
		WebhookEvent: githubwebhook.WebhookEvent{Action: "opened", Sender: "maintainer", Repository: githubwebhook.Repository{Owner: "octo", Name: "repo", FullName: "octo/repo"}},
		Issue:        githubwebhook.IssuePayload{Number: 3, Body: "/semdev", Labels: []string{"semdev"}, Author: "someone-else"},
	}
	b, _ := json.Marshal(e)
	in, _ := Normalize(SubjectIssue, b)
	if len(in.Event.AppliedLabels) != 0 || in.Event.AuthoredText != "" {
		t.Fatalf("sender!=author surfaced an unattributable opt-in signal: %+v", in.Event)
	}
	d, _ := Decide(context.Background(), cfg(), in.Event, &fakeChecker{level: "admin"})
	if d.Admitted {
		t.Errorf("an authorized sender on another author's opt-in content was admitted: %+v", d)
	}
}

// End to end on the supported path: an authorized opener whose issue carries the
// semdev label is admitted; an unauthorized opener with the same label is not.
func TestNormalizeThenDecideOpenedPath(t *testing.T) {
	payload := issuePayload("opened", "alice", []string{"semdev"}, "", 1)
	in, _ := Normalize(SubjectIssue, payload)

	admitted, _ := Decide(context.Background(), cfg(), in.Event, &fakeChecker{level: "write"})
	if !admitted.Admitted {
		t.Error("authorized opener with the semdev label should be admitted")
	}
	rejected, _ := Decide(context.Background(), cfg(), in.Event, &fakeChecker{level: "none"})
	if rejected.Admitted {
		t.Error("unauthorized opener with the semdev label must be rejected (zero-cost)")
	}
}

// Comment, PR, and review events are not M0 intake triggers (deferred pending the
// upstream payload fields), so they normalize to a skip.
func TestNormalizeNonIssueEventsAreSkipped(t *testing.T) {
	for _, subject := range []string{SubjectComment, SubjectPR, SubjectReview, "github.event.unknown"} {
		in, err := Normalize(subject, []byte(`{}`))
		if err != nil {
			t.Fatalf("normalize %s: %v", subject, err)
		}
		if in.Relevant {
			t.Errorf("%s should not be an M0 intake trigger", subject)
		}
	}
}

// Malformed issue JSON is a loud error, not a silent skip.
func TestNormalizeMalformedIssueErrors(t *testing.T) {
	if _, err := Normalize(SubjectIssue, []byte(`{not json`)); err == nil {
		t.Error("expected an error decoding malformed issue JSON")
	}
}
