//go:build e2e

// The NATURAL-LANGUAGE approval journeys (nl-conversation-intent group 6). The
// change-approval gate is driven by a human writing what they MEAN — no exact
// command, no stand-in write — through the full house pattern: the poller Reads a
// non-command message off the thread → the NL bridge stamps conversation.pending.*
// on the run → a rule spawns an inherit-scoped classifier loop → classify_intent
// stamps a ROUTING fact → a routing rule dispatches the deterministic apply
// consumer → the consumer re-authorizes the HARNESS-BOUND author, posts the
// transparency comment, and stamps the gate fact → a lifecycle rule resumes or
// cancels the run.
//
// These ride the POLL transport (pull-first) because it is the shape that reaches
// the thread with no webhook, exactly as a dev-box deployment would. Zero paid
// tokens: every model turn is a scripted mockllm fixture.
//
// WHY THESE MATTER: the gate they open has NO harness floor (design D8) — there is
// no ground truth for "what a human meant". So the journeys assert not just the
// happy release but the CONSERVATIVE cases: ambiguous chatter must NOT approve, and
// two opposite messages must never leave the run carrying both gate facts.
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/natsclient"

	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/forge/forgetest"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/mockllm"
)

// The NL messages the journeys post. Each doubles as the mock's fixture MARKER,
// because the classifier prompt templates the message body verbatim — so a fixture
// only fires for the turn that actually carried that message.
// faultNoteMarker is a distinctive slice of the fallback note's body
// (internal/conversationchannel/parkpost.go). Matching a phrase rather than the
// whole body keeps the journeys readable while still failing if the note is
// reworded into something that does not tell the human what to do.
const faultNoteMarker = "couldn't read that"

const (
	nlApproveMessage = "This looks great to me, please go ahead and ship it."
	nlRejectMessage  = "No — please don't do this, abandon the change."
	nlChatterMessage = "thanks for picking this up!"
)

// classifyFixture scripts one classifier turn: the mock reads the message body in
// the prompt and calls classify_intent with the given intent. The model supplies
// JUDGMENT only — identity and every gate fact are harness-bound (D2), which is
// exactly what these journeys prove end-to-end.
func classifyFixture(marker string, intent conversationintent.Intent, reason string) mockllm.Fixture {
	return mockllm.Fixture{Marker: marker, Tool: &mockllm.ToolCall{
		Name: "classify_intent",
		Args: map[string]any{"intent": string(intent), "reason": reason},
	}}
}

// nlFixtures builds the front-of-arc turns with classifier turns spliced in at the
// gate. Tool fixtures are a POSITIONAL SEQUENCE (see mockllm.Fixture), so the
// classifier turns must sit exactly where they occur in the arc: after
// create_change (the change is authored + validated, the run parks at the gate) and
// before the dev-rewake decide (which only happens if the gate RELEASES).
func nlFixtures(t *testing.T, classifiers ...mockllm.Fixture) []mockllm.Fixture {
	t.Helper()
	front := journeyFrontOfArcFixtures(2)
	// Splice on the DEV-REWAKE marker rather than a hardcoded index (grp6-review
	// M6): the classifier turns must sit after create_change (the run parks at the
	// gate) and before the dev rewake (which only happens if the gate RELEASES). A
	// hardcoded split silently misaligns if journeyFrontOfArcFixtures gains or
	// reorders an entry, and a misaligned tool fixture does NOT error — the mock
	// falls through to "first advertised tool with empty args", which for
	// classify_intent is an off-taxonomy refusal that still produces a fault note.
	// The journey would then pass green for the wrong reason.
	split := -1
	for i, f := range front {
		if f.Marker == journeyDevRewakeMarker {
			split = i
			break
		}
	}
	if split < 0 {
		t.Fatalf("no %q fixture in journeyFrontOfArcFixtures — the classifier turns cannot be positioned, and a misplaced tool fixture passes green with the wrong call", journeyDevRewakeMarker)
	}
	out := append([]mockllm.Fixture{}, front[:split]...)
	out = append(out, classifiers...)
	return append(out, front[split:]...)
}

