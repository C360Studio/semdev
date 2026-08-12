// Package createchange is the create_change author tool (openspec-io): it takes
// the change content a model authored and stamps it as openspec.change.* facts on
// the RUN entity, so the graph is authoritative and the artifacts are a
// projection of it. It is the graph-first "openspec new" step.
//
// It OWNS the openspec.change.* package on the run entity and REPLACES it on every
// re-author (an author→validate→fix→re-author loop), so it writes through its
// contract-bound projection client (graphown.Writer), not an append-only
// TriplePublisher — appending would leave phantom facts and make reconstruction
// nondeterministic.
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
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/openspec"
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

// SlugPredicate is the run-level pointer to this run's change slug
// (openspec.change.slug = <slug>). Every other change fact is slug-SCOPED
// (openspec.change.<slug>.*), so its slug lives in the predicate KEY, which no
// rule condition can wildcard-read. The approval-triggered projection rule
// (dev-from-task/03) fires on the RUN entity and needs the slug as a VALUE to
// thread into the forced project_tasks call, so create_change — which knows the
// slug and owns the namespace — stamps this slug-independent pointer alongside the
// package. It rides the openspec.change.* vocab namespace (single writer stays
// create-change-author-tool, G5; no new vocabulary, G9). It sits OUTSIDE the
// slug-scoped package prefix, so it is added to the replace's removePredicates
// explicitly (below) to stay a clean upsert across a re-author.
const SlugPredicate = "openspec.change.slug"

// RevisionPredicate is the run-level content revision (openspec.change.revision) —
// create_change stamps it and project_tasks reads it to bind task.spec to the VALIDATED
// content: the projector freezes task.spec only when openspec.change.validated equals this
// revision, so a re-authored-but-not-revalidated change is refused (D15 #0). Single-change
// at M0 (beta.147 D1): the slug is out of the key (was openspec.change.<slug>.revision).
const RevisionPredicate = "openspec.change.revision"

// Revision returns a deterministic content revision over the authored change document:
// its JSON hashed with SHA-256, prefixed "sha256:" so the value is never mistaken for a
// number by the rule engine's numeric-first comparison. Go's json.Marshal is deterministic
// for structs, so it is stable across re-authors of identical content and changes iff the
// content changes, letting a validated revision detect a superseded change (D15 #0). The
// revision/slug facts are NOT part of the hashed document, so it is not self-referential.
func Revision(document string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(document))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Executor stamps authored change content onto the run entity as facts.
type Executor struct {
	writer   *graphown.Writer
	platform types.PlatformMeta
	logger   *slog.Logger
}

// New builds the create_change executor. writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if it is nil. platform builds the authoring loop's entity
// ID for the authored marker.
func New(writer *graphown.Writer, platform types.PlatformMeta, logger *slog.Logger) *Executor {
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
	if change.Proposal == nil && change.Design == nil && change.Tasks == nil && len(change.Deltas) == 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "create_change: authored change %q has no content", p.Slug)
	}
	now := time.Now().UTC()

	// Serialize the WHOLE authored change — the openspec.Change model AND the
	// execution-rich, graph-only per-task fields — into ONE scalar document (beta.147 D3).
	// The old openspec.change.<slug>.delta.<cap>.<rid>.<field> tree is not canonicalizable;
	// a change is one artifact, not hundreds of independent facts. richTasks are indexed by
	// toChange as they enter change.Tasks, so their <i> aligns with the thin task at that <i>.
	doc := changefacts.ChangeDocument{Change: change, RichTasks: toRichTasks(richTasks)}
	docJSON, err := changefacts.MarshalDocument(doc)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "create_change: marshal change document for %q: %v", p.Slug, err)
	}

	// The content revision (D15 #0) is a stable hash of the authored document, so a
	// re-author self-invalidates a stale openspec.change.validated: validate_change echoes
	// this on PASS, and project_tasks freezes task.spec only when the two match, so a
	// re-authored-but-not-revalidated change is refused. Hashed over the document JSON (not
	// the revision/slug facts) so it is deterministic and not self-referential.
	rev := Revision(docJSON)

	// create_change OWNS exactly three flat predicates on the run (D3): the document blob,
	// the slug pointer, and the content revision. Replace them as ONE atomic upsert so a
	// re-author overwrites cleanly (no stale tree, no phantom rid-keyed facts). They are
	// distinct predicates from the other openspec.change.* writers (validate/archive), so
	// the explicit remove-list clears only what this tool owns (G5).
	// The explicit remove list is gone: ReplaceOwned clears this owner's whole
	// replace-owned group for the RUN class — which is exactly
	// {document, slug, revision} — and re-adds Desired (design D3a). The other
	// openspec.change.* writers (validate/archive) are different owners, so their
	// facts are outside this group and untouched (G5).
	triples := []message.Triple{
		runFactTriple(runEntityID, changefacts.DocumentPredicate, docJSON, now),
		runFactTriple(runEntityID, SlugPredicate, p.Slug, now),
		runFactTriple(runEntityID, RevisionPredicate, rev, now),
	}
	if err := e.writer.Replace(ctx, runEntityID, triples); err != nil {
		return errResult(call, graphown.WriteErrorKind(err), "create_change: replace the change document on %s: %v", runEntityID, err)
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
		if err := e.writer.Replace(ctx, loopEntityID, marker); err != nil {
			return errResult(call, graphown.WriteErrorKind(err), "create_change: stamp %s on %s: %v", AuthoredPredicate, loopEntityID, err)
		}
	}

	summary, _ := json.Marshal(map[string]any{"slug": p.Slug, "run_entity": runEntityID})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// toRichTasks maps the authored rich per-task fields into the document DTO the
// projector reads (beta.147 D3). Presence is preserved by construction: nil stays nil
// (a projector gap → park) and an authored-empty slice stays non-nil (a real value),
// and the DTO marshals both faithfully (no omitempty). Each rich task carries the flat
// index toChange assigned it as it entered change.Tasks, so it aligns with the thin task
// at that <i> — one walk assigns the index, no second flatten to drift (design D14).
func toRichTasks(tasks []richTask) []changefacts.RichTask {
	out := make([]changefacts.RichTask, len(tasks))
	for i, rt := range tasks {
		out[i] = changefacts.RichTask{
			Index:       rt.Index,
			TargetFiles: rt.TargetFiles,
			TestCommand: rt.TestCommand,
			Assumptions: rt.Assumptions,
			NonGoals:    rt.NonGoals,
			Budget:      rt.Budget,
		}
	}
	return out
}

// runFactTriple builds one content-revision fact on the run entity (Source ==
// the openspec.change.* writer, G5), replace-by-predicate on re-author.
func runFactTriple(runEntityID, predicate, rev string, now time.Time) message.Triple {
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
func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
