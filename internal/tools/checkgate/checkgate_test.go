package checkgate

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

func boolp(b bool) *bool { return &b }
func intp(n int) *int    { return &n }

// The gate policy, pinned exhaustively over the whole input space — the live loop only
// ever drives the happy (advance) path, so the routing table lives or dies here.
func TestDecideRoutingTable(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Decision
	}{
		// Advance: measured green AND no floor rejected, regardless of budget/count.
		{"green-clean-advances", Inputs{Passed: boolp(true), Rejected: boolp(false), Budget: intp(5), AttemptCount: 1}, DecisionAdvance},
		{"green-clean-advances-even-at-budget", Inputs{Passed: boolp(true), Rejected: boolp(false), Budget: intp(1), AttemptCount: 1}, DecisionAdvance},

		// Not clean + budget remains → retry.
		{"failed-test-under-budget-retries", Inputs{Passed: boolp(false), Rejected: boolp(false), Budget: intp(5), AttemptCount: 1}, DecisionRetry},
		{"floor-rejected-under-budget-retries", Inputs{Passed: boolp(true), Rejected: boolp(true), Budget: intp(5), AttemptCount: 2}, DecisionRetry},
		{"both-bad-under-budget-retries", Inputs{Passed: boolp(false), Rejected: boolp(true), Budget: intp(3), AttemptCount: 2}, DecisionRetry},

		// Not clean + budget exhausted → escalate. count==budget is exhausted (the gate
		// runs once per counted attempt: budget N yields exactly N attempts).
		{"failed-test-at-budget-escalates", Inputs{Passed: boolp(false), Rejected: boolp(false), Budget: intp(1), AttemptCount: 1}, DecisionEscalate},
		{"floor-rejected-at-budget-escalates", Inputs{Passed: boolp(true), Rejected: boolp(true), Budget: intp(5), AttemptCount: 5}, DecisionEscalate},
		{"over-budget-escalates", Inputs{Passed: boolp(false), Rejected: boolp(true), Budget: intp(2), AttemptCount: 3}, DecisionEscalate},

		// Fail closed: any missing judgment fact → escalate (never a false retry), even
		// when the budget would otherwise permit a retry.
		{"missing-passed-escalates", Inputs{Passed: nil, Rejected: boolp(false), Budget: intp(5), AttemptCount: 0}, DecisionEscalate},
		{"missing-rejected-escalates", Inputs{Passed: boolp(true), Rejected: nil, Budget: intp(5), AttemptCount: 0}, DecisionEscalate},
		{"missing-budget-escalates", Inputs{Passed: boolp(false), Rejected: boolp(false), Budget: nil, AttemptCount: 0}, DecisionEscalate},
		{"all-missing-escalates", Inputs{}, DecisionEscalate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := Decide(c.in)
			if got != c.want {
				t.Errorf("Decide(%+v) = %q (%s), want %q", c.in, got, reason, c.want)
			}
			if reason == "" {
				t.Error("Decide must return a non-empty reason (G7 honest evidence)")
			}
		})
	}
}

// The budget boundary is exact: with budget N the gate retries for attempts 1..N-1 and
// escalates at N. A fencepost slip (<= vs <) would give N+1 or N-1 attempts.
func TestDecideBudgetBoundaryIsExact(t *testing.T) {
	const budget = 3
	notClean := func(count int) Decision {
		d, _ := Decide(Inputs{Passed: boolp(false), Rejected: boolp(false), Budget: intp(budget), AttemptCount: count})
		return d
	}
	for count := 1; count < budget; count++ {
		if got := notClean(count); got != DecisionRetry {
			t.Errorf("count=%d/%d: got %q, want retry (budget remains)", count, budget, got)
		}
	}
	if got := notClean(budget); got != DecisionEscalate {
		t.Errorf("count=%d/%d: got %q, want escalate (budget exhausted)", budget, budget, got)
	}
}

// --- executor-level (reads + stamps) ---

type fakeReader struct {
	triples []message.Triple
	err     error
}

