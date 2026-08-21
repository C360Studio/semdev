package standards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
)

type stubReader struct {
	triples []message.Triple
	err     error
}

// ReadFacts mirrors the real natsReader's refusal of an empty prefix
// (changefacts/nats_reader.go:51-53): an unscoped read pulls every owner's facts off a
// shared entity, so it is a hard error in production and must not be green in a fake.
func (s stubReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	if prefix == "" {
		return nil, fmt.Errorf("read facts: prefix must be non-empty")
	}
	return s.triples, s.err
}

func tripleOf(subject, predicate string, object any) message.Triple {
	return message.Triple{Subject: subject, Predicate: predicate, Object: object}
}

func TestRepoResolverReadsTheRunsTargetCoordinate(t *testing.T) {
	r := NewRepoResolver(stubReader{triples: []message.Triple{
		tripleOf("run-1", issueRefPredicate, "c360studio/semdev-test#7"),
	}})
	repo, found, err := r.Repo(context.Background(), "run-1")
	if err != nil || !found {
		t.Fatalf("resolve: repo=%q found=%v err=%v", repo, found, err)
	}
	if repo != "c360studio/semdev-test" {
		t.Errorf("resolved %q, want the owner/repo half of the coordinate", repo)
	}
}

// TestRepoResolverFoldsCaseSoOneRepoHasOneIdentity pins what makes the folding
// load-bearing rather than cosmetic. The resolved string is a digest input: the source
// entity is sha256(repo + content) and record identity cites the source. Forge owner/repo
// names are case-insensitive, so a webhook delivering "C360Studio/x" and a hand-typed
// launch of "c360studio/x" must not mint two complete record sets for one repo — they
// would never retire each other (the scope compare is exact), and every standard would
// inject twice, halving the K=10 brief budget.
func TestRepoResolverFoldsCaseSoOneRepoHasOneIdentity(t *testing.T) {
	var got []string
	for _, ref := range []string{"C360Studio/SemDev-Test#7", "c360studio/semdev-test#9"} {
		r := NewRepoResolver(stubReader{triples: []message.Triple{tripleOf("run-1", issueRefPredicate, ref)}})
		repo, found, err := r.Repo(context.Background(), "run-1")
		if err != nil || !found {
			t.Fatalf("resolve %q: repo=%q found=%v err=%v", ref, repo, found, err)
		}
		got = append(got, repo)
	}
	if got[0] != got[1] {
		t.Errorf("two spellings of one repo resolved to %q and %q — each mints its own source "+
			"entity, so the repo ends up with two record sets that never retire each other", got[0], got[1])
	}
}

func TestRepoResolverReportsAbsentCoordinateWithoutError(t *testing.T) {
	r := NewRepoResolver(stubReader{})
	repo, found, err := r.Repo(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("an absent coordinate is not an error: %v", err)
	}
	if found || repo != "" {
		t.Errorf("expected not-found, got repo=%q found=%v", repo, found)
	}
}

// TestRepoResolverRejectsAnUnparseableCoordinate keeps a malformed ref loud. Falling back
// to "unscoped" would let one target's records enter another's retirement candidate set.
func TestRepoResolverRejectsAnUnparseableCoordinate(t *testing.T) {
	r := NewRepoResolver(stubReader{triples: []message.Triple{
		tripleOf("run-1", issueRefPredicate, "not-a-coordinate"),
	}})
	if _, _, err := r.Repo(context.Background(), "run-1"); err == nil {
		t.Fatal("an unparseable run.issue.ref must be an error, not a silently unscoped sync")
	}
}

// pagedRequester serves canned prefix-query pages, recording the cursors it was asked for.
type pagedRequester struct {
	pages    []graph.PrefixQueryResponse
	served   int
	cursors  []string
	prefixes []string
}

// requirePrefixShape mirrors what graph-ingest ACTUALLY rejects on a prefix query
// (semtypes.ValidateEntityIDPrefix): 1 to 6 non-empty segments, no trailing separator.
//
// The bound is 1-6, NOT "exactly 5". Over-constraining a fake is the same fidelity bug as
// under-constraining one, just inverted: a legitimate 6-part exact-ID prefix is valid in
// production and would go RED here, inviting someone to "fix" working code to satisfy a
// rule the server does not have. Under-constraining is how the trailing-dot bug reached a
// parked run in the first place.
func requirePrefixShape(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("invalid entity ID contract input: empty prefix")
	}
	if strings.HasSuffix(prefix, ".") {
		return fmt.Errorf("invalid entity ID contract input: prefix %q has a trailing separator", prefix)
	}
	parts := strings.Split(prefix, ".")
	if len(parts) > 6 {
		return fmt.Errorf("invalid entity ID contract input: prefix %q has %d segments, want at most 6", prefix, len(parts))
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid entity ID contract input: prefix %q has an empty segment", prefix)
		}
	}
	return nil
}

