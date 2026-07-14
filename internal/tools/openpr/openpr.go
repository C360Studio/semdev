// Package openpr is the open_pr tool (forge-io, group 8D): the run's DELIVERY step. When
// the coherence gate (check_coherence) has cleared a run — the clean-room verify passed, the
// change validated, and every task was approved — this records pr.ref, the reference to the
// delivered PR. It is the terminal of the m0 issue→PR arc.
//
// M0 HONESTY (design): the arc terminates here; the real forge-io PR (a live GitHub/GitLab
// PR with a URL) lands at M2 behind the forge-io adapter. So at M0 pr.ref is a DETERMINISTIC
// LOCAL delivery reference, not a live PR URL. This is NOT semspec's placeholder-pass: the
// gate that got here GENUINELY passed (a real cold-container verify=pass, a real approved
// verdict, a real openspec.validated) — only the delivery TARGET is a local stub, a declared
// M0 non-goal. semspec faked the verify OUTCOME (a placeholder that WAS the pass); here the
// outcome is real and only the transport is stubbed.
//
// G3: the schema takes no arguments — the model may only trigger delivery, never supply the
// ref (the harness forms it). G2: it stamps pr.ref and fires no lifecycle transition (a rule
// closing the run reads pr.ref). Single G5 writer of pr.ref (open-pr).
package openpr

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

// ToolName is the registered tool name and the run's delivery handler.
const ToolName = "open_pr"

// Source is stamped on the pr.ref triple. It MUST equal the writer declared for pr.ref in
// internal/vocab (G5) — a conformance pin cross-checks it. (The vocab writer was reconciled
// from the placeholder pr-delivery-adapter to this real Source at 8D.)
const Source = "open-pr"

// RefPredicate is the terminal delivery fact this tool owns on the run entity. Exact
// predicate → latest-wins.
const RefPredicate = "pr.ref"

// localStubPrefix marks pr.ref as an M0 LOCAL delivery reference, not a live forge PR URL —
// so a reader (or a human) can tell an M0 stub from an M2 real PR at a glance. The forge-io
// adapter replaces this with the live PR URL at M2.
const localStubPrefix = "local-delivery:"

// Executor stamps pr.ref for a coherent run.
type Executor struct {
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// New builds the open_pr executor. writer may be nil for schema-only registration; Execute
// fails loudly if it is nil.
func New(writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{writer: writer, logger: logger}
}

// Execute stamps pr.ref (an M0 local delivery reference) on the run entity. It takes no
// arguments (G3) and fires no transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "open_pr: harness not fully wired (writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "open_pr: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	ref, err := Deliver(ctx, e.writer, runEntityID)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "open_pr: stamp %s on %s: %v", RefPredicate, runEntityID, err)
	}

	e.logger.Info("open_pr recorded the delivery reference",
		slog.String("run_entity_id", runEntityID), slog.String("pr_ref", ref))

	summary, _ := json.Marshal(map[string]any{"pr_ref": ref, "delivered": true})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// Deliver stamps pr.ref (the M0 deterministic LOCAL delivery reference) on the run
// entity and returns the ref. It is the shared delivery core: the transitional
// open_pr TOOL calls it (above), and the delivery STATION component (R6) calls it
// off a rule publish — one writer of pr.ref (G5, Source == open-pr), one place the
// M0 stub form lives, so the tool and the component cannot drift. It fires no
// lifecycle transition (G2); a rule reading pr.ref closes the run.
func Deliver(ctx context.Context, writer agentictools.OwnedFactWriter, runEntityID string) (string, error) {
	// The M0 delivery reference is deterministic from the run — a stub proving the coherent
	// run reached delivery; the M2 forge-io adapter replaces it with a live PR URL.
	ref := localStubPrefix + runEntityID
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  RefPredicate,
		Object:     ref,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil); err != nil {
		return "", err
	}
	return ref, nil
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
