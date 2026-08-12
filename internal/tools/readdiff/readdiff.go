// Package readdiff is the read_diff tool (design simplify-m0-execution-rail, R7/task 4.3):
// the reviewer's (Quinn's) window onto the cumulative change a run has authored. The
// checkout is a real git repository (runspace.Checkouts.Materialize git-inits it and commits
// the pristine source; every apply_patch commits the applied attempt), so the cumulative
// authored diff is exactly `git diff <base>..HEAD` over the run's checkout — and under the
// M0 one-in-flight serialization invariant (design R9: honestly single-task, strictly serial
// attempts) HEAD is always the latest committed attempt (attempt.commit, apply_patch's
// stamped pointer). No existing primitive can put the authored diff into a loop (G1): a rule
// only routes/aggregates facts, and prompt templating carries triples, not diff bytes (R7).
//
// It is READ-ONLY: it stamps no fact (there is nothing to measure — G3 does not apply to a
// read) and fires no lifecycle transition (G2). Its schema takes NO arguments — the run is
// resolved from the tool call's metadata, never a model-supplied path or ref.
//
// It runs INSIDE Quinn's multi-turn auto review loop (R2/R7): unlike the forced
// single-turn stations it does NOT set StopLoop on success — she reads the diff, may also
// read_workspace individual files, and then calls submit_review in the same turn sequence.
// A diff larger than the 32KB tool-result cap is truncated (with a note); read_diff, unlike
// read_workspace, does not paginate — the reviewer's other read tool (read_workspace) is the
// finer-grained fallback for a file too large to see in the unified diff alone.
//
// M1 (adversarial review): a raw-byte cap on the diff alone does not bound the MARSHALED
// result. JSON string-escaping (quotes, backslashes, tabs, newlines, control bytes needing
// \u00XX — all realistic in a unified diff) can expand content by up to 6x, so a diff capped
// only at the raw byte level can marshal past the framework's ToolResultMaxBytes (32768),
// silently truncating the JSON tail — exactly where `truncated` sits (Go marshals map keys
// alphabetically after `diff`). fitEnvelope fixes this by measuring the ACTUAL marshaled
// size and shrinking the diff field (recomputing bytes/truncated for the shrunk length)
// until it fits — correct regardless of the diff's escape ratio.
package readdiff

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semstreams/agentic"
)

// ToolName is the registered tool name.
const ToolName = "read_diff"

// resultBudget bounds the FINAL MARSHALED JSON tool result, not the raw diff. The framework
// enforces ToolResultMaxBytes=32768 on the marshaled envelope; JSON string-escaping can
// expand raw content by up to 6x, so a fixed raw-content cap alone cannot guarantee the
// envelope fits — only measuring the actual marshaled size and shrinking to it (fitEnvelope)
// does (M1). 32000 leaves ~768 bytes of margin under the framework's hard cap for the
// envelope's own keys and braces.
const resultBudget = 32000

// Differ resolves the run's cumulative authored diff — base..HEAD over the run's committed
// checkout. Satisfied by *runspace.Checkouts (its Diff method). nil at schema-only
// registration (the censuses scan ListTools without a live checkout); Execute fails loudly
// if it is nil. Diff FAILS CLOSED when no checkout has been materialized for the run — the
// tool surfaces the error and the run parks, never a silent guess (SB5).
type Differ interface {
	Diff(ctx context.Context, runEntityID string) (string, error)
}

// Executor is the read_diff tool.
type Executor struct {
	differ Differ
	logger *slog.Logger
}

// New builds the read_diff executor. differ is nil for schema-only registration (the
// censuses scan ListTools without a live checkout); Execute fails loudly if it is missing —
// a review the harness cannot ground in a diff is a park, never a silent skip.
func New(differ Differ, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{differ: differ, logger: logger}
}

// Execute returns the run's cumulative authored diff (base..HEAD over the committed
// checkout), capped under the tool-result byte limit. It stamps nothing (G3) and fires no
// transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.differ == nil {
		return errResult(call, agentic.ToolErrorInternal, "read_diff: harness not fully wired (differ)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "read_diff: %s missing on the tool call — cannot target the run's checkout", agentic.MetadataKeyRunEntityID)
	}

	diff, err := e.differ.Diff(ctx, runEntityID)
	if err != nil {
		// The run has no checkout, no committed attempt, or git itself failed — a
		// harness/environment fault the reviewer cannot fix by re-calling with
		// different arguments (there are none), so it fails loud rather than as
		// invalid model input.
		return errResult(call, agentic.ToolErrorInternal, "read_diff: resolve run diff: %v", err)
	}

	build := func(c string) map[string]any {
		return map[string]any{
			"diff":      c,
			"bytes":     len(c),
			"truncated": len(c) < len(diff),
		}
	}
	out, ferr := fitEnvelope(resultBudget, diff, build)
	if ferr != nil {
		return errResult(call, agentic.ToolErrorInternal, "read_diff: encode result: %v", ferr)
	}
	// No StopLoop: read_diff runs INSIDE Quinn's multi-turn auto loop (R2/R7) — she
	// reads and continues her own turn (read_workspace, then submit_review) rather
	// than the harness forcibly ending it.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(out)}, nil
}

// fitEnvelope marshals build(content) and, if the result exceeds budget bytes, shrinks
// content (dropping trailing bytes) and re-marshals — looping until the envelope fits or
// content is empty (M1). JSON string-escaping (quotes, backslashes, tabs, newlines, and
// other control bytes needing \u00XX) can expand raw bytes by up to 6x, so a FIXED raw-byte
// cap on content cannot guarantee the MARSHALED envelope stays under the framework's
// ToolResultMaxBytes — only measuring the actual marshaled size and shrinking to fit does.
// build receives the (possibly shrunk) content and must derive every length-dependent field
// (bytes, truncated) from THAT content, not the original, so each iteration's envelope is
// internally consistent on its own.
func fitEnvelope(budget int, content string, build func(content string) map[string]any) ([]byte, error) {
	for {
		out, err := json.Marshal(build(content))
		if err != nil {
			return nil, err
		}
		if len(out) <= budget || content == "" {
			return out, nil
		}
		overshoot := len(out) - budget
		newLen := max(len(content)-overshoot, 0)
		content = content[:newLen]
	}
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
