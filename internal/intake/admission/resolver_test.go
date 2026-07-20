package admission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
)

// fakeRequester scripts the classified prefix-query surface the resolver reads
// through — one JSON response per page, or a transport error.
type fakeRequester struct {
	pages [][]byte
	err   error
	calls int
}

func (f *fakeRequester) RequestClassified(_ context.Context, _ string, _ []byte, _ time.Duration) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls
	f.calls++
	if i < len(f.pages) {
		return f.pages[i], nil
	}
	b, _ := json.Marshal(graph.PrefixQueryResponse{}) // terminal empty page
	return b, nil
}

func newTestResolver(req classifiedRequester) *NATSRunResolver {
	return &NATSRunResolver{client: req, prefix: "c360.semdev.agent.chain.execution"}
}

func runEntity(id, phase, ref string) graph.EntityState {
	e := graph.EntityState{ID: id}
	if phase != "" {
		e.Triples = append(e.Triples, message.Triple{Subject: id, Predicate: agentrun.PhasePredicate, Object: phase})
	}
	if ref != "" {
		e.Triples = append(e.Triples, message.Triple{Subject: id, Predicate: "run.issue.ref", Object: ref})
	}
	return e
}

func mustPage(t *testing.T, resp graph.PrefixQueryResponse) []byte {
	t.Helper()
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	return b
}

// TestResolverSurfacesPhase pins the M7 phase getter (nl-conversation-intent
// D4): ResolveRunByRef surfaces agent.run.phase alongside the run id + approved
// state, so handleMessage can gate the NL bridge on awaiting_approval WITHOUT a
// second query. A run with an approved fact reports approved==true and its own
// phase; a gated run reports phase=="awaiting_approval", approved==false.
func TestResolverSurfacesPhase(t *testing.T) {
	gated := runEntity("run-1", "awaiting_approval", "acme/widgets#1")
	approved := runEntity("run-2", "executing", "acme/widgets#2")
	approved.Triples = append(approved.Triples,
		message.Triple{Subject: "run-2", Predicate: ApprovedPredicate, Object: "true"})
	page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{gated, approved}})

	runID, wasApproved, phase, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
		ResolveRunByRef(context.Background(), "acme/widgets#1")
	if err != nil {
		t.Fatalf("ResolveRunByRef: %v", err)
	}
	if runID != "run-1" {
		t.Errorf("runID = %q, want run-1", runID)
	}
	if wasApproved {
		t.Errorf("approved = true, want false (no run.change.approved fact on the gated run)")
	}
	if phase != "awaiting_approval" {
		t.Fatalf("phase = %q, want awaiting_approval — the resolver must surface agent.run.phase (M7)", phase)
	}

	// The approved run reports its own phase + approved state.
	_, wasApproved2, phase2, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
		ResolveRunByRef(context.Background(), "acme/widgets#2")
	if err != nil {
		t.Fatalf("ResolveRunByRef(approved): %v", err)
	}
	if !wasApproved2 {
		t.Errorf("approved = false, want true")
	}
	if phase2 != "executing" {
		t.Errorf("phase = %q, want executing", phase2)
	}
}

// TestListRunsAwaitingApproval pins the poll transport's enumeration
// (pull-first-transport D2): returns the run.issue.ref of every run at
// agent.run.phase == awaiting_approval, EXCLUDES runs in any other phase or with no
// ref, and is repo-scoped when a repo is bound (review L1).
func TestListRunsAwaitingApproval(t *testing.T) {
	page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{
		runEntity("run-1", "awaiting_approval", "acme/widgets#1"), // included
		runEntity("run-2", "executing", "acme/widgets#2"),         // excluded: not awaiting
		runEntity("run-3", "awaiting_approval", "other/repo#3"),   // excluded by repo scope
		runEntity("run-4", "awaiting_approval", ""),               // excluded: no ref (unlocatable thread)
	}})

	// Repo-scoped: only this repo's awaiting-approval refs (L1).
	got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).ListRunsAwaitingApproval(context.Background(), "acme/widgets")
	if err != nil {
		t.Fatalf("ListRunsAwaitingApproval: %v", err)
	}
	if len(got) != 1 || got[0] != "acme/widgets#1" {
		t.Fatalf("repo-scoped enumeration = %v, want [acme/widgets#1]", got)
	}

	// Unscoped (repo ""): every awaiting-approval run with a ref, foreign repos included.
	all, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).ListRunsAwaitingApproval(context.Background(), "")
	if err != nil {
		t.Fatalf("ListRunsAwaitingApproval(unscoped): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped enumeration = %v, want 2 refs (acme/widgets#1, other/repo#3)", all)
	}
}

// TestListRunsAwaitingApprovalTransportErrorPropagates pins H2 (the ADR-060
// silent-success class): a classified transport error must PROPAGATE as an error,
// NEVER be decoded as an empty slice. Conflating them would make a persistent graph
// fault present as "no runs awaiting" and the poller idle forever — a run parked at
// awaiting_approval with no /semdev approve ever landing.
func TestListRunsAwaitingApprovalTransportErrorPropagates(t *testing.T) {
	got, err := newTestResolver(&fakeRequester{err: errors.New("nats classified fault")}).
		ListRunsAwaitingApproval(context.Background(), "acme/widgets")
	if err == nil {
		t.Fatal("a classified transport error must propagate as err, not decode to an empty slice (H2)")
	}
	if got != nil {
		t.Errorf("on error the result must be nil, got %v", got)
	}
}
