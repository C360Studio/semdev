package intake

import (
	"context"
	"errors"
	"testing"
)

// G6 (5.4) — the zero-cost rejection, end to end: an unauthorized actor's admitted
// issue-opened event produces NO admission (Admitted false), so the adapter stamps
// no intake.admitted and spawns no coordinator — no run is created and no token is
// spent. The permission checker is consulted (one non-LLM API call), never an LLM.
func TestAssessUnauthorizedCreatesNoRun(t *testing.T) {
	payload := issuePayload("opened", "rando", []string{"semdev"}, "/semdev go", 9)
	chk := &fakeChecker{level: "none"} // not a collaborator

	out, err := Assess(context.Background(), cfg(), SubjectIssue, payload, chk)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if out.Admitted {
		t.Fatal("an unauthorized opener was admitted — a run would be created for a stranger")
	}
	if chk.calls != 1 {
		t.Errorf("permission checked %d times; want exactly 1 (deterministic, zero-LLM)", chk.calls)
	}
}

// The happy path: an authorized, opted-in opener is admitted, so the adapter will
// stamp and spawn.
func TestAssessAuthorizedAdmits(t *testing.T) {
	payload := issuePayload("opened", "alice", []string{"semdev"}, "", 9)
	out, err := Assess(context.Background(), cfg(), SubjectIssue, payload, &fakeChecker{level: "write"})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !out.Admitted {
		t.Fatalf("authorized opener not admitted: %+v", out.Decision)
	}
	if out.Intake.IssueRef != "octo/repo#9" {
		t.Errorf("IssueRef = %q", out.Intake.IssueRef)
	}
}

// A non-intake event (comment/PR/review at M0) is never admitted — no decision, no
// effect, and the permission checker is never consulted.
func TestAssessNonIntakeEventDrops(t *testing.T) {
	chk := &fakeChecker{level: "admin"}
	out, err := Assess(context.Background(), cfg(), SubjectComment, []byte(`{}`), chk)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if out.Admitted {
		t.Error("a non-intake event was admitted")
	}
	if chk.calls != 0 {
		t.Errorf("a non-intake event consulted the permission checker %d times; want 0", chk.calls)
	}
}

// A permission-lookup failure surfaces as a fail-closed, retryable error: not
// admitted, and the caller must redeliver rather than treat it as a rejection.
func TestAssessPermissionErrorIsRetryable(t *testing.T) {
	payload := issuePayload("opened", "alice", []string{"semdev"}, "", 9)
	out, err := Assess(context.Background(), cfg(), SubjectIssue, payload, &fakeChecker{err: errors.New("502")})
	if err == nil {
		t.Fatal("expected a retryable error to surface")
	}
	if out.Admitted {
		t.Error("a fail-closed lookup error must not admit")
	}
}

// Malformed payload is a loud error, not a silent admit/drop.
func TestAssessMalformedErrors(t *testing.T) {
	if _, err := Assess(context.Background(), cfg(), SubjectIssue, []byte(`{bad`), &fakeChecker{}); err == nil {
		t.Error("expected an error on malformed payload")
	}
}
