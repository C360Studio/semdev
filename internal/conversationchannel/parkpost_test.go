package conversationchannel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
)

// fakeChannel records posts through the Channel port (Post + identity ResolveThread).
type postedMsg struct {
	thread conversation.ThreadRef
	body   string
}

type fakeChannel struct {
	posts      []postedMsg
	postErr    error
	resolveErr error

	// reads drives Read per-thread (the poll transport, grp3): the full comment set
	// on each thread. Read applies the SAME numeric-cursor contract as the GitHub
	// impl (keep id > cursor; empty result keeps the input cursor). readErr fails a
	// read (the transport-error-is-not-empty pin); readLog records each Read's thread.
	reads   map[conversation.ThreadRef][]conversation.Message
	readErr error
	readLog []conversation.ThreadRef
}

func (f *fakeChannel) ResolveThread(_ context.Context, workRef string) (conversation.ThreadRef, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return conversation.ThreadRef(workRef), nil // GitHub identity
}

func (f *fakeChannel) Post(_ context.Context, thread conversation.ThreadRef, body string) error {
	if f.postErr != nil {
		return f.postErr
	}
	f.posts = append(f.posts, postedMsg{thread: thread, body: body})
	return nil
}

func (f *fakeChannel) Read(_ context.Context, thread conversation.ThreadRef, cursor conversation.Cursor) ([]conversation.Message, conversation.Cursor, error) {
	f.readLog = append(f.readLog, thread)
	if f.readErr != nil {
		return nil, cursor, f.readErr
	}
	var cursorInt int64
	if cursor != "" {
		if n, err := strconv.ParseInt(string(cursor), 10, 64); err == nil {
			cursorInt = n
		}
	}
	maxID := cursorInt
	var out []conversation.Message
	for _, m := range f.reads[thread] {
		id, _ := strconv.ParseInt(m.ID, 10, 64)
		if id <= cursorInt {
			continue
		}
		out = append(out, m)
		if id > maxID {
			maxID = id
		}
	}
	if len(out) == 0 {
		return nil, cursor, nil // empty read keeps the input cursor (M2)
	}
	return out, conversation.Cursor(strconv.FormatInt(maxID, 10)), nil
}

type fakeFetcher struct {
	entities map[string]*graph.EntityState
	err      error
}

func (f *fakeFetcher) Entity(_ context.Context, id string) (*graph.EntityState, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entities[id], nil
}

func entityWith(id string, preds map[string]string) *graph.EntityState {
	e := &graph.EntityState{ID: id}
	for p, o := range preds {
		e.Triples = append(e.Triples, message.Triple{Subject: id, Predicate: p, Object: o})
	}
	return e
}

const (
	postRun  = "c360.semdev-001.agent.chain.execution.r9"
	postLoop = "c360.semdev-001.agent.agentic-loop.execution.l4"
)

