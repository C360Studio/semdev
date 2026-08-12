package floors

import (
	"strings"
	"testing"
)

// findFinding returns the finding for a named floor from a CheckAll result.
func findFinding(t *testing.T, findings []Finding, floor string) Finding {
	t.Helper()
	for _, f := range findings {
		if f.Floor == floor {
			return f
		}
	}
	t.Fatalf("no finding for floor %q", floor)
	return Finding{}
}

// A real, complete attempt passes every floor — the baseline that proves the
// floors do not false-positive on honest work (a go-health-class-shaped fixture).
func TestCleanAttemptPassesEveryFloor(t *testing.T) {
	a := Attempt{
		TargetFiles: []string{"health.go"},
		Files: []File{
			{Path: "health.go", Content: `package health

// Status reports service liveness.
func Status() string { return "ok" }
`},
			{Path: "health_test.go", Content: `package health

import "testing"

func TestStatus(t *testing.T) {
	if Status() != "ok" {
		t.Fatalf("Status() = %q, want ok", Status())
	}
}
`},
		},
	}
	for _, f := range CheckAll(a) {
		if !f.Passed {
			t.Errorf("floor %s rejected a clean attempt: %s", f.Floor, f.Detail)
		}
	}
	if AnyRejected(CheckAll(a)) {
		t.Error("AnyRejected true on a clean attempt")
	}
}

// The presence floor (H1) closes the g4 hole: an attempt that authored NONE of its
// declared targets must REJECT, not pass vacuously through the other five floors.
func TestPresenceFloor(t *testing.T) {
	// Declared targets, NONE authored on disk → reject (the SB5 no-work theater).
	allAbsent := Attempt{TargetFiles: []string{"health.go", "util.go"}}
	if f := PresenceOfWork(allAbsent); f.Passed {
		t.Error("presence must reject an attempt that authored none of its declared targets")
	}
	// It also flows through the aggregate: an all-absent attempt is AnyRejected true,
	// where every OTHER floor passes vacuously (no source to reject).
	if !AnyRejected(CheckAll(allAbsent)) {
		t.Error("an all-absent attempt must be rejected by CheckAll (presence floor) — else it reads green (g4 hole)")
	}

	// At least one target authored → pass (work is present to evaluate).
	oneAuthored := Attempt{
		TargetFiles: []string{"health.go", "util.go"},
		Files:       []File{{Path: "health.go", Content: "package health\n"}},
	}
	if f := PresenceOfWork(oneAuthored); !f.Passed {
		t.Errorf("presence must PASS a partial attempt (a legit fix may touch 1 of N targets): %s", f.Detail)
	}

	// No declared targets at all → not applicable, pass (never a false reject).
	if f := PresenceOfWork(Attempt{}); !f.Passed {
		t.Errorf("presence must not reject an attempt with no declared targets: %s", f.Detail)
	}
}

func TestTestsMustExist(t *testing.T) {
	// production source, no test → reject
	noTest := Attempt{
		TargetFiles: []string{"health.go"},
		Files:       []File{{Path: "health.go", Content: "package health\nfunc Status() string { return \"ok\" }\n"}},
	}
	if f := findFinding(t, CheckAll(noTest), FloorTestsMustExist); f.Passed {
		t.Error("expected tests-must-exist to reject source with no test")
	}

	// only touched non-Go files → floor does not apply (pass)
	docsOnly := Attempt{Files: []File{{Path: "README.md", Content: "# docs"}}}
	if f := TestsMustExist(docsOnly); !f.Passed {
		t.Errorf("non-Go change must not trip tests-must-exist: %s", f.Detail)
	}

	// production source with a test → pass
	withTest := Attempt{
		Files: []File{
			{Path: "health.go", Content: "package health\nfunc Status() string { return \"ok\" }\n"},
			{Path: "health_test.go", Content: "package health\nimport \"testing\"\nfunc TestStatus(t *testing.T){ if Status()!=\"ok\"{t.Fatal(\"bad\")} }\n"},
		},
	}
	if f := TestsMustExist(withTest); !f.Passed {
		t.Errorf("source with a test must pass tests-must-exist: %s", f.Detail)
	}
}

