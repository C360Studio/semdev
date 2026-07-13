// Package checkgate is the check_gate tool (dev-from-task, task 7.3 / group 7D): the
// dev loop's budget-and-route gate. After a task's attempt has been measured
// (measure_task) and structurally checked (check_floors), the gate reads the recorded
// evidence — the in-container measurement outcome, the aggregate floor verdict, and
// how many attempts the task has already spent against its iteration budget — and
// derives the route: ADVANCE the clean attempt, RETRY while the budget has room, or
// ESCALATE to the human when the budget is spent or the evidence is missing.
//
// Why a TOOL and not a rule (the architect's 7D ruling):
//
//   - A rule fires on a single entity; the gate needs facts from the RUN entity
//     (measurement/floors/budget) while chaining off a LOOP terminal — a loop-scoped
//     rule cannot read run facts.
//   - The budget test counts DISTINCT OBJECTS of an appended multi-valued predicate
//     (task.attempt.<i>) — not something a rule's length_* operator expresses, and the
//     at-least-once append means raw cardinality over-counts.
//   - A scalar field-to-field compare (attempt count vs budget) in a rule condition
//     floods the semstreams #519 WARN.
//
// All three force the comparison into Go. The decision is still HARNESS-DERIVED (G3):
// the schema takes only the task index, and every input is a fact the harness stamped
// (the model supplied none of them). The tool fires no lifecycle transition (G2) — it
// records the decision as evidence and stamps a loop marker; three router rules read
// that marker and act (advance → clean-room verify, retry → re-dispatch the developer,
// escalate → park). It fails CLOSED: a missing judgment fact escalates toward the
// human rather than retrying blind (Decide). Single G5 writer of dev.gate.* and
// dev.gate_decision (gate-tools).
package checkgate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/types"
)

// ToolName is the registered tool name and the dev loop's gate handler.
const ToolName = "check_gate"

// Source is stamped on every dev.gate.* triple AND the dev.gate_decision loop marker.
// It MUST equal the single writer declared for dev.gate.* and dev.gate_decision in
// internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "gate-tools"

// GatePrefix is the owned namespace the gate's per-task decision evidence lives under
// on the run entity: dev.gate.<i>.<field>. The router rules do NOT read this (they
// fire on the loop marker below); it is the honest evidence the human who gets parked,
// and the downstream verify/PR steps, read (G7).
const GatePrefix = "dev.gate."

// Fact-key suffixes for one task's gate evidence under dev.gate.<i>.<suffix>.
const (
	FactDecision = "decision" // the route: advance | retry | escalate
	FactReason   = "reason"   // the harness's plain-language justification (G7)
)

// GateDecisionMarker is the chaining marker check_gate stamps on ITS OWN loop entity
// (value = the decision string) once it has derived a route — the slug-independent
// signal the three router rules (dev-from-task/08a/b/c) fire on, each matching a single
// literal decision value (advance/retry/escalate). It rides the gate loop (which
// carries agent.run) so the routers' run_scope=inherit binds to the same run. Mirrors
// measure_task's dev.measure_done → the floors station.
const GateDecisionMarker = "dev.gate_decision"

// Executor reads a task's recorded gate facts, derives the route, and records it.
type Executor struct {
	reader   changefacts.Reader
	writer   agentictools.OwnedFactWriter
	platform types.PlatformMeta // builds the gate loop's entity id for the chaining marker
	logger   *slog.Logger
}

// New builds the check_gate executor. reader/writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if either is nil. platform builds the gate loop's entity id
// for the dev.gate_decision chaining marker.
func New(reader changefacts.Reader, writer agentictools.OwnedFactWriter, platform types.PlatformMeta, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, writer: writer, platform: platform, logger: logger}
}

type payload struct {
	// TaskIndex is a pointer so an ABSENT argument is distinguishable from index 0.
	TaskIndex *int `json:"task_index"`
}

