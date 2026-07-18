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
// This file holds tripwires against the semstreams engine in TWO polarities:
//
//   REGRESSION GUARDS — an upstream fix LANDED, so the tripwire asserts the landed
//   capability is still present and FIRES (red) if a future beta drops it. #519
//   (scalar .value), #528 (per-spawn max_iterations), #529 (typed exhaustion
//   sentinel), and #530 (on_recovery routing gate) are all regression guards as of
//   beta.148/beta.147. The MECHANICAL UPGRADES they enable — per-task iteration
//   budgets (loop_max_iterations, #528), per-task attempt budgets via
//   $entity.triple.task.spec.budget.value (#519), and a reason-aware escalate route
//   (errors.Is ErrMaxIterationsReached, #529) — are routing-behavior changes NOT yet
//   adopted; each guard's doc names its follow-up. The current M0 behavior (uniform
//   iteration cap, literal-3 budget, outcome=failed routing) is proven by the e2e and
//   remains correct until those upgrades are deliberately taken.
//
//   GAP-OPEN TRIPWIRES — the upstream fix is STILL OPEN, so the tripwire is the
//   INVERSE: it asserts the GAP is still present (green while open) and FIRES (red)
//   when the framework CLOSES it — the signal to adopt the real fix in semdev and
//   FLIP the tripwire to a regression guard. TestTripwireExecutorHonorsPerLoopToolAllowlist
//   (the executor per-loop tool-enforcement ask, semdev MEDIUM-3) is the first of these.

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

