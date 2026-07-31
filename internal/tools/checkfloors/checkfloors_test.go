package checkfloors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/measurement"

	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// loopEntity is the floors LOOP the route mirror lands on. It must carry the
// agentic-loop grammar (agent.agentic-loop.execution.*), not the chain grammar the
// RUN uses: route-mirror's projection contract claims only the loop class, so a
// chain-shaped "loop" here is rejected at the write (migrate-beta159 D2a). The old
// fixture used the chain grammar and no assertion could see it.
const loopEntity = "org.plat.agent.agentic-loop.execution.floors-loop-1"

type fakeAttempts struct {
	attempt floors.Attempt
	err     error
	gotIdx  *int // when set, records the taskIndex RunFloors forwarded to Resolve
}

func (f fakeAttempts) Resolve(_ context.Context, _ string, idx int) (floors.Attempt, error) {
	if f.gotIdx != nil {
		*f.gotIdx = idx
	}
	return f.attempt, f.err
}

// fakeReader replays the run's measurement + attempt facts the route mirror reads
// (measurement.result.passed, task.attempt.instance), filtered by the queried prefix —
// and, keyed by ENTITY, the loop's own facts (agent.loop.terminal-reason) so the pins
// prove the transient classification reads L_n, not the run.
type fakeReader struct {
	facts     []message.Triple // served for every entity except loopEntity (the run's facts)
	loopFacts []message.Triple // served for loopEntity (the loop's terminal-reason)
	err       error
}

func (r fakeReader) ReadFacts(_ context.Context, entityID, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	src := r.facts
	if entityID == loopEntity {
		src = r.loopFacts
	}
	var out []message.Triple
	for _, tr := range src {
		if strings.HasPrefix(tr.Predicate, prefix) {
			out = append(out, tr)
		}
	}
	return out, nil
}

// measuredPassed builds the measurement.result.passed fact the route mirror copies.
// Single-task at M0 (beta.147 D1): the predicate carries no task index.
func measuredPassed(passed string) message.Triple {
	return message.Triple{Predicate: measurement.ResultPrefix + measurement.FactPassed, Object: passed, Source: "measurement-harness"}
}

// measuredCommit builds the measurement.result.commit fact that binds a measurement to
// the snapshot it ran against (the semstreams-reviewer HIGH fix: a green is trusted only
// when this equals the run's current attempt.commit.sha).
func measuredCommit(sha string) message.Triple {
	return message.Triple{Predicate: measurement.ResultPrefix + measurement.FactCommit, Object: sha, Source: "measurement-harness"}
}

// attemptCommitFact builds the run's current attempt.commit.sha (apply_patch's latest SHA).
func attemptCommitFact(sha string) message.Triple {
	return message.Triple{Predicate: "attempt.commit.sha", Object: sha, Source: "patch-committer"}
}

// attemptFact builds one appended task.attempt.instance counter triple (object = a loop id).
// Single-task at M0: the predicate carries no task index.
func attemptFact(loopID string) message.Triple {
	return message.Triple{Predicate: "task.attempt.instance", Object: loopID, Source: "dev-dispatch-rule"}
}

// budgetFact builds the run's projected task.spec.budget (the clamped [1,5] per-task attempt
// budget) the route mirror copies onto L_n as route.task.budget (adopt-per-task-routing-budgets).
func budgetFact(b string) message.Triple {
	return message.Triple{Predicate: "task.spec.budget", Object: b, Source: "task-projector"}
}

// transientFact builds one appended task.transient.instance counter triple (object = a
// transiently-retried loop id) the route mirror copies onto L_n as route.transient.instance
// (adopt-reason-aware-escalate).
func transientFact(loopID string) message.Triple {
	return message.Triple{Predicate: "task.transient.instance", Object: loopID, Source: "dev-dispatch-rule"}
}

// terminalReasonFact builds the LOOP's harness-stamped agent.loop.terminal-reason (semstreams
// #569, stamped by the agentic-loop graph writer atomically with the outcome) the transient
// classification reads. Lives on L_n, never the run — feed it to fakeReader.loopFacts.
func terminalReasonFact(reason string) message.Triple {
	return message.Triple{Predicate: "agent.loop.terminal-reason", Object: reason, Source: "agentic-loop"}
}

type fakeWriter struct {
	owned     []string // the stale predicates ReadAuthoritative reports on the run
	entities  []string // the entity each ReplaceOwned targeted (index-aligned with replaces)
	contracts []string // the contract each ReplaceOwned resolved to (findings vs mirror)
	replaces  [][]message.Triple
}

