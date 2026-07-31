package submitreview

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"

	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/measurement"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
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
	replaces  [][]message.Triple
	entityIDs []string
	contracts []string
}

func (w *fakeWriter) ReplaceOwned(_ context.Context, m projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	w.replaces = append(w.replaces, m.Desired)
	w.entityIDs = append(w.entityIDs, m.EntityID)
	w.contracts = append(w.contracts, m.Contract)
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// submit_review writes as TWO owners — reviewer-quinn (the verdict, on the run) and
// route-mirror (the route mirror, on ITS review loop) — so the tool takes two bound
// writers. Both tap the SAME fake here, so existing assertions over w.replaces still
// see every write, while entityIDs/contracts let a test tell the two apart.
func mustW(w *fakeWriter) *graphown.Writer { return graphown.NewWriter(Source, w) }

func mustM(w *fakeWriter) *graphown.Writer { return graphown.NewWriter(RouteMirrorSource, w) }

// taskSpecFact builds one flat task.spec.<field> fact (beta.147 D1: single-task at
// M0 — the per-task index is out of the predicate).
func taskSpecFact(field, obj string) message.Triple {
	return message.Triple{Predicate: devtask.TaskSpecPrefix + field, Object: obj, Source: "task-projector"}
}

// measurementFacts builds the flat measurement.result.* quintet for the run's one
// task (beta.147 D1: no per-task index in the predicate).
func measurementFacts(exit int, ran, timedOut, passed bool) []message.Triple {
	p := measurement.ResultPrefix
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
	res, err := New(&fakeReader{facts: facts}, mustW(w), mustM(w), types.PlatformMeta{}, nil).Execute(context.Background(), call(taskIndex, findings...))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return verdictFor(w, taskIndex), res
}

// run reviews task 0 (the only projected task, single-task at M0).
func run(t *testing.T, facts []message.Triple, w *fakeWriter, findings ...string) (string, agentic.ToolResult) {
	t.Helper()
	return runTask(t, facts, w, 0, findings...)
}

// oneTaskPassing is a run with one projected task and a passing measurement for it
// (beta.147 D1: flat predicates, single task at M0 — there is no other task to key).
func oneTaskPassing() []message.Triple {
	var f []message.Triple
	f = append(f, taskSpecFact(devtask.FactGoal, "add the guard"), taskSpecFact(devtask.FactTestCommand, "go test ./..."))
	f = append(f, measurementFacts(0, true, false, true)...)
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
			// On the run, submit_review writes exactly its two owned facts: the verdict and
			// the (empty here) findings — both under reviewer-quinn (the route mirror lands on
			// the loop, not the run, and only when a LoopID is present).
			if tr.Predicate != verdictPredicate(0) && tr.Predicate != findingsPredicate(0) {
				t.Errorf("run predicate = %q, want %q or %q", tr.Predicate, verdictPredicate(0), findingsPredicate(0))
			}
		}
	}
}

// Red-first, the spec scenario: the reviewed task's measured outcome is a FAILURE
// (exit 1) — the reviewer cannot record an approving verdict even with no findings
// and however the change describes itself. Approval reads the stamped fact.
func TestReviewCannotApproveFalseSuccess(t *testing.T) {
	var f []message.Triple
	f = append(f, taskSpecFact(devtask.FactTestCommand, "go test ./..."))
	f = append(f, measurementFacts(1, true, false /*passed=*/, true)...) // stored passed lies; exit is 1
	w := &fakeWriter{}
	verdict, res := run(t, f, w)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — a failing measurement cannot be approved regardless of the stored passed", verdict, VerdictChangesRequested)
	}
}

// The reviewed task with NO measurement at all blocks its approval — the per-task
// gate proves THIS task's evidence EXISTS and passed, not merely that nothing failed.
func TestReviewBlocksWhenThisTasksMeasurementMissing(t *testing.T) {
	f := []message.Triple{taskSpecFact(devtask.FactTestCommand, "go test ./...")}
	w := &fakeWriter{}
	verdict, res := run(t, f, w)
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Errorf("verdict = %q, want %q — the reviewed task has no measurement", verdict, VerdictChangesRequested)
	}
}

