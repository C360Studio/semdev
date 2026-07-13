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
	"github.com/c360studio/semstreams/types"
)

// ToolName is the registered tool name and the run's verify handler.
const ToolName = "verify_artifact"

// Source is stamped on the verify.result triple AND the dev.verified loop marker. It MUST
// equal the writer declared for verify.result and dev.verified in internal/vocab (G5) — a
// conformance pin cross-checks it.
const Source = "verify-harness"

// ResultPredicate is the terminal clean-room fact this harness owns on the run
// entity. Exact predicate → latest-wins (a retry re-run upserts).
const ResultPredicate = "verify.result"

// VerifiedPredicate is the chaining marker verify_artifact stamps on ITS OWN verify loop
// entity once it has recorded a verify.result — the "this coordinator loop just verified"
// signal the coherence station (group 8D) fires on to spawn check_coherence. It rides the
// verify loop (which carries agent.run) so the coherence-trigger's run_scope=inherit binds
// to the same run. Mirrors measure_task's dev.measure_done / check_floors' dev.floors_done.
const VerifiedPredicate = "dev.verified"

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
	store     secrets.Store      // governed creds-refs (SB2c); nil at M0 (no secrets)
	platform  types.PlatformMeta // builds the verify loop's entity id for the chaining marker
	writer    agentictools.OwnedFactWriter
	logger    *slog.Logger
}

// New builds the verify_artifact executor. prover is always supplied (a pure adapter —
// DefaultProver in production, a fake in tests); clones/manifests/writer are nil for
// schema-only registration (the censuses inspect ListTools without a live checkout or
// NATS client). store is nil at M0 (no governed secrets). platform builds the verify
// loop's entity id for the dev.verified chaining marker. Execute fails loudly if any
// required dependency is missing.
func New(clones VerifyClones, manifests Manifests, prover Prover, store secrets.Store, platform types.PlatformMeta, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{clones: clones, manifests: manifests, prover: prover, store: store, platform: platform, writer: writer, logger: logger}
}