func (w *fakeWriter) ReplaceOwned(_ context.Context, m projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	w.entities = append(w.entities, m.EntityID)
	w.contracts = append(w.contracts, m.Contract)
	w.replaces = append(w.replaces, m.Desired)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// ReadAuthoritative returns the WHOLE entity as the real client does; the caller's
// LOCAL prefix filter reconstructs the owned set the old scoped read returned.
func (w *fakeWriter) ReadAuthoritative(_ context.Context, id string) (*graph.EntityState, error) {
	e := &graph.EntityState{ID: id}
	for _, p := range w.owned {
		e.Triples = append(e.Triples, message.Triple{Subject: id, Predicate: p, Object: "stale"})
	}
	return e, nil
}

// clearedFindings reports whether any write CLEARED floor-tools' owned group. Under
// ReplaceOwned that is a write with an EMPTY Desired: the mutation wipes the whole
// group and re-adds nothing, which is exactly what clearFindings now issues in place
// of the old explicit remove list (migrate-beta159 D3a).
func (w *fakeWriter) clearedFindings() bool {
	for i, batch := range w.replaces {
		if len(batch) == 0 && w.contracts[i] == Source {
			return true
		}
	}
	return false
}

// check_floors writes as TWO owners — floor-tools (the findings, on the RUN; it also
// reads its own package back) and route-mirror (the route inputs, on the firing
// LOOP). Both tap the same fake so existing assertions over replaces see every write.
func findingsWriterFor(w *fakeWriter) *graphown.Writer {
	return graphown.NewReadWriter(Source, w, w)
}

func mirrorWriterFor(w *fakeWriter) *graphown.Writer {
	return graphown.NewWriter(RouteMirrorSource, w)
}

// passingAttempt authors production source plus a real test that asserts on computed
// behavior of the target — it clears all floors.
func passingAttempt() floors.Attempt {
	return floors.Attempt{
		TargetFiles: []string{"h.go"},
		Files: []floors.File{
			{Path: "h.go", Content: "package h\n\nfunc Add(a, b int) int { return a + b }\n"},
			{Path: "h_test.go", Content: "package h\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n"},
		},
	}
}

// vacuousAttempt authors a test whose only assertion is a constant tautology — the
// vacuous-test floor rejects it.
func vacuousAttempt() floors.Attempt {
	return floors.Attempt{
		TargetFiles: []string{"h.go"},
		Files: []floors.File{
			{Path: "h.go", Content: "package h\n\nfunc Add(a, b int) int { return a + b }\n"},
			{Path: "h_test.go", Content: "package h\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif \"ok\" != \"ok\" {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n"},
		},
	}
}

// run calls RunFloors with no route mirror (routeLoopEntityID="", the unit-test path)
// and collapses the writer's stamped triples into a predicate->object map for assertions.
func run(t *testing.T, attempt floors.Attempt, w *fakeWriter, idx int) (map[string]string, FloorResult, error) {
	t.Helper()
	res, err := RunFloors(context.Background(), fakeAttempts{attempt: attempt}, fakeReader{}, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", idx)
	facts := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			facts[tr.Predicate] = tr.Object.(string)
		}
	}
	return facts, res, err
}

// Happy path: every floor passes → the run gets the flat floor.finding.rejected=false
// plus a detail scalar naming every floor as passed (D4: findings flatten to three
// predicates, no per-floor sub-keys at M0 single-task).
func TestCheckFloorsAllPassStampsFindings(t *testing.T) {
	w := &fakeWriter{}
	facts, res, err := run(t, passingAttempt(), w, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	for _, f := range res.Findings {
		if !f.Passed {
			t.Errorf("floor %s: want passed, got rejected (%s)", f.Floor, f.Detail)
		}
	}
	// The aggregate verdict the dev-loop gate reads: no floor rejected → rejected=false.
	if got := facts[floors.RejectedPredicate]; got != "false" {
		t.Errorf("%s = %q, want false (no floor rejected)", floors.RejectedPredicate, got)
	}
	// The concatenated detail scalar names every floor as passed (G7 legibility).
	for _, floor := range []string{floors.FloorPresence, floors.FloorTestsMustExist, floors.FloorVacuousTest, floors.FloorStub, floors.FloorSourceBuild, floors.FloorAntiMock, floors.FloorCleanTree} {
		if !strings.Contains(facts[floors.DetailPredicate], floor+": passed") {
			t.Errorf("%s missing %q: passed, got %q", floors.DetailPredicate, floor, facts[floors.DetailPredicate])
		}
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("finding Source = %q, want %q (G5)", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("finding subject = %q, want run entity (D15)", tr.Subject)
			}
		}
	}
}

