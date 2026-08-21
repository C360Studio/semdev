//go:build e2e

// The REPO-STANDARDS provision journey (standards-via-lessons D9 item 1).
//
// The target repo's committed .semdev/standards.yaml is the law semdev's later
// spawns must receive. This journey proves the provision-time sync against the
// REAL substrate: the declared standards are born as agent.lesson.record entities,
// activated through the contract-bound curator, and each cites a source entity that
// actually resolves — the framework's Promote refuses to activate a record whose
// evidence does not exist, so an ACTIVE record here is itself proof the provenance
// chain is real, not a shape the test asserted into being.
//
// It then re-runs the production sync over the same bytes and proves the pass is
// idempotent. That is the property the whole design rests on: every provision of
// every run re-syncs, so a sync that minted a new record per pass would fill the
// injection budget with duplicates of one standard within a handful of runs.
package e2e

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/graph"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/mockllm"
	"github.com/c360studio/semdev/internal/standards"
	"github.com/c360studio/semdev/internal/vocab"
)

// journeyRepo is the owner/repo half of journeyIssueRef — the retirement scope key
// every born record's source entity declares.
const journeyRepo = "c360studio/semdev-journey"

// The three standards test/fixtures/go-health-class/.semdev/standards.yaml declares,
// with the roles each scopes to. Kept here rather than parsed from the file so a
// silent edit to the fixture fails this journey instead of quietly agreeing with it.
var journeyStandards = map[string][]string{
	"eng-error-context":         {"tag:developer"},
	"eng-exported-doc-comments": {"tag:reviewer"},
	"eng-table-driven-tests":    {"tag:developer", "tag:reviewer"},
}

func TestBridgeProofRepoStandardsBornAndActivated(t *testing.T) {
	mock := mockllm.New(journeyFrontOfArcFixtures(3)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	taskID := publishCoordinatorWake(ctx, t)
	requireCoordinatorDecision(ctx, t, taskID, journeyDecideAction)
	runEntityID := requireRunAnchor(ctx, t, taskID)
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	approveChange(ctx, t, rt, runEntityID)

	// Provisioning is where the sync runs. Readiness is therefore also the signal
	// that the sync neither blocked nor faulted: a declaration fault would have
	// stamped sandbox.provision.blocked and requireSandboxReady fails loudly on it.
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("standards station 1: run %s provisioned ready — the standards sync ran without blocking", runEntityID)

	records := requireStandardRecords(ctx, t, len(journeyStandards))
	sources := map[string]bool{}
	for id, rec := range records {
		stdID, form := injectionStandardID(rec)
		want, declared := journeyStandards[stdID]
		if !declared {
			t.Fatalf("record %s carries injection form %q, which names no standard the fixture declares", id, form)
		}
		if got := tripleStrings(rec, "agent.lesson.status"); len(got) != 1 || got[0] != "active" {
			t.Errorf("standard %q is %v, want exactly [active] — a proposed record never reaches a brief", stdID, got)
		}
		if got := tripleStrings(rec, "agent.lesson.applies-to"); !equalSets(got, want) {
			t.Errorf("standard %q scopes to %v, want %v — role scoping is the ONLY injection axis at beta.160, "+
				"so a wrong tag silently sends a reviewer's standard to the developer or nowhere at all", stdID, got, want)
		}
		evidence := tripleStrings(rec, "agent.lesson.evidence")
		if len(evidence) != 1 {
			t.Fatalf("standard %q cites %d evidence entities, want exactly the standards-file source", stdID, len(evidence))
		}
		sources[evidence[0]] = true
	}

	// One file, one source entity: the digest covers repo + content, so all three
	// records must cite the SAME source. Separate sources would mean the identity
	// derivation saw different bytes for one file.
	if len(sources) != 1 {
		t.Fatalf("the three records cite %d distinct source entities, want 1 — every standard in one file "+
			"shares its provenance", len(sources))
	}
	var sourceID string
	for id := range sources {
		sourceID = id
	}
	requireSourceDeclaresRepo(ctx, t, sourceID, journeyRepo)
	t.Logf("standards station 2: %d standards active, all citing source %s for repo %s", len(records), sourceID, journeyRepo)

	// Idempotency over the production ADAPTER (precise wording matters): a second
	// Provision of THIS run would short-circuit at alreadyReady before ever reaching the
	// standards seam, so what actually re-syncs in life is a NEW run against the same repo.
	// Re-invoking the adapter is the faithful proof of that, and the same bytes must leave
	// the record set identical.
	before := sortedKeys(records)
	resyncStandards(ctx, t, runEntityID)
	after := sortedKeys(requireStandardRecords(ctx, t, len(journeyStandards)))
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Fatalf("re-syncing the same standards file changed the record set:\n before %v\n after  %v\n"+
			"every provision re-syncs, so a non-idempotent pass fills the K=10 injection budget with "+
			"duplicates of one standard within a few runs", before, after)
	}
	t.Logf("standards station 3: re-sync over identical bytes was a no-op — %d records unchanged", len(after))
}

