package submitreview

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeReader struct {
	facts []message.Triple
	err   error
}

func (r *fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, t := range r.facts {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeWriter struct {
	replaces [][]message.Triple
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	w.replaces = append(w.replaces, add)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func taskSpecFact(i int, field, obj string) message.Triple {
	return message.Triple{Predicate: "task.spec." + strconv.Itoa(i) + "." + field, Object: obj, Source: "task-projector"}
}

func measurementFacts(i, exit int, ran, timedOut, passed bool) []message.Triple {
	p := measurement.ResultPrefix + strconv.Itoa(i) + "."
	return []message.Triple{
		{Predicate: p + measurement.FactCommand, Object: "go test ./...", Source: "measurement-harness"},
		{Predicate: p + measurement.FactRan, Object: strconv.FormatBool(ran), Source: "measurement-harness"},
		{Predicate: p + measurement.FactExitCode, Object: strconv.Itoa(exit), Source: "measurement-harness"},
		{Predicate: p + measurement.FactTimedOut, Object: strconv.FormatBool(timedOut), Source: "measurement-harness"},
		{Predicate: p + measurement.FactPassed, Object: strconv.FormatBool(passed), Source: "measurement-harness"},
	}
}

// call builds a submit_review call reviewing one task. findings is variadic; an
// empty variadic omits the findings arg entirely (the absent-vs-empty case).
func call(taskIndex int, findings ...string) agentic.ToolCall {
	args := map[string]any{"task_index": taskIndex}
	if findings != nil {
		fs := make([]any, len(findings))
		for i, f := range findings {
			fs[i] = f
		}
		args["findings"] = fs
	}
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: args,
	}
}

// verdictFor returns the verdict stamped for a task index across the writer's
// captured replaces (or "" if none).
func verdictFor(w *fakeWriter, taskIndex int) string {
	want := verdictPredicate(taskIndex)
	verdict := ""
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == want {
				verdict = tr.Object.(string)
			}
		}
	}
	return verdict
}

// runTask executes the tool reviewing taskIndex and returns the stamped per-task
// verdict (or "") and the result.
func runTask(t *testing.T, facts []message.Triple, w *fakeWriter, taskIndex int, findings ...string) (string, agentic.ToolResult) {
	t.Helper()
	res, err := New(&fakeReader{facts: facts}, w, types.PlatformMeta{}, nil).Execute(context.Background(), call(taskIndex, findings...))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return verdictFor(w, taskIndex), res
}

// run reviews task 0 (the common single-task case).
func run(t *testing.T, facts []message.Triple, w *fakeWriter, findings ...string) (string, agentic.ToolResult) {
	t.Helper()
	return runTask(t, facts, w, 0, findings...)
}

// oneTaskPassing is a run with one projected task (index 0) and a passing
// measurement for it.
func oneTaskPassing() []message.Triple {
	var f []message.Triple
	f = append(f, taskSpecFact(0, "goal", "add the guard"), taskSpecFact(0, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 0, true, false, true)...)
	return f
}

// Happy path: the reviewed task has a passing measurement and the reviewer raised
// no findings — the per-task verdict is approved, stamped with the reviewer Source
// on the run entity.
func TestReviewApprovesWhenMeasuredPassAndNoFindings(t *testing.T) {
	w := &fakeWriter{}
	verdict, res := run(t, oneTaskPassing(), w)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictApproved {
		t.Fatalf("verdict = %q, want %q", verdict, VerdictApproved)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("verdict Source = %q, want %q (G5)", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("verdict subject = %q, want run entity (D15)", tr.Subject)
			}
			if tr.Predicate != verdictPredicate(0) {
				t.Errorf("verdict predicate = %q, want %q (per-task namespace)", tr.Predicate, verdictPredicate(0))
			}
		}
	}
}

// Red-first, the spec scenario: the reviewed task's measured outcome is a FAILURE
// (exit 1) — the reviewer cannot record an approving verdict even with no findings
// and however the change describes itself. Approval reads the stamped fact.
func TestReviewCannotApproveFalseSuccess(t *testing.T) {
	var f []message.Triple
	f = append(f, taskSpecFact(0, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 1, true, false /*passed=*/, true)...) // stored passed lies; exit is 1
	w := &fakeWriter{}
	verdict, res := run(t, f, w)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — a failing measurement cannot be approved regardless of the stored passed", verdict, VerdictChangesRequested)
	}
}