// A vacuous test authored by the attempt is rejected by the vacuous-test floor: the
// returned Finding is passed=false with a detail, the stamped aggregate flags true, and
// the concatenated detail scalar names the rejecting floor (D4).
func TestCheckFloorsVacuousTestRejected(t *testing.T) {
	w := &fakeWriter{}
	facts, res, err := run(t, vacuousAttempt(), w, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	var vacuous floors.Finding
	found := false
	for _, f := range res.Findings {
		if f.Floor == floors.FloorVacuousTest {
			vacuous, found = f, true
		}
	}
	if !found {
		t.Fatal("expected a vacuous-test finding in the returned result")
	}
	if vacuous.Passed {
		t.Errorf("vacuous-test finding must be passed=false")
	}
	if vacuous.Detail == "" {
		t.Errorf("a rejecting finding must carry a detail (legible park)")
	}
	if !res.Rejected {
		t.Errorf("result must flag Rejected=true")
	}
	// The STAMPED aggregate fact (what the gate reads, not the returned struct) must be true.
	if got := facts[floors.RejectedPredicate]; got != "true" {
		t.Errorf("%s = %q, want true (a rejecting floor sets the aggregate)", floors.RejectedPredicate, got)
	}
	if !strings.Contains(facts[floors.DetailPredicate], floors.FloorVacuousTest+": REJECTED") {
		t.Errorf("%s must name the rejecting floor, got %q", floors.DetailPredicate, facts[floors.DetailPredicate])
	}
}

// The task index is still threaded through to Attempts.Resolve (which task's checkout
// to read) even though beta.147 D1 dropped it from the stamped predicate — single-task
// at M0, the findings live in one flat package on the run rather than being keyed per
// task, so there is no longer a fact-shaped way to prove per-task keying; this proves
// the index still reaches the resolver.
func TestCheckFloorsForwardsTaskIndexToResolve(t *testing.T) {
	var gotIdx int
	fa := fakeAttempts{attempt: passingAttempt(), gotIdx: &gotIdx}
	w := &fakeWriter{}
	_, err := RunFloors(context.Background(), fa, fakeReader{}, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", 2)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	if gotIdx != 2 {
		t.Errorf("RunFloors must forward taskIndex to Attempts.Resolve, got %d want 2", gotIdx)
	}
}

// Re-evaluation upserts the same task's findings (the fixed floor set replaces by
// predicate, no clears needed) — a re-attempt's verdict replaces the prior one.
func TestCheckFloorsReEvalUpserts(t *testing.T) {
	w := &fakeWriter{}
	run(t, vacuousAttempt(), w, 0) // first attempt: rejected
	run(t, passingAttempt(), w, 0) // fixed re-attempt: passes
	if len(w.replaces) != 2 {
		t.Fatalf("expected two upserts, got %d", len(w.replaces))
	}
	if w.clearedFindings() {
		t.Error("RunFloors cleared floor-tools' owned group; the fixed floor set should upsert without clears")
	}
}

// Codex P1: the finding set is bound to an attempt identity, and a CHANGED source
// produces a DIFFERENT id — so a gate can tell whether a stamped finding evaluated
// the current attempt or a stale earlier one, and cannot read attempt 1's pass as
// current after attempt 2 changes the source.
func TestCheckFloorsBindsAttemptIdentity(t *testing.T) {
	w1 := &fakeWriter{}
	facts1, _, _ := run(t, passingAttempt(), w1, 0)
	id1 := facts1[floors.AttemptPredicate]
	if id1 == "" {
		t.Fatal("finding set must stamp floor.finding.attempt (the evaluated-source identity)")
	}
	if id1 != floors.AttemptID(passingAttempt()) {
		t.Errorf("stamped attempt id %q != AttemptID(attempt) — the gate cannot recompute it", id1)
	}

	// Attempt 2 changes the source: the id must differ from attempt 1's.
	changed := passingAttempt()
	changed.Files[0].Content += "\nfunc Sub(a, b int) int { return a - b }\n"
	w2 := &fakeWriter{}
	facts2, _, _ := run(t, changed, w2, 0)
	if id2 := facts2[floors.AttemptPredicate]; id2 == id1 {
		t.Errorf("a changed attempt must produce a different attempt id (got %q for both)", id2)
	}
}

// Codex P1: a resolve/check failure must NOT leave a prior attempt's pass readable.
// RunFloors clears the task's stale findings (clear-my-prefix) and surfaces the error,
// so semantic-review eligibility cannot read the stale pass as current.
func TestCheckFloorsResolveFailureClearsStaleFindings(t *testing.T) {
	stale := []string{
		floors.AttemptPredicate,
		floors.RejectedPredicate,
		floors.DetailPredicate,
	}
	w := &fakeWriter{owned: stale}
	_, err := RunFloors(context.Background(), fakeAttempts{err: errors.New("checkout unreadable")}, fakeReader{}, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", 0)
	if err == nil {
		t.Fatal("a failed resolve must surface an error")
	}
	// The stale findings must have been cleared (removed), and nothing new stamped.
	// The clear is now an EMPTY Desired on floor-tools' contract (the group wipe),
	// not an explicit remove list of the stale predicates.
	if !w.clearedFindings() {
		t.Errorf("a resolve failure must CLEAR the task's stale findings (%v), got writes=%v", stale, w.replaces)
	}
	for _, batch := range w.replaces {
		if len(batch) > 0 {
			t.Errorf("a failed resolve must stamp no new findings, got %v", batch)
		}
	}
}

// An attempt that cannot be resolved (checkout read fault) is an error, not a silent pass.
func TestCheckFloorsResolveErrorFails(t *testing.T) {
	w := &fakeWriter{}
	_, err := RunFloors(context.Background(), fakeAttempts{err: errors.New("checkout unreadable")}, fakeReader{}, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", 0)
	if err == nil {
		t.Fatal("a failed attempt resolve must error")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed resolve must stamp nothing")
	}
}

// The ROUTE MIRROR: RunFloors copies the routing inputs onto the caller-supplied
// routeLoopEntityID so the rule-native floors route (advance / not_clean → retry /
// escalate) can fire on them. route.attempt.passed = the run's measurement,
// route.attempt.rejected = its own aggregate, and route.attempt.instance = the
// append-mirror of task.attempt.instance. All carry the route-mirror Source (a distinct
// writer from floor-tools, so no predicate gains two writers, G5). It is stamped for a
// REJECTING run too (the route decides retry).
func TestCheckFloorsMirrorsRouteInputsOntoLoop(t *testing.T) {
	w := &fakeWriter{}
	// The run measured GREEN but the floors REJECT (vacuous test) — route.attempt.passed=true,
	// route.attempt.rejected=true (the "cache-masked fabrication that passes warm" shape). Two
	// attempts already counted (task.attempt.instance has two objects). The measurement is bound
	// to the run's CURRENT attempt.commit.sha (sha-b), so the green is trusted (not stale).
	reader := fakeReader{facts: []message.Triple{
		measuredPassed("true"),
		measuredCommit("sha-b"),
		attemptCommitFact("sha-b"),
		attemptFact("dev-loop-1"),
		attemptFact("dev-loop-2"),
		budgetFact("3"),
	}}
	res, err := RunFloors(context.Background(), fakeAttempts{attempt: vacuousAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	if !res.Rejected {
		t.Fatalf("want a rejecting floors verdict, got Rejected=false")
	}
	var passed, rejected string
	attemptObjs := map[string]bool{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			switch tr.Predicate {
			case RoutePassedPredicate, RouteRejectedPredicate, RouteAttemptPredicate:
				if tr.Subject != loopEntity {
					t.Errorf("route mirror %q stamped on %q, want the floors LOOP entity %q", tr.Predicate, tr.Subject, loopEntity)
				}
				if tr.Source != RouteMirrorSource {
					t.Errorf("route mirror %q Source = %q, want %q (G5)", tr.Predicate, tr.Source, RouteMirrorSource)
				}
			}
			switch tr.Predicate {
			case RoutePassedPredicate:
				passed = tr.Object.(string)
			case RouteRejectedPredicate:
				rejected = tr.Object.(string)
			case RouteAttemptPredicate:
				attemptObjs[tr.Object.(string)] = true
			}
		}
	}
	if passed != "true" {
		t.Errorf("%s = %q, want the measurement copy \"true\"", RoutePassedPredicate, passed)
	}
	if rejected != "true" {
		t.Errorf("%s = %q, want the vacuous-test rejection \"true\"", RouteRejectedPredicate, rejected)
	}
	if len(attemptObjs) != 2 || !attemptObjs["dev-loop-1"] || !attemptObjs["dev-loop-2"] {
		t.Errorf("%s must mirror both task.attempt.instance objects, got %v", RouteAttemptPredicate, attemptObjs)
	}
}

// Fail-closed: when the run has NO measurement (Amelia stopped without measuring),
// route.attempt.passed is copied as "false" — the route treats it as a red attempt, not green.
func TestCheckFloorsMirrorsFailClosedWhenMeasurementAbsent(t *testing.T) {
	w := &fakeWriter{}
	// No measurement fact seeded → route.attempt.passed must fail closed to "false". A
	// budget AND an attempt ARE seeded: the mirror requires both (D7 / task 4.6 — the
	// dispatch rule appends an attempt at spawn, so a running loop always has one),
	// and this test is about measurement absence, not a half-provisioned run.
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, fakeReader{facts: []message.Triple{budgetFact("3"), attemptFact("dev-loop-1")}}, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	var sawPassed bool
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == RoutePassedPredicate {
				sawPassed = true
				if tr.Object.(string) != "false" {
					t.Errorf("%s with no measurement must fail closed to \"false\", got %q", RoutePassedPredicate, tr.Object)
				}
			}
		}
	}
	if !sawPassed {
		t.Errorf("RunFloors must always stamp %s (fail-closed) so the route can fire", RoutePassedPredicate)
	}
}

// Fail-closed on a STALE green (the semstreams-reviewer HIGH): a green measurement bound to
// a PRIOR snapshot (measurement.result.commit=sha-a) must NOT advance a later attempt that
// was re-applied to a new snapshot (attempt.commit.sha=sha-b) but never re-measured.
// route.attempt.passed mirrors "false" so the route treats it as red (not-clean → retry),
// never a stale advance.
func TestCheckFloorsMirrorsFailClosedOnStaleMeasurement(t *testing.T) {
	w := &fakeWriter{}
	// Green measurement bound to sha-a, but the run has since been re-applied to sha-b.
	reader := fakeReader{facts: []message.Triple{
		measuredPassed("true"),
		measuredCommit("sha-a"),
		attemptCommitFact("sha-b"),
		budgetFact("3"),
		// A running loop always carries an attempt (task 4.6); this test is about a
		// STALE measurement, not a half-provisioned run.
		attemptFact("dev-loop-1"),
	}}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == RoutePassedPredicate && tr.Object.(string) != "false" {
				t.Errorf("%s on a STALE green (measured sha-a, current sha-b) must be \"false\", got %q — a stale measurement must not advance a re-applied-but-unmeasured attempt", RoutePassedPredicate, tr.Object)
			}
		}
	}
}

