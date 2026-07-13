package verifyartifact

import (
	"context"
	"errors"
	"testing"

	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
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
// docker-gated in internal/coldproof; here we pin the tool's fact-stamping and
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

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	w.replaces = append(w.replaces, add)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func callVerify() agentic.ToolCall {
	return agentic.ToolCall{
		ID:       "c1",
		Name:     ToolName,
		Metadata: map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

func verdictOf(o verify.Outcome) verify.Verdict { return verify.Verdict{Outcome: o} }

// execWith runs the tool with the given clones/prover and returns the stamped
// verify.result (or "") plus the tool result.
func execWith(t *testing.T, clones VerifyClones, prover Prover, w *fakeWriter) (string, agentic.ToolResult) {
	t.Helper()
	e := New(clones, fakeManifests{m: harness.GoProfile()}, prover, nil, w, nil)
	res, err := e.Execute(context.Background(), callVerify())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == ResultPredicate {
				return tr.Object.(string), res
			}
		}
	}
	return "", res
}

// Happy path: the cold proof passes → verify.result = pass, stamped with the harness
// Source on the run entity, and the tool proved the CLONE root (the committed artifact),
// not the warm checkout.
func TestVerifyPassStampsResult(t *testing.T) {
	w := &fakeWriter{}
	prover := &fakeProver{verdict: verdictOf(verify.OutcomePass)}
	got, res := execWith(t, fakeClones{root: "/verify-clone"}, prover, w)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if got != string(verify.OutcomePass) {
		t.Fatalf("verify.result = %q, want pass", got)
	}
	if !res.StopLoop {
		t.Error("verify_artifact must StopLoop (single forced turn)")
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
func TestVerifyFailStampsResult(t *testing.T) {
	got, _ := execWith(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomeFail)}, &fakeWriter{})
	if got != string(verify.OutcomeFail) {
		t.Fatalf("verify.result = %q, want fail", got)
	}
}

// A transport fault classified by the proof → verify.result = retry (never a terminal
// reject of a good artifact).
func TestVerifyRetryStampsResult(t *testing.T) {
	got, _ := execWith(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomeRetry)}, &fakeWriter{})
	if got != string(verify.OutcomeRetry) {
		t.Fatalf("verify.result = %q, want retry", got)
	}
}

// A proof that COULD NOT RUN (image build flake, incomplete manifest) is a retryable
// TOOL error — it stamps NO verdict (never a false green), and does not StopLoop so the
// forced loop re-runs.
func TestVerifyProveErrorIsRetryableToolError(t *testing.T) {
	w := &fakeWriter{}
	_, res := execWith(t, fakeClones{root: "/c"}, &fakeProver{err: errors.New("docker build: daemon flake")}, w)
	if res.Error == "" {
		t.Error("a proof that could not run must surface as a tool error, not a stamped verdict")
	}
	if len(w.replaces) != 0 {
		t.Error("a proof that could not run must stamp no verify.result")
	}
	if res.StopLoop {
		t.Error("a prove-error must NOT StopLoop — the forced verify loop retries")
	}
}

// The clone step fails closed (no warm checkout) → a tool error, no stamp — the run
// parks rather than verifying over a guessed path (SB5).
func TestVerifyCloneFailsClosed(t *testing.T) {
	w := &fakeWriter{}
	_, res := execWith(t, fakeClones{err: errors.New("no checkout materialized for run")}, &fakeProver{verdict: verdictOf(verify.OutcomePass)}, w)
	if res.Error == "" {
		t.Error("a failed clone must surface as a tool error")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed clone must stamp nothing (no verify over a guessed path)")
	}
}

// Re-verify upserts the verify.result (replace-by-predicate): a passing re-run replaces
// a prior retry, and verify writes ONLY verify.result.
func TestVerifyReVerifyUpserts(t *testing.T) {
	w := &fakeWriter{}
	execWith(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomeRetry)}, w)
	execWith(t, fakeClones{root: "/c"}, &fakeProver{verdict: verdictOf(verify.OutcomePass)}, w)
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

// Schema-only registration (nil clones/manifests/prover/writer) fails loudly.
func TestVerifyFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, nil, nil, nil, nil).Execute(context.Background(), callVerify())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness verify must fail loudly")
	}
}

// G3: the schema takes no arguments at all — the model can only trigger the proof.
func TestVerifySchemaTakesNoInput(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("schema exposes %d properties, want 0 — verify takes no input (G3): %v", len(props), props)
	}
}
