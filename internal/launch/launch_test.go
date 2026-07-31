package launch

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semdev/internal/forge/github"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

const testRef = "acme/widget#7"

type fakeIssues struct {
	issue github.Issue
	err   error
}

// Real six-position run entity ids: the projection client validates the entity
// against experiment-intake's contract pattern, so a bare "run-new" is no longer a
// usable stand-in (migrate-beta159 D2a) — and a fixture that could never occur in
// production was hiding that (G8).
const (
	runNew = "c360.semdev.agent.chain.execution.run-new"
	runOld = "c360.semdev.agent.chain.execution.run-old"
)

func (f fakeIssues) GetIssue(context.Context, string, string, int) (github.Issue, error) {
	return f.issue, f.err
}

type fakePub struct {
	published [][]byte
	subjects  []string
	err       error
}

func (f *fakePub) PublishToStream(_ context.Context, subject string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	f.subjects = append(f.subjects, subject)
	f.published = append(f.published, data)
	return nil
}

// fakeResolver returns a scripted run-id set per call (the last entry repeats), so a test can
// simulate the coordinator minting a NEW run between the pre-publish snapshot and the bind.
type fakeResolver struct {
	responses [][]string
	calls     int
	err       error
}

func (f *fakeResolver) ResolveRunIDsByRef(context.Context, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls
	f.calls++
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	return f.responses[i], nil
}

type fakeWriter struct {
	calls   int
	triples []message.Triple
}

func (f *fakeWriter) ReplaceOwned(_ context.Context, m projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	f.calls++
	f.triples = append(f.triples, m.Desired...)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam Deps.Writer takes, so the launch
// suite exercises graphown.ContractFor for real (design D3b).
func writerFor(w *fakeWriter) *graphown.Writer {
	return graphown.NewWriter(experiment.Source, w)
}

func fast(p Params) Params {
	p.IssueRef = testRef
	p.Model = "mock"
	p.BindTimeout = 500 * time.Millisecond
	p.BindPoll = 5 * time.Millisecond
	return p
}

// A baseline launch reads the issue, publishes a content-bearing wake, binds the run minted
// after publish, and stamps NOTHING (baseline default).
func TestLaunchBaselineMintsAndBinds(t *testing.T) {
	pub := &fakePub{}
	writer := &fakeWriter{}
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Title: "T", Body: "the ask body"}},
		Pub:      pub,
		Resolver: &fakeResolver{responses: [][]string{{}, {runNew}}}, // snapshot empty, then minted
		Writer:   writerFor(writer),
	}
	id, err := Launch(context.Background(), d, fast(Params{}))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if id != runNew {
		t.Errorf("bound id = %q, want run-new", id)
	}
	if len(pub.subjects) != 1 {
		t.Fatalf("want exactly one publish, got %d", len(pub.subjects))
	}
	if writer.calls != 0 {
		t.Errorf("baseline must stamp no condition, got %d writes", writer.calls)
	}
}

// The published wake carries the issue BODY (not the title) — byte-parity with the webhook's
// body-only normalize (review H1/parity).
func TestLaunchWakeCarriesBodyOnly(t *testing.T) {
	pub := &fakePub{}
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Title: "TITLE-SENTINEL", Body: "BODY-SENTINEL"}},
		Pub:      pub,
		Resolver: &fakeResolver{responses: [][]string{{}, {runNew}}},
		Writer:   writerFor(&fakeWriter{}),
	}
	if _, err := Launch(context.Background(), d, fast(Params{})); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	wake := pub.published[0]
	if !bytes.Contains(wake, []byte("BODY-SENTINEL")) {
		t.Error("wake does not carry the issue body")
	}
	if bytes.Contains(wake, []byte("TITLE-SENTINEL")) {
		t.Error("wake carries the issue title — must be body-only to match the webhook normalize")
	}
}

// An unreadable issue fails the launch BEFORE any publish — never a run against an empty ask.
func TestLaunchContentlessIssueFailsBeforePublish(t *testing.T) {
	pub := &fakePub{}
	writer := &fakeWriter{}
	d := Deps{
		Issues:   fakeIssues{err: errors.New("404 not found")},
		Pub:      pub,
		Resolver: &fakeResolver{responses: [][]string{{}}},
		Writer:   writerFor(writer),
	}
	if _, err := Launch(context.Background(), d, fast(Params{})); err == nil {
		t.Error("an unreadable issue must fail the launch")
	}
	if len(pub.subjects) != 0 {
		t.Error("nothing must be published when the issue cannot be read")
	}
	if writer.calls != 0 {
		t.Error("no condition stamped on a failed launch")
	}
}