// The per-task attempt budget is mirrored onto L_n (task 3.3): given a run with
// task.spec.budget=B, the mirror stamps route.task.budget=B on the dispatch loop — the
// scalar the retry/escalate routes substitute for the old constant 3. ATOMICITY (D7/3.6):
// route.task.budget rides the SAME ReplaceTriples pass as route.attempt.* — the routes never
// see an attempt count without the budget (a half-mirror would let them read the fail-open
// empty substitution).
func TestCheckFloorsMirrorStampsBudget(t *testing.T) {
	w := &fakeWriter{}
	reader := fakeReader{facts: []message.Triple{
		measuredPassed("false"),
		attemptFact("dev-loop-1"),
		budgetFact("4"),
	}}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	// Find the mirror batch (the one carrying route.attempt.instance) and assert the budget
	// is stamped in that SAME batch, on the loop entity, with the mirror Source.
	var budget string
	var sawBudget, sawAttempt bool
	for _, batch := range w.replaces {
		hasAttempt, hasBudget := false, false
		for _, tr := range batch {
			switch tr.Predicate {
			case RouteAttemptPredicate:
				hasAttempt = true
			case RouteBudgetPredicate:
				hasBudget = true
				budget = tr.Object.(string)
				if tr.Subject != loopEntity {
					t.Errorf("%s stamped on %q, want the floors LOOP entity %q", RouteBudgetPredicate, tr.Subject, loopEntity)
				}
				if tr.Source != RouteMirrorSource {
					t.Errorf("%s Source = %q, want %q (G5)", RouteBudgetPredicate, tr.Source, RouteMirrorSource)
				}
			}
		}
		if hasAttempt {
			sawAttempt = true
			if !hasBudget {
				t.Errorf("D7 atomicity: route.attempt.* stamped without %s in the same ReplaceTriples pass", RouteBudgetPredicate)
			}
		}
		if hasBudget {
			sawBudget = true
		}
	}
	if !sawAttempt || !sawBudget {
		t.Fatalf("expected the mirror to stamp both route.attempt.instance and %s (sawAttempt=%v sawBudget=%v)", RouteBudgetPredicate, sawAttempt, sawBudget)
	}
	if budget != "4" {
		t.Errorf("%s = %q, want the RAW authored budget copy \"4\"", RouteBudgetPredicate, budget)
	}
}

