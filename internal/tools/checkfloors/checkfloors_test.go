package checkfloors

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeAttempts struct {
	attempt floors.Attempt
	err     error
}

func (f fakeAttempts) Resolve(_ context.Context, _ string, _ int) (floors.Attempt, error) {
	return f.attempt, f.err
}

type fakeWriter struct {
	owned    []string // predicates ReadOwnedPredicates returns (the stale set)
	replaces [][]message.Triple
	removes  [][]string
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, rm []string) error {
	w.replaces = append(w.replaces, add)
	w.removes = append(w.removes, rm)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return w.owned, nil
}

func callFor(idx int) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{"task_index": idx},
	}
}

// passingAttempt authors production source plus a real test that asserts on computed
// behavior of the target — it clears all five floors.
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

func run(t *testing.T, attempt floors.Attempt, w *fakeWriter, idx int) (map[string]string, agentic.ToolResult) {
	t.Helper()
	res, err := New(fakeAttempts{attempt: attempt}, w, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(idx))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	facts := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			facts[tr.Predicate] = tr.Object.(string)
		}
	}
	return facts, res
}

// Happy path: every floor passes → each floor stamps a passed=true finding on the
// run entity under floor.finding.<i>.<floor>, with the floor-tools Source.
func TestCheckFloorsAllPassStampsFindings(t *testing.T) {
	w := &fakeWriter{}
	facts, res := run(t, passingAttempt(), w, 0)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	for _, floor := range []string{floors.FloorPresence, floors.FloorTestsMustExist, floors.FloorVacuousTest, floors.FloorStub, floors.FloorSourceBuild, floors.FloorAntiMock} {
		key := floors.FindingPrefix + "0." + floor + "." + floors.FactPassed
		if facts[key] != "true" {
			t.Errorf("%s = %q, want true", key, facts[key])
		}
	}
	// The aggregate verdict the dev-loop gate reads: no floor rejected → rejected=false.
	if got := facts[floors.FindingPrefix+"0."+floors.FactRejected]; got != "false" {
		t.Errorf("%s0.%s = %q, want false (no floor rejected)", floors.FindingPrefix, floors.FactRejected, got)
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

// A vacuous test authored by the attempt is rejected by the vacuous-test floor: its
// finding stamps passed=false with a detail, and the result flags rejected.
func TestCheckFloorsVacuousTestRejected(t *testing.T) {
	w := &fakeWriter{}
	facts, res := run(t, vacuousAttempt(), w, 0)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if facts[floors.FindingPrefix+"0."+floors.FloorVacuousTest+"."+floors.FactPassed] != "false" {
		t.Errorf("vacuous-test finding must be passed=false")
	}
	if facts[floors.FindingPrefix+"0."+floors.FloorVacuousTest+"."+floors.FactDetail] == "" {
		t.Errorf("a rejecting finding must carry a detail (legible park)")
	}
	if !strings.Contains(res.Content, "\"rejected\":true") {
		t.Errorf("result must flag rejected=true, got %s", res.Content)
	}
	// The STAMPED aggregate fact (what the gate reads, not the tool's Content) must be true.
	if got := facts[floors.FindingPrefix+"0."+floors.FactRejected]; got != "true" {
		t.Errorf("%s0.%s = %q, want true (a rejecting floor sets the aggregate)", floors.FindingPrefix, floors.FactRejected, got)
	}
}

// Findings are keyed per task: task 2's findings land under floor.finding.2.*, never
// clobbering another task's.
func TestCheckFloorsPerTaskKeying(t *testing.T) {
	facts, res := run(t, passingAttempt(), &fakeWriter{}, 2)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if _, ok := facts[floors.FindingPrefix+"2."+floors.FloorSourceBuild+"."+floors.FactPassed]; !ok {
		t.Errorf("expected findings keyed under floor.finding.2.*, got %v", facts)
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
	for _, rm := range w.removes {
		if len(rm) != 0 {
			t.Errorf("check_floors cleared predicates %v; the fixed floor set should upsert without clears", rm)
		}
	}
}

// Codex P1: the finding set is bound to an attempt identity, and a CHANGED source
// produces a DIFFERENT id — so a gate can tell whether a stamped finding evaluated
// the current attempt or a stale earlier one, and cannot read attempt 1's pass as
// current after attempt 2 changes the source.
func TestCheckFloorsBindsAttemptIdentity(t *testing.T) {
	w1 := &fakeWriter{}
	facts1, _ := run(t, passingAttempt(), w1, 0)
	id1 := facts1[floors.FindingPrefix+"0."+floors.FactAttempt]
	if id1 == "" {
		t.Fatal("finding set must stamp floor.finding.0.attempt (the evaluated-source identity)")
	}
	if id1 != floors.AttemptID(passingAttempt()) {
		t.Errorf("stamped attempt id %q != AttemptID(attempt) — the gate cannot recompute it", id1)
	}

	// Attempt 2 changes the source: the id must differ from attempt 1's.
	changed := passingAttempt()
	changed.Files[0].Content += "\nfunc Sub(a, b int) int { return a - b }\n"
	w2 := &fakeWriter{}
	facts2, _ := run(t, changed, w2, 0)
	if id2 := facts2[floors.FindingPrefix+"0."+floors.FactAttempt]; id2 == id1 {
		t.Errorf("a changed attempt must produce a different attempt id (got %q for both)", id2)
	}
}

// Codex P1: a resolve/check failure must NOT leave a prior attempt's pass readable.
// The tool clears the task's stale findings (clear-my-prefix) and surfaces the error,
// so semantic-review eligibility cannot read the stale pass as current.
func TestCheckFloorsResolveFailureClearsStaleFindings(t *testing.T) {
	stale := []string{
		floors.FindingPrefix + "0." + floors.FactAttempt,
		floors.FindingPrefix + "0." + floors.FloorSourceBuild + "." + floors.FactPassed,
		floors.FindingPrefix + "0." + floors.FloorVacuousTest + "." + floors.FactPassed,
	}
	w := &fakeWriter{owned: stale}
	res, err := New(fakeAttempts{err: errors.New("checkout unreadable")}, w, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("a failed resolve must surface an error")
	}
	// The stale findings must have been cleared (removed), and nothing new stamped.
	cleared := false
	for _, rm := range w.removes {
		if len(rm) == len(stale) {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("a resolve failure must CLEAR the task's stale findings, got removes=%v", w.removes)
	}
	for _, batch := range w.replaces {
		if len(batch) > 0 {
			t.Errorf("a failed resolve must stamp no new findings, got %v", batch)
		}
	}
}

// A negative task index is rejected before any resolve or stamp.
func TestCheckFloorsRejectsNegativeIndex(t *testing.T) {
	w := &fakeWriter{}
	res, err := New(fakeAttempts{attempt: passingAttempt()}, w, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(-1))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected an error for a negative task index")
	}
	if len(w.replaces) != 0 {
		t.Error("a rejected index must stamp nothing")
	}
}

// An attempt that cannot be resolved (checkout read fault) is a tool error, not a
// silent pass.
func TestCheckFloorsResolveErrorFails(t *testing.T) {
	w := &fakeWriter{}
	res, err := New(fakeAttempts{err: errors.New("checkout unreadable")}, w, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("a failed attempt resolve must error")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed resolve must stamp nothing")
	}
}

// Schema-only registration (nil attempts/writer) fails loudly if executed.
func TestCheckFloorsFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness check must fail loudly")
	}
}

// G3: the schema takes only the task selector — no outcome/passed field. The floor
// verdicts are computed by the deterministic floors, not supplied.
func TestCheckFloorsSchemaTakesOnlyTaskSelector(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 1 {
		t.Errorf("schema exposes %d properties, want exactly 1 (task_index): %v", len(props), props)
	}
	if _, ok := props["task_index"]; !ok {
		t.Errorf("schema must expose task_index; has %v", props)
	}
	for _, banned := range []string{"passed", "pass", "rejected", "outcome", "findings", "floor"} {
		if _, present := props[banned]; present {
			t.Errorf("schema accepts a floor-outcome field %q (G3): floors are computed, not supplied", banned)
		}
	}
}

// The chaining marker: check_floors stamps dev.floors_done on ITS OWN loop entity
// (value = the task index) after recording the findings, so the gate-trigger
// (dev-from-task/07) fires on the floors loop. It is stamped for a REJECTING run too
// (the gate decides retry), so a fabrication finding still chains to the gate.
func TestCheckFloorsStampsFloorsDoneMarkerOnLoopEvenWhenRejecting(t *testing.T) {
	w := &fakeWriter{}
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	call := callFor(0)
	call.LoopID = "floors-loop-abc"
	// A vacuous test → the floors REJECT; the marker must still land.
	res, err := New(fakeAttempts{attempt: vacuousAttempt()}, w, platform, nil).Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("a rejecting floors run is data, not a tool error: %s", res.Error)
	}
	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, call.LoopID)
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	var found bool
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != FloorsDonePredicate {
				continue
			}
			found = true
			if tr.Subject != loopEntityID {
				t.Errorf("%s stamped on %q, want the floors LOOP entity %q (not the run)", FloorsDonePredicate, tr.Subject, loopEntityID)
			}
			if tr.Source != Source {
				t.Errorf("%s Source = %q, want %q (G5)", FloorsDonePredicate, tr.Source, Source)
			}
			if tr.Object.(string) != "0" {
				t.Errorf("%s object = %q, want the task index \"0\"", FloorsDonePredicate, tr.Object)
			}
		}
	}
	if !found {
		t.Errorf("check_floors must stamp %s on its loop (even for a rejecting run) so the gate-trigger fires", FloorsDonePredicate)
	}
}

