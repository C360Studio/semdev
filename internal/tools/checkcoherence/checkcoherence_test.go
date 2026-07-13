package checkcoherence

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// The coherence roll-up policy, pinned exhaustively (the live loop only drives coherent).
func TestDecideRollup(t *testing.T) {
	approved := map[string]string{"0": "approved"}
	cases := []struct {
		name string
		in   Inputs
		want Decision
	}{
		{"all-green-coherent", Inputs{VerifyResult: "pass", Validated: "rev-1", ProjectedTasks: []string{"0"}, Verdicts: approved}, DecisionCoherent},
		{"multi-task-all-approved", Inputs{VerifyResult: "pass", Validated: "rev-1", ProjectedTasks: []string{"0", "1"}, Verdicts: map[string]string{"0": "approved", "1": "approved"}}, DecisionCoherent},

		// Fail-closed: any non-green/absent signal blocks.
		{"verify-fail-blocks", Inputs{VerifyResult: "fail", Validated: "rev-1", ProjectedTasks: []string{"0"}, Verdicts: approved}, DecisionBlocked},
		{"verify-retry-blocks", Inputs{VerifyResult: "retry", Validated: "rev-1", ProjectedTasks: []string{"0"}, Verdicts: approved}, DecisionBlocked},
		{"verify-missing-blocks", Inputs{VerifyResult: "", Validated: "rev-1", ProjectedTasks: []string{"0"}, Verdicts: approved}, DecisionBlocked},
		{"validated-missing-blocks", Inputs{VerifyResult: "pass", Validated: "", ProjectedTasks: []string{"0"}, Verdicts: approved}, DecisionBlocked},
		{"no-projected-task-blocks", Inputs{VerifyResult: "pass", Validated: "rev-1", ProjectedTasks: nil, Verdicts: approved}, DecisionBlocked},
		{"unapproved-verdict-blocks", Inputs{VerifyResult: "pass", Validated: "rev-1", ProjectedTasks: []string{"0"}, Verdicts: map[string]string{"0": "changes_requested"}}, DecisionBlocked},
		{"absent-verdict-blocks", Inputs{VerifyResult: "pass", Validated: "rev-1", ProjectedTasks: []string{"0"}, Verdicts: map[string]string{}}, DecisionBlocked},
		{"one-of-two-unapproved-blocks", Inputs{VerifyResult: "pass", Validated: "rev-1", ProjectedTasks: []string{"0", "1"}, Verdicts: map[string]string{"0": "approved"}}, DecisionBlocked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := Decide(c.in)
			if got != c.want {
				t.Errorf("Decide(%+v) = %q (%s), want %q", c.in, got, reason, c.want)
			}
			if reason == "" {
				t.Error("Decide must return a non-empty reason (G7)")
			}
		})
	}
}

// --- executor ---

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

type fakeWriter struct{ replaces [][]message.Triple }

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

// coherentFacts: verify pass, validated, one projected+approved task.
func coherentFacts() []message.Triple {
	return []message.Triple{
		triple(VerifyResultPred, "pass"),
		triple(ValidatedPred, "content-rev-1"),
		triple(devtask.TaskSpecKeyPrefix(0)+"goal", "fix it"),
		triple(reviewPrefix+"0", "approved"),
	}
}