// D7a: an ABSENT budget is a loud station fault, never a silent default. RunFloors returns an
// error BEFORE any mirror write, so no route.* is stamped on L_n (atomicity — no attempt count
// without a budget). CRUCIALLY, the current attempt's floor.finding.* on the RUN are genuine
// harness output, already durable, and MUST NOT be cleared — the resolve-fault clear is for a
// stale PRIOR pass only. Asserts both: no L_n mirror AND findings intact (not cleared).
func TestCheckFloorsMirrorAbsentBudgetFaultsFindingsIntact(t *testing.T) {
	w := &fakeWriter{}
	// Measurement + attempts present, but NO task.spec.budget → the D7 mirror fault.
	reader := fakeReader{facts: []message.Triple{
		measuredPassed("false"),
		attemptFact("dev-loop-1"),
	}}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err == nil {
		t.Fatal("an absent task.spec.budget must fault the mirror loudly (D7), got nil error")
	}
	sawFindings := false
	for _, batch := range w.replaces {
		for _, tr := range batch {
			switch tr.Predicate {
			case RoutePassedPredicate, RouteRejectedPredicate, RouteAttemptPredicate, RouteBudgetPredicate:
				t.Errorf("no route mirror may be stamped when the budget is absent (atomicity), but %s was", tr.Predicate)
			case floors.RejectedPredicate:
				sawFindings = true
				if tr.Subject != runEntity {
					t.Errorf("floor.finding stamped on %q, want the RUN %q", tr.Subject, runEntity)
				}
			}
		}
	}
	if !sawFindings {
		t.Error("the current attempt's floor.finding.* must be stamped on the run BEFORE the mirror phase (findings-first ordering)")
	}
	// findings MUST NOT be cleared: the budget fault is not a resolve fault. No ReplaceTriples
	// remove list may carry a floor.finding predicate.
	if w.clearedFindings() {
		t.Error("the budget fault cleared floor-tools' owned group — the current attempt's genuine findings must stay durable (only a stale PRIOR pass is cleared, on a resolve fault)")
	}
}

