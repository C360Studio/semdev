//go:build e2e

// The STATION-FAILURE PARK journey (station-failure-parks D4) + the graph
// add-semantics pin its rules lean on (task 2.2).
//
// The journey reproduces real-LLM run 1's exact failure shape (evidence ledger
// 2026-07-19) with zero paid tokens: the mock authors a change whose single task
// declares NO *_test.go in target_files → the validation station passes it (the
// CLI oracle does not enforce the includes-test contract — run 1 proved it) →
// the human approves → the projection station REFUSES (the projector's
// target_files-includes-tests contract) → the station base retries, exhausts,
// and stamps station.dispatch.failed on the RUN (the dispatched entity, G3) →
// the run-fired park rule (run-lifecycle/05) records run.awaiting.human naming
// projection → the run waits for a human, with NO task.spec, NO cold verify,
// NO delivery (no false green). Before this change the identical arc stalled
// SILENTLY after the projection ERROR log — the unattended-safety wedge M2's
// first floor closes.
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"

	"github.com/c360studio/semdev/internal/mockllm"
)

// parkJourneyChangeArgs is journeyChangeArgs with the ONE mutation that recreates
// run 1's projection refusal: target_files lists ONLY the production file — no
// health_test.go — while test_command still runs `go test`. The projector's
// includes-test contract refuses exactly this shape (the developer would have no
// writable contract to author the test that measures its own work).
func parkJourneyChangeArgs() map[string]any {
	args := journeyChangeArgs(2)
	tasks := args["tasks"].([]any)
	task0 := tasks[0].(map[string]any)
	items := task0["items"].([]any)
	item0 := items[0].(map[string]any)
	item0["target_files"] = []any{"health.go"}
	return args
}

// parkJourneyFixtures scripts ONLY the front-of-arc turns (issue_intake decide,
// create_change decide, the authoring create_change). Nothing after: projection
// refuses before task.spec lands, so the dev re-wake (dev-from-task/02, gated on
// task.spec.test-command + sandbox.provision.ready) never fires and no further
// model turn is legitimate — the journey pins that with an exact RequestCount.
func parkJourneyFixtures() []mockllm.Fixture {
	return []mockllm.Fixture{
		{Marker: journeyIssueRef, Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
			"action": journeyDecideAction, "reason": "new admitted issue " + journeyIssueRef + " needs a run"}}},
		{Marker: journeyIssueRef, Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
			"action": "create_change", "reason": "author the change for " + journeyIssueRef}}},
		{Marker: journeyIssueRef, Tool: &mockllm.ToolCall{Name: "create_change", Args: parkJourneyChangeArgs()}},
	}
}