// The reviewed task with NO measurement blocks its approval — the per-task gate
// proves THIS task's evidence EXISTS and passed, not merely that nothing failed.
func TestReviewBlocksWhenThisTasksMeasurementMissing(t *testing.T) {
	// Two tasks projected; only task 0 is measured. Reviewing task 1 (unmeasured)
	// cannot approve.
	var f []message.Triple
	f = append(f, taskSpecFact(0, "test_command", "go test ./..."), taskSpecFact(1, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 0, true, false, true)...)
	w := &fakeWriter{}
	verdict, res := runTask(t, f, w, 1)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — task 1 has no measurement", verdict, VerdictChangesRequested)
	}
}

// A passing task's verdict is unaffected by a DIFFERENT task's failure (the spec's
// per-task-independence scenario): task 0 passes, task 1 fails; reviewing task 0
// approves.
func TestReviewIsIndependentPerTask(t *testing.T) {
	var f []message.Triple
	f = append(f, taskSpecFact(0, "test_command", "go test ./..."), taskSpecFact(1, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 0, true, false, true)...)  // task 0 passes
	f = append(f, measurementFacts(1, 1, true, false, false)...) // task 1 fails
	w := &fakeWriter{}
	verdict, res := runTask(t, f, w, 0)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictApproved {
		t.Errorf("verdict = %q, want %q — task 0 passes; task 1's failure must not affect it", verdict, VerdictApproved)
	}
}

// A reviewer finding blocks approval even when the task's measurement passes —
// findings are additive constraints the reviewer may require.
func TestReviewFindingBlocksEvenWhenMeasuredPass(t *testing.T) {
	verdict, res := run(t, oneTaskPassing(), &fakeWriter{}, "error handling swallows the context error")
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — an open finding is a required change", verdict, VerdictChangesRequested)
	}
}

// Findings never weaken task.spec: the review tool writes ONLY review.verdict.<i>
// and holds no writer for task.spec, so a finding is structurally incapable of
// removing or relaxing a task.spec requirement (G5 single-writer).
func TestReviewWritesOnlyVerdictNeverTaskSpec(t *testing.T) {
	w := &fakeWriter{}
	run(t, oneTaskPassing(), w, "please also add a benchmark")
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != verdictPredicate(0) {
				t.Errorf("review stamped %q — it must write only %q (findings are additive, never weaken task.spec)", tr.Predicate, verdictPredicate(0))
			}
			if strings.HasPrefix(tr.Predicate, "task.spec.") {
				t.Errorf("review wrote a task.spec predicate %q — findings must not touch the immutable spec", tr.Predicate)
			}
		}
	}
}

// Reviewing a task that was never projected is an ordering error, not a verdict —
// you cannot review work that was never projected.
func TestReviewErrorsWhenTaskNotProjected(t *testing.T) {
	w := &fakeWriter{}
	_, res := run(t, measurementFacts(0, 0, true, false, true), w) // measurements but no task.spec
	if res.Error == "" {
		t.Fatal("expected an error when the reviewed task has no task.spec")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed review must stamp no verdict")
	}
}

// task_index is required and must be non-negative.
func TestReviewRequiresTaskIndex(t *testing.T) {
	c := call(0)
	delete(c.Arguments, "task_index")
	res, err := New(&fakeReader{facts: oneTaskPassing()}, &fakeWriter{}, types.PlatformMeta{}, nil).Execute(context.Background(), c)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a missing task_index must fail loudly (absent != task 0)")
	}
}

// Fail closed: a malformed measurement fact (unparseable evidence) is a tool error,
// not a silent approve or a defaulted verdict — the reviewer cannot judge evidence
// it cannot read.
func TestReviewFailsClosedOnMalformedMeasurement(t *testing.T) {
	var f []message.Triple
	f = append(f, taskSpecFact(0, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 0, true, false, true)...)
	// Corrupt the exit_code fact.
	for i := range f {
		if strings.HasSuffix(f[i].Predicate, "."+measurement.FactExitCode) {
			f[i].Object = "not-an-int"
		}
	}
	w := &fakeWriter{}
	_, res := run(t, f, w)
	if res.Error == "" {
		t.Fatal("a malformed measurement must fail the review closed, not approve")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed review must stamp no verdict")
	}
}

