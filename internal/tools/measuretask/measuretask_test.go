package measuretask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeReader replays scripted task.spec facts, filtered by the prefix the tool asks
// for (mirroring the graph query the NATS reader performs).
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

// fakeRunner is a scripted cliexec.Runner: it returns a canned Result/err and
// records exactly what it was asked to run, so a test can prove the tool ran the
// IMMUTABLE task.spec command in the checkout root (and no model-supplied command).
type fakeRunner struct {
	res     cliexec.Result
	err     error
	gotDir  string
	gotName string
	gotArgs []string
	calls   int
}

func (r *fakeRunner) Run(_ context.Context, dir, name string, args ...string) (cliexec.Result, error) {
	r.calls++
	r.gotDir, r.gotName, r.gotArgs = dir, name, args
	return r.res, r.err
}

// fakeWriter records replace calls. It owns no immutability guard: a measurement is
// MUTABLE (re-measuring upserts the latest), unlike task.spec.
type fakeWriter struct {
	replaces [][]message.Triple
	removes  [][]string
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, rm []string) error {
	w.replaces = append(w.replaces, add)
	w.removes = append(w.removes, rm)
	return nil
}

func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

// fakeWorkspace resolves the run's checkout root (the working dir the test command
// runs in).
type fakeWorkspace struct {
	root string
	err  error
}

func (w fakeWorkspace) Root(_ context.Context, _ string) (string, error) {
	return w.root, w.err
}

func callFor(idx int) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{"task_index": idx},
	}
}

// taskSpecFact builds one projected task.spec.<i>.<field> fact (as the projector
// stamps it) so a test can seed the immutable command the tool must run.
func taskSpecFact(i int, field, obj string) message.Triple {
	return message.Triple{
		Predicate: fmt.Sprintf("task.spec.%d.%s", i, field),
		Object:    obj,
		Source:    "task-projector",
	}
}

func stampedFacts(w *fakeWriter) map[string]string {
	idx := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			idx[tr.Predicate] = tr.Object.(string)
		}
	}
	return idx
}

func exec(t *testing.T, e *Executor, idx int) agentic.ToolResult {
	t.Helper()
	res, err := e.Execute(context.Background(), callFor(idx))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

// happyExecutor wires a fully-projected task 0 whose command exits 0.
func happyExecutor() (*Executor, *fakeRunner, *fakeWriter) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 0, Stdout: "ok"}}
	writer := &fakeWriter{}
	return New(reader, runner, writer, fakeWorkspace{root: "/checkout"}, nil), runner, writer
}

// Happy path: the harness runs the projected command and stamps the derived
// measurement facts on the run entity under measurement.result.<i>, with the
// harness Source and the run-entity subject (D15).
func TestMeasureStampsDerivedResult(t *testing.T) {
	e, _, w := happyExecutor()
	res := exec(t, e, 0)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	want := map[string]string{
		"measurement.result.0.command":   "go test ./...",
		"measurement.result.0.ran":       "true",
		"measurement.result.0.exit_code": "0",
		"measurement.result.0.timed_out": "false",
		"measurement.result.0.passed":    "true",
	}
	for pred, obj := range want {
		if facts[pred] != obj {
			t.Errorf("%s = %q, want %q", pred, facts[pred], obj)
		}
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("measurement fact Source = %q, want %q (G5)", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("measurement fact subject = %q, want run entity (D15)", tr.Subject)
			}
		}
	}
}

// G3, the spec scenario: a non-zero exit records FAILURE regardless of what the
// command's stdout (or any model) claims. The outcome is derived from the real exit
// code, not from text.
func TestMeasureFailingCommandRecordsFailureRegardlessOfText(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 1, Stdout: "ALL TESTS PASSED — 42 OK"}}
	w := &fakeWriter{}
	e := New(reader, runner, w, fakeWorkspace{root: "/checkout"}, nil)

	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	if facts["measurement.result.0.passed"] != "false" {
		t.Errorf("passed = %q, want false — a non-zero exit is a failure regardless of stdout claims (G3)", facts["measurement.result.0.passed"])
	}
	if facts["measurement.result.0.exit_code"] != "1" {
		t.Errorf("exit_code = %q, want 1", facts["measurement.result.0.exit_code"])
	}
}

// The tool runs the IMMUTABLE task.spec command in the run's checkout root — the
// schema carries no command, so the model cannot substitute one. The runner is
// invoked as `sh -c "<the projected command>"`.
func TestMeasureRunsImmutableCommandInCheckout(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(2, "test_command", "make check")}}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 0}}
	e := New(reader, runner, &fakeWriter{}, fakeWorkspace{root: "/work/repo"}, nil)

	if res := exec(t, e, 2); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if runner.gotDir != "/work/repo" {
		t.Errorf("ran in %q, want the checkout root /work/repo", runner.gotDir)
	}
	if runner.gotName != "sh" || len(runner.gotArgs) != 2 || runner.gotArgs[0] != "-c" || runner.gotArgs[1] != "make check" {
		t.Errorf("ran %q %v, want sh -c \"make check\" (the projected command verbatim)", runner.gotName, runner.gotArgs)
	}
}

