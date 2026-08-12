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
// D4): ResolveRunByRef surfaces agent.run.phase alongside the run id + the gate
// decision, so handleMessage can gate the NL bridge on awaiting_approval WITHOUT a
// second query. A gated run reports phase=="awaiting_approval" and NO decision; a
// decided run reports its own phase and its decision value (D13 — one single-valued
// fact, never a pair of contradictable booleans).
func TestResolverSurfacesPhase(t *testing.T) {
	gated := runEntity("run-1", "awaiting_approval", "acme/widgets#1")
	approved := runEntity("run-2", "executing", "acme/widgets#2")
	approved.Triples = append(approved.Triples,
		message.Triple{Subject: "run-2", Predicate: DecisionPredicate, Object: DecisionApprove})
	page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{gated, approved}})

	got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
		ResolveRunByRef(context.Background(), "acme/widgets#1")
	if err != nil {
		t.Fatalf("ResolveRunByRef: %v", err)
	}
	if got.EntityID != "run-1" {
		t.Errorf("EntityID = %q, want run-1", got.EntityID)
	}
	if got.Decided() {
		t.Errorf("Decision = %q, want \"\" (the gated run carries no decision)", got.Decision)
	}
	if got.Phase != "awaiting_approval" {
		t.Fatalf("Phase = %q, want awaiting_approval — the resolver must surface agent.run.phase (M7)", got.Phase)
	}

	// The decided run reports its own phase + decision value.
	got2, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
		ResolveRunByRef(context.Background(), "acme/widgets#2")
	if err != nil {
		t.Fatalf("ResolveRunByRef(approved): %v", err)
	}
	if got2.Decision != DecisionApprove {
		t.Errorf("Decision = %q, want %q", got2.Decision, DecisionApprove)
	}
	if got2.Phase != "executing" {
		t.Errorf("phase = %q, want executing", got2.Phase)
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

// runEntityAt is runEntity plus the FRAMEWORK's last-transition-at audit fact — the value
// preferRun's second tier orders on and (D12a) the gate-open watermark.
//
// Note it seeds an RFC3339Nano OBJECT, not the triple's Timestamp metadata field. That is
// the contract the resolver reads, deliberately: the audit predicate is a declared fact
// with a documented format and a single framework writer, whereas triple metadata is
// incidental bookkeeping. A fixture seeding the metadata would pass against an
// implementation that read it and silently stop proving anything when the source changed —
// which is exactly what happened to an earlier draft of this helper.
func runEntityAt(id, phase, ref string, at time.Time) graph.EntityState {
	e := runEntity(id, phase, ref)
	e.Triples = append(e.Triples, message.Triple{
		Subject:   id,
		Predicate: LastTransitionAtPredicate,
		Object:    at.Format(time.RFC3339Nano),
	})
	return e
}

// TestResolverPrefersTheActiveGatedRun pins D12b — the deterministic resolution that
// replaced first-match-in-page-order.
//
// WHY IT MATTERS: a re-triggered issue leaves an OLD run carrying the SAME run.issue.ref.
// Under first-match, whichever run paging happened to reach first decided whose gate a
// comment released — the enabling half of the historical-approval defect (external review
// #1). Order is now total: at-the-gate ≻ newest gate-open ≻ entity ID.
//
// NOTE the sequencing hazard this pin exists to bound: making the gated run ALWAYS win
// turns a probabilistic mis-resolution into a deterministic one, so a historical comment
// re-read from a cursor-zero restart now reliably targets the FRESH gated run. That is
// only safe once the D12a watermark (msg.At > gateOpenedAt) also lands.
func TestResolverPrefersTheActiveGatedRun(t *testing.T) {
	const ref = "acme/widgets#1"
	older := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	t.Run("a gated run beats a terminal one regardless of page order", func(t *testing.T) {
		// The terminal run is listed FIRST, so first-match would have returned it.
		page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{
			runEntityAt("run-old", "completed", ref, newer),
			runEntityAt("run-gated", PhaseAwaitingApproval, ref, older),
		}})
		got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
			ResolveRunByRef(context.Background(), ref)
		if err != nil {
			t.Fatalf("ResolveRunByRef: %v", err)
		}
		if got.EntityID != "run-gated" {
			t.Fatalf("resolved %q, want run-gated — a decision can only apply to the run "+
				"actually asking for one, whatever order paging returns them in", got.EntityID)
		}
	})

	t.Run("among gated runs the newest gate-open wins, across a page boundary", func(t *testing.T) {
		p1 := mustPage(t, graph.PrefixQueryResponse{
			Entities:   []graph.EntityState{runEntityAt("run-a", PhaseAwaitingApproval, ref, older)},
			NextCursor: "c1",
		})
		p2 := mustPage(t, graph.PrefixQueryResponse{
			Entities: []graph.EntityState{runEntityAt("run-b", PhaseAwaitingApproval, ref, newer)},
		})
		got, err := newTestResolver(&fakeRequester{pages: [][]byte{p1, p2}}).
			ResolveRunByRef(context.Background(), ref)
		if err != nil {
			t.Fatalf("ResolveRunByRef: %v", err)
		}
		if got.EntityID != "run-b" {
			t.Fatalf("resolved %q, want run-b (the freshest proposal) — the scan must cross "+
				"page boundaries before choosing, not return the first page's match", got.EntityID)
		}
	})

	t.Run("identical gate-open times break on entity ID, stably", func(t *testing.T) {
		same := older
		forward := []graph.EntityState{
			runEntityAt("run-b", PhaseAwaitingApproval, ref, same),
			runEntityAt("run-a", PhaseAwaitingApproval, ref, same),
		}
		reversed := []graph.EntityState{forward[1], forward[0]}
		for name, ents := range map[string][]graph.EntityState{"forward": forward, "reversed": reversed} {
			page := mustPage(t, graph.PrefixQueryResponse{Entities: ents})
			got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
				ResolveRunByRef(context.Background(), ref)
			if err != nil {
				t.Fatalf("%s: ResolveRunByRef: %v", name, err)
			}
			if got.EntityID != "run-a" {
				t.Fatalf("%s: resolved %q, want run-a — the tiebreak must be a TOTAL order, so "+
					"the same graph resolves the same way on every call", name, got.EntityID)
			}
		}
	})
}