func TestVacuousTestFloor(t *testing.T) {
	// a test that only logs → vacuous, reject
	vacuous := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestNothing(t *testing.T) {
	t.Log("ran")
}
`}}}
	f := VacuousTest(vacuous)
	if f.Passed {
		t.Error("a log-only test must be rejected as vacuous")
	}
	if !strings.Contains(f.Detail, "TestNothing") {
		t.Errorf("detail should name the offending test: %s", f.Detail)
	}

	// a Skipped test is honest deferral → pass
	skipped := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestDeferred(t *testing.T) {
	t.Skip("needs hardware")
}
`}}}
	if f := VacuousTest(skipped); !f.Passed {
		t.Errorf("a Skipped test is not vacuous: %s", f.Detail)
	}

	// a testify assertion counts as a real assertion → pass
	testify := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import (
	"testing"
	"github.com/stretchr/testify/require"
)
func TestReq(t *testing.T) {
	require.Equal(t, "ok", compute())
}
`}}}
	if f := VacuousTest(testify); !f.Passed {
		t.Errorf("a testify require must count as a real assertion: %s", f.Detail)
	}

	// a non-conventional receiver name is still detected
	renamed := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestRenamed(tt *testing.T) {
	if compute() != "ok" {
		tt.Fatalf("bad")
	}
}
`}}}
	if f := VacuousTest(renamed); !f.Passed {
		t.Errorf("a renamed receiver's Fatalf must count: %s", f.Detail)
	}
}

// Regression pins for the fail-open / false-reject vectors the semstreams review
// reproduced against the text-scanning draft. All are fixed by working on the AST.

// A vacuous test with a lone brace inside a string literal must NOT escape — the
// brace-counting draft returned an empty body and silently skipped it (fail-open).
func TestVacuousTestBraceInStringDoesNotEscape(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestVacuousEscape(t *testing.T) {
	s := "{"
	_ = s
}
`}}}
	if f := VacuousTest(a); f.Passed {
		t.Error("a vacuous test with a brace-bearing string must still be flagged")
	}
}

// An honest test whose message string contains a closing brace must NOT be
// false-rejected — the brace-counting draft truncated the body at the string's }.
func TestVacuousTestBraceStringHonestTestPasses(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestHonest(t *testing.T) {
	msg := "unexpected } token"
	if compute() != "ok" {
		t.Fatal(msg)
	}
}
`}}}
	if f := VacuousTest(a); !f.Passed {
		t.Errorf("a brace-string honest test must pass: %s", f.Detail)
	}
}

// A commented-out assertion does not count — the body is genuinely vacuous.
func TestVacuousTestCommentedAssertionCounts(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestCommented(t *testing.T) {
	// t.Fatal("disabled")
	_ = compute()
}
`}}}
	if f := VacuousTest(a); f.Passed {
		t.Error("a test whose only 'assertion' is commented out must be flagged vacuous")
	}
}

// t.Failed() is a state read, not a failing call — a body whose only t.* call is
// Failed() is vacuous. The draft's substring match let .Fail match .Failed.
func TestVacuousTestFailedIsNotAnAssertion(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestFailedRead(t *testing.T) {
	_ = t.Failed()
}
`}}}
	if f := VacuousTest(a); f.Passed {
		t.Error("_ = t.Failed() is not an assertion; the test must be flagged vacuous")
	}
}

// A helper-delegated assertion (mustEqual(t, ...)) is a real assertion — passing a
// testing value to a helper counts, so honest helper-based tests are not parked.
func TestVacuousTestHelperDelegationPasses(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func mustEqual(t *testing.T, got, want string) {
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
func TestViaHelper(t *testing.T) {
	mustEqual(t, compute(), "ok")
}
`}}}
	if f := VacuousTest(a); !f.Passed {
		t.Errorf("a helper-delegated assertion must count as real: %s", f.Detail)
	}
}

// A constant tautology (if "ok" != "ok" { t.Fatal() }) has a syntactic fail-call
// but asserts nothing about computed behavior — it must be flagged vacuous
// (Codex P2: a bare fail-call must not satisfy the floor).
func TestVacuousTestConstantTautologyIsVacuous(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestLooksReal(t *testing.T) {
	if "ok" != "ok" {
		t.Fatal("bad")
	}
}
`}}}
	if f := VacuousTest(a); f.Passed {
		t.Error("a constant-tautology assertion must be flagged vacuous")
	}
}

