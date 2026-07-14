// Package verifyartifact holds the shared VERIFY CORE (RunVerify): it proves a run's
// COMMITTED artifact COLD, in a fresh throwaway container over a fresh clone with a
// fresh dep cache, and stamps the terminal verify.result. No run reaches open_pr until
// this records a pass. It is the outcome half of the taxonomy's verify (the structural
// coherence half is openspec.validated + review.verdict, group 7/8).
//
// This is the THIRD sandbox instance (design SB4): the cold baseline proved the image
// builds the repo cold BEFORE the dev loop (provision_sandbox); the warm container ran
// the apply_patch → measure → floors iterations; and THIS proof is a SEPARATE fresh cold
// container over a fresh CLONE of the run's checkout — the committed artifact — with a
// fresh dependency cache. That separation is the whole point (SB3): a fix that only
// doctored the warm container's environment, or a fabricated coordinate a warm cache
// masked, cannot survive a fresh clone + fresh cache. semspec's fatal bug was a harness
// fixup that built in the harness and 401'd on a clean checkout; this proof is that clean
// checkout.
//
// RunVerify GATHERS evidence; the pure verify.Decide judges it (inside ProveArtifact,
// shared with the provision-time baseline so a fabrication reads identically in both).
// Crucially it draws the one line Decide cannot: ANY transport/infrastructure fault — a
// container that would not provision, a step that could not run, a resolve failure the
// cleanroom classifier reads as network-class — sets Completed=false so the verdict is
// Retry, never a terminal reject of a good artifact. A GENUINE resolve/test failure (a
// missing/fabricated coordinate a warm cache would have masked, or a non-self-contained
// fix) is Resolved/TestsPassed=false → Fail: the make-or-break reject.
//
// It stamps the single verify.result scalar (pass/fail/retry), upserted latest-wins (a
// retry re-run replaces it). It fires no lifecycle transition (G2): the open_pr gate is a
// rule reading verify.result. verify.result's sole writer is verify-harness (G5). It
// takes no caller-supplied outcome (G3 — the caller may only trigger the proof, never
// supply its outcome).
//
// RunVerify is the shared core called by the verify STATION
// (internal/station/verify, design simplify-m0-execution-rail R6) — the sole caller
// post-reshape, ZERO model turns. The former verify_artifact tool executor that called
// it from a forced coordinator turn was deleted in the group-6 tool-cleanup slice (6E).
package verifyartifact

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// Source is stamped on the verify.result triple. It MUST equal the writer declared for
// verify.result in internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "verify-harness"

// ResultPredicate is the terminal clean-room fact this harness owns on the run entity. Exact
// predicate → latest-wins (a retry re-run upserts). The rule-native delivery route reads it
// directly (08a coherent on eq "pass", 08b blocked on eq "fail"; a "retry" matches neither),
// which is why RunVerify stamps no loop chaining marker (the reshape, R1).
const ResultPredicate = "verify.result"

// dockerBin is the docker CLI binary the cold proof shells (matches cleanroom's and
// provision_sandbox's default). A package const keeps the surface small; a fake Prover
// exercises the fact-stamping without docker.
const dockerBin = "docker"

// VerifyClones makes a fresh COLD clone of a run's checkout — the committed artifact the
// final verify proves. It is runspace.Checkouts.CloneForVerify behind a narrow seam:
// non-destructive of the warm checkout (never re-materializes it), so the applied diff is
// preserved. The verify station's factory fails loudly if it cannot construct one.
type VerifyClones interface {
	CloneForVerify(ctx context.Context, runEntityID string) (string, error)
}

// Manifests resolves a checkout's reproducibility-contract manifest (how to prove it
// cold — the operator-declared image + resolve/test commands + cache-home envs). Narrow
// seam so RunVerify holds no harvest logic.
type Manifests interface {
	Resolve(ctx context.Context, checkoutRoot string) (harness.Manifest, error)
}

// Prover builds the declared image from a clone and proves the artifact resolves+builds+
// tests COLD, returning the verify verdict. It is coldproof.ProveArtifact behind an
// interface so the fact-stamping is unit-testable without docker (a fake returns a canned
// verdict); the real proof runs docker-gated. Always supplied (a pure adapter needing no
// client).
type Prover interface {
	ProveArtifact(ctx context.Context, docker, artifactRoot string, m harness.Manifest, store secrets.Store) (verify.Verdict, error)
}