// Execute reads task <i>'s measurement.result.<i>.passed, floor.finding.<i>.rejected,
// task.spec.<i>.budget, and the distinct count of task.attempt.<i> off the run entity,
// derives the route (fail-closed on any missing judgment fact), stamps the decision as
// dev.gate.<i>.* evidence on the run, and stamps the dev.gate_decision loop marker the
// router rules act on. It stamps no outcome from the caller (G3) and fires no
// transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "check_gate: harness not fully wired (reader/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "check_gate: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_gate: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_gate: decode arguments: %v", err)
	}
	if p.TaskIndex == nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_gate: task_index is required")
	}
	idx := *p.TaskIndex
	if idx < 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_gate: task_index must be non-negative, got %d", idx)
	}

	// Read the four evidence facts off the run. A READ error (transport/graph fault)
	// is retryable — return a tool error WITHOUT a decision so the forced gate loop
	// re-runs (a persistent fault trips MaxIterations, not a false green). A read that
	// SUCCEEDS but finds the fact absent/unparseable is a definite absence Decide folds
	// into a fail-closed escalate below (never invented as a green).
	in, err := e.readInputs(ctx, runEntityID, idx)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "check_gate: read gate facts for task %d on %s: %v", idx, runEntityID, err)
	}

	decision, reason := Decide(in)

	out := gateTriples(runEntityID, idx, decision, reason, time.Now().UTC())
	if err := e.writer.ReplaceTriples(ctx, runEntityID, out, nil); err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "check_gate: stamp dev.gate.%d on %s: %v", idx, runEntityID, err)
	}

	e.logger.Info("check_gate derived a route",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", idx),
		slog.String("decision", string(decision)),
		slog.String("reason", reason))

	// Stamp the chaining marker on THIS loop entity (value = the decision) so the
	// router rules (08a/b/c) can fire on this gate loop and inherit the run. The
	// decision evidence (the substance) is written FIRST; the marker (the routing
	// signal) follows, so a marker-write failure surfaces only AFTER the decision is
	// durably recorded (measure_task's discipline). Failure posture: a marker error
	// returns errResult WITHOUT StopLoop, so the forced loop re-runs check_gate (which
	// re-derives the same decision and re-stamps idempotently) until it lands or
	// MaxIterations trips — never a silent green. LoopID is always present at runtime;
	// a missing one (unit-test-only) skips the marker with a loud warn.
	if call.LoopID == "" {
		e.logger.Warn("check_gate: no loop_id on the tool call — skipping the gate-decision marker; no router rule will fire",
			slog.String("run_entity_id", runEntityID), slog.Int("task_index", idx))
	} else {
		loopEntityID, lerr := agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if lerr != nil {
			return errResult(call, agentic.ToolErrorInternal, "check_gate: construct gate loop entity id: %v", lerr)
		}
		marker := []message.Triple{{
			Subject:    loopEntityID,
			Predicate:  GateDecisionMarker,
			Object:     string(decision),
			Source:     Source,
			Timestamp:  time.Now().UTC(),
			Confidence: 1.0,
		}}
		if merr := e.writer.ReplaceTriples(ctx, loopEntityID, marker, []string{GateDecisionMarker}); merr != nil {
			return errResult(call, changefacts.ReadErrorKind(merr), "check_gate: stamp %s on %s: %v", GateDecisionMarker, loopEntityID, merr)
		}
	}

	summary, _ := json.Marshal(map[string]any{
		"task_index":    idx,
		"decision":      string(decision),
		"reason":        reason,
		"attempt_count": in.AttemptCount,
	})
	// StopLoop: the gate-trigger rule (dev-from-task/07) forces a single-turn gate
	// loop; ending the turn here keeps it one model call (mirrors measure_task /
	// check_floors). A retry/escalate decision is DATA, not a tool error — it ends the
	// turn as a success too; the router rules read the stamped dev.gate_decision marker,
	// not this StopLoop, to route.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// readInputs reads the four gate-evidence facts off the run entity. A missing or