// TestBridgeProofStationFailureParks drives run 1's projection-refusal shape end
// to end and asserts the park lands where the silence used to be.
func TestBridgeProofStationFailureParks(t *testing.T) {
	mock := mockllm.New(parkJourneyFixtures()...)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	// Front of arc through approval — the change authors and VALIDATES clean
	// (awaiting_approval is the validated-gate phase), exactly as run 1 did.
	taskID := publishCoordinatorWake(ctx, t)
	requireCoordinatorDecision(ctx, t, taskID, journeyDecideAction)
	runEntityID := requireRunAnchor(ctx, t, taskID)
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")

	// Hold the real forge POST before the failure is triggered. The recorder logs
	// request arrival before blocking it, giving the test an explicit barrier at
	// which the JetStream delivery must still be ACK-pending. Releasing the double
	// is the only event that can let Channel.Post succeed.
	releasePost := journeyForgeDouble.HoldNextCreateComment()
	defer releasePost()
	approveChange(ctx, t, rt, runEntityID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("park station 1: change authored WITHOUT its test in target_files, validated, approved — projection dispatch fires next")

	// The projection station refuses per attempt, exhausts its bounded retries,
	// stamps station.dispatch.failed on the RUN (run-dispatched, per the census),
	// and the run-fired park rule (run-lifecycle/05) parks toward the human.
	parkMsg := requireRunParked(ctx, t, runEntityID, 90*time.Second)
	if !strings.Contains(parkMsg, "projection-station") {
		t.Fatalf("the park message must name the failed station (projection-station) for the human's resume decision, got %q", parkMsg)
	}
	if !strings.Contains(parkMsg, "_test.go") {
		t.Fatalf("the park message must carry the projector's refusal (the includes-test contract) via the stamped object substitution, got %q", parkMsg)
	}
	if strings.Contains(parkMsg, "$entity.triple") {
		t.Fatalf("the park message carries an unresolved substitution token: %q", parkMsg)
	}
	requireTriplePresent(ctx, t, runEntityID, "station.dispatch.failed",
		"the station harness must stamp the terminal dispatch outcome on the RUN when the projection retries exhaust (station-failure-parks D1)")
	requireTriplePresent(ctx, t, runEntityID, "station.park.routed",
		"the run-fired park rule (run-lifecycle/05) must stamp its one-shot marker alongside the park")
	t.Logf("park station 2: run parked — run.awaiting.human names projection-station and the includes-test refusal (message %q)", parkMsg)

	// REAL PARK-POST DELIVERY + ACK ORDERING. The exact request has reached the
	// conversation-channel consumer and the forge POST is deliberately blocked.
	// The message must remain pending until that side effect succeeds — ACKing at
	// decode/start would lose the human notification on a crash or transport fault.
	requireEventually(t, 30*time.Second, func() bool {
		for _, req := range journeyForgeDouble.Requests() {
			if req.Kind == "create_comment" {
				return true
			}
		}
		return false
	}, "park-post request never reached the real Channel.Post forge adapter")
	if got := parkPostConsumerAckPending(ctx, t); got < 1 {
		t.Fatalf("park-post consumer NumAckPending = %d while Channel.Post is blocked, want >=1 — the request ACKed before its external side effect succeeded", got)
	}
	for _, comment := range journeyForgeDouble.Comments() {
		if strings.Contains(comment.Body, "semdev parked this run") {
			t.Fatalf("forge double recorded the park comment before its explicit release: %q", comment.Body)
		}
	}

	releasePost()
	requireEventually(t, 30*time.Second, func() bool {
		for _, comment := range journeyForgeDouble.Comments() {
			if comment.IssueNumber == journeyIssueNumber &&
				strings.Contains(comment.Body, "semdev parked this run") &&
				strings.Contains(comment.Body, "projection-station") {
				return true
			}
		}
		return false
	}, "the exact durable park-post request did not produce the real thread comment after the forge barrier released")
	requireEventually(t, 30*time.Second, func() bool {
		return parkPostConsumerAckPending(ctx, t) == 0
	}, "park-post request stayed ACK-pending after Channel.Post succeeded")
	t.Log("park station 2b: exact semdev.park-post.request stayed ACK-pending while the forge POST was blocked, then ACKed after the real comment landed")

	// NO FALSE GREEN (the exhaustion journeys' pattern): the refused projection
	// stamped nothing, so nothing downstream may exist. requireRunParked already
	// fails loud on delivery.pr.ref / verify.cleanroom.result during its poll;
	// this is the post-park recheck plus the projection-specific absence.
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	e, ok := scanEntities(ctx, client)[runEntityID]
	if !ok {
		t.Fatalf("run entity %s vanished from ENTITY_STATES after the park", runEntityID)
	}
	for _, pred := range []string{"task.spec.test-command", "verify.cleanroom.result", "delivery.pr.ref"} {
		if v := tripleString(e, pred); v != "" {
			t.Fatalf("parked run %s carries %s=%q — the refused projection must stamp NOTHING downstream (no false green)", runEntityID, pred, v)
		}
	}
	t.Logf("park station 3: no task.spec, no cold verify, no delivery — fail-closed")

	// Turn accounting: exactly the 3 front-of-arc turns. A 4th turn would mean
	// the dev re-wake fired without task.spec (its gate broke) or a developer
	// spawned against a never-projected task.
	if got := mock.RequestCount(); got != 3 {
		t.Fatalf("expected exactly 3 model turns (issue_intake decide + create_change decide + create_change), got %d — a projection-refused run must consume nothing further", got)
	}
}

func parkPostConsumerAckPending(ctx context.Context, t *testing.T) int {
	t.Helper()
	nc, err := nats.Connect(journeyNATSURL())
	if err != nil {
		t.Fatalf("connect for park-post consumer info: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("JetStream context for park-post consumer info: %v", err)
	}
	consumer, err := js.Consumer(ctx, "USER", "conversation-channel-park_post_requests")
	if err != nil {
		t.Fatalf("lookup durable park-post consumer: %v", err)
	}
	info, err := consumer.Info(ctx)
	if err != nil {
		t.Fatalf("read durable park-post consumer info: %v", err)
	}
	return info.NumAckPending
}

// TestPinGraphAddTripleAppendsDuplicatePredicate settles the engine add_triple
// duplicate-predicate question (station-failure-parks task 2.2, D2's open
// question) against the LIVE graph lane: a second add of an already-present
// PREDICATE with a different object APPENDS a second triple. beta.160's
// triple.append is SET-VALUED over exact tuples — only an identical
// (predicate, object) tuple dedups as MutationUnchanged; a same-predicate
// different-object add still lands beside the first. Both park rules are built
// on this answer: the one-shot station.park.routed marker is the ONLY re-fire
// protection, and a loop-fired park landing on an already-parked run yields a
// benign SECOND run.awaiting.human triple ("at most once per failure", never
// "at most one park triple per run") — park messages differ per failure, so
// tuple dedup never collapses them. If a semstreams bump flips this lane to
// replace-by-predicate, this pin fails and the rules' duplicate-park reasoning
// must be revisited.
func TestPinGraphAddTripleAppendsDuplicatePredicate(t *testing.T) {
	mock := mockllm.New() // no model turns — the pin only needs the graph substrate

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	startJourneyRuntime(ctx, t, mock)

	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	pub := agentictools.NewNATSTriplePublisher(client)

	// A synthetic run-shaped entity (canonical 6-part chain grammar; the
	// declared park predicate) so the pin exercises the exact production shape.
	// No park rule can fire on it: station.dispatch.failed is absent.
	const entityID = "c360.semdev.agent.chain.execution.addsemanticspin"
	birth := message.Triple{
		Subject:    entityID,
		Predicate:  "run.awaiting.human",
		Object:     "first park message",
		Source:     "park-rule",
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := pub.Create(ctx, entityID,
		message.Type{Domain: "semdev", Category: "park_pin", Version: "v1"},
		[]message.Triple{birth}); err != nil {
		t.Fatalf("create pin entity: %v", err)
	}

	dup := birth
	dup.Object = "second park message (duplicate-predicate add)"
	dup.Timestamp = time.Now().UTC()
	if err := pub.Append(ctx, []message.Triple{dup}); err != nil {
		t.Fatalf("duplicate-predicate add: %v", err)
	}

	requireEventually(t, 15*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[entityID]
		if !ok {
			return false
		}
		count := 0
		for _, tr := range e.Triples {
			if tr.Predicate == "run.awaiting.human" {
				count++
			}
		}
		return count == 2
	}, "the duplicate-predicate add did not land as a SECOND run.awaiting.human triple — "+
		"graph.mutation.triple.add is pinned as an unconditional APPEND (the park rules' marker-only "+
		"re-fire protection and the benign-duplicate-park trade both rest on it); a count of 1 means "+
		"the lane became replace-by-predicate and the park rules' D2 reasoning must be revisited")
}
