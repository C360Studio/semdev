package intake

import (
	"context"
	"errors"
	"testing"
)

// fakeChecker scripts a permission level (and records whether it was called, to
// prove the allowlist path skips the network).
type fakeChecker struct {
	level string
	err   error
	calls int
}

func (f *fakeChecker) Permission(_ context.Context, _, _, _ string) (string, error) {
	f.calls++
	return f.level, f.err
}

func cfg() Config {
	return Config{Allowlist: []string{"trusted-bot"}, OptInLabel: "semdev", OptInCommand: "/semdev"}
}

// The happy path: a push-capable collaborator whose issue is labeled `semdev` is
// admitted.
func TestAdmitsAuthorizedOptedInCollaborator(t *testing.T) {
	ev := Event{Actor: "alice", Owner: "o", Repo: "r", AppliedLabels: []string{"bug", "semdev"}}
	chk := &fakeChecker{level: "write"}
	d, err := Decide(context.Background(), cfg(), ev, chk)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !d.Admitted || !d.Authorized || !d.OptedIn {
		t.Fatalf("expected admitted collaborator, got %+v", d)
	}
	if d.Actor != "alice" {
		t.Errorf("actor = %q, want alice", d.Actor)
	}
}

// G6 security core: an actor who is neither allowlisted nor a push-capable
// collaborator is rejected deterministically — regardless of opt-in — so no run
// is created and no token is spent.
func TestRejectsUnauthorizedActor(t *testing.T) {
	ev := Event{Actor: "rando", Owner: "o", Repo: "r", AppliedLabels: []string{"semdev"}, AuthoredText: "/semdev please"}
	chk := &fakeChecker{level: "none"} // not a collaborator
	d, err := Decide(context.Background(), cfg(), ev, chk)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Admitted {
		t.Fatalf("an unauthorized actor was admitted despite opt-in: %+v", d)
	}
	if d.Authorized {
		t.Error("permission 'none' must not count as authorized")
	}
}

// A read-only collaborator cannot spend budget (conservative default): only
// push-capable levels authorize.
func TestReadOnlyCollaboratorNotAuthorized(t *testing.T) {
	for _, level := range []string{"read", "triage", "none", ""} {
		ev := Event{Actor: "viewer", Owner: "o", Repo: "r", AppliedLabels: []string{"semdev"}}
		d, _ := Decide(context.Background(), cfg(), ev, &fakeChecker{level: level})
		if d.Admitted {
			t.Errorf("permission %q was admitted; only push-capable levels authorize", level)
		}
	}
	for _, level := range []string{"write", "maintain", "admin", "WRITE", " Admin "} {
		ev := Event{Actor: "dev", Owner: "o", Repo: "r", AppliedLabels: []string{"semdev"}}
		d, _ := Decide(context.Background(), cfg(), ev, &fakeChecker{level: level})
		if !d.Admitted {
			t.Errorf("permission %q should authorize a push-capable collaborator", level)
		}
	}
}

// Authorized but NOT opted in creates no run: authorization alone is insufficient.
func TestAuthorizedButNotOptedInIsNotAdmitted(t *testing.T) {
	ev := Event{Actor: "alice", Owner: "o", Repo: "r", AppliedLabels: []string{"bug"}, AuthoredText: "just a normal comment"}
	d, err := Decide(context.Background(), cfg(), ev, &fakeChecker{level: "admin"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Admitted {
		t.Fatalf("an un-opted-in item started a run: %+v", d)
	}
	if !d.Authorized || d.OptedIn {
		t.Errorf("expected authorized but not opted-in, got %+v", d)
	}
}

// The `/semdev` command opts in, but only as a whole token — "/semdevil" must not.
func TestOptInCommandRequiresWholeToken(t *testing.T) {
	base := Event{Actor: "alice", Owner: "o", Repo: "r"}
	chk := &fakeChecker{level: "write"}

	d, _ := Decide(context.Background(), cfg(), withText(base, "hey /semdev do it"), chk)
	if !d.Admitted {
		t.Error("a standalone /semdev command should opt in")
	}
	d, _ = Decide(context.Background(), cfg(), withText(base, "run /semdevil now"), chk)
	if d.Admitted {
		t.Error("/semdevil must not be read as the /semdev opt-in command")
	}
	d, _ = Decide(context.Background(), cfg(), withText(base, "/SemDev go"), chk)
	if !d.Admitted {
		t.Error("/SemDev should opt in (command match is case-insensitive, like the label)")
	}
}

// An allowlisted actor is admitted WITHOUT any permission call — the common path
// is zero-network as well as zero-token.
func TestAllowlistedActorSkipsPermissionCall(t *testing.T) {
	ev := Event{Actor: "Trusted-Bot", Owner: "o", Repo: "r", AppliedLabels: []string{"semdev"}} // case-insensitive
	chk := &fakeChecker{level: "none"}                                                          // would reject if consulted
	d, err := Decide(context.Background(), cfg(), ev, chk)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !d.Admitted {
		t.Fatalf("allowlisted actor was not admitted: %+v", d)
	}
	if chk.calls != 0 {
		t.Errorf("permission checker was called %d times for an allowlisted actor; want 0 (zero-network path)", chk.calls)
	}
}

// A permission-lookup failure fails CLOSED (not admitted) and surfaces the error
// so the caller retries — a transient host outage neither admits nor permanently
// rejects.
func TestPermissionErrorFailsClosedAndSurfaces(t *testing.T) {
	ev := Event{Actor: "alice", Owner: "o", Repo: "r", AppliedLabels: []string{"semdev"}}
	d, err := Decide(context.Background(), cfg(), ev, &fakeChecker{err: errors.New("api 502")})
	if err == nil {
		t.Fatal("expected the permission error to surface for retry")
	}
	if d.Admitted {
		t.Errorf("a permission-lookup failure must fail closed, got %+v", d)
	}
}

// An empty or whitespace-only actor is never admitted (no subject to authorize),
// and a blank actor must not even burn a permission call.
func TestEmptyActorRejected(t *testing.T) {
	for _, actor := range []string{"", "   "} {
		chk := &fakeChecker{level: "admin"}
		d, _ := Decide(context.Background(), cfg(), Event{Actor: actor, Owner: "o", Repo: "r", AppliedLabels: []string{"semdev"}}, chk)
		if d.Admitted {
			t.Errorf("an event with actor %q was admitted", actor)
		}
		if chk.calls != 0 {
			t.Errorf("a blank actor %q burned %d permission calls; want 0", actor, chk.calls)
		}
	}
}

func withText(ev Event, text string) Event {
	ev.AuthoredText = text
	ev.AppliedLabels = nil // isolate the command path
	return ev
}