// D7 parse-validation: a PRESENT-but-non-canonical budget (anything the engine's coerceToInt
// would reject — a blank, whitespace-padded, or non-integer value) faults the mirror loudly, just
// like an absent budget. readTaskBudget validates the RAW stamped value with strconv.Atoi (no
// trim), so what it admits is exactly what the route can coerce — a value that merely "looks"
// numeric (" 3 ", "3.0") never slips through to a fail-open route stall.
func TestCheckFloorsMirrorNonCanonicalBudgetFaults(t *testing.T) {
	for _, bad := range []string{"", " 3 ", "3.0", "abc"} {
		w := &fakeWriter{}
		reader := fakeReader{facts: []message.Triple{measuredPassed("false"), attemptFact("dev-loop-1"), budgetFact(bad)}}
		_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
		if err == nil {
			t.Errorf("budget %q: a non-canonical budget must fault the mirror (D7), got nil error", bad)
		}
		for _, batch := range w.replaces {
			for _, tr := range batch {
				switch tr.Predicate {
				case RoutePassedPredicate, RouteRejectedPredicate, RouteAttemptPredicate, RouteBudgetPredicate:
					t.Errorf("budget %q: no route mirror may be stamped on a non-canonical budget (atomicity), but %s was", bad, tr.Predicate)
				}
			}
		}
	}
}

// The TRANSIENT-retry counter is mirrored onto L_n (adopt-reason-aware-escalate, task 2.3):
// given a run with N task.transient.instance objects, the mirror stamps N
// route.transient.instance on L_n so the transient-retry route counts the grace. Given none,
// none is stamped (a valid count 0 — no fail-open, unlike the substituted budget).
func TestCheckFloorsMirrorStampsTransientCounter(t *testing.T) {
	w := &fakeWriter{}
	reader := fakeReader{facts: []message.Triple{
		measuredPassed("false"),
		attemptFact("dev-loop-1"),
		budgetFact("3"),
		transientFact("dev-loop-1a"),
		transientFact("dev-loop-1b"),
	}}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	transientObjs := map[string]bool{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == RouteTransientPredicate {
				if tr.Subject != loopEntity {
					t.Errorf("%s stamped on %q, want the floors LOOP entity %q", RouteTransientPredicate, tr.Subject, loopEntity)
				}
				if tr.Source != RouteMirrorSource {
					t.Errorf("%s Source = %q, want %q (G5)", RouteTransientPredicate, tr.Source, RouteMirrorSource)
				}
				transientObjs[tr.Object.(string)] = true
			}
		}
	}
	if len(transientObjs) != 2 || !transientObjs["dev-loop-1a"] || !transientObjs["dev-loop-1b"] {
		t.Errorf("%s must mirror both task.transient.instance objects, got %v", RouteTransientPredicate, transientObjs)
	}
}

