package standards

// The graph-backed production seams the provision wiring binds (D4/D5): the run's
// target coordinate, the lesson-record listing retirement scans, and the evidence
// resolution that scopes retirement to one repo.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/pkg/errs"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/intake/admission"
)

// issueRefPredicate carries the run's target coordinate (owner/repo#number). It stays
// an OPAQUE handle to the arc — only adapters parse it (host-neutrality) — and this is
// one of those adapters.
const issueRefPredicate = "run.issue.ref"

// classifiedRequester is the NATS request surface the graph queries ride. Narrow so the
// seams unit-test against a fake rather than a live substrate.
type classifiedRequester interface {
	RequestClassified(ctx context.Context, subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// maxRecordPages bounds the record listing defensively (a page is 1000 entities). The
// substrate's own expectation is well under one page of standards records; exceeding
// this many is a signal, not a state to paper over — so it errors rather than
// truncating, because a SHORT list silently under-retires (a withdrawn standard would
// stay active forever, which is the one failure retirement exists to prevent).
const maxRecordPages = 16

// queryTimeout bounds each graph request. Set ABOVE graph-ingest's own 10s internal
// budget (processor/graph-ingest/query.go) rather than at the admission resolver's 5s:
// a prefix query is a full key scan of the namespace, and here the timeout is HARD — a
// client-side expiry becomes a substrate fault that burns the station's whole retry
// budget and parks a healthy run. Better to let the server return its own verdict.
const queryTimeout = 15 * time.Second

// factRepoResolver resolves a run's target repo from its run.issue.ref fact.
type factRepoResolver struct{ reader changefacts.Reader }

// NewRepoResolver builds the production RepoResolver over the run-fact reader.
func NewRepoResolver(reader changefacts.Reader) RepoResolver { return factRepoResolver{reader: reader} }

// Repo returns "owner/repo" for the run. A run with no ref yields found=false (the
// caller parks); a malformed ref is an error, because a coordinate that exists and does
// not parse is a defect worth surfacing rather than a silently unscoped sync.
func (f factRepoResolver) Repo(ctx context.Context, runEntityID string) (string, bool, error) {
	triples, err := f.reader.ReadFacts(ctx, runEntityID, issueRefPredicate)
	if err != nil {
		return "", false, fmt.Errorf("read %s on %s: %w", issueRefPredicate, runEntityID, err)
	}
	for _, tr := range triples {
		if tr.Predicate != issueRefPredicate {
			continue
		}
		ref, ok := tr.Object.(string)
		if !ok || ref == "" {
			continue
		}
		owner, repo, _, err := admission.SplitRef(ref)
		if err != nil {
			return "", false, fmt.Errorf("run %s carries an unparseable %s %q: %w", runEntityID, issueRefPredicate, ref, err)
		}
		// Folded to lower case because this string is an IDENTITY input, not just a
		// label: the source-entity digest covers repo+content, and record identity
		// covers the source. Forge owner/repo names are case-insensitive, so the same
		// repo arriving as "C360Studio/x" from a webhook and "c360studio/x" from a
		// hand-typed launch would mint TWO complete record sets that never retire each
		// other — every standard injected twice, halving the K=10 brief budget.
		return strings.ToLower(owner + "/" + repo), true, nil
	}
	return "", false, nil
}

// natsRecordLister lists every lesson record under the platform's record prefix.
type natsRecordLister struct {
	client classifiedRequester
	prefix string
}

// NewRecordLister builds the production RecordLister over the record namespace
// {org}.{platform}.agent.lesson.record.
//
// The prefix comes from the framework's own AgentLessonRecordPrefix rather than a local
// Sprintf, and that is not style: records are MINTED with agentic.AgentLessonEntityID and
// LISTED with this prefix, so a hand-rolled copy lets the two drift apart on an upstream
// namespace change. The failure would be silent and one-directional — the listing returns
// empty, retirement quietly stops, and standards a repo withdrew stay active forever,
// which is the single thing retirement exists to prevent.
//
// It PANICS on an org/platform that cannot form an entity ID. That is deliberate and is
// why NewProvisionSync calls it at construction: the same inputs would otherwise panic
// deep inside AgentLessonEntityID during a provision, mid-run.
func NewRecordLister(client classifiedRequester, org, platform string) RecordLister {
	return &natsRecordLister{client: client, prefix: agentic.AgentLessonRecordPrefix(org, platform)}
}

func (l *natsRecordLister) ListLessonRecords(ctx context.Context) ([]CandidateRecord, error) {
	var out []CandidateRecord
	cursor := ""
	for page := 0; page < maxRecordPages; page++ {
		req := graph.PrefixQueryRequest{Prefix: l.prefix, Cursor: cursor}
		data, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		respData, err := l.client.RequestClassified(ctx, "graph.ingest.query.prefix", data, queryTimeout)
		if err != nil {
			return nil, fmt.Errorf("list lesson records: %w", err)
		}
		var resp graph.PrefixQueryResponse
		if err := json.Unmarshal(respData, &resp); err != nil {
			return nil, fmt.Errorf("decode lesson-record page: %w", err)
		}
		for i := range resp.Entities {
			out = append(out, candidateFrom(&resp.Entities[i]))
		}
		if resp.NextCursor == "" {
			return out, nil
		}
		cursor = resp.NextCursor
	}
	return nil, fmt.Errorf("lesson-record listing exceeded %d pages — refusing a truncated list, which would "+
		"silently under-retire withdrawn standards", maxRecordPages)
}

// candidateFrom projects one listed entity onto the retirement-relevant fields.
func candidateFrom(e *graph.EntityState) CandidateRecord {
	c := CandidateRecord{EntityID: e.ID}
	for _, tr := range e.Triples {
		s, ok := tr.Object.(string)
		if !ok {
			continue
		}
		switch tr.Predicate {
		case "agent.lesson.category":
			c.Category = s
		case "agent.lesson.status":
			c.Status = s
		case "agent.lesson.evidence":
			c.Evidence = append(c.Evidence, s)
		}
	}
	return c
}

// isEntityNotFound reports whether err is the graph's classified "this entity does not
// exist" failure, matched on the CODE rather than by sniffing the message (the framework
// documents the code as the supported discriminator).
func isEntityNotFound(err error) bool {
	var ce *errs.ClassifiedError
	return errors.As(err, &ce) && ce.Code == graph.ErrorCodeEntityNotFound
}

// factEvidenceResolver resolves a cited source entity's declared repo.
type factEvidenceResolver struct{ reader changefacts.Reader }

// NewEvidenceResolver builds the production EvidenceResolver over the fact reader.
func NewEvidenceResolver(reader changefacts.Reader) EvidenceResolver {
	return factEvidenceResolver{reader: reader}
}

// SourceRepo returns the repo a standards-source entity declares. An entity with no
// repo.standards.repo yields found=false — which the retirement filter reads as "not
// one of ours", the isolation that keeps a foreign writer's records out of scope.
func (f factEvidenceResolver) SourceRepo(ctx context.Context, sourceEntityID string) (string, bool, error) {
	const predicate = "repo.standards.repo"
	triples, err := f.reader.ReadFacts(ctx, sourceEntityID, predicate)
	if err != nil {
		if isEntityNotFound(err) {
			// A cited evidence entity that does not exist is not an error: it is exactly
			// the "cannot resolve to this repo" case the filter must treat as out of scope.
			// (The fact reader surfaces a missing entity as a CLASSIFIED failure, not an
			// empty read — beta.160's authority-read contract — so this branch is the only
			// place that absence becomes a value.)
			return "", false, nil
		}
		return "", false, fmt.Errorf("read %s on %s: %w", predicate, sourceEntityID, err)
	}
	for _, tr := range triples {
		if tr.Predicate != predicate {
			continue
		}
		if s, ok := tr.Object.(string); ok && s != "" {
			return s, true, nil
		}
	}
	return "", false, nil
}
