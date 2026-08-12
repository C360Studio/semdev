package classifyintent

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/conversationintent"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeReader struct {
	facts []message.Triple
	err   error
}

// testPlatform is the org/platform the loop-entity id is derived from (the
// recorded mirror). Matches the journeys' platform so ids read realistically.
var testPlatform = component.PlatformMeta{Org: "c360", Platform: "semdev-001"}

func (r *fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, t := range r.facts {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeWriter struct {
	entityIDs []string
	contracts []string
	replaces  [][]message.Triple
}

func (w *fakeWriter) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
	w.entityIDs = append(w.entityIDs, m.EntityID)
	w.contracts = append(w.contracts, m.Contract)
	w.replaces = append(w.replaces, m.Desired)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam the tool takes, so every test
// exercises graphown.ContractFor for real. conversation-classifier is one of the two
// owners with TWO contracts (intent facts on the RUN, the recorded marker on the
// LOOP), so this is where a run/loop misclassification actually surfaces — the
// loop-entity assertion below is what makes it bite.
func writerFor(w *fakeWriter) *graphown.Writer {
	return graphown.NewWriter(conversationintent.ClassifierSource, w)
}

// pendingFact builds one conversation.pending.<field> triple on the run.
func pendingFact(pred, obj string) message.Triple {
	return message.Triple{Subject: runEntity, Predicate: pred, Object: obj, Source: conversationintent.ClassifierSource}
}

// classifiedFact builds one entry of the multi-valued conversation.intent.classified ledger.
func classifiedFact(id string) message.Triple {
	return message.Triple{Subject: runEntity, Predicate: conversationintent.IntentClassifiedPredicate, Object: id, Source: conversationintent.ClassifierSource}
}

// dispatchedFact builds the spawn rule's fire-once marker: the message id this
// classifier loop was dispatched FOR (the group-4 spawn contract — every
// sanctioned classifier spawn stamps it before the publish).
func dispatchedFact(id string) message.Triple {
	return message.Triple{Subject: runEntity, Predicate: conversationintent.ClassifierDispatchedPredicate, Object: id, Source: "conversation-spawn-rule"}
}

// call builds a classify_intent tool call carrying the run entity id (the inherit
// loop's tool calls carry it — the submit_review/measure_task metadata pattern).
func call(intent, reason string) agentic.ToolCall {
	args := map[string]any{}
	if intent != "" {
		args["intent"] = intent
	}
	if reason != "" {
		args["reason"] = reason
	}
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: args,
	}
}

// stampedObject returns the single object stamped for pred across the writer's
// captured replaces (or "" if none), and how many distinct triples carried it.
func stampedObject(w *fakeWriter, pred string) (string, int) {
	obj, n := "", 0
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == pred {
				obj, _ = tr.Object.(string)
				n++
			}
		}
	}
	return obj, n
}