// beta.147 D1 collapsed dev-from-task to single-task at M0: task.spec and
// measurement.result are flat and carry no per-task index, so there is no longer a
// fixture-shaped way to author "task 1" alongside "task 0" — the pre-D1
// per-task-independence scenario has no analogue. What DOES survive: an index other
// than the one projected task ("0") can never be reviewed at all — cross-task
// leakage is structurally impossible because there is no way to address a second
// task's evidence in the first place (the M1 multi-task future re-introduces
// per-task addressing via a per-task entity ID, an explicit seam).
func TestReviewRejectsUnprojectedTaskIndex(t *testing.T) {
	w := &fakeWriter{}
	verdict, res := runTask(t, oneTaskPassing(), w, 1)
	if res.Error == "" {
		t.Fatal("reviewing task_index=1 when only the one task (id 0) is projected must error, not silently pass/fail")
	}
	if verdict != "" {
		t.Errorf("no verdict should be stamped for an unprojected task index, got %q", verdict)
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

// Findings never weaken task.spec: on the run the review tool writes ONLY its owned
// review.verdict / review.findings facts and holds no writer for task.spec, so a
// finding is structurally incapable of removing or relaxing a task.spec requirement (G5).
func TestReviewWritesOnlyVerdictAndFindingsNeverTaskSpec(t *testing.T) {
	w := &fakeWriter{}
	run(t, oneTaskPassing(), w, "please also add a benchmark")
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != verdictPredicate(0) && tr.Predicate != findingsPredicate(0) {
				t.Errorf("review stamped %q on the run — it must write only %q / %q (findings are additive, never weaken task.spec)", tr.Predicate, verdictPredicate(0), findingsPredicate(0))
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
	_, res := run(t, measurementFacts(0, true, false, true), w) // measurements but no task.spec
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
	res, err := New(&fakeReader{facts: oneTaskPassing()}, mustW(&fakeWriter{}), mustM(&fakeWriter{}), types.PlatformMeta{}, nil).Execute(context.Background(), c)
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
	f = append(f, taskSpecFact(devtask.FactTestCommand, "go test ./..."))
	f = append(f, measurementFacts(0, true, false, true)...)
	// Corrupt the exit-code fact.
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
	res, err := New(&fakeReader{facts: oneTaskPassing()}, mustW(w), mustM(w), types.PlatformMeta{}, nil).Execute(context.Background(), call(0, "regression found"))
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
	res, err := New(nil, nil, nil, types.PlatformMeta{}, nil).Execute(context.Background(), call(0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness review must fail loudly")
	}
}

// The ROUTE MIRROR: submit_review copies the routing inputs onto ITS OWN review loop so
// the rule-native review route (approved → verify / changes_requested → retry-or-park) can
// fire on them. route.review.verdict = the verdict, route.attempt.instance = the
// append-mirror of the run's task.attempt.instance (the shared budget, R4). Both carry the
// route-mirror Source (a distinct writer from reviewer-quinn, so no predicate gains two
// writers, G5). It is stamped for a changes_requested verdict too (the route decides
// retry-or-park).
func TestReviewMirrorsRouteInputsOntoLoop(t *testing.T) {
	w := &fakeWriter{}
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	// One passing task (with a budget) + two counted attempts; a finding forces
	// changes_requested. The budget rides the mirror so the review retry/park routes read it.
	facts := append(oneTaskPassing(),
		taskSpecFact(devtask.FactBudget, "3"),
		message.Triple{Predicate: "task.attempt.instance", Object: "dev-loop-1", Source: "dev-dispatch-rule"},
		message.Triple{Predicate: "task.attempt.instance", Object: "dev-loop-2", Source: "dev-dispatch-rule"},
	)
	c := call(0, "regression found") // a finding → changes_requested
	c.LoopID = "review-loop-abc"
	res, err := New(&fakeReader{facts: facts}, mustW(w), mustM(w), platform, nil).Execute(context.Background(), c)
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
	var gotVerdict, gotBudget string
	attemptObjs := map[string]bool{}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			switch tr.Predicate {
			case RouteVerdictPredicate, RouteAttemptPredicate, RouteBudgetPredicate:
				if tr.Subject != loopEntityID {
					t.Errorf("route mirror %q stamped on %q, want the review LOOP entity %q", tr.Predicate, tr.Subject, loopEntityID)
				}
				if tr.Source != RouteMirrorSource {
					t.Errorf("route mirror %q Source = %q, want %q (G5)", tr.Predicate, tr.Source, RouteMirrorSource)
				}
			}
			switch tr.Predicate {
			case RouteVerdictPredicate:
				gotVerdict = tr.Object.(string)
			case RouteAttemptPredicate:
				attemptObjs[tr.Object.(string)] = true
			case RouteBudgetPredicate:
				gotBudget = tr.Object.(string)
			}
		}
	}
	if gotVerdict != VerdictChangesRequested {
		t.Errorf("%s = %q, want %q", RouteVerdictPredicate, gotVerdict, VerdictChangesRequested)
	}
	if len(attemptObjs) != 2 {
		t.Errorf("%s must mirror both task.attempt.instance objects, got %v", RouteAttemptPredicate, attemptObjs)
	}
	// The review route reads the per-task budget off this loop too (07b/07c) — a raw copy.
	if gotBudget != "3" {
		t.Errorf("%s = %q, want the RAW authored budget copy \"3\"", RouteBudgetPredicate, gotBudget)
	}
}

// D7 (task 3.5): an ABSENT budget faults the review mirror loudly. A verdict pass with a
// LoopID but no task.spec.budget returns an errResult back to the loop and stamps NOTHING on
// the review loop (atomicity — no route.attempt.* / route.review.verdict without the budget),
// so rules 07b/07c never evaluate the fail-open empty substitution. The run-side verdict
// (the substance) is still stamped; only the loop mirror is withheld.
func TestReviewMirrorAbsentBudgetErrs(t *testing.T) {
	w := &fakeWriter{}
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	// oneTaskPassing has NO task.spec.budget → the mirror budget read faults.
	facts := append(oneTaskPassing(),
		message.Triple{Predicate: "task.attempt.instance", Object: "dev-loop-1", Source: "dev-dispatch-rule"},
	)
	c := call(0, "regression found")
	c.LoopID = "review-loop-abc"
	res, err := New(&fakeReader{facts: facts}, mustW(w), mustM(w), platform, nil).Execute(context.Background(), c)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("an absent task.spec.budget must fault the review mirror loudly (D7 errResult), got no error")
	}
	if res.StopLoop {
		t.Error("the budget fault must NOT StopLoop — the loop re-runs and faults loudly until the contract holds")
	}
	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, c.LoopID)
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Subject == loopEntityID {
				t.Errorf("nothing may be stamped on the review loop when the budget is absent (atomicity), but %s was", tr.Predicate)
			}
		}
	}
}

