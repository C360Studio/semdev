// Package createchange is the create_change author tool (openspec-io): it takes
// the change content a model authored and stamps it as openspec.change.* facts on
// the RUN entity, so the graph is authoritative and the artifacts are a
// projection of it. It is the graph-first "openspec new" step.
//
// It OWNS the openspec.change.<slug>.* package on the run entity and REPLACES it
// on every re-author (an author→validate→fix→re-author loop), so it takes an
// OwnedFactWriter, not an append-only TriplePublisher — appending would leave
// phantom index/rid-keyed facts and make reconstruction nondeterministic.
//
// G3: the input schema takes only the change CONTENT (proposal/deltas/tasks) —
// never an outcome/validated/pass field. G5: openspec.change.* has a single
// writer, this tool (Source == the vocab writer). D15: facts land on the run
// entity (resolved from call metadata), because the run-lifecycle rules read them
// off the run. Design authorship is deferred: M0 generates proposal → specs →
// tasks (see docs/brief.md); the payload carries no design block yet.
package createchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the coordinator's create_change action
// handler.
const ToolName = "create_change"

// Source is the value stamped on every triple this tool writes. It MUST equal the
// single writer declared for openspec.change.* in internal/vocab (G5) — a
// conformance pin cross-checks it, so the sole-writer claim is verifiable, not a
// comment.
const Source = "create-change-author-tool"

// Executor stamps authored change content onto the run entity as facts.
type Executor struct {
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// New builds the create_change executor. writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if it is nil.
func New(writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{writer: writer, logger: logger}
}

// Execute maps the authored content to a change, projects it to openspec.change.*
// facts via the format engine, and REPLACES the change's owned fact package on the
// run entity in one atomic mutation. It writes no outcome fact — validation is a
// separate harness step.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "create_change: no owned-fact writer wired")
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

	// Read the prior owned package so a re-author REPLACES it (clears stale
	// index/rid-keyed facts) rather than appending a second copy.
	prefix := openspec.ChangeEntityPrefix(p.Slug)
	prior, err := e.writer.ReadOwnedPredicates(ctx, runEntityID, prefix)
	if err != nil {
		return errResult(call, writeErrKind(err), "create_change: read owned change facts under %q on %s: %v", prefix, runEntityID, err)
	}
	if err := e.writer.ReplaceTriples(ctx, runEntityID, triples, prior); err != nil {
		return errResult(call, writeErrKind(err), "create_change: replace %d change facts on %s: %v", len(triples), runEntityID, err)
	}

	summary, _ := json.Marshal(map[string]any{"slug": p.Slug, "facts": len(triples), "run_entity": runEntityID})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
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
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		})
	}
	return out
}

// writeErrKind distinguishes a handler-classified failure (a must-exist
// entity_not_found or other graph error — internal/ordering, not retryable as
// transport) from a genuine transport failure. entity_not_found is NOT a network
// error.
func writeErrKind(err error) agentic.ToolErrorKind {
	var ce *errs.ClassifiedError
	if errors.As(err, &ce) {
		return agentic.ToolErrorInternal
	}
	return agentic.ToolErrorNetwork
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