func (p *pagedRequester) RequestClassified(_ context.Context, _ string, data []byte, _ time.Duration) ([]byte, error) {
	var req graph.PrefixQueryRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	// graph-ingest validates the prefix as entity-ID contract input and rejects
	// anything that is not the bare 5-part namespace — a trailing dot returns
	// "invalid entity ID contract input". A fake that accepts any string lets that
	// class through every offline test and surfaces only as a parked run, which is
	// exactly how it was found. So the fake enforces the same shape.
	if err := requirePrefixShape(req.Prefix); err != nil {
		return nil, err
	}
	p.prefixes = append(p.prefixes, req.Prefix)
	p.cursors = append(p.cursors, req.Cursor)
	if p.served >= len(p.pages) {
		return nil, errors.New("requested more pages than the fake has")
	}
	resp := p.pages[p.served]
	p.served++
	return json.Marshal(resp)
}

func recordEntity(id, category, status, evidence string) graph.EntityState {
	return graph.EntityState{ID: id, Triples: []message.Triple{
		tripleOf(id, "agent.lesson.category", category),
		tripleOf(id, "agent.lesson.status", status),
		tripleOf(id, "agent.lesson.evidence", evidence),
	}}
}

func TestRecordListerWalksEveryPage(t *testing.T) {
	req := &pagedRequester{pages: []graph.PrefixQueryResponse{
		{Entities: []graph.EntityState{recordEntity("org.plat.agent.lesson.record.a", "repo-standard", "active", "src-1")}, NextCursor: "c1"},
		{Entities: []graph.EntityState{recordEntity("org.plat.agent.lesson.record.b", "repo-standard", "proposed", "src-2")}},
	}}
	got, err := NewRecordLister(req, "org", "plat").ListLessonRecords(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d records across two pages, want 2 — a short list silently under-retires", len(got))
	}
	if got[0].Category != "repo-standard" || got[0].Status != "active" || len(got[0].Evidence) != 1 {
		t.Errorf("projection dropped a retirement-relevant field: %+v", got[0])
	}
	if len(req.cursors) != 2 || req.cursors[0] != "" || req.cursors[1] != "c1" {
		t.Errorf("pagination did not follow the cursor; asked for %v", req.cursors)
	}
	for _, got := range req.prefixes {
		if got != "org.plat.agent.lesson.record" {
			t.Errorf("listed prefix %q, want the bare 5-part lesson-record namespace", got)
		}
	}
}

// TestRecordListerRefusesToTruncate pins the fail-closed choice: a listing longer than the
// page bound errors instead of returning what it has. A truncated list reads as "these are
// all the records", and every record it omitted keeps its standard active after the repo
// withdrew it — silently, forever.
func TestRecordListerRefusesToTruncate(t *testing.T) {
	pages := make([]graph.PrefixQueryResponse, maxRecordPages+1)
	for i := range pages {
		pages[i] = graph.PrefixQueryResponse{
			Entities:   []graph.EntityState{recordEntity(fmt.Sprintf("org.plat.agent.lesson.record.%d", i), "repo-standard", "active", "src")},
			NextCursor: fmt.Sprintf("c%d", i+1),
		}
	}
	req := &pagedRequester{pages: pages}
	got, err := NewRecordLister(req, "org", "plat").ListLessonRecords(context.Background())
	if err == nil {
		t.Fatalf("an over-long listing must error, not return a partial set of %d", len(got))
	}
	if !strings.Contains(err.Error(), "under-retire") {
		t.Errorf("the error must say why truncation is unsafe; got %v", err)
	}
}

func TestEvidenceResolverReadsTheDeclaredRepo(t *testing.T) {
	r := NewEvidenceResolver(stubReader{triples: []message.Triple{
		tripleOf("src-1", "repo.standards.repo", "o/r"),
	}})
	repo, found, err := r.SourceRepo(context.Background(), "src-1")
	if err != nil || !found || repo != "o/r" {
		t.Fatalf("resolve: repo=%q found=%v err=%v", repo, found, err)
	}
}

// TestEvidenceResolverTreatsAMissingEntityAsOutOfScope pins the beta.160 read contract:
// a missing entity arrives as a CLASSIFIED failure, not an empty read. Letting that error
// escape would abort the whole retirement pass because one foreign record cited an entity
// we cannot see — so absence must become a value here, and nowhere else.
func TestEvidenceResolverTreatsAMissingEntityAsOutOfScope(t *testing.T) {
	classified := &errs.ClassifiedError{Code: graph.ErrorCodeEntityNotFound, Message: "entity not found"}
	r := NewEvidenceResolver(stubReader{err: fmt.Errorf("read facts failed: %w", classified)})
	repo, found, err := r.SourceRepo(context.Background(), "src-gone")
	if err != nil {
		t.Fatalf("a missing evidence entity must resolve to not-found, not an error: %v", err)
	}
	if found || repo != "" {
		t.Errorf("expected out-of-scope, got repo=%q found=%v", repo, found)
	}
}

// TestEvidenceResolverSurfacesRealReadFaults keeps the above from swallowing everything:
// a transport fault is not "out of scope", it is a fault the caller must not mistake for
// a clean not-mine verdict.
func TestEvidenceResolverSurfacesRealReadFaults(t *testing.T) {
	r := NewEvidenceResolver(stubReader{err: errors.New("request timed out")})
	if _, _, err := r.SourceRepo(context.Background(), "src-1"); err == nil {
		t.Fatal("a transport fault must surface, not read as an unresolvable evidence entity")
	}
}
