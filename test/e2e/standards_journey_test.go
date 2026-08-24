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
	"github.com/c360studio/semdev/internal/floors"
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
		// A throwaway capture store: this re-sync is an idempotency probe over the graph,
		// not a provision, so nothing downstream gates on what it captures. The live
		// lane's store is boot's shared instance.
		Snapshots: standards.NewSnapshots(),
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
	if !strings.HasPrefix(form, standards.InjectionPrefix) {
		return "", form
	}
	end := strings.Index(form, "]")
	if end < 0 {
		return "", form
	}
	return form[len(standards.InjectionPrefix):end], form
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

// journeyFixtureGreenButVetFailingDiff is the gate journey's single authored patch: the
// CORRECT boundary fix (so `go test ./...` passes) plus a self-assignment (so `go vet ./...`
// exits 1). The isolation is the point and it was measured, not assumed — the default vet
// subset `go test` runs omits the assign analyzer, so the attempt measures GREEN and fails
// only the repository's own declared check. Without that isolation a rejected run would not
// distinguish "the repo's check gated this" from "the tests were red anyway".
//
// One patch, not two: the developer loop is apply → measure → stop, byte-identical in shape
// to the green journey above. An earlier draft split this across two apply_patch turns and
// the loop stalled before measuring; no journey here needs a two-patch attempt, so the
// shape that is proven everywhere else is the one to use.
const journeyFixtureGreenButVetFailingDiff = "--- a/health.go\n" +
	"+++ b/health.go\n" +
	"@@ -28,9 +28,18 @@ func Classify(cpu, mem float64) Status {\n" +
	" \tswitch {\n" +
	" \tcase pressure >= criticalThreshold:\n" +
	" \t\treturn Unhealthy\n" +
	"-\tcase pressure > warningThreshold:\n" +
	"+\tcase pressure >= warningThreshold:\n" +
	" \t\treturn Degraded\n" +
	" \tdefault:\n" +
	" \t\treturn Healthy\n" +
	" \t}\n" +
	" }\n" +
	"+\n" +
	"+// Normalize returns the status unchanged. The self-assignment is deliberate: `go vet`\n" +
	"+// reports it, while `go test` does not — the default vet subset `go test` runs omits\n" +
	"+// the assign analyzer — so this isolates the repo-declared required check as the only\n" +
	"+// failing gate.\n" +
	"+func Normalize(s Status) Status {\n" +
	"+\ts = s\n" +
	"+\treturn s\n" +
	"+}\n"

// The three standards' brief lines, by the role that must receive each. The fixture
// declares eng-error-context to [developer], eng-exported-doc-comments to [reviewer],
// and eng-table-driven-tests to neither — which means BOTH injectable roles.
var (
	stdDeveloperOnly = standards.InjectionPrefix + "eng-error-context]"
	stdReviewerOnly  = standards.InjectionPrefix + "eng-exported-doc-comments]"
	stdBothRoles     = standards.InjectionPrefix + "eng-table-driven-tests]"
)

// Distinctive phrases from the two D8 persona fragments, one per role. Without these
// the fragments are unpinned prose: delete both files and every other assertion in
// this journey still passes, because they assert the INJECTED lesson lines, which the
// substrate produces whether or not a fragment taught the role what to do with them.
const (
	reviewerFragmentPhrase  = "do not cite it in a finding"
	developerFragmentPhrase = "not being held to any"
)

