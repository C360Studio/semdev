package verifyartifact

import (
	"context"
	"errors"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeClones stands in for runspace.Checkouts.CloneForVerify.
type fakeClones struct {
	root string
	err  error
}

func (f fakeClones) CloneForVerify(_ context.Context, _ string) (string, error) {
	return f.root, f.err
}

type fakeManifests struct {
	m   harness.Manifest
	err error
}

func (f fakeManifests) Resolve(_ context.Context, _ string) (harness.Manifest, error) {
	return f.m, f.err
}

// fakeProver returns a canned verdict/err — the cold proof itself is exercised
// docker-gated in internal/coldproof; here we pin RunVerify's fact-stamping and
// fail-closed wiring without docker.
type fakeProver struct {
	verdict verify.Verdict
	err     error
	gotRoot string // records the artifact root it was asked to prove
}

func (p *fakeProver) ProveArtifact(_ context.Context, _, artifactRoot string, _ harness.Manifest, _ secrets.Store) (verify.Verdict, error) {
	p.gotRoot = artifactRoot
	return p.verdict, p.err
}

type fakeWriter struct {
	replaces [][]message.Triple
}

func (w *fakeWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	w.replaces = append(w.replaces, m.Desired)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam the production code takes, so
// every test exercises graphown.ContractFor for real — the behavioral proof that
// this owner's predicates are classed onto the entity class it actually writes.
func writerFor(w *fakeWriter) *graphown.Writer { return graphown.NewWriter(Source, w) }

func verdictOf(o verify.Outcome) verify.Verdict { return verify.Verdict{Outcome: o} }

// runVerify runs RunVerify with the given clones/prover and returns the stamped
// verify.result (or "") plus RunVerify's own return values.
func runVerify(t *testing.T, clones VerifyClones, prover Prover, w *fakeWriter) (string, VerifyResult, error) {
	t.Helper()
	res, err := RunVerify(context.Background(), clones, fakeManifests{m: harness.GoProfile()}, prover, nil, writerFor(w), nil, runEntity)
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == ResultPredicate {
				return tr.Object.(string), res, err
			}
		}
	}
	return "", res, err
}

// Happy path: the cold proof passes → verify.result = pass, stamped with the harness
// Source on the run entity, and RunVerify proved the CLONE root (the committed
// artifact), not the warm checkout.
func TestRunVerifyPassStampsResult(t *testing.T) {
	w := &fakeWriter{}
	prover := &fakeProver{verdict: verdictOf(verify.OutcomePass)}
	got, res, err := runVerify(t, fakeClones{root: "/verify-clone"}, prover, w)
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if got != string(verify.OutcomePass) {
		t.Fatalf("verify.result = %q, want pass", got)
	}
	if res.Verdict.Outcome != verify.OutcomePass {
		t.Errorf("VerifyResult.Verdict.Outcome = %q, want pass", res.Verdict.Outcome)
	}
	if prover.gotRoot != "/verify-clone" {
		t.Errorf("prover proved %q, want the fresh clone root /verify-clone (the committed artifact, not the warm checkout)", prover.gotRoot)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("verify.result Source = %q, want %q (G5)", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("verify.result subject = %q, want run entity (D15)", tr.Subject)
			}
		}
	}
}

// A genuine cold failure (fabrication / non-self-contained fix) → verify.result = fail.
// The retry→re-run behavior lives in the caller (the verify station); here we only
// assert the stamp and the returned verdict.
func TestRunVerifyFailStampsResult(t *testing.T) {
	w := &fakeWriter{}
	got, res, err := runVerify(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomeFail)}, w)
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if got != string(verify.OutcomeFail) {
		t.Fatalf("verify.result = %q, want fail", got)
	}
	if res.Verdict.Outcome != verify.OutcomeFail {
		t.Errorf("VerifyResult.Verdict.Outcome = %q, want fail", res.Verdict.Outcome)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == "dev.verified" {
				t.Errorf("RunVerify must not stamp dev.verified (the delivery route reads verify.result on the run): saw it on %q", tr.Subject)
			}
		}
	}
}

// A transport fault classified by the proof → verify.result = retry (never a terminal
// reject of a good artifact), stamped as evidence. RunVerify returns the retry verdict
// with no error — it is the CALLER's (the verify station's) concern to re-run rather
// than treat this as terminal (SB5).
func TestRunVerifyRetryStampsResultAndReturnsVerdict(t *testing.T) {
	w := &fakeWriter{}
	got, res, err := runVerify(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomeRetry)}, w)
	if err != nil {
		t.Fatalf("RunVerify: %v", err)
	}
	if got != string(verify.OutcomeRetry) {
		t.Errorf("verify.result = %q, want retry (evidence of the attempt)", got)
	}
	if res.Verdict.Outcome != verify.OutcomeRetry {
		t.Errorf("VerifyResult.Verdict.Outcome = %q, want retry", res.Verdict.Outcome)
	}
}

// A proof that COULD NOT RUN (image build flake, incomplete manifest) returns an error
// and stamps NO verdict (never a false green) — the caller decides whether to retry.
func TestRunVerifyProveErrorStampsNothing(t *testing.T) {
	w := &fakeWriter{}
	_, _, err := runVerify(t, fakeClones{root: "/c"}, &fakeProver{err: errors.New("docker build: daemon flake")}, w)
	if err == nil {
		t.Error("a proof that could not run must return an error, not a stamped verdict")
	}
	if len(w.replaces) != 0 {
		t.Error("a proof that could not run must stamp no verify.result")
	}
}

// The clone step fails closed (no warm checkout) → an error, no stamp — the run parks
// rather than verifying over a guessed path (SB5).
func TestRunVerifyCloneFailsClosed(t *testing.T) {
	w := &fakeWriter{}
	_, _, err := runVerify(t, fakeClones{err: errors.New("no checkout materialized for run")}, &fakeProver{verdict: verdictOf(verify.OutcomePass)}, w)
	if err == nil {
		t.Error("a failed clone must return an error")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed clone must stamp nothing (no verify over a guessed path)")
	}
}

// Re-verify upserts the verify.result (replace-by-predicate): a passing re-run replaces
// a prior retry, and RunVerify writes ONLY verify.result.
func TestRunVerifyReVerifyUpserts(t *testing.T) {
	w := &fakeWriter{}
	runVerify(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomeRetry)}, w)
	runVerify(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomePass)}, w)
	if len(w.replaces) != 2 {
		t.Fatalf("want two verify.result upserts, got %d", len(w.replaces))
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != ResultPredicate {
				t.Errorf("verify wrote %q — it must write only %q", tr.Predicate, ResultPredicate)
			}
		}
	}
}
