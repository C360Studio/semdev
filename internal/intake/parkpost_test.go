package intake

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
)

type postedComment struct {
	owner, repo string
	number      int
	body        string
}

type fakeCommenter struct {
	posted []postedComment
	err    error
}

func (f *fakeCommenter) CreateComment(_ context.Context, owner, repo string, number int, body string) error {
	if f.err != nil {
		return f.err
	}
	f.posted = append(f.posted, postedComment{owner: owner, repo: repo, number: number, body: body})
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

// TestParkMessageReachesTheIssue pins the run-fired lane: the park publish's
// firing entity IS the run — the poster reads the park message + ref and posts
// the comment to the parsed issue.
func TestParkMessageReachesTheIssue(t *testing.T) {
	commenter := &fakeCommenter{}
	p := &parkPoster{
		commenter: commenter,
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
	if len(commenter.posted) != 1 {
		t.Fatalf("comments posted = %d, want 1", len(commenter.posted))
	}
	c := commenter.posted[0]
	if c.owner != "c360studio" || c.repo != "semdev-fixture" || c.number != 7 {
		t.Errorf("posted to %s/%s#%d, want c360studio/semdev-fixture#7 (parsed from run.issue.ref)", c.owner, c.repo, c.number)
	}
	if !strings.Contains(c.body, "projection-station") || !strings.Contains(c.body, postRun) {
		t.Errorf("comment body must carry the park message + the run pointer, got %q", c.body)
	}
}

// TestLoopFiredParkResolvesTheRun — a loop-fired park (06d/06g/07c/rule 06)
// publishes with the LOOP as the firing entity; the poster follows the
// agent.run.entity-id anchor.
func TestLoopFiredParkResolvesTheRun(t *testing.T) {
	commenter := &fakeCommenter{}
	p := &parkPoster{
		commenter: commenter,
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
	if len(commenter.posted) != 1 || commenter.posted[0].number != 7 {
		t.Fatalf("posted = %+v, want the loop's run resolved via the anchor", commenter.posted)
	}
}

// TestRefLessParkStaysGraphOnly — a run without run.issue.ref has nowhere to
// post: definitive ack (loud log), never an error loop.
func TestRefLessParkStaysGraphOnly(t *testing.T) {
	commenter := &fakeCommenter{}
	p := &parkPoster{
		commenter: commenter,
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{"run.awaiting.human": "parked"}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err != nil {
		t.Fatalf("ref-less park must ack, got %v", err)
	}
	if len(commenter.posted) != 0 {
		t.Errorf("posted %d comments with no ref, want 0", len(commenter.posted))
	}
}

// TestNoForgeClientSkipsQuietly — the journeys/allowlist-only boots have no
// token; the park stays graph-only with no fetch, no error, no retry loop.
func TestNoForgeClientSkipsQuietly(t *testing.T) {
	p := &parkPoster{commenter: nil, fetcher: &fakeFetcher{}, logger: slog.Default()}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"x"}`)); err != nil {
		t.Fatalf("no-client must ack, got %v", err)
	}
}

// TestPostingFaultRedelivers — a forge blip must NOT lose the notification:
// the handler errors so the consumer redelivers (bounded), while the park fact
// itself is already durable upstream.
func TestPostingFaultRedelivers(t *testing.T) {
	p := &parkPoster{
		commenter: &fakeCommenter{err: errors.New("forge 502")},
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

// TestParkRacePublishBeforeFactRedelivers — the publish can land before the
// park add (per-action revisions); the poster redelivers until the message
// exists so the comment never posts empty.
func TestParkRacePublishBeforeFactRedelivers(t *testing.T) {
	p := &parkPoster{
		commenter: &fakeCommenter{},
		fetcher: &fakeFetcher{entities: map[string]*graph.EntityState{
			postRun: entityWith(postRun, map[string]string{"run.issue.ref": "c360studio/semdev-fixture#7"}),
		}},
		logger: slog.Default(),
	}
	if err := p.handleUserResponse(context.Background(), []byte(`{"entity_id":"`+postRun+`"}`)); err == nil {
		t.Fatal("publish-before-park must redeliver until run.awaiting.human exists")
	}
}

func TestSplitRef(t *testing.T) {
	owner, repo, n, err := splitRef("c360studio/semdev-fixture#7")
	if err != nil || owner != "c360studio" || repo != "semdev-fixture" || n != 7 {
		t.Errorf("splitRef = %s/%s#%d (%v)", owner, repo, n, err)
	}
	for _, bad := range []string{"", "no-hash", "#7", "owner#7", "o/r#zero", "o/r#0"} {
		if _, _, _, err := splitRef(bad); err == nil {
			t.Errorf("splitRef(%q) must error", bad)
		}
	}
}