func (r fakeReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	if r.err != nil {
		return nil, r.err
	}
	var out []message.Triple
	for _, tr := range r.triples {
		if strings.HasPrefix(tr.Predicate, prefix) {
			out = append(out, tr)
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

func triple(pred, obj string) message.Triple {
	return message.Triple{Subject: runEntity, Predicate: pred, Object: obj}
}

// facts for task 0: passed, rejected, budget, and `attempts` distinct attempt objects.
func task0Facts(passed, rejected string, budget int, attempts int) []message.Triple {
	trs := []message.Triple{
		triple(measurement.ResultPrefix+"0."+measurement.FactPassed, passed),
		triple(floors.FindingPrefix+"0."+floors.FactRejected, rejected),
		triple(devtask.TaskSpecKeyPrefix(0)+devtask.FactBudget, strconv.Itoa(budget)),
	}
	for i := 0; i < attempts; i++ {
		trs = append(trs, triple(devtask.TaskAttemptKey(0), "dev-loop-"+strconv.Itoa(i)))
	}
	return trs
}

func callFor(idx int) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		LoopID:    "gate-loop-xyz",
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{"task_index": idx},
	}
}

func stampedFacts(w *fakeWriter) map[string]string {
	facts := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			facts[tr.Predicate] = tr.Object.(string)
		}
	}
	return facts
}

// Happy path: a clean attempt advances; the tool stamps dev.gate.0.decision=advance on
// the run AND the dev.gate_decision loop marker for the routers, with the gate-tools
// Source, and StopLoops.
func TestExecuteAdvanceStampsDecisionAndMarker(t *testing.T) {
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	w := &fakeWriter{}
	e := New(fakeReader{triples: task0Facts("true", "false", 5, 1)}, w, platform, nil)
	res, err := e.Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("check_gate must StopLoop (single forced turn)")
	}
	facts := stampedFacts(w)
	if got := facts[GatePrefix+"0."+FactDecision]; got != string(DecisionAdvance) {
		t.Errorf("%s0.%s = %q, want %q", GatePrefix, FactDecision, got, DecisionAdvance)
	}
	if facts[GatePrefix+"0."+FactReason] == "" {
		t.Errorf("%s0.%s must be recorded (G7 honest evidence)", GatePrefix, FactReason)
	}
	// The loop marker on the gate loop, value = the decision.
	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, "gate-loop-xyz")
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	var marker *message.Triple
	for _, batch := range w.replaces {
		for i := range batch {
			if batch[i].Predicate == GateDecisionMarker {
				marker = &batch[i]
			}
		}
	}
	if marker == nil {
		t.Fatal("check_gate must stamp the dev.gate_decision loop marker (the routers fire on it)")
	}
	if marker.Subject != loopEntityID {
		t.Errorf("%s stamped on %q, want the gate LOOP entity %q", GateDecisionMarker, marker.Subject, loopEntityID)
	}
	if marker.Object.(string) != string(DecisionAdvance) {
		t.Errorf("%s object = %q, want the decision %q", GateDecisionMarker, marker.Object, DecisionAdvance)
	}
	if marker.Source != Source {
		t.Errorf("%s Source = %q, want %q (G5)", GateDecisionMarker, marker.Source, Source)
	}
	// All run-facts carry the gate-tools Source (G5).
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("gate fact %s Source = %q, want %q (G5)", tr.Predicate, tr.Source, Source)
			}
		}
	}
}

