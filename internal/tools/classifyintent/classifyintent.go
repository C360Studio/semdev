// Package classifyintent is the classify_intent tool (nl-conversation-intent D2):
// the conversation-classifier persona's ROUTING gate at the change-approval seam.
// An inherit-scoped classifier loop reads ONE authorized human message off the
// run's conversation.pending.* triples and calls classify_intent to record its
// reading — one intent from the closed {approve, reject, none} taxonomy plus a
// short reason.
//
// The split of judgment from identity is the whole point (D2 / architect H3 /
// semstreams HIGH-2):
//
//   - The MODEL supplies only its JUDGMENT — the intent (validated against the
//     closed taxonomy, so a hallucinated value cannot route) and an inert reason.
//     It never names an author and never supplies a message id.
//   - The HARNESS supplies IDENTITY — the classified message's id + author are
//     COPIED from the run's conversation.pending.* triples (the message the loop
//     was spawned to read), never trusted from the model. An LLM-supplied author
//     would be an approval-injection / misattribution hole.
//
// It is the `decide` SHAPE, not the `submit_review` shape (G3): a routing
// classification the harness records, NOT a harness-floored verdict — approval
// has no executable ground-truth, so no measurement floor is possible. The
// consequential gate fact (run.change.decision) is stamped
// deterministically downstream by the apply consumer under one G5 writer
// (approval-adapter), never here; classify_intent fires no lifecycle transition
// (G2). It is a SEPARATE tool from `decide` because `decide` carries no
// message-id grounding and stamps under the coordinator's Source on the
// coordinator lane — reusing it would give that predicate a second writer (G5)
// and collide with the coordinator's routing (the why-not-decide note,
// docs/alignment-notes.md#classify-intent-tool).
//
// It subject-overrides to the RUN — NOT the classifier loop the framework
// `decide` executor would target (decide.go stamps on the loop) — because
// handleMessage's dedup and the intent-routing rule both read the run (architect
// H2): a rule templates only the firing entity's own triples.
package classifyintent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/graphown"
)

// ToolName is the registered tool name and the classifier's intent handler.
const ToolName = "classify_intent"

// Source is stamped on every conversation.intent.* triple this tool writes. It
// MUST equal the writer internal/vocab declares for that namespace (G5) — the
// TestToolSourceMatchesVocabWriter census cross-checks it. It is re-exported from
// the domain package so the vocab tie has a single source of truth.
const Source = conversationintent.ClassifierSource

// Executor reads the run's conversation.pending.* message (harness-bound
// identity) and stamps the classifier's routing intent (conversation.intent.*)
// on the RUN.
type Executor struct {
	reader   changefacts.Reader
	writer   *graphown.Writer
	platform component.PlatformMeta
	logger   *slog.Logger
}

// New builds the classify_intent executor. reader/writer may be nil for
// schema-only registration (the tool censuses inspect ListTools without a live
// NATS client); Execute fails loudly if either is nil. platform supplies the
// org/platform the classifier LOOP entity id is derived from for the recorded
// mirror — a wrong value faults the call closed (before anything routes) rather
// than silently mirroring onto an entity no rule reads.
func New(reader changefacts.Reader, writer *graphown.Writer, platform component.PlatformMeta, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, writer: writer, platform: platform, logger: logger}
}

type payload struct {
	// Intent is the model's classification — validated against the closed
	// taxonomy before anything is stamped (an off-taxonomy value cannot route).
	Intent string `json:"intent"`
	// Reason is the model's inert echo, recorded for the human's visibility.
	Reason string `json:"reason"`
}