// startNLJourney boots the pull-first runtime and drives the arc to the gate,
// returning the forge double (to post the human's message on) and the run id.
func startNLJourney(ctx context.Context, t *testing.T, mock *mockllm.Harness) (*forgetest.Double, string) {
	t.Helper()
	double := startPollJourneyRuntime(ctx, t, mock)

	publishFlattenedIssueEvent(ctx, t)
	requireCoordinatorDecision(ctx, t, journeyIssueRef, journeyDecideAction)
	runEntityID := requireRunAnchor(ctx, t, journeyIssueRef)
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("NL station 1: change authored + validated — run %s parked at the gate, no command issued", runEntityID)
	return double, runEntityID
}

// TestBridgeProofNLApprovalReleasesGate (task 6.1) — the headline proof: a human
// writes "please go ahead and ship it" and the run develops. No exact command
// anywhere in the path, no stand-in write.
func TestBridgeProofNLApprovalReleasesGate(t *testing.T) {
	mock := mockllm.New(nlFixtures(t,
		classifyFixture(nlApproveMessage, conversationintent.Approve, `the author wrote "please go ahead and ship it"`),
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	double, runEntityID := startNLJourney(ctx, t, mock)

	// Station 2 — the human writes what they MEAN, on the thread. Not a command.
	double.AddComment(1, webhookJourneyActor, nlApproveMessage)

	// Station 3 — the whole house pattern runs: poller Read → NL bridge → classifier
	// spawn → classify_intent → routing rule → apply consumer → gate fact → resume.
	requireRunPhase(ctx, t, runEntityID, "executing")
	requireDecision(ctx, t, runEntityID, admission.DecisionApprove,
		"the NL approval never reached the gate — check the classifier spawn, classify_intent, the routing rule, and the apply consumer in that order")
	t.Logf("NL station 2-3: an authorized NL approval released the gate — no /semdev approve anywhere in the path")

	// The gate fact was stamped by the deterministic apply consumer, NOT by the
	// model: exactly ONE approval fact, and no rejection alongside it.
	requireRunTripleCount(ctx, t, runEntityID, admission.DecisionPredicate, 1, 30*time.Second)

	// The announcement names the HARNESS-BOUND author (D2/D8.1 — identity is copied
	// from the pending slot by the harness, never supplied by the model). Asserting
	// the author here makes that claim an END-TO-END proof, not just a unit one
	// (grp6-review M8). NOTE the post-BEFORE-stamp ordering is pinned by
	// TestApplyPostFailureBlocksStamp, not here: this assertion runs after the run
	// already reached executing, so it proves the post happened, not that it
	// preceded the effect (grp6-review L5).
	requireTransparencyPosted(t, double, "Approving this change")
	requireTransparencyPosted(t, double, "@"+webhookJourneyActor)

	// The classification LANDED, so the fallback note must NOT appear (H2): its
	// presence would mean the recorded mirror is missing and every successful
	// classification is drawing a contradictory note.
	requireNoFaultNote(t, double)

	// Turns, asserted HERE rather than at the end of the journey: at this point the
	// arc has consumed exactly the front-of-arc turns plus ONE classifier, which is
	// deterministic. The dev-rewake decide fires later (after provisioning), so a
	// total-count assertion at the end would race it.
	requireModelTurns(t, mock, 4, "2 coordinator decides + create_change + ONE classifier — the dev-rewake decide comes later, after provisioning")

	// Station 4 — the released run connects to the dev rail (the M1-proven tail).
	// Waiting for the sandbox keeps teardown clean of in-flight docker work
	// (grp6-review M7 — the poll journey this is modeled on does the same).
	requireTaskSpecProjected(ctx, t, runEntityID)
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("NL station 4: task.spec projected — the NL-driven gate feeds the same dev rail the command lane does")
}

// TestBridgeProofNLRejectionCancelsRun (task 6.2) — the reject lane end-to-end: an
// NL rejection cancels a GATED run through the phase-guarded lifecycle rule.
func TestBridgeProofNLRejectionCancelsRun(t *testing.T) {
	mock := mockllm.New(nlFixtures(t,
		classifyFixture(nlRejectMessage, conversationintent.Reject, `the author wrote "don't do this, abandon the change"`),
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	double, runEntityID := startNLJourney(ctx, t, mock)

	double.AddComment(1, webhookJourneyActor, nlRejectMessage)

	requireDecision(ctx, t, runEntityID, admission.DecisionReject,
		"the NL rejection never reached the gate — check the classifier spawn, the 03b reject route, and the apply consumer")
	requireRunPhase(ctx, t, runEntityID, "cancelled")
	if n := decisionCount(ctx, t, runEntityID); n != 1 {
		t.Fatalf("a rejected run carries %d decision facts, want exactly 1 (D13 — the gate cannot hold two)", n)
	}
	requireTransparencyPosted(t, double, "Cancelling this run")
	requireNoFaultNote(t, double)
	requireModelTurns(t, mock, 4, "2 coordinator decides + create_change + ONE classifier; a cancelled run never reaches the dev rewake")
	t.Logf("NL station 2-3: an authorized NL rejection cancelled the gated run (phase-guarded, rule-owned)")
}

// TestConservativeNoneDoesNotApprove (task 6.3) — the SAFETY case, and the one that
// matters most. The gate has no harness floor, so the persona is contractually
// conservative: anything short of an unmistakable directive is `none`, and `none`
// routes NOWHERE. Ambiguous positivity must leave the run exactly as gated as it
// was — this is the journey that fails if the taxonomy ever drifts toward
// "friendly means yes".
func TestConservativeNoneDoesNotApprove(t *testing.T) {
	mock := mockllm.New(nlFixtures(t,
		classifyFixture(nlChatterMessage, conversationintent.None, "gratitude, not a directive to proceed"),
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	double, runEntityID := startNLJourney(ctx, t, mock)

	double.AddComment(1, webhookJourneyActor, nlChatterMessage)

	// The classification happens (a model turn is spent) but routes nowhere.
	requireTriplePresent(ctx, t, runEntityID, conversationintent.IntentValuePredicate,
		"the chatter was never classified — the NL bridge or the classifier spawn did not fire")

	// THE ASSERTION: the gate is untouched and the run is still waiting. Given a
	// generous settle window, because proving a NON-event needs time to be honest.
	time.Sleep(20 * time.Second)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	requireTripleAbsent(ctx, t, runEntityID, admission.DecisionPredicate,
		"AMBIGUOUS CHATTER DECIDED THE GATE — the conservative-none contract is broken; this is the false-approval class the whole design guards against")
	requireNoTransparencyPosted(t, double)
	requireModelTurns(t, mock, 4, "2 coordinator decides + create_change + ONE classifier; a `none` routes nowhere so the run stays gated")
	t.Logf("NL conservative case: %q classified `none` — run still gated, zero gate facts, nothing posted", nlChatterMessage)
}

// TestConflictingIntentsResolveToOneTerminal (task 6.4) — two authorized messages
// with OPPOSITE intent, arriving in SEPARATE poll ticks so BOTH are classified.
//
// The sequencing is the whole point (grp6-review H1). An earlier version posted
// both back-to-back, which lands them in one Read: the second overwrites the
// latest-wins pending slot, classify_intent refuses to misattribute, and NO gate
// fact ever lands. That version's "never both" assertions were satisfied by
// 0+0 and exercised none of the guards they named. Waiting for the first decision
// to land before posting the second is what actually drives two classifications
// through the lane and puts the second one against a CLOSED gate.
//
// What this pins: the run ends with EXACTLY ONE gate fact and one terminal. Three
// guards make that true and all three are reached here — the routing rules
// dispatch only while both gate facts are absent (H4a), the apply consumer
// re-checks the gate is still open before stamping, and the NL bridge is
// phase-gated so a message arriving after the run leaves awaiting_approval is not
// classified at all. An approval is irreversible once landed (architect H1); the
// later rejection must NOT cancel the approved, executing run.
func TestConflictingIntentsResolveToOneTerminal(t *testing.T) {
	mock := mockllm.New(nlFixtures(t,
		classifyFixture(nlApproveMessage, conversationintent.Approve, `the author wrote "ship it"`),
		classifyFixture(nlRejectMessage, conversationintent.Reject, `a later message said "abandon the change"`),
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	double, runEntityID := startNLJourney(ctx, t, mock)

	// Decision one: an NL approval, applied end-to-end.
	double.AddComment(1, webhookJourneyActor, nlApproveMessage)
	requireDecision(ctx, t, runEntityID, admission.DecisionApprove,
		"the first NL approval never reached the gate")
	requireRunPhase(ctx, t, runEntityID, "executing")

	// Decision two: an NL rejection, arriving AFTER the gate closed. It must not
	// undo the approval — the run is already developing approved work.
	double.AddComment(1, webhookJourneyActor, nlRejectMessage)
	time.Sleep(20 * time.Second) // settle: proving a non-event needs time to be honest

	if n := decisionCount(ctx, t, runEntityID); n != 1 {
		t.Fatalf("run carries %d decision facts, want exactly 1 — the gate is single-valued (D13)", n)
	}
	if got := runDecision(ctx, t, runEntityID); got != admission.DecisionApprove {
		t.Fatalf("decision = %q, want %q — a late rejection must not overturn a decided gate (H4a + the apply consumer's gate-still-open re-check)", got, admission.DecisionApprove)
	}
	if phase := runPhase(ctx, t, runEntityID); phase == "cancelled" {
		t.Fatalf("a rejection arriving AFTER the approval landed cancelled the run — an approval is irreversible once landed (architect H1), and the reject rule is phase-guarded to awaiting_approval precisely so approved, executing work is never killed")
	}
	t.Logf("NL conflict case: approve applied, later reject refused — decision=%q (one fact), run not cancelled", runDecision(ctx, t, runEntityID))
}

// TestClassifierBindingFaultTellsTheHuman (task 6.4, the one-tick case) — the
// journey that caught the shipped fault-note bug, given its own name because the
// state it drives is a distinct and reachable production shape.
//
// Two authorized messages in ONE poll tick: both bridge, the spawn rule stamps one
// id as the dispatched marker, and the other overwrites the latest-wins pending
// slot. classify_intent then REFUSES — binding the new message's identity to a
// judgment of the old message's text is exactly the misattribution D8.2 bars — so
// nothing is classified and no gate fact lands. That loss is accepted and
// documented (rule 04's LOSS WINDOW).
//
// What is NOT acceptable is silence, and silence is what shipped: the fault-note
// rule keyed on `agent.loop.outcome == "failed"`, but a tool returning a
// ToolResult error does NOT fail its loop, so the refusing classifier terminated
// outcome=success and the note never fired. The human wrote a directive and
// watched nothing happen. This test asserts the note SPECIFICALLY — not "some
// reply" — so it fails if the discriminator regresses.
func TestClassifierBindingFaultTellsTheHuman(t *testing.T) {
	mock := mockllm.New(nlFixtures(t,
		classifyFixture(nlApproveMessage, conversationintent.Approve, `the author wrote "ship it"`),
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	double, runEntityID := startNLJourney(ctx, t, mock)

	// BOTH in one tick — back-to-back against a 5s poll interval.
	double.AddComment(1, webhookJourneyActor, nlApproveMessage)
	double.AddComment(1, webhookJourneyActor, nlRejectMessage)

	// THE ASSERTION: the fallback note, specifically.
	requireFaultNotePosted(ctx, t, double)

	// And the gate is untouched — the loss is a loss, not a decision.
	time.Sleep(10 * time.Second)
	if n := decisionCount(ctx, t, runEntityID); n != 0 {
		t.Errorf("a refused classification stamped %d gate fact(s) — it must stamp NONE; the human's controls stay live precisely because nothing was decided", n)
	}
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("NL binding-fault case: the classification was refused, the gate is untouched, and the human was TOLD")
}

// --- NL journey helpers ---

// requireModelTurns asserts the EXACT number of model turns (mockllm's own
// contract, honored by every other journey in this package). It is not
// bookkeeping: a tool fixture whose marker misses does NOT error — the mock falls
// through to "the first advertised tool with empty args", which for
// classify_intent is an off-taxonomy refusal that produces a fault note. A journey
// asserting only on the note would pass green having exercised the wrong call
// entirely (grp6-review, semstreams MEDIUM-4). Counting turns is what makes a
// misfire visible.
func requireModelTurns(t *testing.T, mock *mockllm.Harness, want int, what string) {
	t.Helper()
	if got := mock.RequestCount(); got != want {
		t.Fatalf("expected exactly %d model turns (%s), got %d — a mismatch means a fixture marker missed and the mock fell through to its empty-args heuristic, a classifier re-spawned, or an unscripted turn fired", want, what, got)
	}
}

// requireTripleAbsent asserts a predicate is NOT on the run. Unlike a presence
// check this cannot "eventually" succeed, so callers must settle first — proving a
// non-event honestly means giving the system time to have done the wrong thing.
func requireTripleAbsent(ctx context.Context, t *testing.T, runEntityID, predicate, why string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	if n := countTriples(ctx, client, runEntityID, predicate); n != 0 {
		t.Fatalf("run %s carries %d %s triple(s) and must carry NONE — %s", runEntityID, n, predicate, why)
	}
}

// countTriples counts a predicate on the run using an ALREADY-OPEN client. Taking
// the client rather than dialing per call is load-bearing for the polling helpers:
// opening a NATS connection on every iteration of a requireEventually loop churns
// through connections fast enough to hit i/o timeouts, which surfaces as a
// mysterious infrastructure failure rather than the assertion under test.
func countTriples(ctx context.Context, client *natsclient.Client, runEntityID, predicate string) int {
	e, ok := scanEntities(ctx, client)[runEntityID]
	if !ok {
		return 0
	}
	n := 0
	for _, tr := range e.Triples {
		if tr.Predicate == predicate {
			n++
		}
	}
	return n
}

// runTripleCount is the one-shot form (opens and closes its own client).
func runTripleCount(ctx context.Context, t *testing.T, runEntityID, predicate string) int {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	return countTriples(ctx, client, runEntityID, predicate)
}

// runPhase returns the run's current agent.run.phase (empty if unset).
func runPhase(ctx context.Context, t *testing.T, runEntityID string) string {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	e, ok := scanEntities(ctx, client)[runEntityID]
	if !ok {
		return ""
	}
	return tripleString(e, agentrun.PhasePredicate)
}

// requireTransparencyPosted asserts the apply consumer announced what it was about
// to do BEFORE it did it (design D8 guard 4). With no harness floor under this
// gate, the announcement is the human's only view of an inferred decision.
func requireTransparencyPosted(t *testing.T, d *forgetest.Double, want string) {
	t.Helper()
	requireEventually(t, 30*time.Second, func() bool {
		for _, c := range d.Comments() {
			if strings.Contains(c.Body, want) {
				return true
			}
		}
		return false
	}, "the apply consumer never posted a transparency comment containing "+want+
		" — the gate moved without telling the human, which is the one thing guard 4 exists to prevent")
}

// requireNoTransparencyPosted asserts semdev stayed SILENT — no decision
// announcement AND no fallback note.
//
// The fault-note half is the one that matters most (grp6-review H2): the recorded
// mirror is now a safety discriminator, and if it ever stops landing — wrong
// PlatformMeta, a lost birth race, a graph blip — then EVERY successful
// classification draws "🤔 I couldn't read that" onto a human's thread, right
// after semdev announced the decision it did take. Nothing else in the suite
// notices, because the run-level intent was already stamped and the route already
// fired before the mirror is even attempted. This assertion is the guard for that
// whole class, which is why the approve, reject, and none journeys all call it.
func requireNoTransparencyPosted(t *testing.T, d *forgetest.Double) {
	t.Helper()
	for _, c := range d.Comments() {
		switch {
		case strings.Contains(c.Body, "Approving this change"), strings.Contains(c.Body, "Cancelling this run"):
			t.Fatalf("semdev announced a decision it should not have taken: %q", c.Body)
		case strings.Contains(c.Body, faultNoteMarker):
			t.Fatalf("semdev posted the fallback note for a classification that LANDED: %q — the recorded mirror is missing, so the fault-note rule now fires on every successful classification", c.Body)
		}
	}
}

// requireNoFaultNote asserts only the fault-note half, for journeys that DO expect
// a decision announcement.
func requireNoFaultNote(t *testing.T, d *forgetest.Double) {
	t.Helper()
	for _, c := range d.Comments() {
		if strings.Contains(c.Body, faultNoteMarker) {
			t.Fatalf("semdev posted the fallback note alongside a decision it DID apply: %q — the human gets a decision announcement immediately contradicted by 'I couldn't read that', which teaches them the announcements are unreliable (the one thing D8 guard 4 cannot afford)", c.Body)
		}
	}
}

// requireFaultNotePosted asserts the fallback note SPECIFICALLY — the deterministic
// guard for the shipped bug, as opposed to requireHumanWasTold's disjunction.
func requireFaultNotePosted(ctx context.Context, t *testing.T, d *forgetest.Double) {
	t.Helper()
	requireEventually(t, 90*time.Second, func() bool {
		for _, c := range d.Comments() {
			if strings.Contains(c.Body, faultNoteMarker) {
				return true
			}
		}
		return false
	}, "the fallback note never posted. A classifier that produced NO reading must tell the human — silence is indistinguishable from being ignored (D9/HIGH-3). "+
		"Check that conversation/05 keys on the ABSENCE of conversation.classifier.recorded and NOT on agent.loop.outcome: a tool error does not fail its loop, so a refusing classifier terminates outcome=success and an outcome-keyed rule never fires")
}

// requireHumanWasTold asserts semdev said SOMETHING back. NOTE this is a
// DISJUNCTION and therefore a weak guard on its own (grp6-review L3): a wholesale
// NL-lane regression in which every classification faults would satisfy it via the
// fault note. It is the right assertion only where either outcome is legitimate;
// the specific guards are requireFaultNotePosted and requireNoFaultNote.
//
// Original doc: asserts semdev said SOMETHING back — either it announced a
// decision it applied, or it said it could not read the message. The one outcome
// this forbids is silence, which from the human's side is indistinguishable from
// being ignored (design D9 / HIGH-3).
func requireHumanWasTold(t *testing.T, d *forgetest.Double) {
	t.Helper()
	requireEventually(t, 90*time.Second, func() bool {
		for _, c := range d.Comments() {
			if strings.Contains(c.Body, "Approving this change") ||
				strings.Contains(c.Body, "Cancelling this run") ||
				strings.Contains(c.Body, "couldn't read that") {
				return true
			}
		}
		return false
	}, "semdev never replied to the human at all — it neither applied a decision nor said it could not read the message. "+
		"Silence here is the D9/HIGH-3 dead-end: the human wrote a directive and watched nothing happen. "+
		"If the classification was legitimately lost (two messages in one poll tick move the latest-wins pending slot "+
		"off the dispatched marker, and classify_intent refuses to misattribute), the fallback note MUST fire — check "+
		"that conversation/05 keys on the ABSENCE of conversation.classifier.recorded and NOT on agent.loop.outcome, "+
		"because a tool error does not fail its loop")
}

// requireDecision polls until the run carries run.change.decision == want. Since D13
// the gate is ONE single-valued fact, so asserting a decision is a VALUE check —
// predicate presence alone would pass for the OPPOSITE decision.
func requireDecision(ctx context.Context, t *testing.T, runEntityID, want, hint string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	// Polled INLINE rather than through requireEventually so the diagnostic can name
	// what actually landed. requireEventually takes a pre-composed string, which would
	// capture the observed value before the first poll — every failure would read
	// "(saw )" and drop the one field that distinguishes "nothing decided" from "the
	// OPPOSITE decision landed".
	deadline := time.Now().Add(30 * time.Second)
	seen := ""
	for {
		if e, found := scanEntities(ctx, client)[runEntityID]; found {
			seen = tripleString(e, admission.DecisionPredicate)
			if seen == want {
				return
			}
		}
		if time.Now().After(deadline) {
			got := seen
			if got == "" {
				got = "<undecided>"
			}
			t.Fatalf("run %s never reached run.change.decision==%s (saw %s) — %s",
				runEntityID, want, got, hint)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// runDecision returns the run's current decision value ("" = undecided).
func runDecision(ctx context.Context, t *testing.T, runEntityID string) string {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	e, ok := scanEntities(ctx, client)[runEntityID]
	if !ok {
		return ""
	}
	return tripleString(e, admission.DecisionPredicate)
}

// decisionCount returns how many run.change.decision triples the run carries. The fact
// is single-valued, so anything but 0 or 1 is itself the defect.
func decisionCount(ctx context.Context, t *testing.T, runEntityID string) int {
	return runTripleCount(ctx, t, runEntityID, admission.DecisionPredicate)
}