// Execute clones the run's committed artifact into a fresh dir, builds the declared image
// and proves it resolves its deps and passes its own tests cold (the test step compiles
// the artifact) in a fresh throwaway container, judges the
// evidence with verify.Decide (inside ProveArtifact), and stamps verify.result. It stamps
// no outcome from the caller (G3) and fires no transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.clones == nil || e.manifests == nil || e.prover == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: harness not fully wired (clones/manifests/prover/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	// Clone the committed artifact into a fresh dir (non-destructive of the warm checkout)
	// — the "--recursive clone" at M0. Fails closed if no checkout was materialized (the
	// run parks, never a verify over a guessed path, SB5).
	cloneRoot, err := e.clones.CloneForVerify(ctx, runEntityID)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: clone artifact for cold verify: %v", err)
	}
	// Resolve the manifest from the CLONE, so the COMMITTED Dockerfile/deps are what's
	// proven — not the warm checkout's environment.
	manifest, err := e.manifests.Resolve(ctx, cloneRoot)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: resolve reproducibility manifest: %v", err)
	}

	// Prove cold: build the image from the clone, run resolve+test in a fresh throwaway
	// container with a fresh dep cache. A returned ERROR is a pre-proof infra/declaration
	// fault (docker flake, unbuildable image) the tool surfaces as retryable — no verdict
	// is stamped, so the forced verify loop re-runs (a persistent fault stalls toward the
	// human, never a false green). A returned Verdict is a definitive result (pass/fail/
	// retry) the tool stamps.
	verdict, err := e.prover.ProveArtifact(ctx, dockerBin, cloneRoot, manifest, e.store)
	if err != nil {
		return errResult(call, agentic.ToolErrorNetwork, "verify_artifact: cold clean-room proof could not run: %v", err)
	}

	if err := e.stampResult(ctx, runEntityID, verdict.Outcome); err != nil {
		return errResult(call, writeErrKind(err), "verify_artifact: stamp %s on %s: %v", ResultPredicate, runEntityID, err)
	}

	e.logger.Info("verify_artifact recorded clean-room verdict",
		slog.String("run_entity_id", runEntityID),
		slog.String("outcome", string(verdict.Outcome)),
		slog.String("profile", manifest.Profile))

	summary, _ := json.Marshal(map[string]any{
		"outcome":       verdict.Outcome,
		"profile":       manifest.Profile,
		"checks":        verdict.Checks,
		"failed_checks": verdict.FailedChecks(),
	})

	// A RETRY verdict is a TRANSIENT infra fault (a container that would not provision, a
	// resolve read as network-class), NOT a verdict about the artifact — the whole point of
	// the Retry classification is to NEVER terminally reject a good artifact on a flake (SB5,
	// verify.go). So it must RE-RUN the cold proof, not chain forward: do NOT stamp the
	// dev.verified marker (a retry that chained to the coherence gate would fail its
	// verify.result eq pass check and PARK a good run on a single flake — the reviewer's 8D
	// MEDIUM) and do NOT StopLoop, so the forced verify loop re-runs verify_artifact. The
	// verify.result=retry stamp stays as evidence of the attempt (a later pass upserts it). A
	// persistent transport fault trips MaxIterations and stalls toward the human (the
	// dev-loop-rail cap-exhaust gap) — fail-closed, never a false green, never a park of a
	// good artifact. Only a TERMINAL verdict (pass = advance, fail = the coherence gate
	// blocks) chains via the marker.
	if verdict.Outcome == verify.OutcomeRetry {
		return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary)}, nil
	}

	// Stamp the chaining marker on THIS verify loop entity so the coherence station
	// (dev-from-task/11) can fire on it and inherit the run. The verify.result (the
	// substance) was written FIRST; the marker (the chaining signal) follows (measure_task's
	// discipline). It is stamped for a FAIL as well as a pass — a failing verify must still
	// chain to the coherence gate (which BLOCKS on the non-pass verify.result); only a
	// retry (above) or a verify ERROR (no marker) stays out of the chain. A marker error
	// returns errResult WITHOUT StopLoop so the forced loop re-runs. A missing LoopID
	// (unit-test-only) skips it.
	if call.LoopID == "" {
		e.logger.Warn("verify_artifact: no loop_id on the tool call — skipping the verified marker; the coherence station will not trigger",
			slog.String("run_entity_id", runEntityID))
	} else {
		loopEntityID, lerr := agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if lerr != nil {
			return errResult(call, agentic.ToolErrorInternal, "verify_artifact: construct verify loop entity id: %v", lerr)
		}
		marker := []message.Triple{{
			Subject:    loopEntityID,
			Predicate:  VerifiedPredicate,
			Object:     string(verdict.Outcome),
			Source:     Source,
			Timestamp:  time.Now().UTC(),
			Confidence: 1.0,
		}}
		if merr := e.writer.ReplaceTriples(ctx, loopEntityID, marker, []string{VerifiedPredicate}); merr != nil {
			return errResult(call, writeErrKind(merr), "verify_artifact: stamp %s on %s: %v", VerifiedPredicate, loopEntityID, merr)
		}
	}

	// StopLoop: the verify-trigger rule forces a single-turn verify loop; ending the turn
	// here keeps it one model call (mirrors measure/floors/gate). A fail verdict is DATA,
	// not a tool error — it ends the turn as a success too; the coherence gate reads the
	// stamped verify.result, not this StopLoop.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// stampResult upserts verify.result on the run entity (replace-by-predicate, so a
// retry re-run replaces the prior outcome rather than appending a second one).
func (e *Executor) stampResult(ctx context.Context, runEntityID string, outcome verify.Outcome) error {
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  ResultPredicate,
		Object:     string(outcome),
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil)
}

// writeErrKind mirrors the sibling tools: a handler-classified graph error is
// internal/ordering, not retryable transport.
func writeErrKind(err error) agentic.ToolErrorKind {
	return changefacts.ReadErrorKind(err)
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