func callCoherence() agentic.ToolCall {
	return agentic.ToolCall{
		ID:       "c1",
		Name:     ToolName,
		LoopID:   "coherence-loop-xyz",
		Metadata: map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

func stamped(w *fakeWriter) map[string]string {
	facts := map[string]string{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			facts[tr.Predicate] = tr.Object.(string)
		}
	}
	return facts
}

// Happy path: all signals green → coherent; stamps pr.coherence.decision on the run + the
// dev.coherence_decided loop marker (value = decision), with the coherence-tools Source.
func TestExecuteCoherentStampsDecisionAndMarker(t *testing.T) {
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	w := &fakeWriter{}
	e := New(fakeReader{triples: coherentFacts()}, w, platform, nil)
	res, err := e.Execute(context.Background(), callCoherence())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("check_coherence must StopLoop")
	}
	facts := stamped(w)
	if got := facts[CoherencePrefix+FactDecision]; got != string(DecisionCoherent) {
		t.Errorf("%s%s = %q, want coherent", CoherencePrefix, FactDecision, got)
	}
	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, "coherence-loop-xyz")
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	var marker *message.Triple
	for _, batch := range w.replaces {
		for i := range batch {
			if batch[i].Predicate == DecidedMarker {
				marker = &batch[i]
			}
		}
	}
	if marker == nil {
		t.Fatal("check_coherence must stamp the dev.coherence_decided loop marker")
	}
	if marker.Subject != loopEntityID || marker.Object.(string) != string(DecisionCoherent) || marker.Source != Source {
		t.Errorf("marker = %+v, want subject=%q object=coherent source=%q", marker, loopEntityID, Source)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("fact %s Source = %q, want %q (G5)", tr.Predicate, tr.Source, Source)
			}
		}
	}
}

// A missing review verdict → blocked (fail closed): verify pass + validated + projected
// task but NO verdict.
func TestExecuteBlocksWhenProjectedTaskUnreviewed(t *testing.T) {
	w := &fakeWriter{}
	trs := []message.Triple{
		triple(VerifyResultPred, "pass"),
		triple(ValidatedPred, "rev-1"),
		triple(devtask.TaskSpecKeyPrefix(0)+"goal", "fix it"),
		// no review.verdict.0
	}
	e := New(fakeReader{triples: trs}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	if _, err := e.Execute(context.Background(), callCoherence()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stamped(w)[CoherencePrefix+FactDecision]; got != string(DecisionBlocked) {
		t.Errorf("decision = %q, want blocked (an unreviewed projected task blocks — absent = not approved)", got)
	}
}

// A failing verify → blocked.
func TestExecuteBlocksOnFailingVerify(t *testing.T) {
	w := &fakeWriter{}
	trs := []message.Triple{
		triple(VerifyResultPred, "fail"),
		triple(ValidatedPred, "rev-1"),
		triple(devtask.TaskSpecKeyPrefix(0)+"goal", "x"),
		triple(reviewPrefix+"0", "approved"),
	}
	e := New(fakeReader{triples: trs}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	if _, err := e.Execute(context.Background(), callCoherence()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stamped(w)[CoherencePrefix+FactDecision]; got != string(DecisionBlocked) {
		t.Errorf("decision = %q, want blocked (failing verify)", got)
	}
}

// A read fault is a retryable tool error, not a stamped block.
func TestExecuteReadFaultIsToolError(t *testing.T) {
	w := &fakeWriter{}
	e := New(fakeReader{err: errors.New("graph query timed out")}, w, types.PlatformMeta{Org: "c", Platform: "p"}, nil)
	res, err := e.Execute(context.Background(), callCoherence())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a read fault must surface as a tool error")
	}
	if len(w.replaces) != 0 {
		t.Error("a read fault must stamp nothing")
	}
}

// Schema-only registration fails loudly.
func TestExecuteFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(nil, nil, types.PlatformMeta{}, nil).Execute(context.Background(), callCoherence())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness check must fail loudly")
	}
}

// G3: the schema takes no arguments.
func TestSchemaTakesNoInput(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("schema exposes %d properties, want 0 — check_coherence takes no input (G3): %v", len(props), props)
	}
}

// The projected-task reader collects distinct indices from the task.spec.* namespace.
func TestProjectedTaskIDs(t *testing.T) {
	trs := []message.Triple{
		triple(devtask.TaskSpecKeyPrefix(0)+"goal", "a"),
		triple(devtask.TaskSpecKeyPrefix(0)+"budget", "3"),
		triple(devtask.TaskSpecKeyPrefix(2)+"goal", "b"),
	}
	ids := projectedTaskIDs(trs)
	if len(ids) != 2 {
		t.Fatalf("want 2 distinct task ids, got %d: %v", len(ids), ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, err := strconv.Atoi(id); err != nil {
			t.Errorf("non-numeric task id %q", id)
		}
		seen[id] = true
	}
	if !seen["0"] || !seen["2"] {
		t.Errorf("missing expected ids: %v", ids)
	}
}
