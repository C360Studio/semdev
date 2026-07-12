// Package checkfloors is the check_floors tool (dev-from-task, tasks 6.5/6.6): the
// floor-tools wrapper. It runs semdev's deterministic floor library over a task's
// current dev-loop attempt and stamps each finding as a floor.finding fact, so the
// loop can route on harness-computed structural facts — did the attempt author a
// test, is that test vacuous, did it ship a stub, does the source parse, did it
// "test" only a mock of the code under test — rather than the persona's claim about
// its own work.
//
// The floors are the pure core (internal/floors); this tool only WRAPS them with
// fact-stamping, which is what keeps the checks offline-testable and G5-clean (the
// floors package writes no facts). The verdict is COMPUTED by the deterministic
// floors, never supplied (G3 — the schema takes only the task index; floor.finding
// carries a harness-derived passed, not a model outcome). It stamps the findings and
// returns whether any floor rejected; it fires no lifecycle transition (G2) — the
// loop-gate that blocks advance-to-review on a rejecting floor.finding is a rule
// (task 6.6, wired with the bounded dev loop). Single G5 writer of floor.finding.*
// (floor-tools).
package checkfloors

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the dev loop's floor-check handler.
const ToolName = "check_floors"

// Source is stamped on every floor.finding triple. It MUST equal the writer declared
// for floor.finding.* in internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "floor-tools"

// Attempts resolves a task's current dev-loop attempt — the files it authored (with
// contents) and the task's declared target files — from the run's checkout, into the
// pure floors.Attempt the deterministic checks consume. It is the seam over the
// checkout (which files this iteration authored is git/workspace state the forge-io
// checkout owns); its production implementation lands with the bounded dev loop. nil
// at M0 (schema-only, like write_change's resolver); Execute fails loudly if absent.
type Attempts interface {
	Resolve(ctx context.Context, runEntityID string, taskIndex int) (floors.Attempt, error)
}

// Executor runs the floors over a task's attempt and stamps the findings.
type Executor struct {
	attempts Attempts
	writer   agentictools.OwnedFactWriter
	logger   *slog.Logger
}

// New builds the check_floors executor. attempts/writer may be nil for schema-only
// registration (the censuses inspect ListTools without a live checkout or NATS
// client); Execute fails loudly if either is nil.
func New(attempts Attempts, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{attempts: attempts, writer: writer, logger: logger}
}

type payload struct {
	// TaskIndex is a pointer so an ABSENT argument is distinguishable from index 0.
	TaskIndex *int `json:"task_index"`
}

// Execute resolves the task's attempt, runs every floor, and stamps the findings as
// floor.finding.<task_index>.<floor> facts on the run entity. The verdicts are the
// deterministic floors' own; the tool records them and reports whether any rejected.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.attempts == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "check_floors: harness not fully wired (attempts/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "check_floors: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: decode arguments: %v", err)
	}
	if p.TaskIndex == nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: task_index is required")
	}
	idx := *p.TaskIndex
	if idx < 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: task_index must be non-negative, got %d", idx)
	}

	attempt, err := e.attempts.Resolve(ctx, runEntityID, idx)
	if err != nil {
		// Fail closed: a resolve/check failure must NOT leave a prior attempt's PASS
		// readable as if it were the current attempt's — that is a direct route to a
		// stale false-green once the gate reads these facts. Clear this task's findings
		// so no current verdict is readable, then surface the resolve fault (retryable;
		// the loop re-runs, and a persistent failure parks toward the human). The
		// attempt-id binding is the belt to this clear's suspenders.
		if cerr := e.clearFindings(ctx, runEntityID, idx); cerr != nil {
			e.logger.Warn("check_floors: could not clear stale findings after a resolve fault",
				slog.Int("task_index", idx), slog.Any("clear_error", cerr))
		}
		return errResult(call, agentic.ToolErrorInternal, "check_floors: resolve attempt for task %d: %v", idx, err)
	}

	findings := floors.CheckAll(attempt)
	rejected := floors.AnyRejected(findings)
	out := findingTriples(runEntityID, idx, floors.AttemptID(attempt), rejected, findings, time.Now().UTC())
	if err := e.writer.ReplaceTriples(ctx, runEntityID, out, nil); err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "check_floors: stamp floor.finding for task %d on %s: %v", idx, runEntityID, err)
	}

	e.logger.Info("check_floors evaluated attempt",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", idx),
		slog.Bool("rejected", rejected))

	summary, _ := json.Marshal(map[string]any{
		"task_index": idx,
		"rejected":   rejected,
		"findings":   findings,
	})
	// StopLoop: the floors-trigger rule (dev-from-task/06) forces a single-turn floors
	// loop; ending the turn here keeps it one model call (mirrors measure_task /
	// project_tasks). A rejecting verdict is DATA, not a tool error — it ends the turn
	// as a success too; the dev-loop gate reads the stamped floor.finding.<i>.rejected,
	// not this StopLoop, to decide advance/retry.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// findingTriples projects the floor findings into the owned per-task package on the
// run entity: floor.finding.<idx>.{attempt,rejected} plus floor.finding.<idx>.<floor>.
// {passed,detail}. attempt binds the whole set to the source it evaluated; rejected is
// the aggregate verdict the dev-loop gate reads (true iff any floor rejected), so the
// gate stays a single literal read rather than re-deriving from the per-floor keys. The
// floor set is fixed (CheckAll always returns the same floors), so the sub-keys upsert
// by predicate and re-evaluating the task replaces its prior findings without leaving a
// stale sub-key.
func findingTriples(runEntityID string, idx int, attemptID string, rejected bool, findings []floors.Finding, now time.Time) []message.Triple {
	base := floors.FindingPrefix + strconv.Itoa(idx) + "."
	out := make([]message.Triple, 0, len(findings)*2+2)
	mk := func(pred, obj string) {
		out = append(out, message.Triple{
			Subject:    runEntityID,
			Predicate:  pred,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		})
	}
	mk(base+floors.FactAttempt, attemptID)
	mk(base+floors.FactRejected, strconv.FormatBool(rejected))
	for _, f := range findings {
		p := base + f.Floor + "."
		mk(p+floors.FactPassed, strconv.FormatBool(f.Passed))
		mk(p+floors.FactDetail, f.Detail)
	}
	return out
}

// clearFindings removes this task's entire floor.finding.<idx>.* package (the "clear
// my prefix" pattern), so a stale earlier attempt's pass is not left readable when
// the current attempt cannot be evaluated. A no-op when nothing is stamped yet.
func (e *Executor) clearFindings(ctx context.Context, runEntityID string, idx int) error {
	prefix := floors.FindingPrefix + strconv.Itoa(idx) + "."
	preds, err := e.writer.ReadOwnedPredicates(ctx, runEntityID, prefix)
	if err != nil {
		return err
	}
	if len(preds) == 0 {
		return nil
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, nil, preds)
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