// stampedObjects returns every object stamped for pred (multi-valued predicates).
func stampedObjects(w *fakeWriter, pred string) []string {
	var out []string
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == pred {
				if s, ok := tr.Object.(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// TestClassifyIntentTakesNoAuthorOrMessageID (task 2.1): the LLM-facing schema
// exposes ONLY intent + reason. It takes NO author and NO message_id — identity
// is harness-bound, not model-supplied (HIGH-2 / H3) — and no outcome-shaped
// field (G3).
func TestClassifyIntentTakesNoAuthorOrMessageID(t *testing.T) {
	defs := New(nil, nil, testPlatform, nil).ListTools()
	if len(defs) != 1 {
		t.Fatalf("ListTools returned %d defs, want 1", len(defs))
	}
	props, ok := defs[0].Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object: %#v", defs[0].Parameters)
	}
	// Exactly the two judgment fields, nothing more.
	want := map[string]bool{"intent": true, "reason": true}
	for name := range props {
		if !want[name] {
			t.Errorf("schema exposes unexpected field %q; the model supplies only intent + reason", name)
		}
	}
	for name := range want {
		if _, present := props[name]; !present {
			t.Errorf("schema is missing required field %q", name)
		}
	}
	// The identity fields must NOT be model-supplied.
	for _, banned := range []string{"author", "message_id", "message-id", "outcome", "approve", "approved"} {
		if _, present := props[banned]; present {
			t.Errorf("schema exposes %q; identity is harness-bound and the intent carries no outcome (G3/H3)", banned)
		}
	}
}

// TestClassifyIntentStampsOnRunFromPending (task 2.2): the tool subject-overrides
// to the RUN and stamps conversation.intent.value from the model, with
// .message-id + .author COPIED from the run's conversation.pending.* (matched by
// pending id), .reason echoed, and the pending id APPENDED to the multi-valued
// conversation.intent.classified ledger — all on the run, none on the loop, and a
// prior ledger entry preserved.
func TestClassifyIntentStampsOnRunFromPending(t *testing.T) {
	facts := []message.Triple{
		pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-42"),
		pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
		pendingFact(conversationintent.PendingPrefix+"body", "yes, let's ship this"),
		dispatchedFact("issuecomment-42"), // the spawn marker names this message
		classifiedFact("issuecomment-7"),  // a prior classification the append-set must preserve
	}
	w := &fakeWriter{}
	res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), call("approve", "the author said to ship it"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s (%s)", res.Error, res.ErrorKind)
	}
	if len(w.entityIDs) == 0 {
		t.Fatal("nothing stamped")
	}
	// Everything lands on the RUN, never the loop.
	for _, id := range w.entityIDs {
		if id != runEntity {
			t.Errorf("stamped on %q, want the run %q (subject-override, H2)", id, runEntity)
		}
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Subject != runEntity {
				t.Errorf("triple %q subject = %q, want the run %q", tr.Predicate, tr.Subject, runEntity)
			}
			if tr.Source != conversationintent.ClassifierSource {
				t.Errorf("triple %q Source = %q, want %q (G5)", tr.Predicate, tr.Source, conversationintent.ClassifierSource)
			}
		}
	}
	if got, n := stampedObject(w, conversationintent.IntentValuePredicate); got != "approve" || n != 1 {
		t.Errorf("intent.value = %q (n=%d), want %q (n=1)", got, n, "approve")
	}
	// Identity is COPIED from pending, not from the (absent) model args.
	if got, _ := stampedObject(w, conversationintent.IntentMessageIDPredicate); got != "issuecomment-42" {
		t.Errorf("intent.message-id = %q, want the pending id %q (harness-bound)", got, "issuecomment-42")
	}
	if got, _ := stampedObject(w, conversationintent.IntentAuthorPredicate); got != "maintainer-jo" {
		t.Errorf("intent.author = %q, want the pending author %q (harness-bound)", got, "maintainer-jo")
	}
	if got, _ := stampedObject(w, conversationintent.IntentReasonPredicate); got != "the author said to ship it" {
		t.Errorf("intent.reason = %q, want the model echo", got)
	}
	// The append-set ledger preserves the prior id and adds the new one (dedup, D5).
	ledger := stampedObjects(w, conversationintent.IntentClassifiedPredicate)
	if !contains(ledger, "issuecomment-7") {
		t.Errorf("classified ledger %v dropped the prior id issuecomment-7 (append-set, not latest-wins)", ledger)
	}
	if !contains(ledger, "issuecomment-42") {
		t.Errorf("classified ledger %v did not record the new id issuecomment-42", ledger)
	}
	if len(ledger) != 2 {
		t.Errorf("classified ledger %v has %d entries, want exactly 2 (no duplicate)", ledger, len(ledger))
	}
	if !res.StopLoop {
		t.Error("classify_intent should StopLoop after recording the single classification")
	}
}

// TestClassifyIntentIgnoresModelSuppliedIdentity pins the security property (H3 /
// HIGH-2): even if the model smuggles author/message_id/outcome into the tool
// arguments, the stamped identity comes ONLY from the run's pending triples and
// no smuggled field leaks onto the graph. Identity is harness-bound, not
// model-supplied — an LLM-named author must never drive attribution.
func TestClassifyIntentIgnoresModelSuppliedIdentity(t *testing.T) {
	facts := []message.Triple{
		pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-42"),
		pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
		dispatchedFact("issuecomment-42"),
	}
	c := call("approve", "ship it")
	// The model tries to supply its own identity + an outcome.
	c.Arguments["author"] = "attacker"
	c.Arguments["message_id"] = "forged-99"
	c.Arguments["message-id"] = "forged-99"
	c.Arguments["approved"] = true
	w := &fakeWriter{}
	res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), c)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s (%s)", res.Error, res.ErrorKind)
	}
	if got, _ := stampedObject(w, conversationintent.IntentAuthorPredicate); got != "maintainer-jo" {
		t.Errorf("intent.author = %q, want the pending author (model-supplied author must be ignored)", got)
	}
	if got, _ := stampedObject(w, conversationintent.IntentMessageIDPredicate); got != "issuecomment-42" {
		t.Errorf("intent.message-id = %q, want the pending id (model-supplied id must be ignored)", got)
	}
	// No smuggled value reached the graph under any predicate.
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if s, _ := tr.Object.(string); s == "attacker" || s == "forged-99" {
				t.Errorf("smuggled value %q reached the graph on predicate %q", s, tr.Predicate)
			}
		}
	}
}

