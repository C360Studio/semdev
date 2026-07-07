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
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cleanroom"
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

// verifyTimeout bounds one clean-room proof step (resolve or test).
const verifyTimeout = 15 * time.Minute

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

// gatherEvidence runs the clean-room proof and folds it into an evidence-only
// verify.Input. It is where the harness draws the transport-vs-genuine line: a
// provisioning fault, a step that could not run, or a resolve failure the classifier
// reads as transport all set Completed=false (→ Retry); a genuine resolve failure is
// Resolved=false (→ Fail). Completed becomes true only once the proof reaches a
// definitive artifact conclusion (a genuine resolve failure, or the tests ran).
func (e *Executor) gatherEvidence(ctx context.Context, root string, m harness.Manifest) verify.Input {
	sb, err := e.runner.Up(ctx, root, m.CacheHomeEnvs)
	if err != nil {
		return verify.Input{Completed: false, TransportError: "could not provision clean-room isolation: " + err.Error()}
	}
	defer func() { _ = e.runner.Down(ctx, sb) }()

	in := verify.Input{
		FreshCacheHome:  len(sb.CacheHomes) > 0,
		CacheHomeDetail: fmt.Sprintf("%d fresh cache home(s): %s", len(sb.CacheHomes), strings.Join(sb.CacheHomes, ", ")),
	}

	// Resolve step. A run error is transport; a non-zero exit is classified.
	resolveRes, err := e.exec(ctx, sb, m.ResolveCmd)
	if err != nil {
		in.Completed = false
		in.TransportError = "resolve step could not run: " + err.Error()
		return in
	}
	switch class, detail := cleanroom.ClassifyResolve(resolveRes); class {
	case cleanroom.ResolveTransport:
		in.Completed = false
		in.TransportError = detail
		return in
	case cleanroom.ResolveFailed:
		// The proof completed with a definitive answer: the artifact's declarations
		// do not resolve cold (a missing/fabricated coordinate). No point running
		// tests — Decide fails on resolution.
		in.Completed = true
		in.Resolved = false
		in.ResolveDetail = detail + excerpt(resolveRes)
		return in
	default: // ResolveOK
		in.Resolved = true
		in.ResolveDetail = detail
	}

	// Test step, in the same fresh isolation.
	testRes, err := e.exec(ctx, sb, m.TestCmd)
	if err != nil {
		in.Completed = false
		in.TransportError = "test step could not run: " + err.Error()
		return in
	}
	in.Completed = true
	in.TestsPassed = testRes.ExitCode == 0
	in.TestsDetail = excerptOr(testRes, "the artifact's own tests ran in fresh isolation")
	return in
}

// exec runs one manifest step under a per-step timeout. An empty command is a
// harness misconfiguration (transport-class: the step could not run).
func (e *Executor) exec(ctx context.Context, sb cleanroom.Sandbox, argv []string) (cleanroom.Result, error) {
	if len(argv) == 0 {
		return cleanroom.Result{}, fmt.Errorf("manifest declares an empty command")
	}
	stepCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	return e.runner.Exec(stepCtx, sb, argv)
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

// excerpt returns a short trailing excerpt of a failed step's output for the detail.
func excerpt(r cleanroom.Result) string {
	out := strings.TrimSpace(r.Stderr)
	if out == "" {
		out = strings.TrimSpace(r.Stdout)
	}
	if out == "" {
		return ""
	}
	const maxLen = 400
	if len(out) > maxLen {
		// Cut to the trailing maxLen bytes, then drop a partial leading rune the byte
		// cut may have split, so the detail is always valid UTF-8.
		out = strings.ToValidUTF8(out[len(out)-maxLen:], "")
	}
	return ": " + out
}

func excerptOr(r cleanroom.Result, fallback string) string {
	if e := excerpt(r); e != "" {
		return strings.TrimPrefix(e, ": ")
	}
	return fallback
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