// unparseable scalar leaves its pointer nil (Decide fails it closed); a read error is
// returned so the caller can distinguish a transport fault (retryable) from a definite
// absence. The attempt count is the number of DISTINCT objects under task.attempt.<i>.
func (e *Executor) readInputs(ctx context.Context, runEntityID string, idx int) (Inputs, error) {
	var in Inputs

	passedPred := measurement.ResultPrefix + strconv.Itoa(idx) + "." + measurement.FactPassed
	b, err := e.readBool(ctx, runEntityID, measurement.ResultPrefix+strconv.Itoa(idx)+".", passedPred)
	if err != nil {
		return in, err
	}
	in.Passed = b

	rejectedPred := floors.FindingPrefix + strconv.Itoa(idx) + "." + floors.FactRejected
	rejected, err := e.readBool(ctx, runEntityID, floors.FindingPrefix+strconv.Itoa(idx)+".", rejectedPred)
	if err != nil {
		return in, err
	}
	in.Rejected = rejected

	budgetPred := devtask.TaskSpecKeyPrefix(idx) + devtask.FactBudget
	n, err := e.readInt(ctx, runEntityID, devtask.TaskSpecKeyPrefix(idx), budgetPred)
	if err != nil {
		return in, err
	}
	in.Budget = n

	// task.attempt.<i> is a BARE per-task predicate (not a sub-key package), so the read
	// scopes to the whole task.attempt. namespace and filters to the exact predicate — an
	// asymmetry with the scalar reads above (which use a per-index trailing-dot prefix)
	// that the exact-match filter makes correct at any task count.
	count, err := e.countDistinct(ctx, runEntityID, devtask.TaskAttemptPrefix, devtask.TaskAttemptKey(idx))
	if err != nil {
		return in, err
	}
	in.AttemptCount = count

	return in, nil
}

// readBool reads a single boolean fact (exact predicate) under prefix. A found-but-
// unparseable value returns (nil, nil) — a definite absence Decide fails closed, not a
// read error to retry. A read fault propagates.
func (e *Executor) readBool(ctx context.Context, runEntityID, prefix, pred string) (*bool, error) {
	obj, found, err := e.readOne(ctx, runEntityID, prefix, pred)
	if err != nil || !found {
		return nil, err
	}
	b, perr := strconv.ParseBool(obj)
	if perr != nil {
		return nil, nil
	}
	return &b, nil
}

// readInt reads a single integer fact (exact predicate) under prefix, same contract
// as readBool.
func (e *Executor) readInt(ctx context.Context, runEntityID, prefix, pred string) (*int, error) {
	obj, found, err := e.readOne(ctx, runEntityID, prefix, pred)
	if err != nil || !found {
		return nil, err
	}
	n, perr := strconv.Atoi(obj)
	if perr != nil {
		return nil, nil
	}
	return &n, nil
}

// readOne reads the run's facts under prefix and returns the string object of the
// triple whose predicate EXACTLY equals pred (prefix scopes the query; the exact match
// guards against a sibling sub-key or a numeric-prefix collision like .0 vs .01).
func (e *Executor) readOne(ctx context.Context, runEntityID, prefix, pred string) (string, bool, error) {
	triples, err := e.reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return "", false, fmt.Errorf("read %s on %s: %w", prefix, runEntityID, err)
	}
	for _, tr := range triples {
		if tr.Predicate != pred {
			continue
		}
		return objectString(tr.Object), true, nil
	}
	return "", false, nil
}

// countDistinct reads the run's facts under prefix and counts the DISTINCT objects of
// the triples whose predicate EXACTLY equals pred — the number of real attempts, robust
// to the at-least-once append double-counting the same object (a lost ack re-appends the
// identical loop instance, which collapses in the set).
func (e *Executor) countDistinct(ctx context.Context, runEntityID, prefix, pred string) (int, error) {
	triples, err := e.reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return 0, fmt.Errorf("read %s on %s: %w", prefix, runEntityID, err)
	}
	seen := make(map[string]struct{})
	for _, tr := range triples {
		if tr.Predicate != pred {
			continue
		}
		seen[objectString(tr.Object)] = struct{}{}
	}
	return len(seen), nil
}

// gateTriples projects the decision into the owned per-task package on the run entity:
// dev.gate.<idx>.{decision,reason}. The fixed sub-key set upserts by predicate, so
// re-gating a task (a retry's gate) replaces its prior decision without a stale sub-key.
func gateTriples(runEntityID string, idx int, decision Decision, reason string, now time.Time) []message.Triple {
	base := GatePrefix + strconv.Itoa(idx) + "."
	mk := func(field, obj string) message.Triple {
		return message.Triple{
			Subject:    runEntityID,
			Predicate:  base + field,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		}
	}
	return []message.Triple{
		mk(FactDecision, string(decision)),
		mk(FactReason, reason),
	}
}

// objectString coerces a triple object to its string form. The engine writes objects
// as strings (scalars via strconv), so this is the identity in practice; the fallback
// handles a hand-written or legacy non-string object rather than dropping it.
func objectString(o any) string {
	if s, ok := o.(string); ok {
		return s
	}
	if o == nil {
		return ""
	}
	return fmt.Sprint(o)
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
