// Package measuretask is the measure_task tool (harness-measurement, task 7.1):
// the executing half of semdev's G3 measurement gate. It runs a projected task's
// declared test_command and stamps the OS-level outcome as a measurement fact —
// so the reviewer (Quinn) and the loop gate on what the command actually did, not
// on what a model claims about it.
//
// The G3 discipline is structural, not advisory:
//
//   - The schema takes only the task index. The command is NOT a parameter — it is
//     read from the IMMUTABLE task.spec.<i>.test_command on the run entity (the
//     projector froze it), so the model cannot substitute a friendlier command.
//   - The outcome is NOT a parameter either. measurement.Measure derives pass/fail
//     from the real exit code, timeout, and completion; the tool stamps that. A
//     non-zero exit records failure regardless of the command's stdout or any model
//     text (the spec scenario), and a command that never started (a missing binary,
//     zero-value Result with a runner error) records ran=false / passed=false — it
//     cannot false-green on the exit-0 a never-run process reports.
//
// The fact is the OWNED per-task package measurement.result.<i>.* on the run entity
// (writer measurement-harness, G5). It is MUTABLE by design — re-measuring a task
// upserts the latest outcome (the loop re-runs a task across iterations) — which is
// the deliberate contrast with the projector's immutable task.spec: a measurement
// is a task's current result, not a definition. Attempt history is task.attempt's
// job (a separate writer), not this fact's.
//
// The tool fires no lifecycle transition (G2): it records evidence; a rule reading
// the review verdict advances the run. WHERE the command runs — the run's checked-
// out workspace — is runtime state the forge-io checkout / clean-room Runner own; a
// narrow Workspace seam resolves it, nil (schema-only) at M0 like write_change's
// resolver, and Execute fails loudly if any dependency is missing so a measurement
// is never silently dropped.
package measuretask

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the dev loop's measurement handler.
const ToolName = "measure_task"

// Source is stamped on every measurement.result triple. It MUST equal the single
// writer declared for measurement.result.* in internal/vocab (G5) — a conformance
// pin cross-checks it.
const Source = "measurement-harness"

// measureTimeout bounds one test_command run. A hung suite is killed by the
// deadline and surfaces (via cliexec) as a non-completion, which measures as
// not-passed rather than blocking the loop forever.
const measureTimeout = 10 * time.Minute

// Workspace resolves the on-disk checkout ROOT of a run's target-repo workspace —
// the directory the task's test_command runs in. It is the seam over the checkout
// (the same run-scoped state write_change's resolver locates the change dir
// within); its production implementation lands with forge-io / the clean-room
// Runner. measure_task depends only on this narrow surface so it holds no
// workspace state of its own (B1).
type Workspace interface {
	Root(ctx context.Context, runEntityID string) (string, error)
}

// Executor reads a task's frozen test_command, runs it, and stamps the measurement.
type Executor struct {
	reader    changefacts.Reader
	runner    cliexec.Runner
	writer    agentictools.OwnedFactWriter
	workspace Workspace
	logger    *slog.Logger
}

// New builds the measure_task executor. reader/runner/writer/workspace may be nil
// for schema-only registration (the tool censuses inspect ListTools without a live
// NATS client or a checked-out workspace); Execute fails loudly if any is nil.
func New(reader changefacts.Reader, runner cliexec.Runner, writer agentictools.OwnedFactWriter, workspace Workspace, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, runner: runner, writer: writer, workspace: workspace, logger: logger}
}

type payload struct {
	// TaskIndex is a pointer so an ABSENT argument is distinguishable from index 0
	// (a valid task). Absent → error; a negative index → error.
	TaskIndex *int `json:"task_index"`
}

