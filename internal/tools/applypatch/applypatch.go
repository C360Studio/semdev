// Package applypatch is the apply_patch tool (sandbox capability, SB6 — the
// code-authoring mechanism semdev entirely lacked). The developer (Amelia) authors a
// fix by emitting a unified diff; this tool applies it to the run's isolated CHECKOUT
// (path-guarded to inside the checkout, never the host) and reports which files it
// touched. It measures NOTHING (G3): the model supplies the intelligence (the diff),
// and the harness measures the real pass/fail SEPARATELY (measure_task) — the schema
// takes only the diff, never an outcome, so a developer can never assert its own
// change works. It stamps no fact and fires no lifecycle transition (G2): it is a
// pure checkout mutation the dev loop's subsequent measure/floors steps read.
package applypatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semstreams/agentic"
)

// ToolName is the registered tool name.
const ToolName = "apply_patch"

// Patcher applies a unified diff to a run's checkout, path-guarded, returning the
// repo-relative files it touched. It is the runspace.Patcher seam; nil at M0
// schema-only registration (Execute fails loudly rather than silently skipping).
type Patcher interface {
	Apply(ctx context.Context, runEntityID, diff string) ([]string, error)
}

// Executor is the apply_patch tool.
type Executor struct {
	patcher Patcher
	logger  *slog.Logger
}

// New builds the apply_patch executor. patcher is nil for schema-only registration
// (the censuses scan ListTools without a live checkout); Execute fails loudly if it
// is missing — a change that cannot be applied is a park, never a silent skip (SB5).
func New(patcher Patcher, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{patcher: patcher, logger: logger}
}

// Execute applies the developer's diff to the run's checkout and reports the touched
// files. It measures no outcome (G3). A rejected diff — path escape, malformed, or a
// clean-apply failure — returns an error the developer sees and re-authors from.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.patcher == nil {
		return errResult(call, agentic.ToolErrorInternal, "apply_patch: harness not fully wired (patcher)")
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

	touched, err := e.patcher.Apply(ctx, runEntityID, args.Diff)
	if err != nil {
		// A bad/escaping/conflicting diff is INVALID MODEL INPUT the developer re-authors
		// from — ToolErrorInvalidArgs (matching the sibling tools), not an executor bug
		// (ToolErrorInternal) and not a retryable transport fault. A security-relevant
		// escape rejection must not be recorded as indistinguishable from a harness bug.
		return errResult(call, agentic.ToolErrorInvalidArgs, "apply_patch: %v", err)
	}

	e.logger.Info("apply_patch applied a diff to the checkout",
		slog.String("run_entity_id", runEntityID),
		slog.Any("files", touched))

	body, _ := json.Marshal(map[string]any{"applied": true, "files": touched})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(body), StopLoop: true}, nil
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