// TestTripwireExecutorHonorsPerLoopToolAllowlist — the executor per-loop
// tool-enforcement ask, semstreams #551 (semdev MEDIUM-3). FILED upstream; this
// gap-open tripwire tracks the ask until the engine capability lands (the G2 /
// port-manifest "file the ask" step is done — the ask is recorded here and parked
// behind the mock backstop + the block on the first real-LLM token).
//
// A GAP-OPEN TRIPWIRE, the INVERSE polarity of the regression guards above: it
// asserts the gap is STILL present and FIRES when the framework CLOSES it.
//
// THE GAP (beta.148): a rule's per-spawn `tools` list is advertised to the MODEL
// (agentic-loop puts it in the request) but NEVER enforced at execution. The
// agentic-tools executor admits a tool call SOLELY on its global AllowedTools
// config — `handleToolCall` gates via `isToolAllowed(call.Name)`, and isToolAllowed
// reads only c.config.AllowedTools with no LoopID / per-loop set. So a loop
// advertised a narrow set (a coordinator routing loop: decide + read tools) whose
// model emitted a globally-allowlisted tool OUTSIDE that set (create_change, a
// forge-io writer) would have it EXECUTED. The narrow advertised list is the
// EFFECTIVE control at the model boundary (OpenAI/Anthropic only emit advertised
// tools); what is missing is the executor-side defense-in-depth BACKSTOP. semdev
// cannot close this in-tree: coordinator/developer/reviewer loops share ONE executor
// with ONE global allowlist (disjoint roles need disjoint tools), and the role-based
// category seam (config.EnableCategories / categories.go) is DEAD — declared, never
// wired into the execution gate.
//
// This asserts the global-only gate is STILL the whole story, across four anchors: the
// gate call site, the isToolAllowed signature + body, the dead EnableCategories seam, AND
// the ADR-067 dispatch-enforced metadata set (DispatchEnforcedMetadataKeys) — the
// framework's OWN precedent mechanism for per-task execution enforcement, and the most
// likely shape MEDIUM-3 closes through (a new tool-allowlist key there would leave the
// isToolAllowed anchors untouched). It TRIPS (fail) when any of these gains per-loop/role
// tool-name awareness — the signal to (1) scope the coordinator re-wakes' tools at the
// executor for real, and (2) flip this to a regression guard. It reads the compiled
// framework source and FAILS LOUD if an anchor moves, because a refactor could BE the fix.
func TestTripwireExecutorHonorsPerLoopToolAllowlist(t *testing.T) {
	src := readSemstreamsSource(t, "processor", "agentic-tools", "component.go")

	// Anchor 0 (call-site floor): the execution gate is still WIRED to isToolAllowed, so the
	// signature/body anchors below guard the LIVE gate, not dead code. If the call site
	// vanishes, the gate moved — re-verify by hand.
	if !strings.Contains(src, "isToolAllowed(call.Name)") {
		t.Fatal("component.go no longer gates via isToolAllowed(call.Name) — the tool-admission gate " +
			"moved, so the signature/body anchors below may now guard dead code. Re-verify MEDIUM-3 by " +
			"hand and re-anchor.")
	}

	// Anchor 1: the execution gate helper still exists and takes ONLY a tool NAME —
	// no loop id, no per-loop allowed-set, no context. A per-loop enforcement fix MUST
	// change this signature (or add a loop-scoped gate beside it).
	const gate = "func (c *Component) isToolAllowed(toolName string) bool"
	if !strings.Contains(src, gate) {
		t.Fatalf("agentic-tools gate %q not found in component.go — the framework refactored the "+
			"tool-admission gate. RE-VERIFY BY HAND whether it now honors the spawning loop's "+
			"advertised tool set (which would CLOSE MEDIUM-3): if so, scope the coordinator re-wakes' "+
			"tools at the executor and flip this tripwire to a regression guard; if it merely moved, "+
			"re-anchor.", gate)
	}

	// Anchor 2: the gate BODY reads the GLOBAL allowlist and references nothing
	// per-loop. Extract the helper body (gofmt puts the closing brace at column 0, so
	// the first "\n}" ends it) and assert the shape.
	body := src[strings.Index(src, gate):]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "AllowedTools") {
		t.Fatalf("the isToolAllowed gate no longer reads c.config.AllowedTools — the admission gate "+
			"changed shape; re-verify MEDIUM-3 by hand and re-anchor. Body:\n%s", body)
	}
	// The trip condition: any per-loop / per-role token entering the gate means the
	// executor may now scope tools beyond the global list — MEDIUM-3 may be CLOSED.
	for _, perLoop := range []string{"LoopID", "loopID", "Categor", "category", "advertised", "Advertised", "PerLoop", "perLoop"} {
		if strings.Contains(body, perLoop) {
			t.Fatalf("REACHED (MEDIUM-3): the isToolAllowed gate now references %q — the agentic-tools "+
				"executor appears to scope tools PER LOOP/ROLE, not just by the global AllowedTools. "+
				"VERIFY the spawning loop's advertised `tools` (or a role/category scope) is now ENFORCED "+
				"at execution; if so, scope the coordinator re-wakes' tools at the executor for real and "+
				"flip this tripwire to a regression guard (semstreams per-loop tool-enforcement ask "+
				"landed). Body:\n%s", perLoop, body)
		}
	}

	// Anchor 3 (floor + secondary trip): the role-based category seam is still DECLARED
	// but DEAD. If config.EnableCategories vanishes, the scoping story moved — re-verify.
	// If it appears in the EXECUTOR (component.go), the fix may be wiring it — trip.
	cfg := readSemstreamsSource(t, "processor", "agentic-tools", "config.go")
	if !strings.Contains(cfg, "EnableCategories") {
		t.Fatalf("agentic-tools config.go no longer declares EnableCategories — the role-based " +
			"tool-category seam moved or was removed; re-verify MEDIUM-3's scoping story by hand and re-anchor")
	}
	if strings.Contains(src, "EnableCategories") || strings.Contains(src, "GetToolCategory") {
		t.Fatal("REACHED (MEDIUM-3): agentic-tools component.go now references the category seam " +
			"(EnableCategories / GetToolCategory) in the execution path — role-based tool filtering may " +
			"be wired. Verify whether per-role tool scoping is now ENFORCED at execution; if so, adopt it " +
			"for the coordinator re-wakes and flip this tripwire to a regression guard.")
	}

	// Anchor 4 (the framework's OWN likely closing shape): semstreams enforces per-task execution
	// policy via ADR-067 dispatch-stamped metadata — DispatchEnforcedMetadataKeys in
	// agentic/exec_policy.go, the keys dispatchToolCall stamps AUTHORITATIVELY onto every ToolCall
	// and the executor then enforces (filesystem policy, scratch paths, decide-action allowlist). The
	// most likely way MEDIUM-3 closes is a NEW tool-name allowlist key added to THIS set + a gate in
	// handleToolCall — which would leave isToolAllowed's signature/body AND EnableCategories untouched
	// (Anchors 0-3 all stay green). So watch the set directly: it is EXACTLY the three known keys
	// today; a vanished key, a fourth key, or a tool-scoping key by name trips.
	policy := readSemstreamsSource(t, "agentic", "exec_policy.go")
	const sliceAnchor = "DispatchEnforcedMetadataKeys = []string{"
	si := strings.Index(policy, sliceAnchor)
	if si < 0 {
		t.Fatalf("agentic/exec_policy.go no longer declares %q — the ADR-067 dispatch-enforced metadata "+
			"mechanism moved or was renamed; a per-loop tool allowlist would most likely land in this set, "+
			"so re-verify MEDIUM-3's closing shape by hand and re-anchor.", sliceAnchor)
	}
	set := policy[si+len(sliceAnchor):]
	if end := strings.Index(set, "}"); end >= 0 {
		set = set[:end]
	}
	for _, known := range []string{"MetadataKeyFilesystemPolicy", "MetadataKeyScratchPaths", "MetadataKeyDecideActionAllowlist"} {
		if !strings.Contains(set, known) {
			t.Fatalf("DispatchEnforcedMetadataKeys no longer lists %q — the ADR-067 enforced-key set changed "+
				"shape; re-verify whether a per-loop TOOL allowlist key was added (which would CLOSE MEDIUM-3) "+
				"and re-anchor. Set:\n%s", known, set)
		}
	}
	if n := strings.Count(set, "MetadataKey"); n != 3 {
		t.Fatalf("REACHED (MEDIUM-3?): DispatchEnforcedMetadataKeys now has %d enforced keys, not the 3 known "+
			"(filesystem policy, scratch paths, decide-action allowlist). A NEW dispatch-enforced key landed — "+
			"if it scopes per-loop TOOL NAMES (the ADR-067 shape that closes MEDIUM-3), adopt it for the "+
			"coordinator re-wakes and flip this tripwire to a regression guard. Set:\n%s", n, set)
	}
	// Belt-and-suspenders: a tool-scoping enforcement key declared by NAME in the policy file or the
	// wire type, in case it lands before being added to the enforced set.
	wire := readSemstreamsSource(t, "agentic", "tools.go")
	for _, hay := range []string{policy, wire} {
		for _, tok := range []string{"MetadataKeyToolAllowlist", "MetadataKeyAllowedTools", "MetadataKeyToolScope", "MetadataKeyToolAllowed"} {
			if strings.Contains(hay, tok) {
				t.Fatalf("REACHED (MEDIUM-3): the framework declares %q — a per-loop tool-name enforcement key "+
					"exists. Verify it is stamped at dispatch and enforced by the executor; if so, scope the "+
					"coordinator re-wakes' tools for real and flip this tripwire to a regression guard.", tok)
			}
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
