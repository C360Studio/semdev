package measuretask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"
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

// fakeSandboxes resolves a scripted warm sandbox — a cleanroom.MockRunner + a
// provisioned Sandbox — so a test can prove the tool Execs the IMMUTABLE task.spec
// command IN the container (and folds the exec's exit/transport through Measure),
// with no docker. A resolve err stands in for "no sandbox was provisioned" (park).
type fakeSandboxes struct {
	runner cleanroom.Runner
	sb     cleanroom.Sandbox
	err    error
}

func (s *fakeSandboxes) Resolve(_ context.Context, _ string) (cleanroom.Runner, cleanroom.Sandbox, error) {
	if s.err != nil {
		return nil, cleanroom.Sandbox{}, s.err
	}
	return s.runner, s.sb, nil
}

// blockingRunner is a cleanroom.Runner whose Exec blocks until the ctx deadline
// fires — standing in for a hung/slow command the measure timeout kills. It returns
// the ctx error (the seam's transport contract) so Measure records a non-completion.
type blockingRunner struct{}

func (blockingRunner) Up(_ context.Context, _ string, _ []string) (cleanroom.Sandbox, error) {
	return cleanroom.Sandbox{}, nil
}

func (blockingRunner) Exec(ctx context.Context, _ cleanroom.Sandbox, _ []string) (cleanroom.Result, error) {
	<-ctx.Done()
	return cleanroom.Result{ExitCode: -1}, ctx.Err()
}

func (blockingRunner) Down(_ context.Context, _ cleanroom.Sandbox) error { return nil }

// warmSandboxOf wraps a mock runner as a provisioned warm sandbox the tool Execs into.
func warmSandboxOf(runner *cleanroom.MockRunner) *fakeSandboxes {
	return &fakeSandboxes{runner: runner, sb: cleanroom.Sandbox{WorkDir: "/work", Handle: "warm-container"}}
}

// execWith builds an executor over a scripted reader + warm sandbox + writer.
func execWith(reader *fakeReader, runner *cleanroom.MockRunner, w *fakeWriter) *Executor {
	return New(reader, warmSandboxOf(runner), w, types.PlatformMeta{}, nil)
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

// happyExecutor wires a fully-projected task 0 whose in-container command exits 0.
func happyExecutor() (*Executor, *cleanroom.MockRunner, *fakeWriter) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 0, Stdout: "ok"}}}}
	writer := &fakeWriter{}
	return execWith(reader, runner, writer), runner, writer
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

// The chaining marker: measure_task stamps dev.measure_done on ITS OWN loop entity
// (not the run) so the floors-trigger (dev-from-task/06) can fire on the measure loop.
// It is stamped even for a FAILING measurement — a failing attempt must still chain to
// floors→gate so the gate can decide retry; only a measure ERROR stalls the chain.
func TestMeasureStampsMeasureDoneMarkerOnLoopEvenWhenFailing(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 1}}}} // a FAILING measurement
	w := &fakeWriter{}
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	e := New(reader, warmSandboxOf(runner), w, platform, nil)

	call := callFor(0)
	call.LoopID = "measure-loop-abc"
	res, err := e.Execute(context.Background(), call)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if facts := stampedFacts(w); facts["measurement.result.0.passed"] != "false" {
		t.Fatalf("precondition: this measurement must be failing, got passed=%q", facts["measurement.result.0.passed"])
	}

	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, call.LoopID)
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	found := false
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != MeasureDonePredicate {
				continue
			}
			found = true
			if tr.Subject != loopEntityID {
				t.Errorf("%s stamped on %q, want the measure LOOP entity %q (not the run)", MeasureDonePredicate, tr.Subject, loopEntityID)
			}
			if tr.Source != Source {
				t.Errorf("%s Source = %q, want %q (G5)", MeasureDonePredicate, tr.Source, Source)
			}
		}
	}
	if !found {
		t.Errorf("measure_task must stamp %s on its loop (even for a failing measurement) so the floors-trigger fires", MeasureDonePredicate)
	}
}