// TestClassifyIntentDedupIsIdempotentOnRedelivery pins the append-set dedup (D5 /
// L2): re-classifying a message id ALREADY in the ledger leaves the ledger
// unchanged (no duplicate entry), so a redelivery re-stamps nothing new.
func TestClassifyIntentDedupIsIdempotentOnRedelivery(t *testing.T) {
	facts := []message.Triple{
		pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-42"),
		pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
		dispatchedFact("issuecomment-42"),
		classifiedFact("issuecomment-42"), // already classified — the redelivery case
	}
	w := &fakeWriter{}
	res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), call("approve", "ship it"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s (%s)", res.Error, res.ErrorKind)
	}
	ledger := stampedObjects(w, conversationintent.IntentClassifiedPredicate)
	if len(ledger) != 1 || ledger[0] != "issuecomment-42" {
		t.Errorf("classified ledger = %v, want exactly [issuecomment-42] (no duplicate on redelivery)", ledger)
	}
}

// TestClassifyIntentFaultsWhenPendingSlotMoved pins the read-once binding
// (grp4-review HIGH-1): the pending slot is latest-wins and the spawn marker
// names the ONE message this loop was dispatched for. If the slot no longer
// holds that id at execution time (a second authorized message landed during
// the model turn), the tool must FAULT and stamp NOTHING — binding the new
// message's identity to a judgment of the old message's text would misattribute
// the intent AND permanently dedup a message that was never read. A missing
// marker is the same fault (the loop was spawned outside the sanctioned spawn
// rule, or the slot was already released) — fail closed, never guess.
func TestClassifyIntentFaultsWhenPendingSlotMoved(t *testing.T) {
	cases := map[string][]message.Triple{
		"slot moved mid-flight": {
			pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-43"), // the countermand replaced the slot
			pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
			dispatchedFact("issuecomment-42"), // ...but this loop was dispatched for 42
		},
		"marker absent": {
			pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-42"),
			pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
		},
	}
	for name, facts := range cases {
		w := &fakeWriter{}
		res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), call("approve", "ship it"))
		if err != nil {
			t.Fatalf("%s: execute: %v", name, err)
		}
		if res.Error == "" {
			t.Errorf("%s: classification succeeded; the tool must fault when the dispatched id does not match the pending slot", name)
		}
		if res.ErrorKind != agentic.ToolErrorInternal {
			t.Errorf("%s: fault kind = %q, want %q (a harness-contract fault, not a model error)", name, res.ErrorKind, agentic.ToolErrorInternal)
		}
		if len(w.replaces) != 0 {
			t.Errorf("%s: %d batches stamped; a mismatched classification must write nothing — no intent, no ledger entry (the unread message must stay classifiable)", name, len(w.replaces))
		}
	}
}

