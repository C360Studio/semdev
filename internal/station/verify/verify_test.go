package verify

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/verifyartifact"
	sverify "github.com/c360studio/semdev/internal/verify"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

// fakeClones stands in for runspace.Checkouts.CloneForVerify.
type fakeClones struct {
	root string
	err  error
}

// Real six-position entity ids: the projection client validates the entity against
// its contract pattern, so bare "run-1"/"review-loop-1" no longer stand in for one
// (migrate-beta159 D2a). The loop id carries the agentic-loop grammar, the run the
// chain grammar — a distinction the old fixtures could not express (G8).
const (
	runEntity        = "org.plat.agent.chain.execution.run-1"
	reviewLoopEntity = "org.plat.agent.agentic-loop.execution.review-loop-1"
)

func (f fakeClones) CloneForVerify(_ context.Context, _ string) (string, error) {
	return f.root, f.err
}

type fakeManifests struct{}

func (fakeManifests) Resolve(_ context.Context, _ string) (harness.Manifest, error) {
	return harness.GoProfile(), nil
}

type fakeProver struct {
	verdict sverify.Verdict
	err     error
}

func (p fakeProver) ProveArtifact(_ context.Context, _, _ string, _ harness.Manifest, _ secrets.Store) (sverify.Verdict, error) {
	return p.verdict, p.err
}

type okWriter struct{ replaces [][]message.Triple }

func (w *okWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	w.replaces = append(w.replaces, m.Desired)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

func verdictOf(o sverify.Outcome) sverify.Verdict { return sverify.Verdict{Outcome: o} }

func newHandler(clones fakeClones, prover fakeProver, w *okWriter) *handler {
	return &handler{
		clones:    clones,
		manifests: fakeManifests{},
		prover:    prover,
		writer:    graphown.NewWriter(verifyartifact.Source, w),
		logger:    slog.Default(),
	}
}

// The review-approved rule fires on Quinn's review loop, so the run travels as the
// run_entity_id property. Without it the station cannot stamp verify.result on the run —
// it errors rather than verifying against "".
func TestHandleRejectsMissingRunEntity(t *testing.T) {
	h := newHandler(fakeClones{root: "/c"}, fakeProver{verdict: verdictOf(sverify.OutcomePass)}, &okWriter{})
	err := h.Handle(context.Background(), station.Request{EntityID: reviewLoopEntity, Properties: map[string]string{}})
	if err == nil {
		t.Error("verify handler must error when the dispatch carries no run_entity_id property")
	}
}

// A pass verdict stamps verify.result and Handle returns nil (the delivery route reads it
// off the run).
func TestHandlePassStampsAndSucceeds(t *testing.T) {
	w := &okWriter{}
	h := newHandler(fakeClones{root: "/c"}, fakeProver{verdict: verdictOf(sverify.OutcomePass)}, w)
	if err := h.Handle(context.Background(), station.Request{EntityID: reviewLoopEntity, Properties: map[string]string{RunEntityProperty: runEntity}}); err != nil {
		t.Fatalf("pass verdict must succeed: %v", err)
	}
	got := ""
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == verifyartifact.ResultPredicate {
				got, _ = tr.Object.(string)
			}
		}
	}
	if got != string(sverify.OutcomePass) {
		t.Errorf("verify.result = %q, want pass", got)
	}
}

// A clone fault (no checkout materialized) fails closed: Handle returns an error and stamps
// nothing — the base retries, and no verify.result advances a run over a guessed path.
func TestHandleFailsClosedOnCloneError(t *testing.T) {
	w := &okWriter{}
	h := newHandler(fakeClones{err: errors.New("no checkout materialized for run")}, fakeProver{verdict: verdictOf(sverify.OutcomePass)}, w)
	err := h.Handle(context.Background(), station.Request{EntityID: reviewLoopEntity, Properties: map[string]string{RunEntityProperty: runEntity}})
	if err == nil {
		t.Error("verify handler must fail closed when the artifact cannot be cloned")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed clone must stamp nothing (no verify over a guessed path)")
	}
}

// A retry verdict returns an error (so the base re-runs the cold proof) — a transient flake
// must never park a good artifact, and a "retry" matches neither delivery route.
func TestHandleRetryReturnsErrorForBaseRetry(t *testing.T) {
	w := &okWriter{}
	h := newHandler(fakeClones{root: "/c"}, fakeProver{verdict: verdictOf(sverify.OutcomeRetry)}, w)
	err := h.Handle(context.Background(), station.Request{EntityID: reviewLoopEntity, Properties: map[string]string{RunEntityProperty: runEntity}})
	if err == nil {
		t.Error("a retry verdict must return an error so the base retries the cold proof (transient resilience)")
	}
}