// require.Equal with two constant arguments is a tautology; with a computed
// argument it is a real assertion.
func TestVacuousTestAssertionLibNeedsComputedArg(t *testing.T) {
	tautology := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import (
	"testing"
	"github.com/stretchr/testify/require"
)
func TestConstReq(t *testing.T) {
	require.Equal(t, "ok", "ok")
}
`}}}
	if f := VacuousTest(tautology); f.Passed {
		t.Error("require.Equal(t, \"ok\", \"ok\") is a tautology and must be flagged vacuous")
	}

	computed := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import (
	"testing"
	"github.com/stretchr/testify/require"
)
func TestComputedReq(t *testing.T) {
	require.Equal(t, "ok", compute())
}
`}}}
	if f := VacuousTest(computed); !f.Passed {
		t.Errorf("require.Equal on a computed value must count: %s", f.Detail)
	}
}

// A guard over a COMPUTED value is a real assertion even though it is an if/Fatal
// shape — only all-constant conditions are tautologies.
func TestVacuousTestComputedGuardPasses(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestComputed(t *testing.T) {
	got := compute()
	if got != "ok" {
		t.Fatalf("got %q", got)
	}
}
`}}}
	if f := VacuousTest(a); !f.Passed {
		t.Errorf("a guard over a computed value must count as a real assertion: %s", f.Detail)
	}
}

// Passing t to a NON-asserting helper is not a real assertion — the setup-plus-
// discarded-result shape must still be flagged (re-review BLOCKING: helper
// delegation must credit only a helper that itself asserts).
func TestVacuousTestNonAssertingHelperIsVacuous(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func setupServer(t *testing.T) {
	_ = t
}
func TestSetupOnly(t *testing.T) {
	setupServer(t)
	resp := handle("GET")
	_ = resp
}
`}}}
	if f := VacuousTest(a); f.Passed {
		t.Error("a test that passes t only to a non-asserting setup helper must be flagged vacuous")
	}
}

// t.Log(t) hands t to a non-asserting sink — not an assertion.
func TestVacuousTestLogWithTArgIsVacuous(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestLogT(t *testing.T) {
	t.Log(t)
}
`}}}
	if f := VacuousTest(a); f.Passed {
		t.Error("t.Log(t) is not an assertion; the test must be flagged vacuous")
	}
}

// A helper that delegates to a further asserting helper is credited transitively.
func TestVacuousTestTransitiveHelperPasses(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func assertOK(t *testing.T, got string) {
	if got != "ok" {
		t.Fatalf("got %q", got)
	}
}
func check(t *testing.T) {
	assertOK(t, compute())
}
func TestTransitive(t *testing.T) {
	check(t)
}
`}}}
	if f := VacuousTest(a); !f.Passed {
		t.Errorf("a transitively-asserting helper chain must count: %s", f.Detail)
	}
}

// A table-driven test whose subtest closure renames the receiver still counts —
// the assertion is sub.Fatalf, not t.Fatalf.
func TestVacuousTestRenamedSubtestReceiverPasses(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: `package x
import "testing"
func TestTable(t *testing.T) {
	for _, name := range []string{"a", "b"} {
		t.Run(name, func(sub *testing.T) {
			if compute() != "ok" {
				sub.Fatalf("bad for %s", name)
			}
		})
	}
}
`}}}
	if f := VacuousTest(a); !f.Passed {
		t.Errorf("a renamed subtest receiver's Fatalf must count: %s", f.Detail)
	}
}

// AntiMock must catch an unexported hand-written double (fakeStore) — the
// case-sensitive draft regex missed it (fail-open).
func TestAntiMockCatchesUnexportedDouble(t *testing.T) {
	a := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			{Path: "svc.go", Content: "package svc\nfunc FetchUser(id string) string { return id }\n"},
			{Path: "svc_test.go", Content: `package svc
import "testing"
type fakeStore struct{}
func (fakeStore) get() string { return "x" }
func TestGet(t *testing.T) {
	f := fakeStore{}
	if f.get() != "x" {
		t.Fatal("bad")
	}
}
`},
		},
	}
	if f := AntiMock(a); f.Passed {
		t.Error("an unexported mock-only test (fakeStore) must be rejected")
	}
}