// TestResolverReturnsAMatchFoundBeforePageExhaustion pins the regression the deterministic
// scan introduced and the review caught.
//
// The old first-match loop short-circuited, so it never hit the page cap once it had a
// match. Scanning every page to APPLY a preference means the cap is now reachable with a
// perfectly good match already in hand — and discarding it turns every approval into a
// transient error, which redelivers to MaxDeliver and then vanishes. `/semdev approve`
// would silently stop working on EVERY run once the chain-execution prefix outgrew
// maxRunPages*1000 entities. The ordering is a PREFERENCE, not a correctness requirement,
// so a partial scan must degrade to "a deterministically-chosen run", never to nothing.
func TestResolverReturnsAMatchFoundBeforePageExhaustion(t *testing.T) {
	const ref = "acme/widgets#1"
	pages := make([][]byte, 0, maxRunPages+2)
	// The match is on page 1; every later page is full and keeps handing back a cursor,
	// so the scan runs out of pages long after it found what it needed.
	pages = append(pages, mustPage(t, graph.PrefixQueryResponse{
		Entities:   []graph.EntityState{runEntity("run-gated", PhaseAwaitingApproval, ref)},
		NextCursor: "more",
	}))
	for i := 0; i < maxRunPages+1; i++ {
		pages = append(pages, mustPage(t, graph.PrefixQueryResponse{
			Entities:   []graph.EntityState{runEntity("noise", "executing", "other/repo#9")},
			NextCursor: "more",
		}))
	}

	got, err := newTestResolver(&fakeRequester{pages: pages}).ResolveRunByRef(context.Background(), ref)
	if err != nil {
		t.Fatalf("a run found before page exhaustion must RESOLVE, not error: %v — "+
			"erroring here makes every approval transient and the gate unreachable", err)
	}
	if got.EntityID != "run-gated" {
		t.Fatalf("resolved %q, want run-gated", got.EntityID)
	}
}

// TestResolverSurfacesGateOpenedAt pins the D12a watermark SOURCE.
//
// It is read from the framework's declared audit fact (agent.run.last-transition-at,
// RFC3339Nano) rather than the phase triple's Timestamp metadata, because a security
// guard must read something a contract PROMISES is populated, not something that happens
// to be there. And it is surfaced ONLY while the run is at the gate: "gate opened at"
// carrying some other transition's time is a lie the moment a caller compares a message
// timestamp against it.
func TestResolverSurfacesGateOpenedAt(t *testing.T) {
	opened := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	t.Run("a gated run reports when its gate opened", func(t *testing.T) {
		page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{
			runEntityAt("run-1", PhaseAwaitingApproval, "acme/widgets#1", opened),
		}})
		got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
			ResolveRunByRef(context.Background(), "acme/widgets#1")
		if err != nil {
			t.Fatalf("ResolveRunByRef: %v", err)
		}
		if !got.GateOpenedAt.Equal(opened) {
			t.Fatalf("GateOpenedAt = %v, want %v — without it every inbound message fails "+
				"closed and the gate is unreachable", got.GateOpenedAt, opened)
		}
	})

	t.Run("a run NOT at the gate reports no gate-open time", func(t *testing.T) {
		page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{
			runEntityAt("run-2", "executing", "acme/widgets#2", opened),
		}})
		got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
			ResolveRunByRef(context.Background(), "acme/widgets#2")
		if err != nil {
			t.Fatalf("ResolveRunByRef: %v", err)
		}
		if !got.GateOpenedAt.IsZero() {
			t.Fatalf("GateOpenedAt = %v on a run at %q, want zero — the last transition is "+
				"the gate opening ONLY while the run is still at the gate", got.GateOpenedAt, got.Phase)
		}
	})

	t.Run("an unparseable timestamp yields zero, which fails closed downstream", func(t *testing.T) {
		e := runEntity("run-3", PhaseAwaitingApproval, "acme/widgets#3")
		e.Triples = append(e.Triples, message.Triple{
			Subject: "run-3", Predicate: LastTransitionAtPredicate, Object: "not-a-timestamp"})
		page := mustPage(t, graph.PrefixQueryResponse{Entities: []graph.EntityState{e}})
		got, err := newTestResolver(&fakeRequester{pages: [][]byte{page}}).
			ResolveRunByRef(context.Background(), "acme/widgets#3")
		if err != nil {
			t.Fatalf("ResolveRunByRef: %v", err)
		}
		if !got.GateOpenedAt.IsZero() {
			t.Fatal("a malformed timestamp must yield ZERO (fail closed downstream), never a " +
				"partially-parsed or defaulted time that would admit historical messages")
		}
	})
}