// A run with NO task.transient.instance mirrors zero route.transient.instance (a valid count 0,
// no fault) — the common path where no transient failure has occurred.
func TestCheckFloorsMirrorAbsentTransientIsZeroNoFault(t *testing.T) {
	w := &fakeWriter{}
	reader := fakeReader{facts: []message.Triple{measuredPassed("false"), attemptFact("dev-loop-1"), budgetFact("3")}}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("an absent transient counter must NOT fault (valid count 0): %v", err)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == RouteTransientPredicate {
				t.Errorf("no task.transient.instance seeded, but %s was mirrored", RouteTransientPredicate)
			}
		}
	}
}

// The TRANSIENT FLAG classification (adopt-reason-aware-escalate): route.attempt.transient is
// "true" iff the LOOP's harness-stamped agent.loop.terminal-reason is in the transient set
// (model_error/handler_error), "false" for every other reason INCLUDING absent (the normal
// completed-loop terminal) — and it is ALWAYS stamped, so the convergence routes' eq "false"
// exclusion never reads an absent field.
func TestCheckFloorsMirrorTransientFlagClassification(t *testing.T) {
	cases := []struct {
		name      string
		loopFacts []message.Triple
		want      string
	}{
		{"absent reason (completed loop)", nil, "false"},
		{"model_error is transient", []message.Triple{terminalReasonFact("model_error")}, "true"},
		{"handler_error is transient", []message.Triple{terminalReasonFact("handler_error")}, "true"},
		{"max_iterations is a genuine terminal", []message.Triple{terminalReasonFact("max_iterations")}, "false"},
		{"graph_state_reset_required is a genuine terminal", []message.Triple{terminalReasonFact("graph_state_reset_required")}, "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{}
			reader := fakeReader{
				facts:     []message.Triple{measuredPassed("false"), attemptFact("dev-loop-1"), budgetFact("3")},
				loopFacts: tc.loopFacts,
			}
			_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
			if err != nil {
				t.Fatalf("RunFloors: %v", err)
			}
			var got string
			for _, batch := range w.replaces {
				for _, tr := range batch {
					if tr.Predicate == RouteTransientFlagPredicate {
						got = tr.Object.(string)
						if tr.Subject != loopEntity {
							t.Errorf("%s stamped on %q, want the floors LOOP entity %q", RouteTransientFlagPredicate, tr.Subject, loopEntity)
						}
						if tr.Source != RouteMirrorSource {
							t.Errorf("%s Source = %q, want %q (G5)", RouteTransientFlagPredicate, tr.Source, RouteMirrorSource)
						}
					}
				}
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q (the flag must ALWAYS be stamped — an absent flag makes the eq exclusions read absent→false and stalls the convergence routes)", RouteTransientFlagPredicate, got, tc.want)
			}
		})
	}
}

// THE RACE PIN (adopt-reason-aware-escalate, the double-dispatch caught live by
// TestBridgeProofTransientGraceRetries): the transient flag must ride the SAME ReplaceTriples
// call as route.attempt.passed — the engine writes each mutation as its own KV revision and
// evaluates rules per debounce-flush against fetched state, so a flag landing in a SEPARATE
// revision creates an eval pass where 06b's unclean chain is satisfiable but the transient
// exclusion is not yet visible → 06c convergence-retry fires alongside 06f transient-retry
// (double dispatch, one-in-flight violated). Atomic-with-passed is the WHOLE fix — this pin
// fails if the flag is ever moved to its own write (or back to a rule-stamped collapse).
func TestCheckFloorsMirrorTransientFlagAtomicWithPassed(t *testing.T) {
	w := &fakeWriter{}
	reader := fakeReader{
		facts:     []message.Triple{measuredPassed("false"), attemptFact("dev-loop-1"), budgetFact("3")},
		loopFacts: []message.Triple{terminalReasonFact("model_error")},
	}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader, findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	found := false
	for i, batch := range w.replaces {
		hasPassed, hasFlag := false, false
		for _, tr := range batch {
			if tr.Predicate == RoutePassedPredicate {
				hasPassed = true
			}
			if tr.Predicate == RouteTransientFlagPredicate {
				hasFlag = true
			}
		}
		if !hasPassed {
			continue
		}
		found = true
		if w.entities[i] != loopEntity {
			t.Errorf("the mirror ReplaceTriples targeted entity %q, want the floors LOOP entity %q — a mirror written to the wrong entity fires no route while every triple-level pin stays green", w.entities[i], loopEntity)
		}
		if !hasFlag {
			t.Fatalf("route.attempt.passed stamped WITHOUT %s in the same ReplaceTriples — the transient classification must be atomic with the mirror or the convergence routes race it (the pinned double-dispatch)", RouteTransientFlagPredicate)
		}
		// Upsert (rather than accreting a second value on a re-mirror) is now the
		// group wipe: ReplaceOwned clears route-mirror's whole loop group before
		// adding Desired, so the single-valued flag replaces its prior value by
		// construction — provided the write goes out under the ROUTE-MIRROR contract
		// and not floor-tools'. That contract check is the assertion the old
		// remove-list pin becomes (migrate-beta159 D3a).
		if w.contracts[i] != RouteMirrorSource {
			t.Errorf("the mirror resolved to contract %q, want %q — under floor-tools' contract the mirror predicates are outside the group and the write is rejected", w.contracts[i], RouteMirrorSource)
		}
	}
	if !found {
		t.Fatal("no ReplaceTriples batch carried route.attempt.passed — the mirror did not run")
	}
}