// TestBridgeProofRepoStandardsReachBriefsAndGate is D9 items 2 and 4: the declared
// standards reach the RIGHT briefs, scoped by role, and the arc completes green when the
// work satisfies them.
//
// Item 2 is why this journey captures prompts at all. That the records are ACTIVE and
// carry `tag:developer` (the sibling journey's proof) says the graph holds the right
// shape; it does NOT say the substrate delivered that shape into a brief. Role scoping is
// the ONLY injection axis at beta.160, so the failure this pins is silent in both
// directions: a reviewer-only standard reaching the developer is law she was never meant
// to be held to, and a developer standard reaching nobody is a rule the repository
// believes it declared. Reading the bytes the model was actually sent is the only
// evidence that distinguishes those from a working lane.
func TestBridgeProofRepoStandardsReachBriefsAndGate(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{
			Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{
			Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{
			Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...).WithPromptCapture()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	runEntityID := driveSharedFrontOfArc(ctx, t, rt)
	requireStandardRecords(ctx, t, len(journeyStandards))

	requireDeveloperLoopCompleted(ctx, t, runEntityID)
	requireMeasurementPassed(ctx, t, runEntityID)
	requireFloorsPassed(ctx, t, runEntityID)
	t.Logf("standards station 4: floors passed WITH the repo's declared checks in the set on run %s", runEntityID)

	requireReviewApproved(ctx, t, runEntityID)
	requireVerifyPassed(ctx, t, runEntityID)
	requirePRDelivered(ctx, t, runEntityID)
	t.Logf("standards station 5: the arc completed green end-to-end with standards in force")

	// D9 item 2 — role scoping, read off the bytes the model received.
	prompts := mock.Prompts()
	if len(prompts) == 0 {
		t.Fatal("no prompts were captured; WithPromptCapture is not recording and every " +
			"assertion below would pass vacuously against an empty corpus")
	}

	dev := requirePromptContaining(t, prompts, journeyDeveloperMarker)
	requirePromptCarries(t, "developer", dev, stdDeveloperOnly, stdBothRoles)
	requirePromptOmits(t, "developer", dev, stdReviewerOnly,
		"a reviewer-scoped standard in the developer's brief is law she was never meant to be held to")

	rev := requirePromptContaining(t, prompts, journeyReviewMarker)
	requirePromptCarries(t, "reviewer", rev, stdReviewerOnly, stdBothRoles)
	requirePromptOmits(t, "reviewer", rev, stdDeveloperOnly,
		"a developer-scoped standard in Quinn's brief invites a finding against a rule the repo scoped elsewhere")

	// The coordinator is not an injectable role, so its brief must carry no standard at
	// all — the negative control on the axis itself. Without it, an injector that ignored
	// scoping entirely and sent everything everywhere would still satisfy both assertions
	// above, because each role's expected set is a subset of the whole.
	coord := requirePromptContaining(t, prompts, journeyDevRewakeMarker)
	requirePromptOmits(t, "coordinator", coord, standards.InjectionPrefix,
		"the coordinator is not an injectable role; a standard here means scoping is not being applied at all")

	// D8 — the fragments themselves reached their roles. seedPersonas fails closed only
	// on the coordinator's required set, and persona.LoadFromDirectory warn-and-returns
	// nil on an unreadable fragment, so a dropped standards fragment is silent.
	requirePromptCarries(t, "developer", dev, developerFragmentPhrase)
	requirePromptOmits(t, "developer", dev, reviewerFragmentPhrase,
		"the reviewer's contract is scoped to the reviewer role; its presence means fragment binding is not role-scoped")
	requirePromptCarries(t, "reviewer", rev, reviewerFragmentPhrase)

	// Turn accounting, the discipline mockllm's own doc requires of every journey:
	// front-of-arc 4 + dev loop (apply, measure, stop = 3) + Quinn 1 = 8. Without it a
	// spurious extra spawn — a duplicate dev dispatch, a second reviewer — passes unseen.
	if got := mock.RequestCount(); got != 8 {
		t.Fatalf("expected exactly 8 model turns (front-of-arc 4 + dev loop 3 + review 1), got %d — "+
			"a mismatch means an extra loop spawned or a turn went unscripted", got)
	}

	// Report what each brief actually carried. A silent pass here is indistinguishable
	// from a lane that never ran — the corollary this repo paid for when the checks lane's
	// first green e2e left no trace at all.
	t.Logf("standards station 6: role-scoped injection proven on %d captured turns — developer brief carries %v, "+
		"reviewer brief carries %v, coordinator brief carries %v",
		len(prompts), standardTagsIn(dev), standardTagsIn(rev), standardTagsIn(coord))
}

// TestBridgeProofRequiredRepoCheckGatesLikeAFloor is D9 item 3: a repository's own
// declared REQUIRED check rejects an attempt exactly as a built-in structural floor does.
//
// The attempt here MEASURES GREEN — the fix is correct and `go test ./...` passes — and is
// rejected anyway, because the repo declared `go vet ./...` required and the attempt's own
// patch violates it. That is the whole claim of the checks lane: a repository can
// declare a gate semdev has never seen and have it bind. If this journey were driven by a
// red measurement instead, a passing run and a gating check would be indistinguishable.
//
// Budget 1 makes the terminal deterministic: the floors reject routes not_clean, and 06d
// escalates at count 1 = budget rather than re-dispatching, so the run PARKS toward the
// human and Quinn is never spawned. No review on rejected work is the fail-closed half.
func TestBridgeProofRequiredRepoCheckGatesLikeAFloor(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(1),
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{
			Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureGreenButVetFailingDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{
			Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// Cursor guard, never consumed: a rejected attempt never reaches review. It is
		// load-bearing for a reason worth stating exactly, or the next editor deletes it
		// after concluding it is unreachable: at an exhausted cursor ssmock CLAMPS to the
		// last entry and re-evaluates it. Remove this and that entry is measure_task,
		// whose marker DOES match Amelia's prompt — so she re-calls measure_task every
		// turn to her iteration cap. (The "first advertised tool" fallback cannot occur
		// here at all: with tool rounds already taken, an unmatched turn returns a
		// completion.)
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{
			Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	runEntityID := driveSharedFrontOfArc(ctx, t, rt)
	requireStandardRecords(ctx, t, len(journeyStandards))
	requireDeveloperLoopCompleted(ctx, t, runEntityID)

	// The measurement is GREEN. This is the assertion that gives the rest of the journey its
	// meaning: whatever rejects the attempt below, it is not the task's own test command.
	requireMeasurementPassed(ctx, t, runEntityID)
	t.Logf("gate station 1: attempt measured GREEN on run %s — the task's own tests pass", runEntityID)

	// ...and the attempt is rejected anyway, by the repository's required check.
	//
	// Assert on the run-level floor facts, not the route mirror: route.attempt.* is stamped
	// on the developer LOOP entity, so a run-level lookup for it reports absence forever and
	// would fail this journey for the wrong reason. The aggregate verdict and the per-floor
	// detail are what land on the run.
	detail := requireFloorsRejected(ctx, t, runEntityID)

	// The detail must name the REPO'S OWN check as the rejecting one. Without this the
	// journey would pass if any built-in floor happened to reject — which would prove
	// nothing about the checks lane, the only thing it exists to prove.
	wantCheck := standards.FloorPrefix + "go-vet"
	if !strings.Contains(detail, wantCheck) {
		t.Fatalf("the floor detail does not name %q, so something other than the repo's declared check "+
			"rejected this attempt:\n%s", wantCheck, detail)
	}
	if !strings.Contains(detail, wantCheck+": REJECTED") {
		t.Fatalf("%q is present but not REJECTED — the required check did not gate:\n%s", wantCheck, detail)
	}
	// REJECTED alone does not say the check RAN. A could-not-run finding renders
	// "repo-check:go-vet: REJECTED — not-run — the command could not be executed…",
	// a byte-compatible prefix — so a reaped container or a dead exec seam would reject
	// every required check, park the run, and pass this journey while its log claimed
	// the repo's gate bound. "FAILED with exit status" is produced ONLY by the
	// ran-and-exited-non-zero path (internal/standards/checks.go), so it is the token
	// that separates them. This is D7a's own ran-vs-could-not-run rule applied to the
	// pin for D7a.
	if !strings.Contains(detail, "FAILED with exit status") {
		t.Fatalf("%q rejected, but nothing says the command RAN — a not-run finding rejects identically, "+
			"so this cannot distinguish the repo's gate binding from a dead exec seam:\n%s", wantCheck, detail)
	}
	// The fixture declares no `proof` negative control, so D7a requires the check gate
	// while stamped `unproven` — never a clean pass. No other journey pins this token.
	if !strings.Contains(detail, "unproven") {
		t.Errorf("the required check is not stamped `unproven` — the fixture declares no proof, so an "+
			"unproven status is exactly what D7a requires here:\n%s", detail)
	}
	// The non-required check is reported, never gating: the advisory half of the same lane.
	if adv := standards.FloorPrefix + "gofmt"; !strings.Contains(detail, adv+": passed") {
		t.Errorf("the floor detail does not report the repo's non-required check %q as passed — presence "+
			"alone would also match \"FAILED (advisory …)\", which proves nothing about reported-not-gating, "+
			"and its absence entirely is the gate-deletion shape D7a exists to prevent:\n%s", adv, detail)
	}
	t.Logf("gate station 2: floors REJECTED a green-measuring attempt via the repo's own required check.\nfloor detail:\n%s", detail)

	// Fail-closed: rejected work never reaches review, and the run parks at budget 1.
	parkMsg := requireRunParked(ctx, t, runEntityID, 90*time.Second)
	requireNoReviewVerdict(ctx, t, runEntityID)
	t.Logf("gate station 3: run parked toward the human with no review verdict: %s", parkMsg)

	// front-of-arc 4 + one dev loop (apply, measure, stop = 3) = 7, and NO Quinn turn.
	// An 8th would mean review was spawned on rejected work — the exact fail-open this pins.
	if got := mock.RequestCount(); got != 7 {
		t.Fatalf("expected exactly 7 model turns (front-of-arc 4 + dev loop apply/measure/stop 3), got %d — "+
			"an 8th turn means Quinn was spawned on an attempt the repo's own check rejected", got)
	}
}

// requirePromptContaining returns the first captured prompt carrying marker, failing with
// the corpus size when none does.
func requirePromptContaining(t *testing.T, prompts []string, marker string) string {
	t.Helper()
	for _, p := range prompts {
		if strings.Contains(p, marker) {
			return p
		}
	}
	t.Fatalf("no captured prompt contains %q (corpus: %d prompts) — the spawn never ran, or its "+
		"persona changed and this marker no longer identifies it", marker, len(prompts))
	return ""
}

// requirePromptCarries asserts every want appears in the role's brief.
func requirePromptCarries(t *testing.T, role, prompt string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(prompt, w) {
			t.Errorf("the %s brief does not carry %s — the standard is ACTIVE in the graph and scoped to "+
				"this role, so the substrate is not delivering it into the brief", role, w)
		}
	}
}

// requirePromptOmits asserts the role's brief does NOT carry unwanted, explaining the cost.
func requirePromptOmits(t *testing.T, role, prompt, unwanted, why string) {
	t.Helper()
	if strings.Contains(prompt, unwanted) {
		t.Errorf("the %s brief carries %s — %s", role, unwanted, why)
	}
}

// requireNoReviewVerdict asserts no review verdict was ever stamped on the run. Quinn is
// spawned by the advance route (06a), which a rejected attempt must never reach.
func requireNoReviewVerdict(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	e, ok := scanEntities(ctx, client)[runEntityID]
	if !ok {
		t.Fatalf("run %s not found while asserting the absence of a review verdict", runEntityID)
	}
	if v := tripleString(e, "review.verdict.value"); v != "" {
		t.Fatalf("run %s carries review.verdict.value=%q — an attempt the repo's required check "+
			"rejected reached review anyway, which is the fail-open the checks lane exists to prevent",
			runEntityID, v)
	}
}

// standardTagsIn returns every "[std:<id>]" tag a captured prompt carries, sorted. Used to
// REPORT what a brief actually held, so a passing run leaves evidence the injection lane
// ran rather than only evidence that nothing complained.
func standardTagsIn(prompt string) []string {
	var out []string
	for rest := prompt; ; {
		i := strings.Index(rest, standards.InjectionPrefix)
		if i < 0 {
			break
		}
		rest = rest[i:]
		end := strings.Index(rest, "]")
		if end < 0 {
			break
		}
		out = append(out, rest[:end+1])
		rest = rest[end+1:]
	}
	sort.Strings(out)
	return out
}

// requireFloorsRejected waits for the run's floor aggregate to reject and returns the
// per-floor detail prose. Both facts are stamped on the RUN by check_floors.
func requireFloorsRejected(ctx context.Context, t *testing.T, runEntityID string) string {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	var detail string
	requireEventually(t, 90*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		detail = tripleString(e, floors.DetailPredicate)
		return tripleString(e, floors.RejectedPredicate) == "true"
	}, "the run's floor aggregate never rejected — the repo's required check `go vet ./...` fails on "+
		"this attempt's own diff, so the aggregate must reject even though the measurement passed")
	return detail
}