// A never-started command cannot false-green: the runner returns a zero-value
// Result (ExitCode 0) with a non-nil error (missing binary). The measurement must
// record ran=false / passed=false — the exit-0 of a process that never ran is not a
// pass.
func TestMeasureNeverStartedCannotFalseGreen(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "pytest")}}
	runner := &fakeRunner{res: cliexec.Result{}, err: errors.New(`exec: "pytest": executable file not found in $PATH`)}
	w := &fakeWriter{}
	e := New(reader, runner, w, fakeWorkspace{root: "/checkout"}, nil)

	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	if facts["measurement.result.0.ran"] != "false" {
		t.Errorf("ran = %q, want false — the command never started", facts["measurement.result.0.ran"])
	}
	if facts["measurement.result.0.passed"] != "false" {
		t.Errorf("passed = %q, want false — a never-started command is not a pass", facts["measurement.result.0.passed"])
	}
}

// A timeout is not a pass: the runner returns TimedOut with a non-nil error (the
// deadline killed it), so the measurement records timed_out=true / passed=false.
func TestMeasureTimeoutIsNotPass(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &fakeRunner{res: cliexec.Result{ExitCode: 1, TimedOut: true}, err: errors.New("run sh: timed out")}
	w := &fakeWriter{}
	e := New(reader, runner, w, fakeWorkspace{root: "/checkout"}, nil)

	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	if facts["measurement.result.0.timed_out"] != "true" {
		t.Errorf("timed_out = %q, want true", facts["measurement.result.0.timed_out"])
	}
	if facts["measurement.result.0.passed"] != "false" {
		t.Errorf("passed = %q, want false — a timed-out run is not a pass", facts["measurement.result.0.passed"])
	}
}

// A measurement is MUTABLE: re-measuring the same task is allowed (no immutability
// rejection, unlike task.spec) and upserts — it stamps again, latest wins.
func TestMeasureIsMutableReMeasureReplaces(t *testing.T) {
	e, runner, w := happyExecutor()
	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("first measure: %s", res.Error)
	}
	runner.res = cliexec.Result{ExitCode: 1} // the code regressed on the second run
	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("second measure must be allowed (measurements are mutable): %s", res.Error)
	}
	if len(w.replaces) != 2 {
		t.Fatalf("expected two upserts, got %d", len(w.replaces))
	}
	// The measure_task tool passes no removePredicates: the fixed per-task sub-key
	// set upserts by predicate, so no stale sub-key can survive.
	for _, rm := range w.removes {
		if len(rm) != 0 {
			t.Errorf("measure_task cleared predicates %v; the fixed sub-key set should upsert without clears", rm)
		}
	}
}

// A task that was never projected (no task.spec.<i>) is an error, not a silent
// no-op — there is no command to measure.
func TestMeasureMissingTaskSpecErrors(t *testing.T) {
	e := New(&fakeReader{}, &fakeRunner{}, &fakeWriter{}, fakeWorkspace{root: "/checkout"}, nil)
	res := exec(t, e, 3)
	if res.Error == "" {
		t.Fatal("expected an error when task.spec.3 does not exist")
	}
}

// A task.spec present but missing its test_command is an error (the projector
// guarantees it, but the tool must not run an empty command).
func TestMeasureMissingTestCommandErrors(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "goal", "add the guard")}}
	runner := &fakeRunner{}
	res := exec(t, New(reader, runner, &fakeWriter{}, fakeWorkspace{root: "/checkout"}, nil), 0)
	if res.Error == "" || !strings.Contains(res.Error, "test_command") {
		t.Fatalf("missing test_command must error, got %q", res.Error)
	}
	if runner.calls != 0 {
		t.Error("must not run anything when there is no command")
	}
}

// A negative task index is rejected before any read or run.
func TestMeasureRejectsNegativeIndex(t *testing.T) {
	runner := &fakeRunner{}
	res := exec(t, New(&fakeReader{}, runner, &fakeWriter{}, fakeWorkspace{root: "/checkout"}, nil), -1)
	if res.Error == "" {
		t.Fatal("expected an error for a negative task index")
	}
	if runner.calls != 0 {
		t.Error("a rejected index must run nothing")
	}
}

// An absent task_index argument is rejected (index 0 is valid, so absence must be
// distinguished from zero).
func TestMeasureRequiresTaskIndex(t *testing.T) {
	call := agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{},
	}
	res, err := New(&fakeReader{}, &fakeRunner{}, &fakeWriter{}, fakeWorkspace{root: "/checkout"}, nil).Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected an error when task_index is absent")
	}
}

// Schema-only registration (nil reader/writer/workspace) fails loudly if executed,
// never silently drops the measurement.
func TestMeasureFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, nil, nil, nil).Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness measurement must fail loudly")
	}
}

// A missing run entity on the call is an internal error — the tool cannot target
// the run's facts.
func TestMeasureRequiresRunEntity(t *testing.T) {
	e, _, _ := happyExecutor()
	call := callFor(0)
	call.Metadata = map[string]any{}
	res, err := e.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a call without a run entity must fail")
	}
}

// G3 at the schema surface: the input schema accepts only the task selector — no
// outcome-shaped field. (The conformance census enforces this across all tools;
// this pins the measurement tool's own schema locally.)
func TestMeasureSchemaTakesOnlyTaskSelector(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, ok := defs[0].Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object: %#v", defs[0].Parameters)
	}
	if len(props) != 1 {
		t.Errorf("schema exposes %d properties, want exactly 1 (task_index): %v", len(props), props)
	}
	if _, ok := props["task_index"]; !ok {
		t.Errorf("schema must expose task_index; has %v", props)
	}
	for _, banned := range []string{"pass", "passed", "exit_code", "success", "resolved", "outcome", "command"} {
		if _, present := props[banned]; present {
			t.Errorf("schema accepts outcome/command field %q (G3): the harness stamps outcomes and runs the immutable task.spec command", banned)
		}
	}
}