// requireStandardRecords waits for exactly want repo-standard lesson records and
// returns them keyed by entity ID.
func requireStandardRecords(ctx context.Context, t *testing.T, want int) map[string]graph.EntityState {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	var found map[string]graph.EntityState
	requireEventually(t, 60*time.Second, func() bool {
		found = map[string]graph.EntityState{}
		for id, e := range scanEntities(ctx, client) {
			if tripleString(e, "agent.lesson.category") == "repo-standard" {
				found[id] = e
			}
		}
		return len(found) == want
	}, "the graph never held exactly the expected number of repo-standard lesson records")
	if len(found) != want {
		t.Fatalf("found %d repo-standard records, want %d", len(found), want)
	}
	return found
}

// requireSourceDeclaresRepo asserts the cited source entity exists and names the repo.
// Promote already refused to activate without it, so this pins WHICH repo — the key
// retirement scopes on, and the isolation that keeps one target out of another's set.
func requireSourceDeclaresRepo(ctx context.Context, t *testing.T, sourceID, wantRepo string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	e, ok := scanEntities(ctx, client)[sourceID]
	if !ok {
		t.Fatalf("source entity %s does not exist, yet records citing it are ACTIVE — the curator's "+
			"evidence-resolution guarantee is not holding", sourceID)
	}
	if got := tripleString(e, "repo.standards.repo"); got != wantRepo {
		t.Fatalf("source %s declares repo %q, want %q", sourceID, got, wantRepo)
	}
	if tripleString(e, "repo.standards.digest") == "" || tripleString(e, "repo.standards.path") == "" {
		t.Errorf("source %s is missing its digest/path provenance: %+v", sourceID, e.Triples)
	}
}

// resyncStandards runs the PRODUCTION standards sync a second time over the same
// fixture bytes, through the same construction boot uses.
func resyncStandards(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	// Declare before building, for the same reason internal/boot funnels both of its entry
	// points through declaredGraphClients: contract validation calls
	// vocabulary.RequireDeclaredPredicate, so a client built on an undeclared registry fails
	// outright. This works today only because startJourneyRuntime already registered — an
	// ordering coincidence exactly like the one that left the launch lane dead since
	// beta.159, so the helper states its own precondition rather than inheriting one.
	vocab.Register()
	clients, err := graphown.NewClients(client)
	if err != nil {
		t.Fatalf("build graph clients for the re-sync: %v", err)
	}
	sync, err := standards.NewProvisionSync(standards.Wiring{
		NATS:     client,
		Clients:  clients,
		Reader:   changefacts.NewNATSReader(client),
		Org:      "c360",
		Platform: "semdev-001",
	})
	if err != nil {
		t.Fatalf("build the production standards sync: %v", err)
	}
	reason, err := sync.Sync(ctx, runEntityID, journeySandboxSourceDir(t))
	if err != nil {
		t.Fatalf("re-sync faulted: %v", err)
	}
	if reason != "" {
		t.Fatalf("re-sync blocked on bytes that provisioned cleanly minutes ago: %s", reason)
	}
}

// injectionStandardID extracts the "[std:<id>]" prefix the injection form carries.
func injectionStandardID(e graph.EntityState) (string, string) {
	form := tripleString(e, "agent.lesson.injection-form")
	if !strings.HasPrefix(form, "[std:") {
		return "", form
	}
	end := strings.Index(form, "]")
	if end < 0 {
		return "", form
	}
	return form[len("[std:"):end], form
}

// tripleStrings returns every string object for the predicate, sorted.
func tripleStrings(e graph.EntityState, predicate string) []string {
	var out []string
	for _, tr := range e.Triples {
		if tr.Predicate != predicate {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func equalSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	w := append([]string(nil), want...)
	sort.Strings(w)
	for i := range got {
		if got[i] != w[i] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]graph.EntityState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