// Without a LoopID (unit/registration path) the findings still land but the marker is
// skipped — a missing marker never fails a recorded findings write.
func TestCheckFloorsWithoutLoopIDSkipsMarkerButRecords(t *testing.T) {
	w := &fakeWriter{}
	facts, res := run(t, passingAttempt(), w, 0) // callFor sets no LoopID
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if _, ok := facts[FloorsDonePredicate]; ok {
		t.Errorf("no LoopID → the floors-done marker must be skipped, but %s was stamped", FloorsDonePredicate)
	}
	if facts[floors.FindingPrefix+"0."+floors.FactRejected] != "false" {
		t.Error("the findings must still be recorded when the marker is skipped")
	}
}

// Sanity: the pure floors agree with what the tool stamps (the wrapper does not
// re-derive or alter the verdict).
func TestCheckFloorsMatchesPureVerdict(t *testing.T) {
	att := vacuousAttempt()
	want := floors.CheckAll(att)
	w := &fakeWriter{}
	facts, _ := run(t, att, w, 0)
	for _, f := range want {
		key := floors.FindingPrefix + "0." + f.Floor + "." + floors.FactPassed
		if facts[key] != strconv.FormatBool(f.Passed) {
			t.Errorf("floor %s: stamped %q, pure verdict %v", f.Floor, facts[key], f.Passed)
		}
	}
}