// Without a routeLoopEntityID (a unit test, or any caller with no loop to mirror onto)
// the findings still land but the route mirror is skipped — a missing mirror never
// fails a recorded findings write.
func TestCheckFloorsWithoutLoopIDSkipsMirrorButRecords(t *testing.T) {
	w := &fakeWriter{}
	facts, _, err := run(t, passingAttempt(), w, 0) // routeLoopEntityID=""
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	if _, ok := facts[RoutePassedPredicate]; ok {
		t.Errorf("no routeLoopEntityID → the route mirror must be skipped, but %s was stamped", RoutePassedPredicate)
	}
	if facts[floors.RejectedPredicate] != "false" {
		t.Error("the findings must still be recorded when the mirror is skipped")
	}
}

// Sanity: the pure floors agree with what RunFloors returns and stamps (the wrapper
// does not re-derive or alter the verdict, and the stamped detail scalar is exactly
// FormatDetail's concatenation of the pure findings).
func TestCheckFloorsMatchesPureVerdict(t *testing.T) {
	att := vacuousAttempt()
	want := floors.CheckAll(att)
	w := &fakeWriter{}
	facts, res, err := run(t, att, w, 0)
	if err != nil {
		t.Fatalf("RunFloors: %v", err)
	}
	if len(res.Findings) != len(want) {
		t.Fatalf("RunFloors returned %d findings, pure floors.CheckAll returned %d", len(res.Findings), len(want))
	}
	for i, f := range want {
		got := res.Findings[i]
		if got.Floor != f.Floor || got.Passed != f.Passed || got.Detail != f.Detail {
			t.Errorf("finding %d: RunFloors returned %+v, pure verdict %+v", i, got, f)
		}
	}
	wantDetail := floors.FormatDetail(want)
	if facts[floors.DetailPredicate] != wantDetail {
		t.Errorf("stamped detail = %q, want %q (FormatDetail of the pure findings)", facts[floors.DetailPredicate], wantDetail)
	}
}

// TestCheckFloorsFaultsOnEmptyAttemptSet is the migrate-beta159 task 4.6 pin.
//
// Under the OLD writer an empty task.attempt.instance read was HARMLESS: the mirror's
// remove list was [passed, rejected, budget, transient-flag], so a previously
// mirrored route.attempt.instance survived untouched. Under ReplaceOwned the group
// wipe removes all seven route.* predicates and re-adds only Desired — so an empty
// read DELETES the attempt count, the retry/escalate routes read length 0 as
// budget-unexhausted, and the run retries past its budget on paid tokens. Worse, the
// deletion returns CommitVerified, so nothing downstream can tell.
//
// The empty set cannot occur on a healthy run (the dispatch rule appends one at
// spawn), which is exactly why it must fault loudly rather than be tolerated.
func TestCheckFloorsFaultsOnEmptyAttemptSet(t *testing.T) {
	w := &fakeWriter{}
	// Budget present, measurement present — ONLY the attempt set is empty.
	reader := fakeReader{facts: []message.Triple{measuredPassed("true"), budgetFact("3")}}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, reader,
		findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, loopEntity, 0)
	if err == nil {
		t.Fatal("an EMPTY task.attempt.instance must fault — mirroring would group-wipe the prior attempt count and read as budget-unexhausted")
	}
	if !strings.Contains(err.Error(), RouteAttemptPredicate) && !strings.Contains(err.Error(), "task.attempt.instance") {
		t.Errorf("error %q must name the empty predicate", err)
	}
	// Nothing may be mirrored onto the loop: a partial mirror is what the D7
	// atomicity invariant forbids.
	for i, batch := range w.replaces {
		if w.entities[i] == loopEntity && len(batch) > 0 {
			t.Errorf("the mirror wrote %d triples on the loop despite the fault — no route fact may be stamped without the attempt count", len(batch))
		}
	}
}
