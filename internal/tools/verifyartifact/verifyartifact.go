// Package verifyartifact is the verify_artifact tool (clean-room-verify, G4 — the
// make-or-break gate): it proves the COMMITTED artifact COLD, in a fresh throwaway
// container over a fresh clone with a fresh dep cache, and stamps the terminal
// verify.result. No run reaches open_pr until this records a pass. It is the outcome
// half of the taxonomy's verify (the structural coherence half is openspec.validated +
// review.verdict, group 7/8).
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
// The tool GATHERS evidence; the pure verify.Decide judges it (inside ProveArtifact,
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
// takes no arguments (G3 — the model may only trigger the proof, never supply its
// outcome).
package verifyartifact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the run's verify handler.
const ToolName = "verify_artifact"

// Source is stamped on the verify.result triple. It MUST equal the writer declared for
// verify.result in internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "verify-harness"

// ResultPredicate is the terminal clean-room fact this harness owns on the run entity. Exact
// predicate → latest-wins (a retry re-run upserts). The rule-native delivery route reads it
// directly (08a coherent on eq "pass", 08b blocked on eq "fail"; a "retry" matches neither),
// which is why verify_artifact no longer stamps a loop chaining marker (the reshape, R1).
const ResultPredicate = "verify.result"

// dockerBin is the docker CLI binary the cold proof shells (matches cleanroom's and
// provision_sandbox's default). A package const keeps the surface small; a fake Prover
// exercises the fact-stamping without docker.
const dockerBin = "docker"

// VerifyClones makes a fresh COLD clone of a run's checkout — the committed artifact the
// final verify proves. It is runspace.Checkouts.CloneForVerify behind a narrow seam:
// non-destructive of the warm checkout (never re-materializes it), so the applied diff is
// preserved. nil at M0 schema-only registration; Execute fails loudly if absent.
type VerifyClones interface {
	CloneForVerify(ctx context.Context, runEntityID string) (string, error)
}

// Manifests resolves a checkout's reproducibility-contract manifest (how to prove it
// cold — the operator-declared image + resolve/test commands + cache-home envs). Narrow
// seam so the tool holds no harvest logic; nil at M0 schema-only registration.
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

// Executor runs the clean-room cold proof and stamps verify.result.
type Executor struct {
	clones    VerifyClones
	manifests Manifests
	prover    Prover
	store     secrets.Store // governed creds-refs (SB2c); nil at M0 (no secrets)
	writer    agentictools.OwnedFactWriter
	logger    *slog.Logger
}

// New builds the verify_artifact executor. prover is always supplied (a pure adapter —
// DefaultProver in production, a fake in tests); clones/manifests/writer are nil for
// schema-only registration (the censuses inspect ListTools without a live checkout or
// NATS client). store is nil at M0 (no governed secrets). It stamps only verify.result on
// the run (no loop chaining marker — the delivery route reads verify.result directly, R1).
// Execute fails loudly if any required dependency is missing.
func New(clones VerifyClones, manifests Manifests, prover Prover, store secrets.Store, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{clones: clones, manifests: manifests, prover: prover, store: store, writer: writer, logger: logger}
}

// VerifyResult is the shared verify core's outcome: the definitive cold clean-room
// verdict plus the resolved manifest profile (for the caller's summary/logging).
type VerifyResult struct {
	Verdict verify.Verdict
	Profile string
}

// RunVerify clones the run's COMMITTED artifact into a fresh dir, resolves its
// reproducibility manifest FROM THE CLONE, proves it resolves+builds+tests COLD in a
// fresh throwaway container with a fresh dependency cache, and stamps the definitive
// verify.result (pass/fail/retry) on the run. It is the shared core of the verify_artifact
// tool AND the verify station (design simplify-m0-execution-rail R6) — one writer of
// verify.result (verify-harness, G5), so the two callers cannot drift.
//
// It returns a pre-proof infra error (clone/resolve/prove-could-not-run) that stamps
// NOTHING — never a false green — for the caller to surface as retryable; or a stamped
// VerifyResult carrying the definitive verdict. A retry Verdict is stamped (evidence) but
// signals a TRANSIENT infra fault (a container that would not provision, a resolve read as
// network-class), never a terminal reject of a good artifact (SB5). Fires no lifecycle
// transition (G2); takes no caller-supplied outcome (G3).
//
// CONTRACT: clones/manifests/prover/writer must be non-nil — both callers guarantee it
// (the tool Execute nil-checks first; the station factory constructs them and fails loud on
// a nil seam). A nil logger is defended (defaults to slog.Default()).
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

// Execute is the (transitional) verify_artifact tool body: it reads the run entity from the
// tool-call metadata, runs the shared RunVerify core, and shapes the forced-loop control
// (StopLoop) around the verdict. It stamps no outcome from the caller (G3) and fires no
// transition (G2).
//
// This is a DEAD path post-reshape — the verify STATION (internal/station/verify) replaced
// the forced verify coordinator turn (R6), so no rule spawns this tool in production — but
// it is kept correct (and its tests green) until the executor is deleted in the group-6
// tool-cleanup slice (6E).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.clones == nil || e.manifests == nil || e.prover == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: harness not fully wired (clones/manifests/prover/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	res, err := RunVerify(ctx, e.clones, e.manifests, e.prover, e.store, e.writer, e.logger, runEntityID)
	if err != nil {
		// A pre-proof infra fault (clone/resolve/prove-could-not-run) stamped no verdict —
		// surface it as a retryable tool error (the forced verify loop re-runs; never a false
		// green). The graph-vs-transport classification collapses to ReadErrorKind (the sibling
		// tools' idiom); the specific kind no longer matters on this dead path.
		return errResult(call, changefacts.ReadErrorKind(err), "%v", err)
	}

	summary, _ := json.Marshal(map[string]any{
		"outcome":       res.Verdict.Outcome,
		"profile":       res.Profile,
		"checks":        res.Verdict.Checks,
		"failed_checks": res.Verdict.FailedChecks(),
	})

	// A RETRY verdict is a TRANSIENT infra fault, NOT a verdict about the artifact — the whole
	// point of the Retry classification is to NEVER terminally reject a good artifact on a
	// flake (SB5). So it RE-RUNS the cold proof rather than terminating: it does NOT StopLoop,
	// so the forced verify loop re-runs verify_artifact. The verify.result=retry stamp stays as
	// evidence (a later pass/fail upserts it). The rule-native delivery route keys on
	// verify.result eq "pass"/"fail", so a "retry" value matches NEITHER route — a single
	// docker flake never triggers delivery nor parks a good run.
	if res.Verdict.Outcome == verify.OutcomeRetry {
		return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary)}, nil
	}

	// A TERMINAL verdict (pass or fail) ends the turn (StopLoop) — the verify.result the core
	// stamped on the RUN is what the delivery route reads (08a coherent on pass, 08b blocked on
	// fail). A fail verdict is DATA, not a tool error; it ends the turn as a success too.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// stampResult upserts verify.result on the run entity (replace-by-predicate, so a
// retry re-run replaces the prior outcome rather than appending a second one). A free
// function so the tool and the verify station share one writer of verify.result (G5).
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

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