// D7 parse-validation: a PRESENT-but-non-canonical budget (blank, whitespace-padded, or
// non-integer — anything the engine's coerceToInt rejects) faults the review mirror loudly, like
// an absent budget. readTaskBudget validates the RAW stamped value with strconv.Atoi (no trim), so
// a value that merely "looks" numeric (" 2 ", "2.5") never slips through to a fail-open route stall.
func TestReviewMirrorNonCanonicalBudgetErrs(t *testing.T) {
	platform := types.PlatformMeta{Org: "c360", Platform: "semdev-001"}
	loopEntityID, err := agentic.TryLoopExecutionEntityID(platform.Org, platform.Platform, "review-loop-abc")
	if err != nil {
		t.Fatalf("loop entity id: %v", err)
	}
	for _, bad := range []string{"", " 2 ", "2.5", "abc"} {
		w := &fakeWriter{}
		facts := append(oneTaskPassing(), taskSpecFact(devtask.FactBudget, bad),
			message.Triple{Predicate: "task.attempt.instance", Object: "dev-loop-1", Source: "dev-dispatch-rule"})
		c := call(0, "regression found")
		c.LoopID = "review-loop-abc"
		res, err := New(&fakeReader{facts: facts}, mustW(w), mustM(w), platform, nil).Execute(context.Background(), c)
		if err != nil {
			t.Fatalf("budget %q: execute: %v", bad, err)
		}
		if res.Error == "" {
			t.Errorf("budget %q: a non-canonical budget must fault the review mirror (D7 errResult), got no error", bad)
		}
		for _, batch := range w.replaces {
			for _, tr := range batch {
				if tr.Subject == loopEntityID {
					t.Errorf("budget %q: nothing may be stamped on the review loop on a non-canonical budget, but %s was", bad, tr.Predicate)
				}
			}
		}
	}
}

// review.findings.value is stamped on the run (Quinn's prose) so a changes_requested
// re-entry can re-read it; it is always stamped (empty when none) so a re-review clears it.
func TestReviewStampsFindingsOnRun(t *testing.T) {
	w := &fakeWriter{}
	verdict, res := run(t, oneTaskPassing(), w, "boundary off by one", "add the missing test")
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if verdict != VerdictChangesRequested {
		t.Fatalf("verdict = %q, want changes_requested (findings raised)", verdict)
	}
	var findings string
	var sawFindings bool
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == FindingsPredicate {
				sawFindings = true
				findings = tr.Object.(string)
				if tr.Source != Source {
					t.Errorf("review.findings Source = %q, want %q (G5)", tr.Source, Source)
				}
			}
		}
	}
	if !sawFindings {
		t.Fatalf("submit_review must stamp %s on the run", FindingsPredicate)
	}
	if !strings.Contains(findings, "boundary off by one") || !strings.Contains(findings, "add the missing test") {
		t.Errorf("%s must carry Quinn's prose, got %q", FindingsPredicate, findings)
	}
}

// Without a LoopID (unit/registration path) the verdict still lands but the route mirror
// is skipped — a missing mirror never fails a recorded verdict.
func TestReviewWithoutLoopIDSkipsMirrorButRecords(t *testing.T) {
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
			if tr.Predicate == RouteVerdictPredicate {
				t.Errorf("no LoopID → the route mirror must be skipped, but %s was stamped", RouteVerdictPredicate)
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
