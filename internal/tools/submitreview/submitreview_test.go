package submitreview

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
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

func call(findings ...string) agentic.ToolCall {
	args := map[string]any{}
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

// run executes the tool and returns the stamped review.verdict (or "" if none) and
// the result.
func run(t *testing.T, facts []message.Triple, w *fakeWriter, findings ...string) (string, agentic.ToolResult) {
	t.Helper()
	res, err := New(&fakeReader{facts: facts}, w, nil).Execute(context.Background(), call(findings...))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	verdict := ""
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == VerdictPredicate {
				verdict = tr.Object.(string)
			}
		}
	}
	return verdict, res
}

// oneTaskPassing is a run with one projected task and a passing measurement for it.
func oneTaskPassing() []message.Triple {
	var f []message.Triple
	f = append(f, taskSpecFact(0, "goal", "add the guard"), taskSpecFact(0, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 0, true, false, true)...)
	return f
}

// Happy path: every required task has a passing measurement and the reviewer raised
// no findings — the verdict is approved, stamped with the reviewer Source on the run
// entity.
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
		}
	}
}

// 7.4 red-first, the spec scenario: the measured outcome is a FAILURE (exit 1) —
// the reviewer cannot record an approving verdict even with no findings and however
// the change describes itself. Approval reads the stamped fact, not the prose.
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

// A required task with NO measurement blocks approval — the gate proves the required
// evidence EXISTS, not merely that nothing observed failed.
func TestReviewBlocksWhenRequiredMeasurementMissing(t *testing.T) {
	// Two tasks projected, only task 0 measured.
	var f []message.Triple
	f = append(f, taskSpecFact(0, "test_command", "go test ./..."), taskSpecFact(1, "test_command", "go test ./..."))
	f = append(f, measurementFacts(0, 0, true, false, true)...)
	verdict, res := run(t, f, &fakeWriter{})
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — task 1 has no measurement", verdict, VerdictChangesRequested)
	}
}

// A reviewer finding blocks approval even when every measurement passes — findings
// are additive constraints the reviewer may require.
func TestReviewFindingBlocksEvenWhenMeasuredPass(t *testing.T) {
	verdict, res := run(t, oneTaskPassing(), &fakeWriter{}, "error handling swallows the context error")
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — an open finding is a required change", verdict, VerdictChangesRequested)
	}
}

// Findings never weaken task.spec: the review tool writes ONLY review.verdict and
// holds no writer for task.spec, so a finding is structurally incapable of removing
// or relaxing a task.spec requirement (G5 single-writer).
func TestReviewWritesOnlyVerdictNeverTaskSpec(t *testing.T) {
	w := &fakeWriter{}
	run(t, oneTaskPassing(), w, "please also add a benchmark")
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != VerdictPredicate {
				t.Errorf("review stamped %q — it must write only %q (findings are additive, never weaken task.spec)", tr.Predicate, VerdictPredicate)
			}
			if strings.HasPrefix(tr.Predicate, "task.spec.") {
				t.Errorf("review wrote a task.spec predicate %q — findings must not touch the immutable spec", tr.Predicate)
			}
		}
	}
}

// A run with no projected tasks is an ordering error, not a verdict — you cannot
// review work that was never projected.
func TestReviewErrorsWhenNoTasksProjected(t *testing.T) {
	w := &fakeWriter{}
	_, res := run(t, measurementFacts(0, 0, true, false, true), w) // measurements but no task.spec
	if res.Error == "" {
		t.Fatal("expected an error when there are no task.spec facts to review")
	}
	if len(w.replaces) != 0 {
		t.Error("a failed review must stamp no verdict")
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

// Re-review upserts the latest verdict (replace-by-predicate): a run that regressed
// records changes_requested over a prior approved.
func TestReviewReReviewUpserts(t *testing.T) {
	w := &fakeWriter{}
	if v, _ := runW(t, oneTaskPassing(), w); v != VerdictApproved {
		t.Fatalf("first review verdict = %q, want approved", v)
	}
	// Now a finding appears on re-review.
	res, err := New(&fakeReader{facts: oneTaskPassing()}, w, nil).Execute(context.Background(), call("regression found"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("re-review error: %s", res.Error)
	}
	// The verdict predicate upserts, so both writes target review.verdict.
	if len(w.replaces) != 2 {
		t.Fatalf("expected two verdict upserts, got %d", len(w.replaces))
	}
}

// runW is run() but keeps the writer across calls (for the re-review test).
func runW(t *testing.T, facts []message.Triple, w *fakeWriter) (string, agentic.ToolResult) {
	t.Helper()
	res, err := New(&fakeReader{facts: facts}, w, nil).Execute(context.Background(), call())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	verdict := ""
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == VerdictPredicate {
				verdict = tr.Object.(string)
			}
		}
	}
	return verdict, res
}

// Schema-only registration (nil reader/writer) fails loudly if executed.
func TestReviewFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, nil).Execute(context.Background(), call())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness review must fail loudly")
	}
}

// G3 at the schema surface: the input schema accepts only findings — no outcome or
// approve/verdict field. The verdict is derived from the measured facts.
func TestReviewSchemaTakesNoOutcomeField(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 1 {
		t.Errorf("schema exposes %d properties, want exactly 1 (findings): %v", len(props), props)
	}
	if _, ok := props["findings"]; !ok {
		t.Errorf("schema must expose findings; has %v", props)
	}
	for _, banned := range []string{"approve", "approved", "verdict", "pass", "passed", "outcome", "decision", "success"} {
		if _, present := props[banned]; present {
			t.Errorf("schema accepts outcome/verdict field %q — the verdict is derived from the measured facts, not supplied", banned)
		}
	}
}