// AntiMock must apply to UNEXPORTED target code — a task changing an internal
// helper (parseThing) whose test only exercises a fake must be rejected, not
// waved through with "no exported target symbols" (Codex P2).
func TestAntiMockAppliesToUnexportedTarget(t *testing.T) {
	a := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			{Path: "svc.go", Content: "package svc\nfunc parseThing(s string) string { return s }\n"},
			{Path: "svc_test.go", Content: `package svc
import "testing"
type fakeParser struct{}
func (fakeParser) parse() string { return "x" }
func TestParse(t *testing.T) {
	p := fakeParser{}
	if p.parse() != "x" {
		t.Fatal("bad")
	}
}
`},
		},
	}
	if f := AntiMock(a); f.Passed {
		t.Error("a mock-only test of an unexported target (parseThing) must be rejected")
	}
}

// A same-package test that calls the unexported target passes AntiMock even with a
// mock present.
func TestAntiMockUnexportedTargetReferencedPasses(t *testing.T) {
	a := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			{Path: "svc.go", Content: "package svc\nfunc parseThing(s string) string { return s }\n"},
			{Path: "svc_test.go", Content: `package svc
import "testing"
type fakeDep struct{}
func TestParse(t *testing.T) {
	if parseThing("a") != "a" {
		t.Fatal("bad")
	}
}
`},
		},
	}
	if f := AntiMock(a); !f.Passed {
		t.Errorf("a test that calls the unexported target must pass: %s", f.Detail)
	}
}

// AntiMock must NOT be fooled by a target symbol that appears only in a subtest
// label string or a doc comment — those are not code references.
func TestAntiMockStringOrCommentReferenceDoesNotCount(t *testing.T) {
	a := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			{Path: "svc.go", Content: "package svc\nfunc FetchUser(id string) string { return id }\n"},
			{Path: "svc_test.go", Content: `package svc
import "testing"
// TestGet exercises FetchUser end to end.
type MockStore struct{}
func TestGet(t *testing.T) {
	m := MockStore{}
	t.Run("FetchUser", func(t *testing.T) {
		if (MockStore{}) != m {
			t.Fatal("bad")
		}
	})
}
`},
		},
	}
	if f := AntiMock(a); f.Passed {
		t.Error("a target named only in a string/comment must not satisfy the reference check")
	}
}

func TestStubFloor(t *testing.T) {
	cases := []struct {
		name    string
		content string
		reject  bool
	}{
		{"panic-not-implemented", "package x\nfunc F() { panic(\"not implemented\") }\n", true},
		{"panic-todo", "package x\nfunc F() { panic(\"TODO: wire this\") }\n", true},
		{"panic-unimplemented", "package x\nfunc F() { panic(\"unimplemented\") }\n", true},
		{"sentinel-error", "package x\nimport \"errors\"\nfunc F() error { return errors.New(\"not implemented\") }\n", true},
		{"errorf-sentinel", "package x\nimport \"fmt\"\nfunc F() error { return fmt.Errorf(\"not yet implemented: %s\", \"x\") }\n", true},
		{"wrapped-panic", "package x\nimport \"fmt\"\nfunc F() { panic(fmt.Sprintf(\"not implemented: %d\", 1)) }\n", true},
		{"plain-panic-not-a-stub", "package x\nfunc F() { panic(\"boom\") }\n", false},
		{"clean", "package x\nfunc F() string { return \"ok\" }\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := StubArtifact(Attempt{Files: []File{{Path: "x.go", Content: c.content}}})
			if c.reject && f.Passed {
				t.Errorf("expected stub floor to reject %s", c.name)
			}
			if !c.reject && !f.Passed {
				t.Errorf("stub floor false-positive on %s: %s", c.name, f.Detail)
			}
		})
	}
}

// A domain error string that merely contains "todo" as a substring of a larger
// word ("todos") is not a stub marker — word-boundary matching prevents the
// false-reject (re-review LOW).
func TestStubFloorWordBoundaryTODO(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x.go", Content: "package x\nimport \"errors\"\nfunc F() error { return errors.New(\"todos remaining in queue\") }\n"}}}
	if f := StubArtifact(a); !f.Passed {
		t.Errorf("'todos remaining' is a domain string, not a stub marker: %s", f.Detail)
	}
}

// A future-work TODO comment on complete, working code is NOT a shipped stub. The
// floor keys on explicit code markers (panic / sentinel error), so a bare TODO —
// which is invisible to an AST walk — never parks honest work (reviewer MEDIUM 6).
func TestStubFloorIgnoresFutureWorkTODO(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x.go", Content: "package x\n// TODO(perf): cache this later\nfunc F() string { return \"ok\" }\n"}}}
	if f := StubArtifact(a); !f.Passed {
		t.Errorf("a future-work TODO on complete code must not trip the stub floor: %s", f.Detail)
	}
}