// A failing measurement under budget routes retry (marker value = retry).
func TestExecuteRetryWhenBudgetRemains(t *testing.T) {
	w := &fakeWriter{}
	e := New(fakeReader{triples: task0Facts("false", "false", 5, 1)}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	if _, err := e.Execute(context.Background(), callFor(0)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stampedFacts(w)[GatePrefix+"0."+FactDecision]; got != string(DecisionRetry) {
		t.Errorf("decision = %q, want retry", got)
	}
}

// Budget exhausted (count==budget, not clean) escalates.
func TestExecuteEscalateWhenBudgetExhausted(t *testing.T) {
	w := &fakeWriter{}
	e := New(fakeReader{triples: task0Facts("false", "false", 2, 2)}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	if _, err := e.Execute(context.Background(), callFor(0)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stampedFacts(w)[GatePrefix+"0."+FactDecision]; got != string(DecisionEscalate) {
		t.Errorf("decision = %q, want escalate", got)
	}
}

// Fail closed: a MISSING measurement fact (never stamped) escalates rather than
// retrying blind — the gate cannot invent the outcome (G3) and must not skip toward a
// false green. Uses facts WITHOUT measurement.result.0.passed.
func TestExecuteFailsClosedOnMissingMeasurement(t *testing.T) {
	w := &fakeWriter{}
	// budget + rejected present, passed ABSENT.
	trs := []message.Triple{
		triple(floors.FindingPrefix+"0."+floors.FactRejected, "false"),
		triple(devtask.TaskSpecKeyPrefix(0)+devtask.FactBudget, "5"),
		triple(devtask.TaskAttemptKey(0), "dev-loop-0"),
	}
	e := New(fakeReader{triples: trs}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	if _, err := e.Execute(context.Background(), callFor(0)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stampedFacts(w)[GatePrefix+"0."+FactDecision]; got != string(DecisionEscalate) {
		t.Errorf("decision = %q, want escalate (fail closed on missing measurement)", got)
	}
}

// The distinct-object count is robust to the at-least-once append double-counting the
// same loop instance: two triples with the SAME object count as ONE attempt.
func TestExecuteCountsDistinctAttemptObjects(t *testing.T) {
	w := &fakeWriter{}
	// budget 2, NOT clean; two attempt triples but the SAME object → distinct count 1 <
	// 2 → retry (a raw-cardinality count would read 2 == budget → wrongly escalate).
	trs := []message.Triple{
		triple(measurement.ResultPrefix+"0."+measurement.FactPassed, "false"),
		triple(floors.FindingPrefix+"0."+floors.FactRejected, "false"),
		triple(devtask.TaskSpecKeyPrefix(0)+devtask.FactBudget, "2"),
		triple(devtask.TaskAttemptKey(0), "dev-loop-0"),
		triple(devtask.TaskAttemptKey(0), "dev-loop-0"), // duplicate (lost-ack re-append)
	}
	e := New(fakeReader{triples: trs}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	if _, err := e.Execute(context.Background(), callFor(0)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stampedFacts(w)[GatePrefix+"0."+FactDecision]; got != string(DecisionRetry) {
		t.Errorf("decision = %q, want retry — the double-appended attempt must count once (distinct objects)", got)
	}
}

// A read fault is a retryable tool error, NOT a stamped escalate decision (the forced
// loop re-runs; a persistent fault trips MaxIterations — never a false green).
func TestExecuteReadFaultIsToolError(t *testing.T) {
	w := &fakeWriter{}
	e := New(fakeReader{err: errors.New("graph query timed out")}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	res, err := e.Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a read fault must surface as a tool error, not a silently-stamped decision")
	}
	if len(w.replaces) != 0 {
		t.Error("a read fault must stamp no decision")
	}
	if res.StopLoop {
		t.Error("a read-fault error must NOT StopLoop — the forced loop retries")
	}
}

// A negative / absent task index is rejected before any read or stamp.
func TestExecuteRejectsBadIndex(t *testing.T) {
	w := &fakeWriter{}
	e := New(fakeReader{}, w, types.PlatformMeta{}, nil)
	res, err := e.Execute(context.Background(), callFor(-1))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a negative index must be rejected")
	}
	if len(w.replaces) != 0 {
		t.Error("a rejected index must stamp nothing")
	}
}

// Schema-only registration (nil reader/writer) fails loudly if executed.
func TestExecuteFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, types.PlatformMeta{}, nil).Execute(context.Background(), callFor(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness check must fail loudly")
	}
}

// G3: the schema takes only the task selector — no decision/outcome/count field. The
// route is derived from recorded evidence, not supplied.
func TestSchemaTakesOnlyTaskSelector(t *testing.T) {
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
	for _, banned := range []string{"decision", "advance", "retry", "escalate", "passed", "rejected", "count", "budget", "outcome"} {
		if _, present := props[banned]; present {
			t.Errorf("schema accepts a gate-outcome field %q (G3): the route is derived, not supplied", banned)
		}
	}
}