// No loop id on the call (a unit-test / degenerate case) skips the marker with a warn
// rather than failing a recorded measurement — the measurement still lands.
func TestMeasureWithoutLoopIDSkipsMarkerButRecords(t *testing.T) {
	e, _, w := happyExecutor() // callFor sets no LoopID
	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	if facts["measurement.result.0.passed"] != "true" {
		t.Error("the measurement must still be recorded when no loop id is present")
	}
	if _, ok := facts[MeasureDonePredicate]; ok {
		t.Error("no loop id → the marker must be skipped, not stamped on the run")
	}
}

// G3, the spec scenario: a non-zero exit records FAILURE regardless of what the
// command's stdout (or any model) claims. The outcome is derived from the real exit
// code, not from text.
func TestMeasureFailingCommandRecordsFailureRegardlessOfText(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 1, Stdout: "ALL TESTS PASSED — 42 OK"}}}}
	w := &fakeWriter{}
	e := execWith(reader, runner, w)

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

// The tool Execs the IMMUTABLE task.spec command IN the sandbox container — the
// schema carries no command, so the model cannot substitute one. The container is
// asked to run `sh -c "<the projected command>"` (the sandbox itself supplies the
// /work dir and cache env, so the tool passes no directory).
func TestMeasureRunsImmutableCommandInSandbox(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(2, "test_command", "make check")}}
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 0}}}}
	e := execWith(reader, runner, &fakeWriter{})

	if res := exec(t, e, 2); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if len(runner.ExecArgv) != 1 {
		t.Fatalf("expected exactly one in-container exec, got %d", len(runner.ExecArgv))
	}
	got := runner.ExecArgv[0]
	if len(got) != 3 || got[0] != "sh" || got[1] != "-c" || got[2] != "make check" {
		t.Errorf("Exec'd %v, want [sh -c \"make check\"] (the projected command verbatim, in-container)", got)
	}
}

// A command the sandbox could NOT run cannot false-green: the container exec returns
// a transport fault (a dead/absent container, a cancelled exec). The measurement must
// record ran=false / passed=false — the seam's exit-vs-transport contract keeps a
// broken sandbox from being read as a passing verdict (SB5).
func TestMeasureUnrunnableCommandCannotFalseGreen(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Err: errors.New("cleanroom: docker could not exec sh: docker unavailable")}}}
	w := &fakeWriter{}
	e := execWith(reader, runner, w)

	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	if facts["measurement.result.0.ran"] != "false" {
		t.Errorf("ran = %q, want false — the sandbox could not run the command (transport fault)", facts["measurement.result.0.ran"])
	}
	if facts["measurement.result.0.passed"] != "false" {
		t.Errorf("passed = %q, want false — a command the sandbox could not run is not a pass", facts["measurement.result.0.passed"])
	}
}

// A timeout is not a pass, AND it is recorded as a timeout (not confused with a
// transport fault): the measure deadline cancels the in-container exec, so ran=false /
// passed=false AND timed_out=true — the tool derives timed_out from the exec ctx's
// DeadlineExceeded so a hung suite stays distinguishable in the graph facts from a
// missing-binary / dead-container fault (both of which are ran=false otherwise).
func TestMeasureTimeoutIsRecordedAndNotPass(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	w := &fakeWriter{}
	// A caller ctx with a short deadline propagates as the effective exec deadline
	// (the earlier of it and measureTimeout), so the blocking runner is killed by
	// DeadlineExceeded — exactly what a real hung suite does at measureTimeout.
	e := New(reader, &fakeSandboxes{runner: blockingRunner{}, sb: cleanroom.Sandbox{WorkDir: "/work", Handle: "warm"}}, w, types.PlatformMeta{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	res, err := e.Execute(ctx, callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("a timeout is a recorded measurement, not a tool error: %s", res.Error)
	}
	facts := stampedFacts(w)
	if facts["measurement.result.0.timed_out"] != "true" {
		t.Errorf("timed_out = %q, want true — a killed-by-deadline exec must record as a timeout", facts["measurement.result.0.timed_out"])
	}
	if facts["measurement.result.0.ran"] != "false" {
		t.Errorf("ran = %q, want false — a timed-out exec did not complete", facts["measurement.result.0.ran"])
	}
	if facts["measurement.result.0.passed"] != "false" {
		t.Errorf("passed = %q, want false — a timed-out run is not a pass", facts["measurement.result.0.passed"])
	}
}

// A measurement is MUTABLE: re-measuring the same task is allowed (no immutability
// rejection, unlike task.spec) and upserts — it stamps again, latest wins.
func TestMeasureIsMutableReMeasureReplaces(t *testing.T) {
	// The code regressed between runs: exec 1 passes, exec 2 exits non-zero.
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0, Stdout: "ok"}},
		{Result: cleanroom.Result{ExitCode: 1}},
	}}
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	w := &fakeWriter{}
	e := execWith(reader, runner, w)
	if res := exec(t, e, 0); res.Error != "" {
		t.Fatalf("first measure: %s", res.Error)
	}
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
	e := execWith(&fakeReader{}, &cleanroom.MockRunner{}, &fakeWriter{})
	res := exec(t, e, 3)
	if res.Error == "" {
		t.Fatal("expected an error when task.spec.3 does not exist")
	}
}

