package conformance

import (
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	gtypes "github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/processor/rule/expression"
)

// Upstream-ask tripwires (simplify-m0-execution-rail, task 1.2).
//
// The reshaped rail carries three deliberate interims because the semstreams
// engine cannot yet express the ideal form. Each interim is honest and
// functional (design R11), but each MUST be swapped for the real capability
// the moment the upstream fix lands — a mechanical upgrade that is easy to
// forget once the arc is green. These tests pin the CURRENT beta.146
// limitation so they go RED when the capability arrives, turning "someone has
// to remember" into a failing build that names the upgrade.
//
// A tripwire firing is not a regression — it is the signal to do the swap and
// then re-baseline (or delete) the pin. Each test's failure message says which.

// TestTripwire519ScalarValueSubstitution — semstreams #519
// (rule-scalar-value-substitution; fix drafted upstream, unmerged).
//
// LIMITATION: a rule condition's compare Value is a raw literal. There is no
// field-to-field form — you cannot write `value: "$entity.triple.X.value"` to
// compare one predicate against another predicate's value. This is why the
// reshaped rail hardcodes the attempt budget as the literal 3 in the route
// rules (design R3) instead of reading task.spec.0.budget off the run.
//
// MECHANICAL UPGRADE when this goes RED: replace the constant-3 budget
// literals in configs/rules/dev-from-task/* with
// `$entity.triple.task.spec.0.budget.value`, so per-task budgets from the
// projected contract become load-bearing (they are advisory today).
func TestTripwire519ScalarValueSubstitution(t *testing.T) {
	const sentinel = "MATCHES-ONLY-WHEN-519-LANDS"

	// An entity carrying two scalar predicates with the SAME object value.
	entity := &gtypes.EntityState{
		ID: "org.platform.agent.chain.execution.tripwire519",
		Triples: []message.Triple{
			{Predicate: "test.attempt.count", Object: sentinel, Confidence: 1.0},
			{Predicate: "test.budget", Object: sentinel, Confidence: 1.0},
		},
	}

	ev := expression.NewExpressionEvaluator()

	// POSITIVE CONTROL — a LITERAL RHS equal to the field value DOES match. This proves
	// the eq operator + field lookup actually work, so a `false` from the `.value` form
	// below genuinely means "not substituted" and not a rotted harness (a future beta
	// renaming `eq`, changing field resolution, or making Evaluate error would otherwise
	// return false for an unrelated reason and the tripwire would silently stay green when
	// #519 actually lands — the exact failure a canary exists to prevent).
	control := expression.LogicalExpression{
		Logic: "and",
		Conditions: []expression.ConditionExpression{{
			Field:    "test.attempt.count",
			Operator: "eq",
			Value:    sentinel,
		}},
	}
	if matched, err := ev.Evaluate(entity, control); err != nil || !matched {
		t.Fatalf("#519 positive control failed (matched=%v err=%v): the eq operator or "+
			"field lookup changed shape — re-verify this tripwire's anchor before trusting it", matched, err)
	}

	// Compare the first predicate against the SECOND predicate's value via the drafted
	// `.value` field-to-field form. Today the RHS is compared as the literal string
	// "$entity.triple.test.budget.value" (never substituted), so it cannot equal the
	// sentinel and the condition does NOT match. An error here is unexpected today (the
	// RHS is just an unmatched literal) — treat it as the anchor rotting, not a pass.
	expr := expression.LogicalExpression{
		Logic: "and",
		Conditions: []expression.ConditionExpression{{
			Field:    "test.attempt.count",
			Operator: "eq",
			Value:    "$entity.triple.test.budget.value",
		}},
	}
	matched, err := ev.Evaluate(entity, expr)
	if err != nil {
		t.Fatalf("#519 tripwire: evaluating the `.value` form errored (%v) — unexpected on "+
			"beta.146 (the RHS should be an inert literal); re-verify the anchor", err)
	}
	if matched {
		t.Fatal("TRIPWIRE #519 FIRED: rule conditions now resolve " +
			"`$entity.triple.X.value` field-to-field. Do the mechanical upgrade — " +
			"swap the constant-3 budget literals in configs/rules/dev-from-task/* " +
			"for `$entity.triple.task.spec.0.budget.value` — then update/remove this pin.")
	}
}