// TestClassifyIntentRejectsOffTaxonomy (task 2.3): an intent outside the closed
// set (a hallucinated value) is rejected and stamps NOTHING, so it cannot route.
func TestClassifyIntentRejectsOffTaxonomy(t *testing.T) {
	facts := []message.Triple{
		pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-42"),
		pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
		dispatchedFact("issuecomment-42"),
	}
	for _, bad := range []string{"ship_it", "approved", "yes", ""} {
		w := &fakeWriter{}
		res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), call(bad, "looks good"))
		if err != nil {
			t.Fatalf("execute(%q): %v", bad, err)
		}
		if res.Error == "" {
			t.Errorf("intent %q was accepted; an off-taxonomy value must be rejected", bad)
		}
		if res.ErrorKind != agentic.ToolErrorInvalidArgs {
			t.Errorf("intent %q rejected with kind %q, want %q", bad, res.ErrorKind, agentic.ToolErrorInvalidArgs)
		}
		if len(w.replaces) != 0 {
			t.Errorf("intent %q stamped %d batches; a rejected classification must write nothing", bad, len(w.replaces))
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestClassifyIntentMirrorsRecordedOnItsLoop pins the loop mirror that makes the
// fallback note possible at all.
//
// The fault-note rule fires on the classifier LOOP, and rule conditions can only
// read the FIRING entity's own facts — the run's conversation.intent.* is
// unreachable from there. So "did this classifier produce a reading?" has to be
// answerable from the loop, and this mirror is that answer.
//
// The alternative the design originally assumed — key the note on
// agent.loop.outcome == "failed" — shipped broken: a tool returning a ToolResult
// error does NOT fail its loop, so a classifier that deliberately refused to
// classify terminated outcome=success and the human was told nothing. Hence the
// discriminator is the ABSENCE of this fact, which makes its presence-on-success
// and absence-on-refusal both load-bearing.
const testLoopID = "classifier-loop-1"

// loopCall is call() plus a LoopID, so the recorded mirror is reachable. The
// default call() carries none, which exercises the documented skip path.
func loopCall(intent, reason string) agentic.ToolCall {
	c := call(intent, reason)
	c.LoopID = testLoopID
	return c
}

func TestClassifyIntentMirrorsRecordedOnItsLoop(t *testing.T) {
	t.Run("a landed classification stamps the mirror on the LOOP", func(t *testing.T) {
		facts := []message.Triple{
			pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-42"),
			pendingFact(conversationintent.PendingAuthorPredicate, "maintainer-jo"),
			pendingFact(conversationintent.PendingPrefix+"body", "yes, let's ship this"),
			dispatchedFact("issuecomment-42"),
		}
		w := &fakeWriter{}
		res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), loopCall("approve", "ship it"))
		if err != nil || res.Error != "" {
			t.Fatalf("Execute: err=%v result.Error=%q", err, res.Error)
		}
		wantLoop, lerr := agentic.TryLoopExecutionEntityID(testPlatform.Org, testPlatform.Platform, testLoopID)
		if lerr != nil {
			t.Fatalf("derive loop entity id: %v", lerr)
		}
		var mirrored *message.Triple
		for ci, batch := range w.replaces {
			for i, tr := range batch {
				if tr.Predicate == conversationintent.ClassifierRecordedPredicate {
					mirrored = &w.replaces[ci][i]
				}
			}
		}
		if mirrored == nil {
			t.Fatalf("no %s stamped — without it the fault-note rule cannot tell a landed classification from a refused one, and EVERY successful classification would draw a spurious note",
				conversationintent.ClassifierRecordedPredicate)
		}
		if mirrored.Subject != wantLoop {
			t.Errorf("mirror stamped on %q, want the LOOP entity %q (a rule firing on the loop cannot read the run)", mirrored.Subject, wantLoop)
		}
		// The ENTITY the write was addressed to is the load-bearing half
		// (grp6-review M4): asserting only the triple's Subject field passes even if
		// ReplaceTriples is called with runEntityID, which in production leaves the
		// loop bare and fires the note on EVERY classification — the exact bug under
		// repair, reintroduced and invisible.
		var addressedLoop bool
		for _, id := range w.entityIDs {
			if id == wantLoop {
				addressedLoop = true
			}
		}
		if !addressedLoop {
			t.Errorf("no ReplaceTriples was ADDRESSED to the loop entity %q (got %v) — the mirror must be written to the loop, not merely carry it as a Subject", wantLoop, w.entityIDs)
		}
		if mirrored.Object != "issuecomment-42" {
			t.Errorf("mirror object = %v, want the classified message id %q", mirrored.Object, "issuecomment-42")
		}
		if mirrored.Source != conversationintent.ClassifierSource {
			t.Errorf("mirror Source = %q, want %q", mirrored.Source, conversationintent.ClassifierSource)
		}
	})

	t.Run("a REFUSED classification stamps no mirror (so the note fires)", func(t *testing.T) {
		// The read-once binding fault: the pending slot moved off the dispatched
		// marker, so the tool refuses rather than misattribute. Nothing may be
		// stamped — the absent mirror is exactly what summons the fallback note.
		facts := []message.Triple{
			pendingFact(conversationintent.PendingMessageIDPredicate, "issuecomment-99"), // the slot MOVED
			pendingFact(conversationintent.PendingAuthorPredicate, "someone-else"),
			pendingFact(conversationintent.PendingPrefix+"body", "a newer message"),
			dispatchedFact("issuecomment-42"), // this loop was dispatched for 42
		}
		w := &fakeWriter{}
		res, err := New(&fakeReader{facts: facts}, writerFor(w), testPlatform, nil).Execute(context.Background(), loopCall("approve", "ship it"))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Error == "" {
			t.Fatal("a moved pending slot must be refused (the misattribution guard)")
		}
		for _, batch := range w.replaces {
			for _, tr := range batch {
				if tr.Predicate == conversationintent.ClassifierRecordedPredicate {
					t.Errorf("a REFUSED classification stamped %s — the fault-note rule keys on its absence, so stamping it here restores the exact silence this whole lane exists to remove", tr.Predicate)
				}
			}
		}
	})
}