// A task.spec present but missing its test_command is an error (the projector
// guarantees it, but the tool must not run an empty command).
func TestMeasureMissingTestCommandErrors(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "goal", "add the guard")}}
	runner := &cleanroom.MockRunner{}
	res := exec(t, execWith(reader, runner, &fakeWriter{}), 0)
	if res.Error == "" || !strings.Contains(res.Error, "test_command") {
		t.Fatalf("missing test_command must error, got %q", res.Error)
	}
	if runner.ExecCalls != 0 {
		t.Error("must not run anything when there is no command")
	}
}

// A negative task index is rejected before any read or run.
func TestMeasureRejectsNegativeIndex(t *testing.T) {
	runner := &cleanroom.MockRunner{}
	res := exec(t, execWith(&fakeReader{}, runner, &fakeWriter{}), -1)
	if res.Error == "" {
		t.Fatal("expected an error for a negative task index")
	}
	if runner.ExecCalls != 0 {
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
	res, err := execWith(&fakeReader{}, &cleanroom.MockRunner{}, &fakeWriter{}).Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected an error when task_index is absent")
	}
}

// Schema-only registration (nil reader/sandboxes/writer) fails loudly if executed,
// never silently drops the measurement.
func TestMeasureFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, nil, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness measurement must fail loudly")
	}
}

// No warm sandbox provisioned for the run → Resolve fails closed → measure_task
// errors (park toward the human), never a silent host exec over an unproven
// environment (SB5).
func TestMeasureFailsClosedWithoutSandbox(t *testing.T) {
	reader := &fakeReader{facts: []message.Triple{taskSpecFact(0, "test_command", "go test ./...")}}
	w := &fakeWriter{}
	e := New(reader, &fakeSandboxes{err: errors.New("runspace: no warm sandbox for run")}, w, types.PlatformMeta{}, nil)

	res := exec(t, e, 0)
	if res.Error == "" || !strings.Contains(res.Error, "sandbox") {
		t.Fatalf("an unprovisioned sandbox must fail closed with a sandbox error, got %q", res.Error)
	}
	if len(w.replaces) != 0 {
		t.Error("a run with no sandbox must stamp NO measurement (never a false result over an absent environment)")
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

// Anti-drift round-trip: what measure_task STAMPS (measurementTriples) must read
// back through the review gate's reconstruction (measurement.ResultsFromFacts) as
// the same measurement — the write and read sides of measurement.result.* are
// inverse, so the reviewer sees exactly what the harness recorded.
func TestMeasurementTriplesRoundTripThroughReconstruction(t *testing.T) {
	// A failing run: the encoding a false measurement must survive intact.
	r := measurement.Measure("0", "go test ./...", cliexec.Result{ExitCode: 1, Stdout: "boom"}, nil)
	triples := measurementTriples(runEntity, 0, r, time.Unix(0, 0).UTC())

	got, err := measurement.ResultsFromFacts(triples)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 reconstructed result, got %d", len(got))
	}
	g := got[0]
	// Only the stamped fields survive the fact round-trip (stdout/stderr/run_error
	// are LLM-facing, not stamped) — compare those.
	if g.TaskID != "0" || g.Command != r.Command || g.Ran != r.Ran || g.ExitCode != r.ExitCode || g.TimedOut != r.TimedOut || g.Passed != r.Passed {
		t.Errorf("round-trip drift: stamped %+v, read back %+v", r, g)
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