// TestTripwire528PerSpawnMaxIterations — semstreams #528 (per-spawn
// max_iterations, filed 2026-07-13).
//
// LIMITATION: the spawn wire (agentic.TaskMessage) carries no per-spawn loop
// iteration budget. It has MaxDepth (agent-tree depth) but no MaxIterations —
// the spawned loop's turn budget comes only from the agentic-loop COMPONENT
// config (uniform for every role). This is why the reshaped developer loop
// takes the uniform component-level cap at M0 (design R2) rather than a
// per-task budget.
//
// MECHANICAL UPGRADE when this goes RED: set the developer loop's iteration
// budget per spawn (publish_agent action) instead of the uniform component
// cap, so a hard task gets more turns than a trivial one.
func TestTripwire528PerSpawnMaxIterations(t *testing.T) {
	tm := reflect.TypeOf(agentic.TaskMessage{})

	// Sanity: we are reflecting the right type — the depth budget IS present.
	if _, ok := tm.FieldByName("MaxDepth"); !ok {
		t.Fatalf("agentic.TaskMessage no longer has MaxDepth — the spawn-wire shape "+
			"changed under this tripwire; re-verify #528's anchor before trusting it (type=%s)", tm)
	}

	for i := 0; i < tm.NumField(); i++ {
		f := tm.Field(i)
		name := strings.ToLower(f.Name)
		tag := strings.ToLower(f.Tag.Get("json"))
		if strings.Contains(name, "maxiteration") || strings.Contains(tag, "max_iteration") {
			t.Fatalf("TRIPWIRE #528 FIRED: agentic.TaskMessage now carries a "+
				"per-spawn iteration budget (field %q, json %q). Do the mechanical "+
				"upgrade — set the developer loop's max_iterations per publish_agent "+
				"spawn instead of the uniform component cap — then remove this pin.",
				f.Name, f.Tag.Get("json"))
		}
	}
}

// TestTripwire529UniformExhaustionReason — semstreams #529 (uniform exhaustion
// reason, filed 2026-07-13).
//
// LIMITATION: when a loop exhausts its iteration cap it terminates with the
// generic agentic.OutcomeFailed and a free-form Reason string — the SAME
// outcome a model error produces, and the reason text is not a stable,
// documented enum (the exhaustion path emits "max_iterations" in one place and
// "max iterations reached before tool results returned" in another). So the
// reshaped rail's retry/escalate routes key on outcome=failed only and cannot
// distinguish "ran out of turns" from "the model erred" (design R2 risk note).
//
// MECHANICAL UPGRADE when this goes RED: once exhaustion carries a uniform,
// typed signal, make the escalate route reason-aware (exhaustion → escalate
// toward the human; transient model error → retry within budget) instead of
// treating every failed outcome identically.
//
// COVERAGE CAVEAT (Go can't reflect package constants): the OTHER plausible #529
// shape is a NEW distinct outcome constant (e.g. agentic.OutcomeExhausted) rather
// than a LoopFailedEvent field. Anchor 3 reflects struct FIELDS and will not see a
// new constant, and there is no runtime way to enumerate a package's constants — so
// that shape is NOT auto-detected here. On every semstreams bump, re-read this ask
// manually: if exhaustion gained a distinct terminal outcome, do the upgrade above.
func TestTripwire529UniformExhaustionReason(t *testing.T) {
	// Anchor 1: the four outcome constants this rail routes over still EXIST (a
	// compile-time floor — it does NOT prove the set is exactly four; a new
	// OutcomeExhausted would compile fine here, see the coverage caveat above).
	_ = []string{
		agentic.OutcomeSuccess,
		agentic.OutcomeFailed,
		agentic.OutcomeCancelled,
		agentic.OutcomeTruncated,
	}

	lfe := reflect.TypeOf(agentic.LoopFailedEvent{})

	// Anchor 2: the failure cause is carried in a free-form Reason string — the
	// non-uniform channel we route AROUND today.
	reason, ok := lfe.FieldByName("Reason")
	if !ok || reason.Type.Kind() != reflect.String {
		t.Fatalf("agentic.LoopFailedEvent.Reason is no longer a free-form string "+
			"(ok=%v). The failure-cause channel changed shape — re-verify #529's "+
			"anchor and, if a uniform exhaustion reason landed, make the escalate "+
			"route reason-aware before re-baselining this pin (type=%s)", ok, lfe)
	}

	// Anchor 3: no typed failure-classifier field has appeared. The most likely
	// shape of a "uniform exhaustion reason" fix is a typed class/category/kind
	// field distinct from the free-form Reason. Its arrival trips the wire.
	classifierTokens := []string{"class", "category", "kind", "exhaust", "failuretype", "reasoncode"}
	for i := 0; i < lfe.NumField(); i++ {
		name := strings.ToLower(lfe.Field(i).Name)
		for _, tok := range classifierTokens {
			if strings.Contains(name, tok) {
				t.Fatalf("TRIPWIRE #529 FIRED: agentic.LoopFailedEvent gained a typed "+
					"failure classifier (field %q). Do the mechanical upgrade — make the "+
					"dev-loop escalate route reason-aware (exhaustion escalates; model "+
					"error retries) instead of routing on outcome=failed alone — then "+
					"remove this pin.", lfe.Field(i).Name)
			}
		}
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
