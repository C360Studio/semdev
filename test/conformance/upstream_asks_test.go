package conformance

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
)

// Upstream-ask tripwires (simplify-m0-execution-rail, task 1.2).
//
// The reshaped rail carried three deliberate interims because the semstreams
// engine could not yet express the ideal form. As of beta.148 all three upstream
// fixes have LANDED (#519 scalar .value, #528 per-spawn max_iterations, #529 typed
// exhaustion sentinel — alongside #530 in beta.147), so these tests have flipped from
// "gap-open tripwires" to REGRESSION GUARDS: each now asserts the landed capability is
// still present and FIRES if a future beta drops it (which would silently re-open the
// gap the reshaped rail routes around).
//
// The MECHANICAL UPGRADES the capabilities enable — per-task iteration budgets
// (loop_max_iterations, #528), per-task attempt budgets via $entity.triple.task.spec.budget.value
// (#519), and a reason-aware escalate route (errors.Is ErrMaxIterationsReached, #529) — are
// routing-behavior changes NOT yet adopted; each guard's doc names its follow-up. The current
// M0 behavior (uniform iteration cap, literal-3 budget, outcome=failed routing) is proven by
// the e2e and remains correct until those upgrades are deliberately taken.

// TestTripwire519ScalarValueSubstitution — semstreams #519 (scalar .value
// field-to-field). Now a REGRESSION GUARD: the #519 fix LANDED in beta.148.
//
// The ExecutionContext resolves `$entity.triple.<3-part-predicate>.value` to the scalar
// object (applyTripleValueSubstitutions in processor/rule/execution_context.go), so a rule
// can compare one predicate against another predicate's value. KEY: the fix is at the
// ExecutionContext substitution layer (which runs BEFORE evaluation), NOT the raw
// ExpressionEvaluator — the old evaluator-level anchor tested the wrong layer and never
// would have fired, so this is re-anchored to source-inspect the substitution helper (the
// same module-cache read the #530 guard uses; the helper is unexported).
//
// MECHANICAL UPGRADE (available, not yet adopted — a routing follow-up): replace the
// constant-3 attempt-budget literals in configs/rules/dev-from-task/* with
// `$entity.triple.task.spec.budget.value`, so the per-task budget from the projected
// contract becomes load-bearing (it is advisory today).
func TestTripwire519ScalarValueSubstitution(t *testing.T) {
	src := readSemstreamsSource(t, "processor", "rule", "execution_context.go")
	const anchor = "applyTripleValueSubstitutions"
	if !strings.Contains(src, anchor) {
		t.Fatalf("REGRESSION (#519): the %q scalar-value substitution helper is gone from "+
			"execution_context.go — `$entity.triple.X.value` field-to-field no longer resolves. "+
			"The beta.148 fix was dropped; the per-task-budget upgrade depends on it. Restore "+
			"upstream or re-open #519 and re-block the .value budget rules.", anchor)
	}
	// Floor: the gh#519 marker still sits by the helper, so a rename/move surfaces as a
	// visible re-anchor task rather than a silently-green guard over vanished behavior.
	if !strings.Contains(src, "gh#519") {
		t.Fatalf("execution_context.go no longer references gh#519 near %q — the .value contract "+
			"may have moved; re-verify BY HAND that scalar-value substitution still resolves before "+
			"trusting this pin", anchor)
	}
}

// TestTripwire528PerSpawnMaxIterations — semstreams #528 (per-spawn
// max_iterations). Now a REGRESSION GUARD: the #528 fix LANDED in beta.148.
//
// agentic.TaskMessage gained a per-spawn MaxIterations *int (nil = "use the agentic-loop
// component default"), and the publish_agent rule action exposes it as loop_max_iterations.
// So a spawned loop can carry its own turn budget instead of only the uniform component cap.
// This asserts the field is still present; it FIRES if a future beta drops it.
//
// MECHANICAL UPGRADE (available, not yet adopted — a routing follow-up): set the developer
// loop's iteration budget per publish_agent spawn (loop_max_iterations) instead of the
// uniform component cap, so a hard task gets more turns than a trivial one.
func TestTripwire528PerSpawnMaxIterations(t *testing.T) {
	tm := reflect.TypeOf(agentic.TaskMessage{})

	// Sanity: we are reflecting the right type — the depth budget IS present.
	if _, ok := tm.FieldByName("MaxDepth"); !ok {
		t.Fatalf("agentic.TaskMessage no longer has MaxDepth — the spawn-wire shape "+
			"changed under this pin; re-verify #528's anchor before trusting it (type=%s)", tm)
	}

	f, ok := tm.FieldByName("MaxIterations")
	if !ok {
		t.Fatal("REGRESSION (#528): agentic.TaskMessage no longer carries a per-spawn " +
			"MaxIterations budget — the beta.148 fix was dropped. The developer loop can no " +
			"longer take a per-task turn budget (back to the uniform component cap); restore " +
			"upstream or re-open #528 before relying on loop_max_iterations.")
	}
	// The nil = "use component default" contract depends on a POINTER; a change to a bare int
	// would silently make 0 mean "zero turns" instead of "inherit" — surface it as a re-anchor.
	if f.Type.Kind() != reflect.Ptr {
		t.Fatalf("#528: agentic.TaskMessage.MaxIterations is %s, not a pointer — the nil='use "+
			"the component default' contract changed; re-verify before trusting the per-spawn budget", f.Type)
	}
}