// The bind is set-difference: a pre-existing run sharing the ref (from a prior webhook wake) is
// NOT bound — only the run minted after this launch's publish (review M2). A pre-existing run
// requires --force (Force), which is what this exercises.
func TestLaunchBindsTheRunItMintedNotAPreExisting(t *testing.T) {
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Body: "ask"}},
		Pub:      &fakePub{},
		Resolver: &fakeResolver{responses: [][]string{{runOld}, {runOld, runNew}}},
		Writer:   writerFor(&fakeWriter{}),
	}
	id, err := Launch(context.Background(), d, fast(Params{Force: true}))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if id != runNew {
		t.Errorf("bound id = %q, want run-new (the pre-existing run-old must be excluded)", id)
	}
}

// A launch against a ref that ALREADY has a run refuses to mint a duplicate (the webhook's
// run-existence guard, on the operator door — review HIGH): fail closed, publish nothing, stamp
// nothing. --force overrides.
func TestLaunchRefusesDuplicateWhenRunExists(t *testing.T) {
	pub := &fakePub{}
	writer := &fakeWriter{}
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Body: "ask"}},
		Pub:      pub,
		Resolver: &fakeResolver{responses: [][]string{{"run-existing"}}},
		Writer:   writerFor(writer),
	}
	if _, err := Launch(context.Background(), d, fast(Params{})); err == nil {
		t.Error("a launch against an already-running ref must refuse to mint a duplicate")
	}
	if len(pub.subjects) != 0 {
		t.Error("nothing must be published when refusing a duplicate")
	}
	if writer.calls != 0 {
		t.Error("no condition stamped when refusing a duplicate")
	}

	// --force overrides the guard.
	force := &fakeResolver{responses: [][]string{{"run-existing"}, {"run-existing", "run-forced"}}}
	id, err := Launch(context.Background(), Deps{Issues: d.Issues, Pub: &fakePub{}, Resolver: force, Writer: writerFor(&fakeWriter{})}, fast(Params{Force: true}))
	if err != nil {
		t.Fatalf("forced launch: %v", err)
	}
	if id != "run-forced" {
		t.Errorf("forced bind = %q, want run-forced", id)
	}
}

// Two new runs appearing for one ref (a concurrent front door raced the launch) is an ambiguous
// bind — fail closed rather than label one nondeterministically (review MEDIUM).
func TestLaunchAmbiguousBindFailsClosed(t *testing.T) {
	writer := &fakeWriter{}
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Body: "ask"}},
		Pub:      &fakePub{},
		Resolver: &fakeResolver{responses: [][]string{{}, {"run-a", "run-b"}}},
		Writer:   writerFor(writer),
	}
	_, err := Launch(context.Background(), d, fast(Params{Condition: experiment.Config{Condition: experiment.ConditionBaseline}}))
	if err == nil {
		t.Fatal("two new runs for one ref must fail closed (ambiguous bind)")
	}
	if writer.calls != 0 {
		t.Error("no condition may be stamped on an ambiguous bind")
	}
}

// When no new run appears within the window, the launch fails loud and stamps NOTHING (G2 /
// fail-closed — never invent a run).
func TestLaunchBindTimeoutFailsLoudWithoutStamping(t *testing.T) {
	writer := &fakeWriter{}
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Body: "ask"}},
		Pub:      &fakePub{},
		Resolver: &fakeResolver{responses: [][]string{{}}}, // never a new id
		Writer:   writerFor(writer),
	}
	// A DECLARED condition, so a bind failure would otherwise reach the stamp — prove it does not.
	_, err := Launch(context.Background(), d, fast(Params{Condition: experiment.Config{Condition: experiment.ConditionBaseline}}))
	if err == nil {
		t.Fatal("an unbound run must fail loud")
	}
	if writer.calls != 0 {
		t.Error("no condition may be stamped when no run bound")
	}
}

// A declared condition is stamped once on the bound run (experiment.Launch's stamp leg).
func TestLaunchDeclaredConditionIsStamped(t *testing.T) {
	writer := &fakeWriter{}
	d := Deps{
		Issues:   fakeIssues{issue: github.Issue{Number: 7, Body: "ask"}},
		Pub:      &fakePub{},
		Resolver: &fakeResolver{responses: [][]string{{}, {runNew}}},
		Writer:   writerFor(writer),
	}
	id, err := Launch(context.Background(), d, fast(Params{Condition: experiment.Config{Condition: experiment.ConditionBaseline}}))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if id != runNew {
		t.Errorf("bound id = %q, want run-new", id)
	}
	if writer.calls != 1 {
		t.Fatalf("want the condition stamped once, got %d writes", writer.calls)
	}
	if len(writer.triples) != 1 || writer.triples[0].Predicate != experiment.ConditionPredicate || writer.triples[0].Object != experiment.ConditionBaseline {
		t.Errorf("stamped triple = %+v, want %s=%s", writer.triples, experiment.ConditionPredicate, experiment.ConditionBaseline)
	}
}
