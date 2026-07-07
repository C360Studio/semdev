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
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/devtask"
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
	// The slug is model-supplied and becomes both a fact-predicate namespace and,
	// downstream, a changes/<slug>/ filesystem path — reject a traversal slug at
	// the authoring source so nothing further down (write_change) must re-guard it.
	if err := openspec.ValidateSlug(p.Slug); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "create_change: %v", err)
	}

	change, richTasks := p.toChange()
	now := time.Now().UTC()
	// Thin facts (via the format engine) and the execution-rich, graph-only per-task
	// facts are stamped TOGETHER in one atomic replace, so create_change's ownership
	// of the whole openspec.change.<slug>.* package stays whole (design D14 / the
	// architect's ruling — mock the intelligence, not the plumbing). richTasks are
	// indexed by toChange as they enter change.Tasks, so their <i> equals the thin
	// facts' <i> by construction.
	triples := changeTriples(runEntityID, change, now)
	triples = append(triples, richTaskTriples(runEntityID, p.Slug, richTasks, now)...)
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

// richTaskTriples emits the execution-rich, graph-only per-task facts create_change
// authors ALONGSIDE the format engine's thin task facts — the fields dev-from-task
// needs (target_files, test_command, assumptions, non_goals, budget) that OpenSpec's
// thin tasks.md does not carry.
//
// Each rich task carries the flat index toChange assigned it as it entered
// change.Tasks, so its <i> equals the thin facts' <i> BY CONSTRUCTION — one walk
// assigns the index, no second flatten to drift (design D14). That <i> is the
// projector's contiguous RawTask.Index.
//
// Presence is preserved and load-bearing: an ABSENT field emits NO fact, so the
// projector reads nil (a gap) and fails toward the human; an authored-empty
// assumptions/non_goals emits "[]" (a real value the projector accepts). This tool
// does NOT validate the Karpathy schema — it records what was authored; the
// projector owns gap detection (division of labor).
func richTaskTriples(runEntityID, slug string, tasks []richTask, now time.Time) []message.Triple {
	base := openspec.ChangeEntityPrefix(slug) + "task."
	var out []message.Triple
	mk := func(pred, obj string) {
		out = append(out, message.Triple{
			Subject:    runEntityID,
			Predicate:  pred,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		})
	}
	for _, rt := range tasks {
		p := base + strconv.Itoa(rt.Index) + "."
		if rt.TargetFiles != nil {
			mk(p+devtask.FactTargetFiles, jsonArray(rt.TargetFiles))
		}
		if strings.TrimSpace(rt.TestCommand) != "" {
			mk(p+devtask.FactTestCommand, rt.TestCommand)
		}
		if rt.Assumptions != nil {
			mk(p+devtask.FactAssumptions, jsonArray(rt.Assumptions))
		}
		if rt.NonGoals != nil {
			mk(p+devtask.FactNonGoals, jsonArray(rt.NonGoals))
		}
		if rt.Budget != nil {
			mk(p+devtask.FactBudget, strconv.Itoa(*rt.Budget))
		}
	}
	return out
}

// jsonArray encodes a string slice as its JSON array form — the graph's string
// form for a list, the same shape the projector reads back.
func jsonArray(xs []string) string {
	b, _ := json.Marshal(xs)
	return string(b)
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
