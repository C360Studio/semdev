package verify

import "testing"

// passing is a fully-green clean-room evidence set, so a test can knock out one
// field and prove the corresponding classification in isolation.
func passing() Input {
	return Input{
		Completed:      true,
		FreshCacheHome: true,
		Resolved:       true,
		TestsPassed:    true,
	}
}

func checkByName(v Verdict, name string) (CheckResult, bool) {
	for _, c := range v.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return CheckResult{}, false
}

// The happy path: a clean-room proof that completed in fresh isolation, resolved
// cold, and passed its tests → Pass.
func TestDecidePasses(t *testing.T) {
	v := Decide(passing())
	if v.Outcome != OutcomePass {
		t.Fatalf("outcome = %q, want pass; failed: %+v", v.Outcome, v.FailedChecks())
	}
	if len(v.FailedChecks()) != 0 {
		t.Errorf("a passing verdict has no failed checks, got %+v", v.FailedChecks())
	}
}

// G4 core (cache-masked fabrication): a proof that completed in fresh isolation
// but did NOT resolve is a GENUINE failure — the fabricated/missing dependency the
// warm cache masked is exposed cold and terminally rejected.
func TestDecideRejectsCacheMaskedFabrication(t *testing.T) {
	in := passing()
	in.Resolved = false
	in.ResolveDetail = "cannot find module providing package example.com/ghost"
	v := Decide(in)
	if v.Outcome != OutcomeFail {
		t.Fatalf("outcome = %q, want fail (genuine unresolved dependency)", v.Outcome)
	}
	c, ok := checkByName(v, checkResolution)
	if !ok || c.Passed {
		t.Errorf("resolution check should have failed, got %+v", c)
	}
}

// Fail-closed retry (8.8): a harness that could not complete due to a
// transport/infrastructure error yields Retry — NOT a terminal Fail, NOT a Pass.
func TestDecideRetriesOnTransportError(t *testing.T) {
	in := passing()
	in.Completed = false
	in.TransportError = "sandbox unreachable: dial tcp timeout"
	v := Decide(in)
	if v.Outcome != OutcomeRetry {
		t.Fatalf("outcome = %q, want retry (a transport error is not a verdict)", v.Outcome)
	}
	if v.Outcome == OutcomeFail {
		t.Error("a transport error must never record a terminal fail")
	}
}

// A transport error takes precedence even when a downstream signal also looks
// bad — the incomplete proof is untrusted, so it is Retry, not Fail.
func TestDecideTransportErrorPrecedesGenuineFailure(t *testing.T) {
	in := Input{Completed: false, TransportError: "infra down", FreshCacheHome: false, Resolved: false, TestsPassed: false}
	if v := Decide(in); v.Outcome != OutcomeRetry {
		t.Fatalf("outcome = %q, want retry (incomplete proof is untrusted)", v.Outcome)
	}
}

// Defense in depth: an incoherent harness report (Completed=true but a transport
// error is set) is treated as a completion doubt → Retry, never a trusted verdict.
func TestDecideRetriesOnIncoherentTransportError(t *testing.T) {
	in := passing()
	in.TransportError = "connection reset mid-run"
	if v := Decide(in); v.Outcome != OutcomeRetry {
		t.Fatalf("outcome = %q, want retry (Completed with a transport error is incoherent)", v.Outcome)
	}
}

// G4 isolation assertion (8.7): a completed proof that did NOT establish a fresh
// cache home is an infra/harness fault — Retry (do not terminally reject a
// possibly-good artifact over a harness misconfiguration), never Pass.
func TestDecideRetriesWithoutFreshCacheHome(t *testing.T) {
	in := passing()
	in.FreshCacheHome = false
	v := Decide(in)
	if v.Outcome == OutcomePass {
		t.Fatal("a proof without a fresh cache home must not pass")
	}
	if v.Outcome != OutcomeRetry {
		t.Fatalf("outcome = %q, want retry (missing isolation is an infra fault)", v.Outcome)
	}
}

// A genuine test failure in fresh isolation is a terminal Fail.
func TestDecideFailsOnTestFailure(t *testing.T) {
	in := passing()
	in.TestsPassed = false
	in.TestsDetail = "FAIL: TestWidget"
	v := Decide(in)
	if v.Outcome != OutcomeFail {
		t.Fatalf("outcome = %q, want fail (genuine test failure)", v.Outcome)
	}
	if c, ok := checkByName(v, checkTests); !ok || c.Passed {
		t.Errorf("tests check should have failed, got %+v", c)
	}
}

// Every clean-room verdict emits all four ordered checks, so the operator sees the
// full picture regardless of outcome.
func TestDecideEmitsAllChecks(t *testing.T) {
	v := Decide(passing())
	for _, name := range []string{checkCompletion, checkIsolation, checkResolution, checkTests} {
		if _, ok := checkByName(v, name); !ok {
			t.Errorf("verdict missing the %q check", name)
		}
	}
}

// The build-file tripwire (SB3): ForbiddenVerdict is a definitive Fail whose SOLE check is
// the self-containment violation — the cold-run checks are not reported because they never
// ran (G7 honest evidence). detail is surfaced.
func TestForbiddenVerdict(t *testing.T) {
	v := ForbiddenVerdict("Dockerfile:2 references banned \"raw.githubusercontent.com\"")
	if v.Outcome != OutcomeFail {
		t.Fatalf("outcome = %q, want fail (a non-self-contained artifact)", v.Outcome)
	}
	if len(v.Checks) != 1 {
		t.Fatalf("want exactly 1 check (only the tripwire was evaluated), got %d: %+v", len(v.Checks), v.Checks)
	}
	c := v.Checks[0]
	if c.Name != checkSelfContained || c.Passed {
		t.Errorf("check = %+v, want the failed self-contained check", c)
	}
	if c.Detail == "" {
		t.Error("ForbiddenVerdict must carry the offending-file detail (G7)")
	}
}

// FailedChecks lists exactly the non-passing checks, in stable name order.
func TestFailedChecks(t *testing.T) {
	in := passing()
	in.Resolved = false
	in.TestsPassed = false
	failed := Decide(in).FailedChecks()
	if len(failed) != 2 {
		t.Fatalf("want 2 failed checks, got %d: %+v", len(failed), failed)
	}
	// sorted by name: "resolution" < "tests"
	if failed[0].Name != checkResolution || failed[1].Name != checkTests {
		t.Errorf("failed checks not in stable order: %+v", failed)
	}
}
