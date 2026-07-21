package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/natsclient"
)

// PhaseAwaitingApproval is the run phase at the change-approval gate — the phase
// the poll transport enumerates (pull-first-transport D2). It is a framework
// phase VALUE (agentrun's state machine) semdev READS, never writes (G2). The
// PREDICATE is agentrun.PhasePredicate ("agent.run.phase").
const PhaseAwaitingApproval = "awaiting_approval"

// classifiedRequester is the classified request/response surface the resolver's
// read-only prefix queries need — *natsclient.Client satisfies it. Abstracting it
// (rather than holding the concrete client) makes the enumeration unit-testable and
// keeps the ADR-060 contract pinnable offline: a classified ERROR body must
// PROPAGATE as err, never decode as a zero-valued empty response.
type classifiedRequester interface {
	RequestClassified(ctx context.Context, subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// DecisionPredicate is the SINGLE-VALUED change-approval decision fact — the one gate
// fact the lifecycle rules key on (nl-conversation-intent D13, group 8). It REPLACES the
// former boolean pair run.change.approved / run.change.rejected.
//
// WHY ONE FACT (external review #2, CONFIRMED): with two independent booleans a run could
// carry BOTH — the exact-command fast-paths stamped one without ever reading the other —
// and the mutually-exclusive lifecycle rules then fired NEITHER. That run was wedged at the
// gate permanently: no park (a park at this gate is itself unrecoverable, the B1 wedge), no
// post, no operator surface. The design previously accepted that cell as "an unsurfaced
// stall" and documented it. Documenting a permanent wedge is not fixing it.
//
// Because the graph is REPLACE-BY-PREDICATE, a single-valued decision makes the
// contradictory state UNREPRESENTABLE: the cell space collapses from four cells
// (neither/approved/rejected/BOTH) to three, and the wedge is gone by construction rather
// than by a partition every rule must remember to carry. The worst a race can now produce
// is ONE of two decisions a human actually asked for.
//
// The writer (Source) is the approval adapter's own const — ONE logical writer across the
// exact-command fast-path and the apply consumer, censused (G5).
const DecisionPredicate = "run.change.decision"

// DecisionApprove and DecisionReject are the ONLY legal DecisionPredicate objects. The
// resume rule (run-lifecycle/02) matches approve; the phase-guarded cancel rule
// (run-lifecycle/07) matches reject.
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

// LastTransitionAtPredicate is the FRAMEWORK-written RFC3339Nano timestamp of a run's
// most recent lifecycle transition (agentrun's audit spec — `AuditPredicates.At`, stamped
// by the lifecycle manager on create AND on every transition). semdev READS it, never
// writes it (G2).
//
// While a run's phase IS awaiting_approval, its last transition is by definition the one
// that ENTERED awaiting_approval — so this fact is exactly "when the change-approval gate
// opened", which is the D12a watermark. It is deliberately preferred over the phase
// triple's own `Timestamp` metadata field: this is a first-class declared fact with a
// documented format and a single framework writer, whereas triple metadata is incidental
// bookkeeping that no contract obliges any writer to populate. A watermark is a security
// guard; it must read something that is promised, not something that happens to be there.
const LastTransitionAtPredicate = "agent.run.last-transition-at"

// RunState is what the resolver reports about the run bound to an issue ref. It is a
// STRUCT rather than a return tuple because the callers need three independent facts
// about the run (its identity, whether the gate is already decided, and where it is in
// its lifecycle) and adding a fourth to a positional tuple is how a caller silently
// swaps two same-typed values.
//
// A zero RunState means NO run carries the ref (yet) — the wake→mint race, or a ref that
// never admitted. Callers MUST check EntityID before trusting any other field.
type RunState struct {
	// EntityID is the run's graph entity ID; "" when no run carries the ref.
	EntityID string
	// Decision is the run's single-valued change-approval decision — "" (undecided),
	// DecisionApprove, or DecisionReject (D13).
	Decision string
	// Phase is the run's agent.run.phase — the M7 getter the NL bridge gates on
	// (awaiting_approval). semdev READS it, never writes it (G2).
	Phase string
	// GateOpenedAt is the moment this run entered awaiting_approval — the D12a
	// watermark. It is set ONLY while Phase is awaiting_approval: a "gate opened at"
	// carrying some other transition's time would be a lie the moment a caller compares
	// a message timestamp against it. Zero on a gated run means the watermark could NOT
	// be established, which callers MUST treat as fail-closed, never as "no lower bound".
	GateOpenedAt time.Time
}

// Decided reports whether the change-approval gate already carries a decision.
func (s RunState) Decided() bool { return s.Decision != "" }

// RunResolver finds the run entity bound to a host-neutral issue ref via its
// rule-stamped run.issue.ref fact. Implemented over the graph's prefix query;
// faked in unit pins. Shared by issue-intake (duplicate-delivery discrimination),
// the conversation-channel approval adapter, and the operator launch driver.
type RunResolver interface {
	// ResolveRunByRef returns the state of the run carrying run.issue.ref == ref, a
	// ZERO RunState when none exists (yet), or an error on a transport fault.
	ResolveRunByRef(ctx context.Context, ref string) (RunState, error)
}

// NATSRunResolver resolves ref→run over the graph's paginated prefix query
// (the framework's lesson-reader pattern): list this platform's
// chain-execution entities, match run.issue.ref.
type NATSRunResolver struct {
	client classifiedRequester
	// prefix is the 5-part chain namespace {org}.{platform}.agent.chain.execution.
	prefix string
}

// maxRunPages bounds the pagination defensively (a page is 1000 entities).
const maxRunPages = 16

// ResolveRunByRef returns the state of the run carrying run.issue.ref == ref (its
// single-valued change-approval decision and its agent.run.phase), a ZERO RunState when
// none exists yet, or an error on a transport fault — the RunResolver contract, over the
// graph prefix query.
//
// RESOLUTION IS DETERMINISTIC, not first-match-in-page-order (D12b, external review #1).
// This function previously returned the first ref-matching entity it encountered and
// conceded in a comment that "two runs sharing one ref is reachable". That concession is
// the enabling half of the historical-approval defect: a re-triggered issue leaves an OLD
// run carrying the same ref, and whichever one paging happened to reach first decided
// whose gate a comment released. The order is now explicit and total:
//
//  1. a run AT the change-approval gate beats one that is not — a decision can only
//     sensibly apply to the run actually asking for one;
//  2. among those, the most recently OPENED gate wins (the freshest proposal);
//  3. ties break on entity ID, so the result is stable across pages and repeat calls.
//
// Every page is scanned before choosing; the loop no longer returns early.
func (r *NATSRunResolver) ResolveRunByRef(ctx context.Context, ref string) (RunState, error) {
	var best RunState
	found := false

	cursor := ""
	for page := 0; page < maxRunPages; page++ {
		req := graph.PrefixQueryRequest{Prefix: r.prefix, Cursor: cursor}
		data, err := json.Marshal(req)
		if err != nil {
			return RunState{}, err
		}
		respData, err := r.client.RequestClassified(ctx, "graph.ingest.query.prefix", data, 5*time.Second)
		if err != nil {
			return RunState{}, fmt.Errorf("prefix query: %w", err)
		}
		var resp graph.PrefixQueryResponse
		if err := json.Unmarshal(respData, &resp); err != nil {
			return RunState{}, fmt.Errorf("decode prefix response: %w", err)
		}
		for i := range resp.Entities {
			e := &resp.Entities[i]
			var refMatch bool
			var cand RunState
			var lastTransition time.Time
			for _, tr := range e.Triples {
				switch tr.Predicate {
				case "run.issue.ref":
					if s, ok := tr.Object.(string); ok && s == ref {
						refMatch = true
					}
				case DecisionPredicate:
					if s, ok := tr.Object.(string); ok {
						cand.Decision = s
					}
				case agentrun.PhasePredicate:
					// The M7 phase getter: the framework run phase the NL bridge
					// gates on (awaiting_approval). semdev READS it, never writes it (G2).
					if s, ok := tr.Object.(string); ok {
						cand.Phase = s
					}
				case LastTransitionAtPredicate:
					if s, ok := tr.Object.(string); ok {
						if at, perr := time.Parse(time.RFC3339Nano, s); perr == nil {
							lastTransition = at
						}
					}
				}
			}
			if !refMatch {
				continue
			}
			cand.EntityID = e.ID
			// The last transition is the GATE OPENING only while the run is still at
			// the gate (see GateOpenedAt).
			if cand.Phase == PhaseAwaitingApproval {
				cand.GateOpenedAt = lastTransition
			}
			if !found || preferRun(cand, best) {
				best, found = cand, true
			}
		}
		if resp.NextCursor == "" {
			return best, nil
		}
		cursor = resp.NextCursor
	}
	// PAGE EXHAUSTION — return what we found rather than discarding it. The old
	// first-match loop short-circuited and so was immune to this; scanning every page
	// to apply a deterministic preference (D12b) reintroduced the exposure, and
	// discarding `best` here would be a REGRESSION, not a safety measure.
	//
	// Concretely: the chain-execution prefix accumulates every run semdev has ever
	// minted, so a long-lived deployment crosses maxRunPages*1000 entities. Past that
	// point, returning an error for a run that was found on page 1 makes releaseGate
	// treat every approval as transient — redeliver, MaxDeliver, gone — and
	// `/semdev approve` silently stops working on EVERY run. The ordering is a
	// PREFERENCE, not a correctness requirement, so a partial scan degrades to "a
	// deterministically-chosen run" rather than to nothing.
	if found {
		return best, nil
	}
	return RunState{}, fmt.Errorf("run resolution exceeded %d pages", maxRunPages)
}

// preferRun reports whether candidate cand beats the incumbent best under the D12b
// ordering: at-the-gate first, then most-recently-opened gate, then entity ID (a total
// order, so the winner does not depend on page arrival).
func preferRun(cand, best RunState) bool {
	candGated := cand.Phase == PhaseAwaitingApproval
	bestGated := best.Phase == PhaseAwaitingApproval
	if candGated != bestGated {
		return candGated
	}
	if !cand.GateOpenedAt.Equal(best.GateOpenedAt) {
		return cand.GateOpenedAt.After(best.GateOpenedAt)
	}
	return cand.EntityID < best.EntityID
}

// ResolveRunIDsByRef returns EVERY run entity ID carrying run.issue.ref == ref (unordered) —
// the SET the operator launch driver diffs across its front-door publish to bind the run IT
// minted, excluding any pre-existing run that already shared the ref (e.g. from a prior webhook
// wake). A read-only observation (graph prefix query), never a lifecycle write (G2).
func (r *NATSRunResolver) ResolveRunIDsByRef(ctx context.Context, ref string) ([]string, error) {
	var ids []string
	cursor := ""
	for page := 0; page < maxRunPages; page++ {
		req := graph.PrefixQueryRequest{Prefix: r.prefix, Cursor: cursor}
		data, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		respData, err := r.client.RequestClassified(ctx, "graph.ingest.query.prefix", data, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("prefix query: %w", err)
		}
		var resp graph.PrefixQueryResponse
		if err := json.Unmarshal(respData, &resp); err != nil {
			return nil, fmt.Errorf("decode prefix response: %w", err)
		}
		for i := range resp.Entities {
			e := &resp.Entities[i]
			for _, tr := range e.Triples {
				if tr.Predicate == "run.issue.ref" {
					if s, ok := tr.Object.(string); ok && s == ref {
						ids = append(ids, e.ID)
						break
					}
				}
			}
		}
		if resp.NextCursor == "" {
			return ids, nil
		}
		cursor = resp.NextCursor
	}
	return nil, fmt.Errorf("run-id enumeration exceeded %d pages", maxRunPages)
}

// ListRunsAwaitingApproval returns the run.issue.ref of every run at
// agent.run.phase == PhaseAwaitingApproval (the change-approval gate) — the threads
// the poll transport reads (pull-first-transport D2). A read-only graph prefix query
// (G2 — never a lifecycle write). When repo is bound ("owner/name"; "" = all) the
// enumeration is scoped to that repo's refs, so the poller never reads a foreign
// thread it cannot access (review L1). A run with no ref yet is skipped (its thread
// is not locatable).
//
// It uses RequestClassified so a classified ERROR body PROPAGATES as err and is NEVER
// decoded as a zero-valued (empty) response (review H2, the ADR-060 silent-success
// class): an empty Entities on a real fault would read as "no runs awaiting", the
// poller would idle, and a run would park at awaiting_approval forever — silently.
func (r *NATSRunResolver) ListRunsAwaitingApproval(ctx context.Context, repo string) ([]string, error) {
	var refs []string
	cursor := ""
	for page := 0; page < maxRunPages; page++ {
		req := graph.PrefixQueryRequest{Prefix: r.prefix, Cursor: cursor}
		data, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		respData, err := r.client.RequestClassified(ctx, "graph.ingest.query.prefix", data, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("prefix query: %w", err)
		}
		var resp graph.PrefixQueryResponse
		if err := json.Unmarshal(respData, &resp); err != nil {
			return nil, fmt.Errorf("decode prefix response: %w", err)
		}
		for i := range resp.Entities {
			e := &resp.Entities[i]
			var awaiting bool
			var ref string
			for _, tr := range e.Triples {
				switch tr.Predicate {
				case agentrun.PhasePredicate:
					if s, ok := tr.Object.(string); ok && s == PhaseAwaitingApproval {
						awaiting = true
					}
				case "run.issue.ref":
					if s, ok := tr.Object.(string); ok {
						ref = s
					}
				}
			}
			if awaiting && ref != "" && refInRepo(ref, repo) {
				refs = append(refs, ref)
			}
		}
		if resp.NextCursor == "" {
			return refs, nil
		}
		cursor = resp.NextCursor
	}
	return nil, fmt.Errorf("awaiting-approval enumeration exceeded %d pages", maxRunPages)
}

// refInRepo reports whether ref ("owner/repo#number") belongs to repo ("owner/name";
// "" matches every repo) — the poll enumeration's repo scope (L1).
func refInRepo(ref, repo string) bool {
	if repo == "" {
		return true
	}
	owner, name, _, err := SplitRef(ref)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(repo), owner+"/"+name)
}

// NewRunResolver builds the graph-backed ref→run resolver over a live NATS client — the seam
// the operator launch driver binds through (by observation; it fires no lifecycle transition,
// G2). org/platform form the 5-part chain namespace {org}.{platform}.agent.chain.execution the
// prefix query lists. It satisfies both RunResolver (first-match) and the driver's id-set read.
func NewRunResolver(client *natsclient.Client, org, platform string) *NATSRunResolver {
	return &NATSRunResolver{
		client: client,
		prefix: fmt.Sprintf("%s.%s.agent.chain.execution", org, platform),
	}
}