// coldProver is the production Prover: the real cold clean-room verify (design SB4.3).
type coldProver struct{}

func (coldProver) ProveArtifact(ctx context.Context, docker, artifactRoot string, m harness.Manifest, store secrets.Store) (verify.Verdict, error) {
	return coldproof.ProveArtifact(ctx, docker, artifactRoot, m, store)
}

// DefaultProver returns the production cold-verify Prover for boot to wire.
func DefaultProver() Prover { return coldProver{} }

// VerifyResult is the shared verify core's outcome: the definitive cold clean-room
// verdict plus the resolved manifest profile (for the caller's summary/logging).
type VerifyResult struct {
	Verdict verify.Verdict
	Profile string
}

// RunVerify clones the run's COMMITTED artifact into a fresh dir, resolves its
// reproducibility manifest FROM THE CLONE, proves it resolves+builds+tests COLD in a
// fresh throwaway container with a fresh dependency cache, and stamps the definitive
// verify.result (pass/fail/retry) on the run. It is the shared VERIFY CORE the verify
// station (internal/station/verify, design simplify-m0-execution-rail R6) calls off a
// publish-triggered dispatch — the sole writer of verify.result (verify-harness, G5).
//
// It returns a pre-proof infra error (clone/resolve/prove-could-not-run) that stamps
// NOTHING — never a false green — for the caller to surface as retryable; or a stamped
// VerifyResult carrying the definitive verdict. A retry Verdict is stamped (evidence) but
// signals a TRANSIENT infra fault (a container that would not provision, a resolve read as
// network-class), never a terminal reject of a good artifact (SB5). Fires no lifecycle
// transition (G2); takes no caller-supplied outcome (G3).
//
// CONTRACT: clones/manifests/prover/writer must be non-nil — the station factory
// constructs them and fails loud on a nil seam before ever calling RunVerify. A nil
// logger is defended (defaults to slog.Default()).
func RunVerify(ctx context.Context, clones VerifyClones, manifests Manifests, prover Prover, store secrets.Store, writer agentictools.OwnedFactWriter, logger *slog.Logger, runEntityID string) (VerifyResult, error) {
	if logger == nil {
		logger = slog.Default()
	}
	// Clone the committed artifact into a fresh dir (non-destructive of the warm checkout)
	// — the "--recursive clone" at M0. Fails closed if no checkout was materialized (the
	// run parks, never a verify over a guessed path, SB5).
	cloneRoot, err := clones.CloneForVerify(ctx, runEntityID)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify_artifact: clone artifact for cold verify: %w", err)
	}
	// Resolve the manifest from the CLONE, so the COMMITTED Dockerfile/deps are what's
	// proven — not the warm checkout's environment.
	manifest, err := manifests.Resolve(ctx, cloneRoot)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify_artifact: resolve reproducibility manifest: %w", err)
	}
	// Prove cold: build the image from the clone, run resolve+test in a fresh throwaway
	// container with a fresh dep cache. A returned ERROR is a pre-proof infra/declaration
	// fault (docker flake, unbuildable image) the caller surfaces as retryable — no verdict
	// is stamped, so the proof re-runs (a persistent fault stalls toward the human, never a
	// false green). A returned Verdict is a definitive result (pass/fail/retry) stamped here.
	verdict, err := prover.ProveArtifact(ctx, dockerBin, cloneRoot, manifest, store)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify_artifact: cold clean-room proof could not run: %w", err)
	}
	if err := stampResult(ctx, writer, runEntityID, verdict.Outcome); err != nil {
		return VerifyResult{}, fmt.Errorf("verify_artifact: stamp %s on %s: %w", ResultPredicate, runEntityID, err)
	}
	logger.Info("verify_artifact recorded clean-room verdict",
		slog.String("run_entity_id", runEntityID),
		slog.String("outcome", string(verdict.Outcome)),
		slog.String("profile", manifest.Profile))
	return VerifyResult{Verdict: verdict, Profile: manifest.Profile}, nil
}

// stampResult upserts verify.result on the run entity (replace-by-predicate, so a
// retry re-run replaces the prior outcome rather than appending a second one). A free
// function so every caller of RunVerify shares one writer of verify.result (G5).
func stampResult(ctx context.Context, writer agentictools.OwnedFactWriter, runEntityID string, outcome verify.Outcome) error {
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  ResultPredicate,
		Object:     string(outcome),
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil)
}