// TestTripwire529UniformExhaustionReason — semstreams #529 (uniform exhaustion
// reason). Now a REGRESSION GUARD: the #529 fix LANDED in beta.148 — but NOT in the
// shape the old field-reflection anchor watched for (its documented coverage caveat).
//
// The fix is a TYPED SENTINEL ERROR, agentic.ErrMaxIterationsReached, returned on budget
// exhaustion and matchable via errors.Is — distinct from an unrelated "loop not found"
// operational error, which callers MUST NOT misreport as exhaustion. So a route can finally
// distinguish "ran out of turns" from "the model erred" by branching on the sentinel rather
// than the free-form Reason string. This asserts the sentinel still exists and still matches
// (self and wrapped) via errors.Is; it FIRES if the sentinel is removed or stops matching.
//
// MECHANICAL UPGRADE (available, not yet adopted — a routing follow-up): make the dev-loop
// escalate route reason-aware — exhaustion (errors.Is ErrMaxIterationsReached) → escalate
// toward the human; a transient model error → retry within budget — instead of routing on
// outcome=failed alone.
func TestTripwire529UniformExhaustionReason(t *testing.T) {
	// Anchor 1: the outcome constants this rail routes over still EXIST (compile-time floor).
	_ = []string{
		agentic.OutcomeSuccess,
		agentic.OutcomeFailed,
		agentic.OutcomeCancelled,
		agentic.OutcomeTruncated,
	}

	// Anchor 2: the typed exhaustion sentinel exists (compile-time reference) and matches
	// itself via errors.Is — impossible to fail unless it was redefined out from under us.
	if !errors.Is(agentic.ErrMaxIterationsReached, agentic.ErrMaxIterationsReached) {
		t.Fatal("agentic.ErrMaxIterationsReached does not match itself via errors.Is — the " +
			"sentinel was redefined; re-anchor #529")
	}

	// Anchor 3 (the regression check): a WRAPPED sentinel still matches via errors.Is — the
	// exact discipline a reason-aware escalate route relies on (the exhaustion error reaches
	// the route wrapped in loop-terminal context). If this breaks, the typed exhaustion signal
	// is gone and the rail is back to routing on outcome=failed alone.
	wrapped := fmt.Errorf("agentic loop terminated: %w", agentic.ErrMaxIterationsReached)
	if !errors.Is(wrapped, agentic.ErrMaxIterationsReached) {
		t.Fatal("REGRESSION (#529): a wrapped agentic.ErrMaxIterationsReached no longer matches " +
			"via errors.Is — the typed exhaustion signal is broken. The reason-aware escalate " +
			"upgrade depends on it; restore upstream or re-open #529.")
	}
}