// Execute reads task.spec.<task_index>.test_command off the run entity, runs it in
// the run's workspace, and stamps the derived measurement.result.<task_index> — or
// fails toward the human if the task was never projected. It stamps no outcome from
// the caller (G3) and fires no transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.runner == nil || e.writer == nil || e.workspace == nil {
		return errResult(call, agentic.ToolErrorInternal, "measure_task: harness not fully wired (reader/runner/writer/workspace)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "measure_task: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "measure_task: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "measure_task: decode arguments: %v", err)
	}
	if p.TaskIndex == nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "measure_task: task_index is required")
	}
	idx := *p.TaskIndex
	if idx < 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "measure_task: task_index must be non-negative, got %d", idx)
	}

	command, err := e.readTestCommand(ctx, runEntityID, idx)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "measure_task: %v", err)
	}
	if command == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs,
			"measure_task: task.spec.%d has no test_command on %s — was the change projected (project_tasks)?", idx, runEntityID)
	}

	root, err := e.workspace.Root(ctx, runEntityID)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "measure_task: resolve workspace root: %v", err)
	}

	// Run the frozen command through a shell so an authored command line (`go test
	// ./...`, `make check`) executes as written; the exit code the shell propagates
	// is the harness verdict. A non-completion (missing binary, timeout) comes back
	// as a runner error, which Measure folds into ran=false / passed=false.
	runCtx, cancel := context.WithTimeout(ctx, measureTimeout)
	defer cancel()
	res, runErr := e.runner.Run(runCtx, root, "sh", "-c", command)

	result := measurement.Measure(strconv.Itoa(idx), command, res, runErr)
	out := measurementTriples(runEntityID, idx, result, time.Now().UTC())
	if err := e.writer.ReplaceTriples(ctx, runEntityID, out, nil); err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "measure_task: stamp measurement.result.%d on %s: %v", idx, runEntityID, err)
	}

	e.logger.Info("measure_task recorded measurement",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", idx),
		slog.String("command", command),
		slog.Bool("ran", result.Ran),
		slog.Int("exit_code", result.ExitCode),
		slog.Bool("passed", result.Passed))

	summary, _ := json.Marshal(map[string]any{
		"task_index": idx,
		"command":    command,
		"ran":        result.Ran,
		"exit_code":  result.ExitCode,
		"timed_out":  result.TimedOut,
		"passed":     result.Passed,
		"stdout":     result.Stdout,
		"stderr":     result.Stderr,
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary)}, nil
}

// readTestCommand reads the frozen task.spec.<idx>.test_command off the run entity.
// It returns "" (no error) when the task or its command is absent — the caller
// distinguishes "not projected" from a read fault.
func (e *Executor) readTestCommand(ctx context.Context, runEntityID string, idx int) (string, error) {
	prefix := devtask.TaskSpecKeyPrefix(idx)
	triples, err := e.reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return "", fmt.Errorf("read %s on %s: %w", prefix, runEntityID, err)
	}
	want := prefix + devtask.FactTestCommand
	for _, tr := range triples {
		if tr.Predicate != want {
			continue
		}
		s, ok := tr.Object.(string)
		if !ok {
			return "", fmt.Errorf("%s has non-string object %T", want, tr.Object)
		}
		return s, nil
	}
	return "", nil
}

// measurementTriples projects one task's measurement into the owned per-task fact
// package on the run entity: measurement.result.<idx>.<field>. The fixed sub-key
// set upserts by predicate (no removePredicates needed), so re-measuring the task
// replaces its prior facts and leaves other tasks untouched.
func measurementTriples(runEntityID string, idx int, r measurement.Result, now time.Time) []message.Triple {
	prefix := measurement.ResultPrefix + strconv.Itoa(idx) + "."
	mk := func(field, obj string) message.Triple {
		return message.Triple{
			Subject:    runEntityID,
			Predicate:  prefix + field,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		}
	}
	return []message.Triple{
		mk(measurement.FactCommand, r.Command),
		mk(measurement.FactRan, strconv.FormatBool(r.Ran)),
		mk(measurement.FactExitCode, strconv.Itoa(r.ExitCode)),
		mk(measurement.FactTimedOut, strconv.FormatBool(r.TimedOut)),
		mk(measurement.FactPassed, strconv.FormatBool(r.Passed)),
	}
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
