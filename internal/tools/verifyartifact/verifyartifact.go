// Package verifyartifact is the verify_artifact tool (clean-room-verify, G4 — the
// make-or-break gate): it proves the delivered artifact COLD and stamps the terminal
// verify.result. No run reaches open_pr until this records a pass. It is the outcome
// half of the taxonomy's verify (the structural coherence half is openspec.validated
// + review.verdict, group 7).
//
// The tool GATHERS evidence; the pure verify.Decide judges it. It provisions a fresh
// clean-room sandbox with a distinct build-cache home (the universal G4 control,
// design D5), runs the artifact's OWN reproducibility-contract manifest —
// resolve-from-declarations then the artifact's own tests — and folds the results
// into an evidence-only verify.Input (G3: every field harness-measured, no model
// outcome). Crucially it draws the one line Decide cannot (verify.go): ANY
// transport/infrastructure fault — a sandbox that would not provision, a step that
// could not run, or a resolve step whose failure the cleanroom classifier reads as
// network-class — sets Completed=false so the verdict is Retry, never a terminal
// reject of a good artifact. A GENUINE resolve failure (a missing/fabricated
// coordinate a warm cache would have masked) is Resolved=false → Fail: the
// cache-masked-fabrication reject.
//
// It stamps the single verify.result scalar (pass/fail/retry), upserted latest-wins
// (a retry re-run replaces it). It fires no lifecycle transition (G2): the open_pr
// gate is a rule reading verify.result. verify.result's sole writer is verify-harness
// (G5).
package verifyartifact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the run's verify handler.
const ToolName = "verify_artifact"

// Source is stamped on the verify.result triple. It MUST equal the writer declared
// for verify.result in internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "verify-harness"

// ResultPredicate is the terminal clean-room fact this harness owns on the run
// entity. Exact predicate → latest-wins (a retry re-run upserts).
const ResultPredicate = "verify.result"

// Workspace resolves the on-disk checkout ROOT of a run's target-repo workspace —
// the artifact the clean-room proof resolves/builds/tests. Narrow seam over the
// checkout (production lands with forge-io / the Runner); nil at M0.
type Workspace interface {
	Root(ctx context.Context, runEntityID string) (string, error)
}

// Manifests resolves a checkout's reproducibility-contract manifest (how to prove it
// cold — design D6: harvested by `semdev init` into .semdev/harness.yaml, else the
// detected profile default). Narrow seam so the tool holds no harvest logic; nil at
// M0 (the live harvest/read lands with semdev init).
type Manifests interface {
	Resolve(ctx context.Context, checkoutRoot string) (harness.Manifest, error)
}

// Executor runs the clean-room proof and stamps verify.result.
type Executor struct {
	runner    cleanroom.Runner
	workspace Workspace
	manifests Manifests
	writer    agentictools.OwnedFactWriter
	logger    *slog.Logger
}

// New builds the verify_artifact executor. runner is always supplied (LocalRunner in
// production); workspace/manifests/writer may be nil for schema-only registration
// (the censuses inspect ListTools without a live checkout or NATS client). Execute
// fails loudly if any dependency is missing.
func New(runner cleanroom.Runner, workspace Workspace, manifests Manifests, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{runner: runner, workspace: workspace, manifests: manifests, writer: writer, logger: logger}
}

// Execute provisions fresh isolation, runs the artifact's own resolve+test cold,
// judges the evidence with verify.Decide, and stamps verify.result.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.runner == nil || e.workspace == nil || e.manifests == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: harness not fully wired (runner/workspace/manifests/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	root, err := e.workspace.Root(ctx, runEntityID)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: resolve workspace root: %v", err)
	}
	manifest, err := e.manifests.Resolve(ctx, root)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "verify_artifact: resolve reproducibility manifest: %v", err)
	}

	input := e.gatherEvidence(ctx, root, manifest)
	verdict := verify.Decide(input)

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
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// gatherEvidence runs the clean-room proof (resolve + the artifact's own TESTS) in one
// fresh-isolation sandbox via the shared cold-build core (coldproof), and folds the
// neutral evidence into verify.Input. coldproof is where the harness draws the
// transport-vs-genuine line — shared with the provision-time baseline so a fabrication
// reads identically in both proofs.
func (e *Executor) gatherEvidence(ctx context.Context, root string, m harness.Manifest) verify.Input {
	// nil scrubber: at M0 the verify runner is wired with no governed secrets. When a
	// secret-aware runner lands here, thread the matching secrets.NewScrubber(secretEnv)
	// so any echoed secret is redacted from this tool's surfaced result (G7).
	return coldproof.Gather(ctx, e.runner, root, m.CacheHomeEnvs, m.ResolveCmd, m.TestCmd, nil).ToVerifyInput()
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
