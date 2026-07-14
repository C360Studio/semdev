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

// TestTripwireOnRecoveryRoutingGate — semstreams on_recovery routing gap (R8;
// design group 7). The M0-correct posture for a restarted, still-in-flight run is
// a rule-native FAIL-CLOSED PARK: a rule with an EMPTY on_enter (so nothing fires
// during live operation, when a run is `executing` its whole working life) and the
// park in `on_recovery` (which the framework's bootstrap-recovery fork fires only
// after a restart, for a rule that was matching before the crash).
//
// LIMITATION (beta.146): that rule is INERT as wired. The rule Processor routes a
// rule to the stateful evaluator (where the on_recovery fork lives) ONLY when it
// has a non-empty on_enter/on_exit/while_true — the `hasStatefulActions` gate in
// processor/rule/message_handler.go EXCLUDES on_recovery. So an on_recovery-only
// rule is never evaluated: it persists no state during live operation and never
// fires on_recovery on restart. (A second issue compounds it: the stale-revision
// guard in stateful_evaluator.go, `prevState.SourceRevision >= ev.Revision`, likely
// suppresses the bootstrap recovery fork even if the gate were fixed — verify on a
// real restart when doing the upgrade.) There is no end-to-end test upstream proving
// on_recovery fires through the wired path. Per G2 (engine gap → file the upstream
// ask + park toward the human, never a silent Go reconciler), the restart-recovery
// park is DEFERRED and a restarted in-flight run wedges — a known, documented M0 gap.
//
// MECHANICAL UPGRADE when this goes RED: the gate now admits on_recovery. Add the
// run-lifecycle recovery-park rule (on_enter empty, on_recovery stamps
// run.awaiting_human + posts user.response, guarded on phase==executing / pr.ref
// absent / awaiting_human absent), re-add its shape pin + the sanctioned-park-writer
// entry, and drive the docker-gated restart-recovery station (design R10). First
// re-verify the stale-revision-guard interaction on a real restart.
//
// This tripwire reads the ACTUAL compiled framework source (the module cache) rather
// than reflecting, because the gate is an unexported local in message_handler.go with
// no exported surface. It FAILS LOUD if it cannot locate/parse the gate (so a moved
// file or refactor surfaces as a visible re-anchor task, never a silent green).
func TestTripwireOnRecoveryRoutingGate(t *testing.T) {
	src := readSemstreamsSource(t, "processor", "rule", "message_handler.go")

	// The routing gate. In beta.146 it reads:
	//   hasStatefulActions := hasDefinition && (len(ruleDef.OnEnter) > 0 || len(ruleDef.OnExit) > 0 || len(ruleDef.WhileTrue) > 0)
	// We anchor on the gate assignment and assert it does NOT yet consider OnRecovery.
	const anchor = "hasStatefulActions :="
	var gateLines []string
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, anchor) {
			gateLines = append(gateLines, strings.TrimSpace(line))
		}
	}
	if len(gateLines) == 0 {
		t.Fatalf("could not find the %q routing gate in message_handler.go — the framework "+
			"refactored the rule-dispatch path; re-anchor this tripwire against the new gate "+
			"before trusting it (the on_recovery routing gap must be re-verified by hand)", anchor)
	}
	// Sanity: the gate still gates on the stateful lists we expect (a floor — if these
	// vanished the gate changed shape and the tripwire's premise is stale).
	for _, g := range gateLines {
		if !strings.Contains(g, "OnEnter") {
			t.Fatalf("the %q gate no longer references OnEnter (%q) — the gate changed shape; "+
				"re-verify the on_recovery routing gap by hand before trusting this pin", anchor, g)
		}
	}
	for _, g := range gateLines {
		if strings.Contains(g, "OnRecovery") {
			t.Fatalf("TRIPWIRE FIRED (on_recovery routing gap): message_handler.go's "+
				"hasStatefulActions gate now admits OnRecovery (%q). A restarted in-flight run "+
				"can finally park rule-natively. Do the mechanical upgrade — add the "+
				"run-lifecycle recovery-park rule (on_enter empty + on_recovery park), re-add its "+
				"shape pin + sanctioned-park-writer entry, and drive the docker restart-recovery "+
				"station (R10). FIRST re-verify the stale-revision guard fires the recovery fork "+
				"on a real restart. Then remove this pin.", g)
		}
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
