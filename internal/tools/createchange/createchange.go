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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/types"
)

// ToolName is the registered tool name and the coordinator's create_change action
// handler.
const ToolName = "create_change"

// Source is the value stamped on every triple this tool writes. It MUST equal the
// single writer declared for openspec.change.* in internal/vocab (G5) — a
// conformance pin cross-checks it, so the sole-writer claim is verifiable, not a
// comment.
const Source = "create-change-author-tool"

// AuthoredPredicate is the fixed marker create_change stamps on the AUTHORING
// LOOP entity (value = the slug) once the change facts land on the run. It is the
// slug-independent "a change was authored here" signal the validate station's
// rule fires on — the run's own facts are slug-namespaced (openspec.change.<slug>.*),
// which no rule condition can wildcard-match. It lives on the LOOP (not the run)
// so the validate rule fires on an entity that carries agent.run for run_scope=inherit.
// It is under the openspec.change.* vocab namespace, so its single writer stays
// create-change-author-tool (G5) with no new vocabulary (G9).
const AuthoredPredicate = "openspec.change.authored"

// revisionSubkey is the slug-scoped content-revision sub-key
// (openspec.change.<slug>.revision) — the same revision value, keyed under the
// change's own package so project_tasks can bind BOTH the slug and the content:
// it freezes task.spec only when openspec.validated equals THIS slug's current
// revision, so a re-authored-but-not-revalidated or alternate change is refused.
const revisionSubkey = "revision"

// SlugRevisionPredicate returns the slug-scoped content-revision predicate
// (openspec.change.<slug>.revision) create_change stamps and project_tasks reads.
func SlugRevisionPredicate(slug string) string {
	return openspec.ChangeEntityPrefix(slug) + revisionSubkey
}

// Revision returns a deterministic content revision over a change's authored
// facts: the sorted (predicate, object) pairs hashed with SHA-256, prefixed
// "sha256:" so the value is never mistaken for a number by the rule engine's
// numeric-first comparison. It is stable across re-authors of identical content
// and changes iff the content changes, so a validated revision can detect a
// superseded change (D15 #0). The revision facts themselves are NOT part of the
// input (Execute computes this over the content triples before appending them),
// so it is not self-referential.
func Revision(contentTriples []message.Triple) string {
	pairs := make([]string, 0, len(contentTriples))
	for _, t := range contentTriples {
		obj, _ := t.Object.(string)
		// Length-prefix both fields so the encoding is injective for ANY bytes in
		// predicate/object — no delimiter can be forged inside a value to make
		// differing content collide (robust even if a future object carries a
		// binary/base64 blob).
		pairs = append(pairs, fmt.Sprintf("%d:%s%d:%s", len(t.Predicate), t.Predicate, len(obj), obj))
	}
	sort.Strings(pairs)
	h := sha256.New()
	for _, p := range pairs {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Executor stamps authored change content onto the run entity as facts.
type Executor struct {
	writer   agentictools.OwnedFactWriter
	platform types.PlatformMeta
	logger   *slog.Logger
}

// New builds the create_change executor. writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if it is nil. platform builds the authoring loop's entity
// ID for the authored marker.
func New(writer agentictools.OwnedFactWriter, platform types.PlatformMeta, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{writer: writer, platform: platform, logger: logger}
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

	// Stamp the slug-scoped content revision (D15 #0) so a re-author
	// self-invalidates a stale openspec.validated: validate_change echoes this into
	// openspec.validated on PASS, and project_tasks freezes task.spec only when the
	// two match, so a re-authored-but-not-revalidated change is refused. Computed
	// over the content facts above (NOT the revision fact) so it is deterministic
	// and not self-referential, and rides the SAME atomic replace as the content so
	// it cannot skew from it. (The change-approval GATE also needs to require this
	// freshness, but the gate is slug-blind and the engine has no warn-free
	// slug-independent field-to-field compare — see design D15 #0 / semstreams
	// #519; deferred to a forward-contract, and project_tasks carries the reachable
	// guard meanwhile.)
	rev := Revision(triples)
	triples = append(triples, revisionTriple(runEntityID, SlugRevisionPredicate(p.Slug), rev, now))

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

	// Stamp the authored marker on THIS loop entity (value = slug) so the validate
	// station's rule can fire and inherit the run anchor. The change facts (the
	// substance) are written FIRST; the marker (the chaining signal) follows, so a
	// marker-write failure surfaces only AFTER the change is durably recorded.
	//
	// Failure posture: a marker-write error returns via errResult WITHOUT StopLoop,
	// so the loop retries this turn (tool_choice=function re-forces create_change,
	// which re-stamps the same facts idempotently) until the marker lands or
	// MaxIterations trips and the loop fails — never a silent green. The two writes
	// are NOT atomic across entities: a process crash BETWEEN them leaves the change
	// authored but unmarked, so the run stalls in executing with no validate chain
	// (M0-acceptable; no Go backstop ticker by design — the escalation belongs
	// upstream, G2). LoopID is always present at runtime; a missing one (unit-test-
	// only) skips the marker with a loud warn rather than failing an authored change.
	if call.LoopID == "" {
		e.logger.Warn("create_change: no loop_id on the tool call — skipping the authored marker; the validate station will not trigger for this change",
			"slug", p.Slug, "run_entity", runEntityID)
	} else {
		loopEntityID, err := agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if err != nil {
			return errResult(call, agentic.ToolErrorInternal, "create_change: construct authoring loop entity id: %v", err)
		}
		marker := []message.Triple{{
			Subject:    loopEntityID,
			Predicate:  AuthoredPredicate,
			Object:     p.Slug,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		}}
		if err := e.writer.ReplaceTriples(ctx, loopEntityID, marker, []string{AuthoredPredicate}); err != nil {
			return errResult(call, writeErrKind(err), "create_change: stamp %s on %s: %v", AuthoredPredicate, loopEntityID, err)
		}
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

// revisionTriple builds one content-revision fact on the run entity (Source ==
// the openspec.change.* writer, G5), replace-by-predicate on re-author.
func revisionTriple(runEntityID, predicate, rev string, now time.Time) message.Triple {
	return message.Triple{
		Subject:    runEntityID,
		Predicate:  predicate,
		Object:     rev,
		Source:     Source,
		Timestamp:  now,
		Confidence: 1.0,
	}
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