// A TODO in a test file is not a shipped stub — the floor scopes to production
// source and, moreover, no longer reads comments at all.
func TestStubFloorExemptsTestFiles(t *testing.T) {
	a := Attempt{Files: []File{{Path: "x_test.go", Content: "package x\nfunc TestF(t *testing.T){ if F()!=\"ok\"{t.Fatal(\"x\")} }\n"}}}
	if f := StubArtifact(a); !f.Passed {
		t.Errorf("a test file must not trip the stub floor: %s", f.Detail)
	}
}

func TestSourceBuildFloor(t *testing.T) {
	// unparseable source → reject
	broken := Attempt{Files: []File{{Path: "x.go", Content: "package x\nfunc F( { return }\n"}}}
	if f := SourceBuild(broken); f.Passed {
		t.Error("unparseable Go source must be rejected by source-build")
	}

	// valid source → pass
	ok := Attempt{Files: []File{{Path: "x.go", Content: "package x\nfunc F() string { return \"ok\" }\n"}}}
	if f := SourceBuild(ok); !f.Passed {
		t.Errorf("valid Go source must pass source-build: %s", f.Detail)
	}
}

func TestAntiMockFloor(t *testing.T) {
	target := File{Path: "svc.go", Content: `package svc

type Store struct{}

func FetchUser(id string) string { return id }
`}

	// declares a mock, references NO target symbol → reject
	onlyMock := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			target,
			{Path: "svc_test.go", Content: `package svc
import "testing"
type MockStore struct{}
func (MockStore) get() string { return "x" }
func TestGet(t *testing.T) {
	m := MockStore{}
	if m.get() != "x" {
		t.Fatal("bad")
	}
}
`},
		},
	}
	if f := AntiMock(onlyMock); f.Passed {
		t.Errorf("a test that touches only its mock must be rejected: %s", f.Detail)
	}

	// declares a mock BUT also exercises the target → pass
	mockPlusTarget := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			target,
			{Path: "svc_test.go", Content: `package svc
import "testing"
type MockStore struct{}
func TestFetch(t *testing.T) {
	if FetchUser("u1") != "u1" {
		t.Fatal("bad")
	}
}
`},
		},
	}
	if f := AntiMock(mockPlusTarget); !f.Passed {
		t.Errorf("a test that mocks a collaborator but exercises the target must pass: %s", f.Detail)
	}

	// no mock declared → floor passes regardless
	noMock := Attempt{
		TargetFiles: []string{"svc.go"},
		Files: []File{
			target,
			{Path: "svc_test.go", Content: "package svc\nimport \"testing\"\nfunc TestX(t *testing.T){ if FetchUser(\"a\")!=\"a\"{t.Fatal(\"x\")} }\n"},
		},
	}
	if f := AntiMock(noMock); !f.Passed {
		t.Errorf("no declared mock must pass anti-mock: %s", f.Detail)
	}
}

func TestAnyRejected(t *testing.T) {
	if AnyRejected([]Finding{pass("a", ""), pass("b", "")}) {
		t.Error("AnyRejected true when all pass")
	}
	if !AnyRejected([]Finding{pass("a", ""), reject("b", "x")}) {
		t.Error("AnyRejected false when one rejects")
	}
}

// The clean-tree floor (group 2, task 2.5) rejects a working tree that diverged from the
// committed attempt (non-empty DirtyPaths) — floors read the working tree, cold verify
// clones the commit, so a dirty tree means they prove different bytes (G7 tampering). A
// clean tree passes.
func TestCleanTreeFloor(t *testing.T) {
	if f := CleanTree(Attempt{}); !f.Passed {
		t.Errorf("clean tree (no dirty paths) must pass: %s", f.Detail)
	}
	dirty := Attempt{DirtyPaths: []string{" M health.go", "?? residue.txt"}}
	if f := CleanTree(dirty); f.Passed {
		t.Error("a dirty working tree must REJECT — floors and cold verify would prove different bytes")
	} else if !strings.Contains(f.Detail, "health.go") {
		t.Errorf("clean-tree rejection should name a diverged path, got: %s", f.Detail)
	}
}
