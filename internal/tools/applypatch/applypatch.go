// Package applypatch is the apply_patch tool (sandbox capability, SB6 — the
// code-authoring mechanism semdev entirely lacked). The developer (Amelia) authors a
// fix by emitting a unified diff; this tool applies it to the run's isolated CHECKOUT
// (path-guarded to inside the checkout, never the host), COMMITS the applied attempt,
// and reports which files it touched. It measures NOTHING (G3): the model supplies the
// intelligence (the diff), and the harness measures the real pass/fail SEPARATELY
// (measure_task) — the schema takes only the diff, never an outcome, so a developer can
// never assert its own change works.
//
// It stamps exactly one HARNESS fact and fires no lifecycle transition (G2): the commit
// SHA the harness created, as attempt.commit (latest-wins, its own writer patch-committer,
// G5). This is the immutable-snapshot pointer — the cold verify clones this commit and
// read_diff diffs base..this commit — so what is verified and reviewed is a committed
// tree, never the mutable warm checkout (G4/G7). The SHA is the harness's record of what
// it committed, not a model-supplied outcome, so G3 is intact.
package applypatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name.
const ToolName = "apply_patch"

// CommitPredicate is the run-entity fact apply_patch owns: the SHA of the latest committed
// attempt. Exact predicate → latest-wins (a retry's commit upserts it). The cold verify and
// read_diff read it to target the immutable snapshot rather than the warm working tree.
const CommitPredicate = "attempt.commit"

// Source is stamped on the attempt.commit triple. It MUST equal the single writer declared
// for attempt.commit in the vocabulary (G5).
const Source = "patch-committer"

// Patcher applies a unified diff to a run's checkout path-guarded, commits the result under
// the harness identity, and returns the repo-relative files it touched plus the new commit
// SHA. It is the runspace.Patcher seam; nil at M0 schema-only registration (Execute fails
// loudly rather than silently skipping).
type Patcher interface {
	Apply(ctx context.Context, runEntityID, diff string) ([]string, string, error)
}

// Executor is the apply_patch tool.
type Executor struct {
	patcher Patcher
	writer  agentictools.OwnedFactWriter
	logger  *slog.Logger
}

// New builds the apply_patch executor. patcher/writer are nil for schema-only registration
// (the censuses scan ListTools without a live checkout or NATS client); Execute fails
// loudly if either is missing — a change that cannot be applied is a park, never a silent
// skip (SB5).
func New(patcher Patcher, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{patcher: patcher, writer: writer, logger: logger}
}

// Execute applies the developer's diff to the run's checkout and reports the touched
// files. It measures no outcome (G3). A rejected diff — path escape, malformed, or a
// clean-apply failure — returns an error the developer sees and re-authors from.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.patcher == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "apply_patch: harness not fully wired (patcher/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "apply_patch: %s missing on the tool call — cannot target the run's checkout", agentic.MetadataKeyRunEntityID)
	}

	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "apply_patch: re-encode arguments: %v", err)
	}
	var args struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		// The model's arguments failed to decode — a schema/argument fault, not an
		// executor bug (ToolErrorInvalidArgs, like the sibling tools).
		return errResult(call, agentic.ToolErrorInvalidArgs, "apply_patch: decode arguments: %v", err)
	}

	touched, commitSHA, err := e.patcher.Apply(ctx, runEntityID, args.Diff)
	if err != nil {
		// A bad/escaping/conflicting diff is INVALID MODEL INPUT the developer re-authors
		// from — ToolErrorInvalidArgs (matching the sibling tools), not an executor bug
		// (ToolErrorInternal) and not a retryable transport fault. A security-relevant
		// escape rejection must not be recorded as indistinguishable from a harness bug.
		// A commit failure (applied but couldn't snapshot) also lands here — the attempt
		// did not durably land, so the developer re-authors rather than measuring a ghost.
		return errResult(call, agentic.ToolErrorInvalidArgs, "apply_patch: %v", err)
	}

	// Stamp the immutable-snapshot pointer: the SHA the harness just committed, latest-wins
	// (a retry's commit replaces it). This is the harness's record of what it committed
	// (G3-clean — not a model outcome), the single writer of attempt.commit (G5).
	//
	// Failure posture: the commit has ALREADY landed (HEAD advanced) by the time we stamp,
	// so a stamp failure leaves a committed-but-unrecorded attempt. We return the error
	// WITHOUT StopLoop (the sibling posture to measure_task) so the loop stays alive rather
	// than advancing to a verify that would clone a tree whose pointer is unrecorded. The
	// writer already retried transient no-responders internally, so a surfaced error is
	// persistent: a re-emitted IDENTICAL diff fails `git apply` (already applied), so only a
	// fresh corrective diff recovers — otherwise the loop churns to its cap and escalates
	// toward the human. Fail-closed either way (never a silent green over a wrong tree).
	commit := []message.Triple{{
		Subject:    runEntityID,
		Predicate:  CommitPredicate,
		Object:     commitSHA,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}}
	if werr := e.writer.ReplaceTriples(ctx, runEntityID, commit, []string{CommitPredicate}); werr != nil {
		return errResult(call, changefacts.ReadErrorKind(werr), "apply_patch: stamp %s on %s: %v", CommitPredicate, runEntityID, werr)
	}

	e.logger.Info("apply_patch applied a diff to the checkout and committed the attempt",
		slog.String("run_entity_id", runEntityID),
		slog.String("attempt_commit", commitSHA),
		slog.Any("files", touched))

	// NO StopLoop (the reshape, group 4): apply_patch runs INSIDE Amelia's bounded
	// multi-turn loop (tool_choice=auto). The tool result — the applied files + the commit
	// SHA — is her feedback; she continues (typically to measure_task) rather than the loop
	// ending here. The loop terminates when she stops calling tools (success) or hits the
	// component iteration cap (failed); routing reads the harness-stamped facts, never her
	// stopping decision (G3).
	body, _ := json.Marshal(map[string]any{"applied": true, "files": touched, "commit": commitSHA})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(body)}, nil
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
