package conversationchannel

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/forge/conversation"
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