// Execute reads the run's pending message, harness-binds its id + author, and
// stamps the routing intent on the run. A hallucinated intent is rejected and
// stamps nothing; a missing pending message is a spawn-contract violation (the
// spawn rule fires only once pending exists) and faults loudly rather than
// stamping a groundless intent.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "classify_intent: harness not fully wired (reader/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "classify_intent: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "classify_intent: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "classify_intent: decode arguments: %v", err)
	}
	// The model supplies only its judgment; a value outside the closed taxonomy
	// (a hallucination) is rejected here so it can never drive the gate. The loop
	// re-runs (no StopLoop) so a weak model gets a chance to answer in-taxonomy;
	// a persistent failure faults the classifier (the D9 fallback-note path).
	if !conversationintent.Valid(p.Intent) {
		return errResult(call, agentic.ToolErrorInvalidArgs, "classify_intent: intent %q is not one of %v", p.Intent, conversationintent.Names())
	}

	// HARNESS-BIND identity: the message this loop was spawned to read is on the
	// run's conversation.pending.* triples. Copy its id + author (never the
	// model's word) so the apply consumer re-authorizes the real author (D2/H3).
	pending, err := e.reader.ReadFacts(ctx, runEntityID, conversationintent.PendingPrefix)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "classify_intent: read %s on %s: %v", conversationintent.PendingPrefix, runEntityID, err)
	}
	pendingID := firstObject(pending, conversationintent.PendingMessageIDPredicate)
	pendingAuthor := firstObject(pending, conversationintent.PendingAuthorPredicate)
	if pendingID == "" || pendingAuthor == "" {
		// The spawn rule fires only once handleMessage has stamped pending, so an
		// absent pending id/author is a contract violation (a bug), not a normal
		// input. Fault loudly (no StopLoop) rather than stamp a groundless intent.
		return errResult(call, agentic.ToolErrorInternal, "classify_intent: no pending message id/author on %s — the classifier was spawned without a grounded message (got id=%q author=%q)", runEntityID, pendingID, pendingAuthor)
	}

	// READ-ONCE BINDING (grp4-review HIGH-1): the pending slot is latest-wins and
	// can be overwritten by a second authorized message DURING this loop's model
	// turn — but this loop's prompt carried, and its judgment is of, the ONE
	// message the spawn rule dispatched it for, whose id the rule stamped as the
	// conversation.classifier.dispatched marker. If the slot no longer holds that
	// id, stamping would bind the NEW message's identity (and permanently dedup
	// it in the ledger) to a judgment of the OLD message's text — a
	// misattribution the D8.2 grounding guard exists to prevent. Fault instead
	// (no stamp, no ledger entry): the terminal-release rule retires the slot and
	// the marker, and the fallback-note lane surfaces the miss so the human
	// re-nudges or uses the exact command. A missing marker is the same fault
	// fail-closed: this loop was spawned outside the sanctioned spawn rule, or
	// its slot was already released.
	dispatched, err := e.reader.ReadFacts(ctx, runEntityID, conversationintent.ClassifierDispatchedPredicate)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "classify_intent: read %s on %s: %v", conversationintent.ClassifierDispatchedPredicate, runEntityID, err)
	}
	dispatchedID := firstObject(dispatched, conversationintent.ClassifierDispatchedPredicate)
	if dispatchedID == "" {
		return errResult(call, agentic.ToolErrorInternal, "classify_intent: no %s marker on %s — this loop was not dispatched by the sanctioned spawn rule (or its slot was already released); refusing to classify", conversationintent.ClassifierDispatchedPredicate, runEntityID)
	}
	if dispatchedID != pendingID {
		return errResult(call, agentic.ToolErrorInternal, "classify_intent: the pending slot moved mid-flight — dispatched for message %q but the slot now holds %q; refusing to bind the new message's identity to a judgment of the old message's text (the release retires the slot; the fallback note surfaces it)", dispatchedID, pendingID)
	}

	// The append-set dedup ledger (D5): read the ids already classified, then
	// re-stamp the full set plus this one. Writer.Replace reconciles the whole
	// owned group, so an append is expressed as read-all-then-write-all
	// (the submit_review route-mirror pattern) — the whole set in one atomic
	// mutation. A redelivery re-stamping the same id is idempotent (deduped).
	//
	// LOAD-BEARING single-writer-per-run invariant: this read→replace is NOT
	// serialized by the framework (the reconcile assumes one concurrent writer
	// of the group). It is safe here because — unlike submit_review's mirror, which
	// targets the per-loop entity and so cannot race itself — this ledger lives on
	// the RUN, shared by every classifier loop of that run, and the group-4 spawn
	// rule serializes classifier spawns per run via the fire-once
	// conversation.classifier.dispatched marker (D5). If two classifier loops ever
	// ran concurrently on one run, replace-by-predicate would drop the other's just
	// -added id (a bounded re-classification, never a false approval — the gate fact
	// is a downstream deterministic writer). The read is assumed to return the
	// COMPLETE multi-valued set (bounded by the human messages on one run).
	classified, err := e.reader.ReadFacts(ctx, runEntityID, conversationintent.IntentClassifiedPredicate)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "classify_intent: read %s on %s: %v", conversationintent.IntentClassifiedPredicate, runEntityID, err)
	}
	ledger := ledgerWith(objectsOf(classified, conversationintent.IntentClassifiedPredicate), pendingID)

	// THE LOOP MIRROR, PART 1 — resolve the target BEFORE anything routes.
	//
	// The fault-note rule (conversation/05) fires on the classifier LOOP, and rule
	// conditions read only the FIRING entity's facts — the run's
	// conversation.intent.* is unreachable from there. So the note needs a
	// loop-local witness, and its ABSENCE is the complete "produced no reading"
	// discriminator: it covers the read-once binding refusal above, a model error,
	// a truncation, and cap exhaustion alike. Keying the note on
	// agent.loop.outcome == "failed" is WRONG and was the shipped bug — a tool
	// returning a ToolResult error does not fail its loop, so a classifier that
	// deliberately refused still terminated outcome=success and the human was told
	// nothing (observed end-to-end, not theorized).
	//
	// ORDERING IS A SAFETY PROPERTY (grp6-review M1). Deriving the loop id here,
	// ahead of stampIntent, makes a mis-wired platform or a missing LoopID fault
	// CLOSED: nothing is stamped, so no route fires and no gate releases. Doing it
	// after the run write — which is what submit_review's shape looks like at a
	// glance — would be fail-OPEN here: the routing fact would already be on the
	// run, 03a/03b would already have fired, the gate might already have released,
	// and the only remaining effect would be a contradictory note. Same code shape,
	// opposite safety direction, because submit_review's mirror IS the chaining
	// signal while this one is only a witness.
	var loopEntityID string
	if call.LoopID == "" {
		// Unit-test construction only; a dispatched loop always carries one.
		e.logger.Warn("classify_intent: no loop_id on the tool call — skipping the recorded mirror; a fallback note may be posted for a classification that DID land",
			slog.String("run_entity_id", runEntityID), slog.String("message_id", pendingID))
	} else {
		var lerr error
		loopEntityID, lerr = agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if lerr != nil {
			return errResult(call, agentic.ToolErrorInternal, "classify_intent: construct classifier loop entity id: %v", lerr)
		}
	}

	if err := e.stampIntent(ctx, runEntityID, p.Intent, pendingID, pendingAuthor, p.Reason, ledger); err != nil {
		return errResult(call, graphown.WriteErrorKind(err), "classify_intent: stamp %s on %s: %v", conversationintent.IntentValuePredicate, runEntityID, err)
	}

	// THE LOOP MIRROR, PART 2 — record that a classification actually landed.
	//
	// FAILURE POSTURE (grp6-review M2, corrected). errResult carries no StopLoop, so
	// a mirror failure gives the model ANOTHER TURN, and pass 2 can return a
	// DIFFERENT intent whose stampIntent replaces the value pass 1 may already have
	// routed on. The writes are idempotent; the judgment is not. What actually keeps
	// that safe is downstream and deterministic — 03a/03b dispatch only while BOTH
	// gate facts are absent, and the apply consumer re-checks the gate is still open
	// before stamping — NOT anything the mirror does. The residual effect of a
	// mirror failure is a spurious fallback note on a classification that landed,
	// which the note consumer suppresses once a gate fact exists (parkpost.go).
	if loopEntityID != "" {
		mirror := message.Triple{
			Subject:    loopEntityID,
			Predicate:  conversationintent.ClassifierRecordedPredicate,
			Object:     pendingID,
			Source:     conversationintent.ClassifierSource,
			Timestamp:  time.Now().UTC(),
			Confidence: 1.0,
		}
		if merr := e.writer.Replace(ctx, loopEntityID, []message.Triple{mirror}); merr != nil {
			return errResult(call, graphown.WriteErrorKind(merr), "classify_intent: stamp %s on %s: %v", conversationintent.ClassifierRecordedPredicate, loopEntityID, merr)
		}
	}

	e.logger.Info("classify_intent recorded intent",
		slog.String("run_entity_id", runEntityID),
		slog.String("intent", p.Intent),
		slog.String("message_id", pendingID),
		slog.String("author", pendingAuthor))

	summary, _ := json.Marshal(map[string]any{
		"intent":     p.Intent,
		"message_id": pendingID,
		"author":     pendingAuthor,
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// stampIntent upserts the intent facts on the RUN in one atomic mutation. The
// single-valued facts (value/message-id/author/reason) are replaced by predicate;
// the multi-valued conversation.intent.classified ledger is written as the full
// deduped set (append-set semantics over a replace-by-predicate writer). All
// carry Source (G5). No remove list exists under ReplaceOwned — the owner's
// replace-owned group IS the removal, and every predicate in it is re-supplied here
// (the ledger as its full deduped set), so nothing is dropped (migrate-beta159 D3a).
func (e *Executor) stampIntent(ctx context.Context, runEntityID, intent, messageID, author, reason string, ledger []string) error {
	now := time.Now().UTC()
	mk := func(pred, obj string) message.Triple {
		return message.Triple{Subject: runEntityID, Predicate: pred, Object: obj, Source: Source, Timestamp: now, Confidence: 1.0}
	}
	triples := []message.Triple{
		mk(conversationintent.IntentValuePredicate, intent),
		mk(conversationintent.IntentMessageIDPredicate, messageID),
		mk(conversationintent.IntentAuthorPredicate, author),
		mk(conversationintent.IntentReasonPredicate, reason),
	}
	for _, id := range ledger {
		triples = append(triples, mk(conversationintent.IntentClassifiedPredicate, id))
	}
	return e.writer.Replace(ctx, runEntityID, triples)
}

// firstObject returns the first string object of pred among triples, or "". A
// non-string object yields "" — which routes to the fail-CLOSED absent-pending
// branch, never a fail-open guess (the conversation-adapter writes these ids as
// strings, so this only guards a malformed transport value).
func firstObject(triples []message.Triple, pred string) string {
	for _, tr := range triples {
		if tr.Predicate == pred {
			if s, ok := tr.Object.(string); ok {
				return s
			}
		}
	}
	return ""
}

// objectsOf returns every string object of pred among triples (multi-valued).
func objectsOf(triples []message.Triple, pred string) []string {
	var out []string
	for _, tr := range triples {
		if tr.Predicate != pred {
			continue
		}
		if s, ok := tr.Object.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ledgerWith returns existing ∪ {id}, order-preserving and deduped, so the
// append-set never grows a duplicate on redelivery.
func ledgerWith(existing []string, id string) []string {
	seen := make(map[string]bool, len(existing)+1)
	out := make([]string, 0, len(existing)+1)
	for _, e := range existing {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	if !seen[id] {
		out = append(out, id)
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
