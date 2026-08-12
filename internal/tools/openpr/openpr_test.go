package openpr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/forge/github"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeWriter struct {
	replaces [][]message.Triple
	err      error
}

func (w *fakeWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	if w.err != nil {
		return projection.MutationReceipt{Commit: projection.CommitNotCommitted}, w.err
	}
	w.replaces = append(w.replaces, m.Desired)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam the production code takes, so
// every test exercises graphown.ContractFor for real — the behavioral proof that
// this owner's predicates are classed onto the entity class it actually writes.
func writerFor(w *fakeWriter) *graphown.Writer { return graphown.NewWriter(Source, w) }

// fakeReader satisfies changefacts.Reader: it returns its seeded triples, prefix-scoped
// (mirroring the NATS reader's filter), or an injected fault. An empty reader models a
// run that has not yet been delivered.
type fakeReader struct {
	triples []message.Triple
	err     error
}

func (r *fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, t := range r.triples {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

// fakeForge records the API calls (the forge-level idempotency ordering) and
// serves a configurable existing PR.
type fakeForge struct {
	calls    []string
	existing *github.PR
	created  []github.PRRequest
	findErr  error
}

func (f *fakeForge) FindPRByHead(_ context.Context, _, _, _, _ string) (*github.PR, error) {
	f.calls = append(f.calls, "find")
	return f.existing, f.findErr
}

func (f *fakeForge) CreatePR(_ context.Context, _, _ string, pr github.PRRequest) (*github.PR, error) {
	f.calls = append(f.calls, "create")
	f.created = append(f.created, pr)
	return &github.PR{Number: 7, HTMLURL: "https://forge.example/acme/repo/pull/7", State: "open"}, nil
}

// fakeRunner records the git invocations.
type fakeRunner struct {
	runs [][]string
	res  cliexec.Result
	err  error
}

func (r *fakeRunner) Run(_ context.Context, dir, name string, args ...string) (cliexec.Result, error) {
	r.runs = append(r.runs, append([]string{dir, name}, args...))
	return r.res, r.err
}

type fakeRoots struct{ root string }

func (f fakeRoots) Root(_ context.Context, _ string) (string, error) {
	if f.root == "" {
		return "", errors.New("no checkout materialized")
	}
	return f.root, nil
}

func newTestDelivery(reader *fakeReader, writer *fakeWriter, forge *fakeForge, runner *fakeRunner) *Delivery {
	// Every delivering run carries the verified snapshot pointer — Deliver
	// pushes THAT sha, never bare HEAD (G4/G7; the absent case fails closed,
	// pinned separately).
	reader.triples = append(reader.triples, message.Triple{
		Subject: runEntity, Predicate: "attempt.commit.sha", Object: "abc123def", Source: "patch-committer",
	})
	return &Delivery{
		Reader: reader,
		Writer: writerFor(writer),
		API:    forge,
		Roots:  fakeRoots{root: "/tmp/checkout"},
		Runner: runner,
		Forge:  ForgeConfig{Owner: "acme", Repo: "repo", RemoteURL: "file:///tmp/bare.git", BaseBranch: "main"},
	}
}

// TestDeliverPushesQueriesThenCreates pins the whole real path in ORDER: push
// the committed branch, query-by-head FIRST (the forge-level guard), create on
// absence, stamp the REAL PR URL with the open-pr Source.
func TestDeliverPushesQueriesThenCreates(t *testing.T) {
	w := &fakeWriter{}
	forge := &fakeForge{}
	runner := &fakeRunner{}
	d := newTestDelivery(&fakeReader{}, w, forge, runner)

	ref, err := d.Deliver(context.Background(), runEntity)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if ref != "https://forge.example/acme/repo/pull/7" {
		t.Errorf("ref = %q, want the created PR's html_url", ref)
	}

	if len(runner.runs) != 1 {
		t.Fatalf("git invocations = %d, want 1 (the branch push)", len(runner.runs))
	}
	push := strings.Join(runner.runs[0], " ")
	if !strings.Contains(push, "git push") || !strings.Contains(push, "abc123def:refs/heads/"+BranchPrefix+"run-1") {
		t.Errorf("push = %q, want a git push of the RECORDED attempt.commit.sha (never bare HEAD) to the semdev/<run-suffix> branch", push)
	}
	if got := strings.Join(forge.calls, ","); got != "find,create" {
		t.Fatalf("forge call order = %q, want find,create (query-by-head BEFORE create — the idempotency ordering)", got)
	}
	req := forge.created[0]
	if req.Head != BranchPrefix+"run-1" || req.Base != "main" {
		t.Errorf("PR head/base = %s/%s, want %srun-1/main", req.Head, req.Base, BranchPrefix)
	}
	if !strings.Contains(req.Body, "delivery evidence") || !strings.Contains(req.Body, runEntity) {
		t.Errorf("PR body must carry the evidence summary + the run trajectory pointer, got %q", req.Body)
	}

	var triples []message.Triple
	for _, batch := range w.replaces {
		triples = append(triples, batch...)
	}
	if len(triples) != 1 || triples[0].Predicate != RefPredicate || triples[0].Source != Source {
		t.Fatalf("stamped %v, want exactly one %s triple with Source %s", triples, RefPredicate, Source)
	}
	if triples[0].Object != ref {
		t.Errorf("stamped object = %v, want the returned ref", triples[0].Object)
	}
}

// TestDeliverAdoptsExistingPRByHead pins the forge-level guard's adopt leg: a
// PR already exists for the head branch (a concurrent double-fire got there
// first) — NO create, the existing URL is stamped.
func TestDeliverAdoptsExistingPRByHead(t *testing.T) {
	w := &fakeWriter{}
	forge := &fakeForge{existing: &github.PR{Number: 3, HTMLURL: "https://forge.example/acme/repo/pull/3"}}
	d := newTestDelivery(&fakeReader{}, w, forge, &fakeRunner{})

	ref, err := d.Deliver(context.Background(), runEntity)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if ref != "https://forge.example/acme/repo/pull/3" {
		t.Errorf("ref = %q, want the ADOPTED PR's URL", ref)
	}
	if got := strings.Join(forge.calls, ","); got != "find" {
		t.Errorf("forge calls = %q, want find only (no create on an existing head PR)", got)
	}
}

// TestDeliverFailsClosedWithoutForge pins the stub deletion: no forge config →
// a LOUD error, nothing pushed, nothing stamped (the run parks via the
// station-failure lane).
func TestDeliverFailsClosedWithoutForge(t *testing.T) {
	w := &fakeWriter{}
	runner := &fakeRunner{}
	d := newTestDelivery(&fakeReader{}, w, &fakeForge{}, runner)
	d.Forge = ForgeConfig{} // unconfigured

	_, err := d.Deliver(context.Background(), runEntity)
	if err == nil || !strings.Contains(err.Error(), "no forge configured") {
		t.Fatalf("Deliver = %v, want the loud no-forge fail-closed error (the local-delivery stub is DELETED)", err)
	}
	if len(runner.runs) != 0 || len(w.replaces) != 0 {
		t.Errorf("unconfigured delivery pushed %d / stamped %d, want 0/0", len(runner.runs), len(w.replaces))
	}
}

// TestDeliverIsIdempotentOnReplay — the graph-side guard (reshape 7.4,
// unchanged contract): once pr.ref exists, a replay returns the SAME ref and
// touches NOTHING (no push, no API call, no write).
func TestDeliverIsIdempotentOnReplay(t *testing.T) {
	w := &fakeWriter{}
	forge := &fakeForge{}
	runner := &fakeRunner{}
	r := &fakeReader{}
	d := newTestDelivery(r, w, forge, runner)

	ref1, err := d.Deliver(context.Background(), runEntity)
	if err != nil {
		t.Fatalf("first Deliver: %v", err)
	}
	r.triples = append(r.triples, w.replaces[0]...)

	ref2, err := d.Deliver(context.Background(), runEntity)
	if err != nil {
		t.Fatalf("replay Deliver: %v", err)
	}
	if ref2 != ref1 {
		t.Errorf("replay ref = %q, want %q", ref2, ref1)
	}
	if len(w.replaces) != 1 || len(runner.runs) != 1 || len(forge.calls) != 2 {
		t.Errorf("replay side-effects: writes=%d pushes=%d api=%d — want 1/1/2 (the replay must touch NOTHING)",
			len(w.replaces), len(runner.runs), len(forge.calls))
	}
}

// TestDeliverReturnsStoredRefWithoutRewriting — the guard returns the RECORDED
// value (a live PR URL is not re-derivable from the run).
func TestDeliverReturnsStoredRefWithoutRewriting(t *testing.T) {
	const storedRef = "https://forge.example/acme/repo/pull/42"
	w := &fakeWriter{}
	r := &fakeReader{triples: []message.Triple{{
		Subject: runEntity, Predicate: RefPredicate, Object: storedRef, Source: Source,
	}}}
	d := newTestDelivery(r, w, &fakeForge{}, &fakeRunner{})

	ref, err := d.Deliver(context.Background(), runEntity)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if ref != storedRef {
		t.Errorf("ref = %q, want the stored %q", ref, storedRef)
	}
	if len(w.replaces) != 0 {
		t.Errorf("wrote %d batches on an already-delivered run, want 0", len(w.replaces))
	}
}

// TestDeliverFailsClosedOnNonStringRef — a corrupt recorded ref is an error,
// never a fall-through to a duplicate forge PR.
func TestDeliverFailsClosedOnNonStringRef(t *testing.T) {
	w := &fakeWriter{}
	r := &fakeReader{triples: []message.Triple{{
		Subject: runEntity, Predicate: RefPredicate, Object: 42, Source: Source,
	}}}
	d := newTestDelivery(r, w, &fakeForge{}, &fakeRunner{})
	if _, err := d.Deliver(context.Background(), runEntity); err == nil {
		t.Fatal("must fail closed on a present non-string pr.ref")
	}
	if len(w.replaces) != 0 {
		t.Errorf("stamped on a malformed pr.ref, want nothing")
	}
}

// TestDeliverFailsClosedWithoutVerifiedCommit — no attempt.commit.sha means
// nothing verified exists to deliver: fail closed before any push.
func TestDeliverFailsClosedWithoutVerifiedCommit(t *testing.T) {
	runner := &fakeRunner{}
	d := &Delivery{
		Reader: &fakeReader{}, // no attempt.commit.sha
		Writer: writerFor(&fakeWriter{}),
		API:    &fakeForge{},
		Roots:  fakeRoots{root: "/tmp/checkout"},
		Runner: runner,
		Forge:  ForgeConfig{Owner: "acme", Repo: "repo", RemoteURL: "file:///tmp/bare.git"},
	}
	_, err := d.Deliver(context.Background(), runEntity)
	if err == nil || !strings.Contains(err.Error(), "attempt.commit.sha") {
		t.Fatalf("Deliver = %v, want the fail-closed no-verified-commit error", err)
	}
	if len(runner.runs) != 0 {
		t.Errorf("pushed without a verified commit, want no push at all")
	}
}

// TestDeliverFailsClosedOnReadFault — the existence check failing means NO
// delivery attempt at all.
func TestDeliverFailsClosedOnReadFault(t *testing.T) {
	readErr := errors.New("graph query down")
	runner := &fakeRunner{}
	d := newTestDelivery(&fakeReader{err: readErr}, &fakeWriter{}, &fakeForge{}, runner)
	if _, err := d.Deliver(context.Background(), runEntity); !errors.Is(err, readErr) {
		t.Fatalf("want the read fault surfaced, got %v", err)
	}
	if len(runner.runs) != 0 {
		t.Errorf("pushed despite a read fault")
	}
}

// TestDeliverFailedPushStopsDelivery — a non-zero push exit aborts before any
// API call, with the token never leaking into the error.
func TestDeliverFailedPushStopsDelivery(t *testing.T) {
	forge := &fakeForge{}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 1, Stderr: "fatal: could not read from remote with tok-secret-123"}}
	d := newTestDelivery(&fakeReader{}, &fakeWriter{}, forge, runner)
	d.Token = "tok-secret-123"

	_, err := d.Deliver(context.Background(), runEntity)
	if err == nil {
		t.Fatal("a failed push must fail delivery")
	}
	if strings.Contains(err.Error(), "tok-secret-123") {
		t.Errorf("the token LEAKED into the error: %v", err)
	}
	if len(forge.calls) != 0 {
		t.Errorf("API called after a failed push, want none")
	}
}

// TestPushURLInjectsTokenForHTTPSOnly — the x-access-token form for https,
// pass-through for file:// (the journey's bare-repo remote).
func TestPushURLInjectsTokenForHTTPSOnly(t *testing.T) {
	d := &Delivery{Token: "tok", Forge: ForgeConfig{RemoteURL: "https://github.com/acme/repo.git"}}
	u, err := d.pushURL()
	if err != nil {
		t.Fatalf("pushURL: %v", err)
	}
	if !strings.Contains(u, "x-access-token:tok@github.com") {
		t.Errorf("https push URL = %q, want the x-access-token credential form", u)
	}
	d.Forge.RemoteURL = "file:///tmp/bare.git"
	if u, _ = d.pushURL(); u != "file:///tmp/bare.git" {
		t.Errorf("file:// remote must pass through untouched, got %q", u)
	}
}