// TestTripwireOnRecoveryRoutingGate — semstreams #530 (on_recovery routing gate;
// R8, design group 7). Now a REGRESSION GUARD: the #530 gate fix LANDED in beta.147.
//
// The M0-correct posture for a restarted, still-in-flight run is a rule-native
// FAIL-CLOSED PARK: a rule with an EMPTY on_enter (so nothing fires during live
// operation, when a run is `executing` its whole working life) and the park in
// `on_recovery` (which the framework's bootstrap-recovery fork fires only after a
// restart, for a rule that was matching before the crash).
//
// Through beta.146 that rule was INERT: the rule Processor routed a rule to the
// stateful evaluator (where the on_recovery fork lives) ONLY on a non-empty
// on_enter/on_exit/while_true — the gate EXCLUDED on_recovery. beta.147 fixed it: the
// gate now delegates to the `hasStatefulRuleActions(ruleDef)` helper, which admits
// `len(def.OnRecovery) > 0`, so an on_recovery-only park is finally routed. This test
// flips to a REGRESSION GUARD: it asserts that helper still admits OnRecovery, and
// FIRES if a future beta drops it (which would make the park inert again and silently
// wedge a restarted run).
//
// STILL UNVERIFIED (do NOT assume restart-recovery works end-to-end yet): the
// COMPOUNDING stale-revision guard in stateful_evaluator.go
// (`prevState.SourceRevision >= ev.Revision`) is UNCHANGED in beta.147 and may still
// suppress the bootstrap recovery fork for an entity whose last live write triggered
// its last eval. There is no upstream e2e proving on_recovery fires through the wired
// path across a real restart. So the restart-recovery park + its docker station (design
// R10, task 10.4) remain a FOLLOW-UP: build the run-lifecycle recovery-park rule
// (on_enter empty, on_recovery stamps run.awaiting_human + posts user.response, guarded
// on phase==executing / pr.ref absent / awaiting_human absent) + its shape pin + the
// sanctioned-park-writer entry, and FIRST prove on a real restart that the recovery fork
// actually fires past the stale-revision guard.
//
// It reads the ACTUAL compiled framework source (the module cache) rather than
// reflecting, because the gate helper is unexported. It FAILS LOUD if it cannot locate
// the helper (a moved/renamed gate surfaces as a visible re-anchor task, never a silent
// green that hides a #530 regression).
func TestTripwireOnRecoveryRoutingGate(t *testing.T) {
	src := readSemstreamsSource(t, "processor", "rule", "message_handler.go")

	// beta.147's gate:
	//   hasStatefulActions := hasDefinition && hasStatefulRuleActions(ruleDef)
	//   func hasStatefulRuleActions(def Definition) bool {
	//       return len(def.OnEnter) > 0 || len(def.OnExit) > 0 || len(def.WhileTrue) > 0 || len(def.OnRecovery) > 0
	//   }
	// Anchor on the helper body and assert it STILL admits OnRecovery.
	const helperAnchor = "func hasStatefulRuleActions("
	start := strings.Index(src, helperAnchor)
	if start < 0 {
		t.Fatalf("could not find the %q gate helper in message_handler.go — the framework refactored "+
			"the rule-dispatch gate again; re-anchor this pin and re-verify BY HAND that an "+
			"on_recovery-only rule is still routed to the stateful evaluator (#530), else a restarted "+
			"run silently wedges", helperAnchor)
	}
	body := src[start:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	body = strings.TrimSpace(body)

	// Floor: the helper still gates on the entry lists we expect (a moved-shape guard).
	if !strings.Contains(body, "OnEnter") {
		t.Fatalf("the %q helper no longer references OnEnter (%q) — the gate changed shape; re-anchor "+
			"this pin and re-verify the on_recovery routing by hand", helperAnchor, body)
	}
	// The #530 fix: the helper must admit OnRecovery. If it does not, we have REGRESSED.
	if !strings.Contains(body, "OnRecovery") {
		t.Fatalf("REGRESSION (#530): the %q gate helper NO LONGER admits OnRecovery (%q). An "+
			"on_recovery-only park is inert again — a restarted in-flight run would silently wedge. "+
			"The gate fix landed in beta.147; restore it upstream (or re-block the restart-recovery "+
			"park and re-open #530) before relying on on_recovery.", helperAnchor, body)
	}
}

// readSemstreamsSource reads a source file from the compiled semstreams module in the
// module cache (the exact version go.mod pins), so a source-anchored tripwire inspects
// the framework code that actually runs. Fails loud (t.Fatal) on any miss.
func readSemstreamsSource(t *testing.T, parts ...string) string {
	t.Helper()
	const modPath = "github.com/c360studio/semstreams"

	// Resolve the pinned version from the repo's go.mod (the require line).
	goMod, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	version := ""
	for _, line := range strings.Split(string(goMod), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == modPath {
			version = f[1]
			break
		}
	}
	if version == "" {
		t.Fatalf("could not find the %s require version in go.mod — re-anchor this tripwire", modPath)
	}

	// Locate the module cache. GOMODCACHE wins; else <GOPATH>/pkg/mod.
	gomodcache := os.Getenv("GOMODCACHE")
	if gomodcache == "" {
		gomodcache = filepath.Join(build.Default.GOPATH, "pkg", "mod")
	}
	full := filepath.Join(append([]string{gomodcache, modPath + "@" + version}, parts...)...)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read semstreams source %s: %v — the module cache layout changed or the module "+
			"is vendored; re-anchor this tripwire so the on_recovery routing gap is not silently "+
			"lost", full, err)
	}
	return string(data)
}
