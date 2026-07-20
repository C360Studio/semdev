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

// ApprovedPredicate is the change-approval fact the approval adapter stamps on the
// run and the resume rule (run-lifecycle/02) matches. It lives here because BOTH
// the RunResolver (which reports whether a run is already approved) and the
// approval adapter (which writes it) reference it — the shared admission core is
// their common home. The WRITER (Source) is the approval adapter's own const.
const ApprovedPredicate = "run.change.approved"

// RunResolver finds the run entity bound to a host-neutral issue ref via its
// rule-stamped run.issue.ref fact. Implemented over the graph's prefix query;
// faked in unit pins. Shared by issue-intake (duplicate-delivery discrimination),
// the conversation-channel approval adapter, and the operator launch driver.
type RunResolver interface {
	// ResolveRunByRef returns the run entity ID carrying run.issue.ref == ref,
	// "" when none exists (yet), or an error on a transport fault.
	ResolveRunByRef(ctx context.Context, ref string) (runEntityID string, approved bool, err error)
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

// ResolveRunByRef returns the run entity ID carrying run.issue.ref == ref (with
// its run.change.approved state), "" when none exists yet, or an error on a
// transport fault — the RunResolver contract, over the graph prefix query.
func (r *NATSRunResolver) ResolveRunByRef(ctx context.Context, ref string) (string, bool, error) {
	cursor := ""
	for page := 0; page < maxRunPages; page++ {
		req := graph.PrefixQueryRequest{Prefix: r.prefix, Cursor: cursor}
		data, err := json.Marshal(req)
		if err != nil {
			return "", false, err
		}
		respData, err := r.client.RequestClassified(ctx, "graph.ingest.query.prefix", data, 5*time.Second)
		if err != nil {
			return "", false, fmt.Errorf("prefix query: %w", err)
		}
		var resp graph.PrefixQueryResponse
		if err := json.Unmarshal(respData, &resp); err != nil {
			return "", false, fmt.Errorf("decode prefix response: %w", err)
		}
		for i := range resp.Entities {
			e := &resp.Entities[i]
			var refMatch, approved bool
			for _, tr := range e.Triples {
				if tr.Predicate == "run.issue.ref" {
					if s, ok := tr.Object.(string); ok && s == ref {
						refMatch = true
					}
				}
				// approved = the VALUE the resume rule matches ("true"), not
				// mere presence (review finding).
				if tr.Predicate == ApprovedPredicate {
					if s, ok := tr.Object.(string); ok && s == "true" {
						approved = true
					}
				}
			}
			if refMatch {
				// First match in page order. Two runs sharing one ref is
				// reachable only via a re-triggered issue after a terminal
				// park; when the resume lane lands, prefer-newest (or an
				// explicit disambiguation) replaces this — noted in the
				// design's resume carry-forward.
				return e.ID, approved, nil
			}
		}
		if resp.NextCursor == "" {
			return "", false, nil
		}
		cursor = resp.NextCursor
	}
	return "", false, fmt.Errorf("run resolution exceeded %d pages", maxRunPages)
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
