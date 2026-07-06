// Package createchange is the create_change author tool (openspec-io): it takes
// the change content a model authored and stamps it as openspec.change.* facts on
// the RUN entity, so the graph is authoritative and the artifacts are a
// projection of it. It is the graph-first "openspec new" step.
//
// G3: the input schema takes only the change CONTENT (proposal/deltas/tasks) —
// never an outcome/validated/pass field. G5: openspec.change.* has a single
// writer, this tool. D15: facts land on the run entity (resolved from call
// metadata), because the run-lifecycle rules read them off the run.
package createchange

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/message"
)

// ToolName is the registered tool name and the coordinator's create_change action
// handler.
const ToolName = "create_change"

const toolSource = "create_change-author-tool"

// Executor stamps authored change content onto the run entity as facts.
type Executor struct {
	publisher agentictools.TriplePublisher
	logger    *slog.Logger
}

// New builds the create_change executor. publisher may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if it is nil.
func New(publisher agentictools.TriplePublisher, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{publisher: publisher, logger: logger}
}

// Execute maps the authored content to a change, projects it to openspec.change.*
// facts via the format engine, and stamps every fact on the run entity in one
// atomic batch. It writes no outcome fact — validation is a separate harness step.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.publisher == nil {
		return errResult(call, agentic.ToolErrorInternal, "create_change: no triple publisher wired")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "create_change: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "create_change: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "create_change: decode arguments: %v", err)
	}
	if p.Slug == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "create_change: slug is required")
	}

	change := p.toChange()
	triples := changeTriples(runEntityID, change, time.Now().UTC())
	if len(triples) == 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "create_change: authored change %q produced no facts", p.Slug)
	}

	if err := e.publisher.AddTriplesBatch(ctx, triples); err != nil {
		return errResult(call, agentic.ToolErrorNetwork, "create_change: stamp %d change facts on %s: %v", len(triples), runEntityID, err)
	}

	summary, _ := json.Marshal(map[string]any{"slug": p.Slug, "facts": len(triples), "run_entity": runEntityID})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary)}, nil
}

// changeTriples projects a change to openspec.change.* facts, each stamped on the
// run entity. Object is the engine's string form (prose, scalars, JSON arrays).
func changeTriples(runEntityID string, change *openspec.Change, now time.Time) []message.Triple {
	facts := change.Facts()
	out := make([]message.Triple, 0, len(facts))
	for _, f := range facts {
		out = append(out, message.Triple{
			Subject:    runEntityID,
			Predicate:  f.Predicate,
			Object:     f.Object,
			Source:     toolSource,
			Timestamp:  now,
			Confidence: 1.0,
		})
	}
	return out
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