// TestParkPostPostsViaPort pins the carve (conversation-channel-seam 4.2): the
// run-fired park publish's firing entity IS the run — the poster reads the park
// message + ref and posts to the resolved THREAD via Channel.Post (ResolveThread +
// Post), NOT a GitHub CreateComment call. The thread is the resolved (identity)
// coordinate; the body carries the park message + the run pointer.
func TestParkPostPostsViaPort(t *testing.T) {
	ch := &fakeChannel{}
	p := &parkPoster{
		channel: ch,
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{
				"run.awaiting.human": "station dispatch failed after retries: projection-station: no test",
				"run.issue.ref":      "c360studio/semdev-fixture#7",
			}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err != nil {
		t.Fatalf("handleUserResponse: %v", err)
	}
	if len(ch.posts) != 1 {
		t.Fatalf("posts via the port = %d, want 1", len(ch.posts))
	}
	got := ch.posts[0]
	if got.thread != conversation.ThreadRef("c360studio/semdev-fixture#7") {
		t.Errorf("posted to thread %q, want the resolved c360studio/semdev-fixture#7 (identity ResolveThread)", got.thread)
	}
	if !strings.Contains(got.body, "projection-station") || !strings.Contains(got.body, postRun) {
		t.Errorf("message body must carry the park message + the run pointer, got %q", got.body)
	}
}

// TestLoopFiredParkResolvesTheRun — a loop-fired park publishes with the LOOP as
// the firing entity; the poster follows the agent.run.entity-id anchor.
func TestLoopFiredParkResolvesTheRun(t *testing.T) {
	ch := &fakeChannel{}
	p := &parkPoster{
		channel: ch,
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postLoop: entityWith(postLoop, map[string]string{"agent.run.entity-id": postRun}),
			postRun: entityWith(postRun, map[string]string{
				"run.awaiting.human": "the dev loop exhausted its attempt budget",
				"run.issue.ref":      "c360studio/semdev-fixture#7",
			}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postLoop+`"}`)); err != nil {
		t.Fatalf("handleUserResponse: %v", err)
	}
	if len(ch.posts) != 1 || ch.posts[0].thread != conversation.ThreadRef("c360studio/semdev-fixture#7") {
		t.Fatalf("posts = %+v, want the loop's run resolved via the anchor", ch.posts)
	}
}

// TestRefLessParkStaysGraphOnly — a run without run.issue.ref has nowhere to post:
// definitive ack (loud log), never an error loop.
func TestRefLessParkStaysGraphOnly(t *testing.T) {
	ch := &fakeChannel{}
	p := &parkPoster{
		channel: ch,
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{"run.awaiting.human": "parked"}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err != nil {
		t.Fatalf("ref-less park must ack, got %v", err)
	}
	if len(ch.posts) != 0 {
		t.Errorf("posted %d messages with no ref, want 0", len(ch.posts))
	}
}

// TestMalformedRefStaysGraphOnly (grp2-review carry-forward b) — a run whose
// run.issue.ref is unparseable has nowhere to post: the poster validates the
// coordinate BEFORE posting and treats a malformed ref as a definitive graph-only
// skip, NOT a Channel.Post-error redeliver.
func TestMalformedRefStaysGraphOnly(t *testing.T) {
	ch := &fakeChannel{}
	p := &parkPoster{
		channel: ch,
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{
				"run.awaiting.human": "parked",
				"run.issue.ref":      "this-is-not-a-ref",
			}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err != nil {
		t.Fatalf("a malformed ref must ack (graph-only skip), got %v", err)
	}
	if len(ch.posts) != 0 {
		t.Errorf("posted %d messages for a malformed ref, want 0 (never redeliver a permanent bad ref)", len(ch.posts))
	}
}

// TestNoChannelSkipsQuietly — the journeys/allowlist-only boots have no token, so
// the channel is nil; the park stays graph-only with no fetch, no error, no retry
// loop (grp2-review carry-forward a).
func TestNoChannelSkipsQuietly(t *testing.T) {
	p := &parkPoster{channel: nil, fetcher: &fakeFetcher{}, logger: slog.Default()}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"x"}`)); err != nil {
		t.Fatalf("no-channel must ack, got %v", err)
	}
}

// TestPostingFaultRedelivers — a transport blip must NOT lose the notification: the
// handler errors so the consumer redelivers (bounded), while the park fact itself
// is already durable upstream.
func TestPostingFaultRedelivers(t *testing.T) {
	p := &parkPoster{
		channel: &fakeChannel{postErr: errors.New("forge 502")},
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{
				"run.awaiting.human": "parked",
				"run.issue.ref":      "c360studio/semdev-fixture#7",
			}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err == nil {
		t.Fatal("a posting fault must redeliver (bounded), not silently drop the human's notification")
	}
}

// TestParkRacePublishBeforeFactRedelivers — the publish can land before the park
// add (per-action revisions); the poster redelivers until the message exists so the
// message never posts empty.
func TestParkRacePublishBeforeFactRedelivers(t *testing.T) {
	p := &parkPoster{
		channel: &fakeChannel{},
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{"run.issue.ref": "c360studio/semdev-fixture#7"}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err == nil {
		t.Fatal("publish-before-park must redeliver until run.awaiting.human exists")
	}
}

// --- the fault-note lane (grp5-review H3: handleUserNote had zero coverage) ---

func newTestNotePoster(ch conversation.Channel, fetcher admission.EntityFetcher) *parkPoster {
	return &parkPoster{channel: ch, fetcher: fetcher, logger: slog.Default()}
}

// TestFaultNotePostsAndNeverParks pins the D9/HIGH-3 lane AND its load-bearing
// restraint: the note reaches the human, and it stamps NOTHING — in particular not
// run.awaiting.human, which run-lifecycle/02 reads as a resume blocker. A fault
// that parked the run would turn a recoverable one-message miss into a permanently
// stuck run (the same hazard as grp5-review B1).
func TestFaultNotePostsAndNeverParks(t *testing.T) {
	ctx := context.Background()
	const loopID = "c360.semdev.agent.loop.execution.classifier-1"
	const runID = "c360.semdev.agent.chain.execution.run-1"
	ch := &fakeChannel{}
	p := newTestNotePoster(ch, &fakeFetcher{entities: map[string]*graph.EntityState{
		loopID: entityWith(loopID, map[string]string{"agent.run.entity-id": runID}),
		runID:  entityWith(runID, map[string]string{"run.issue.ref": "c360studio/semdev-fixture#7"}),
	}})

	payload, err := json.Marshal(publishEnvelope{EntityID: loopID})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := p.handleUserNote(ctx, payload); err != nil {
		t.Fatalf("handleUserNote: %v", err)
	}
	if len(ch.posts) != 1 {
		t.Fatalf("want exactly one fault note posted, got %d", len(ch.posts))
	}
	if !strings.Contains(ch.posts[0].body, "/semdev approve") {
		t.Errorf("the fault note must point at the deterministic escape hatch, got %q", ch.posts[0].body)
	}
}

// TestFaultNoteDefinitiveVsTransient: a shape semdev's own rule engine could not
// have produced is acked (never redelivered forever); a forge blip redelivers (the
// note is the human's only signal that their message was not understood).
func TestFaultNoteDefinitiveVsTransient(t *testing.T) {
	ctx := context.Background()
	const loopID = "c360.semdev.agent.loop.execution.classifier-1"
	const runID = "c360.semdev.agent.chain.execution.run-1"
	entities := map[string]*graph.EntityState{
		loopID: entityWith(loopID, map[string]string{"agent.run.entity-id": runID}),
		runID:  entityWith(runID, map[string]string{"run.issue.ref": "c360studio/semdev-fixture#7"}),
	}
	good, err := json.Marshal(publishEnvelope{EntityID: loopID})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Run("malformed envelope is definitive", func(t *testing.T) {
		p := newTestNotePoster(&fakeChannel{}, &fakeFetcher{entities: entities})
		if err := p.handleUserNote(ctx, []byte("not json")); err != nil {
			t.Errorf("a malformed envelope must be acked, got: %v", err)
		}
	})
	t.Run("post failure is transient", func(t *testing.T) {
		p := newTestNotePoster(&fakeChannel{postErr: errors.New("forge 503")}, &fakeFetcher{entities: entities})
		if err := p.handleUserNote(ctx, good); err == nil {
			t.Error("a forge blip must redeliver — the note is the human's only signal")
		}
	})
	t.Run("no channel is definitive", func(t *testing.T) {
		p := newTestNotePoster(nil, &fakeFetcher{entities: entities})
		p.channel = nil
		if err := p.handleUserNote(ctx, good); err != nil {
			t.Errorf("a channel-less deployment is acked (logged loud), got: %v", err)
		}
	})
	t.Run("run without an issue ref is definitive", func(t *testing.T) {
		p := newTestNotePoster(&fakeChannel{}, &fakeFetcher{entities: map[string]*graph.EntityState{
			loopID: entityWith(loopID, map[string]string{"agent.run.entity-id": runID}),
			runID:  entityWith(runID, map[string]string{}),
		}})
		if err := p.handleUserNote(ctx, good); err != nil {
			t.Errorf("a run with nowhere to post is acked, got: %v", err)
		}
	})
}

// TestFaultNoteSuppressedOnceGateDecided pins the guard that keeps the note lane
// honest, and it is load-bearing for two separate hazards.
//
// (1) CONTRADICTION. The apply consumer's transparency post is the ENTIRE
// visibility case for a gate with no harness floor (D8 guard 4). If a note could
// follow a decision, the human would read "✅ Approving this change based on @jo's
// decision" and then "🤔 I couldn't read that" — which does not merely confuse,
// it teaches them the announcements are unreliable, dissolving the guard.
//
// (2) REPLAY BLAST RADIUS. conversation/05 publishes a human-visible comment with
// no self-extinguish marker, so if RULE_STATE were ever lost while ENTITY_STATES
// survived, every historically-unclassified conversation loop would re-fire. This
// suppression is what bounds that: every run whose gate was decided — which is
// every completed run — posts nothing on replay. The residual is runs still (or
// permanently) sitting at an undecided gate, where the note's content is still
// true. Without this guard the same replay would re-litigate settled runs.
func TestFaultNoteSuppressedOnceGateDecided(t *testing.T) {
	ctx := context.Background()
	const loopID = "c360.semdev.agent.loop.execution.classifier-1"
	const runID = "c360.semdev.agent.chain.execution.run-1"
	payload, err := json.Marshal(publishEnvelope{EntityID: loopID})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	runFacts := func(extra map[string]string) map[string]*graph.EntityState {
		run := map[string]string{"run.issue.ref": "c360studio/semdev-fixture#7"}
		for k, v := range extra {
			run[k] = v
		}
		return map[string]*graph.EntityState{
			loopID: entityWith(loopID, map[string]string{"agent.run.entity-id": runID}),
			runID:  entityWith(runID, run),
		}
	}

	for _, decided := range []string{admission.ApprovedPredicate, admission.RejectedPredicate} {
		t.Run("silent once "+decided+" landed", func(t *testing.T) {
			ch := &fakeChannel{}
			p := newTestNotePoster(ch, &fakeFetcher{entities: runFacts(map[string]string{decided: "true"})})
			if err := p.handleUserNote(ctx, payload); err != nil {
				t.Fatalf("handleUserNote: %v", err)
			}
			if len(ch.posts) != 0 {
				t.Errorf("posted %q after %s already landed — a decision was applied AND announced, so a note claiming semdev could not read the message is simply wrong", ch.posts[0].body, decided)
			}
		})
	}

	t.Run("posts while the gate is still OPEN", func(t *testing.T) {
		// The anti-vacuity half: if this stops posting, the suppression above
		// passes for the wrong reason and the whole lane is silent again.
		ch := &fakeChannel{}
		p := newTestNotePoster(ch, &fakeFetcher{entities: runFacts(nil)})
		if err := p.handleUserNote(ctx, payload); err != nil {
			t.Fatalf("handleUserNote: %v", err)
		}
		if len(ch.posts) != 1 {
			t.Fatalf("want exactly one note while the gate is undecided, got %d — the suppression must not swallow the case the lane exists for", len(ch.posts))
		}
	})
}