// Re-review upserts the latest verdict (replace-by-predicate): a task that regressed
// records changes_requested over a prior approved, on the same per-task predicate.
func TestReviewReReviewUpserts(t *testing.T) {
	w := &fakeWriter{}
	if v, _ := runTask(t, oneTaskPassing(), w, 0); v != VerdictApproved {
		t.Fatalf("first review verdict = %q, want approved", v)
	}
	// Now a finding appears on re-review of the same task.
	res, err := New(&fakeReader{facts: oneTaskPassing()}, w, types.PlatformMeta{}, nil).Execute(context.Background(), call(0, "regression found"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("re-review error: %s", res.Error)
	}
	if len(w.replaces) != 2 {
		t.Fatalf("expected two verdict upserts on the same predicate, got %d", len(w.replaces))
	}
	if v := verdictFor(w, 0); v != VerdictChangesRequested {
		t.Errorf("re-review verdict = %q, want %q", v, VerdictChangesRequested)
	}
}

// Schema-only registration (nil reader/writer) fails loudly if executed.
func TestReviewFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, types.PlatformMeta{}, nil).Execute(context.Background(), call(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness review must fail loudly")
	}
}

// The chaining marker: submit_review stamps dev.reviewed on ITS OWN review loop entity
// (value = the task index) after the verdict, so the verify station (8D) can fire on the
// review loop. It is stamped for a changes_requested verdict too (the coherence gate must
// still see the review), so a blocking review still chains forward.
func TestReviewStampsReviewedMarkerOnLoopEvenWhenBlocking(t *testing.T) {
	w := &fakeWriter{}
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	c := call(0, "regression found") // a finding → changes_requested
	c.LoopID = "review-loop-abc"
	res, err := New(&fakeReader{facts: oneTaskPassing()}, w, platform, nil).Execute(context.Background(), c)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("a blocking verdict is data, not a tool error: %s", res.Error)
	}
	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, c.LoopID)
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	var found bool
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != ReviewedPredicate {
				continue
			}
			found = true
			if tr.Subject != loopEntityID {
				t.Errorf("%s stamped on %q, want the review LOOP entity %q (not the run)", ReviewedPredicate, tr.Subject, loopEntityID)
			}
			if tr.Source != Source {
				t.Errorf("%s Source = %q, want %q (G5)", ReviewedPredicate, tr.Source, Source)
			}
		}
	}
	if !found {
		t.Errorf("submit_review must stamp %s on its loop (even for a blocking verdict) so the verify station fires", ReviewedPredicate)
	}
}

// Without a LoopID (unit/registration path) the verdict still lands but the marker is
// skipped — a missing marker never fails a recorded verdict.
func TestReviewWithoutLoopIDSkipsMarkerButRecords(t *testing.T) {
	w := &fakeWriter{}
	verdict, res := run(t, oneTaskPassing(), w) // call sets no LoopID
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictApproved {
		t.Fatalf("verdict = %q, want approved", verdict)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == ReviewedPredicate {
				t.Errorf("no LoopID → the reviewed marker must be skipped, but %s was stamped", ReviewedPredicate)
			}
		}
	}
}

// G3 at the schema surface: the input schema accepts only the task selector and
// findings — no outcome or approve/verdict field. The verdict is derived from the
// measured facts.
func TestReviewSchemaTakesNoOutcomeField(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 2 {
		t.Errorf("schema exposes %d properties, want exactly 2 (task_index, findings): %v", len(props), props)
	}
	for _, want := range []string{"task_index", "findings"} {
		if _, ok := props[want]; !ok {
			t.Errorf("schema must expose %q; has %v", want, props)
		}
	}
	for _, banned := range []string{"approve", "approved", "verdict", "pass", "passed", "outcome", "decision", "success"} {
		if _, present := props[banned]; present {
			t.Errorf("schema accepts outcome/verdict field %q — the verdict is derived from the measured facts, not supplied", banned)
		}
	}
}
