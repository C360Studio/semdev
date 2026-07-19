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
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"
)

// Upstream-ask tripwires (simplify-m0-execution-rail, task 1.2).
//
// This file holds tripwires against the semstreams engine in TWO polarities:
//
//   REGRESSION GUARDS — an upstream fix LANDED, so the tripwire asserts the landed
//   capability is still present and FIRES (red) if a future beta drops it. #519
//   (scalar .value), #528 (per-spawn max_iterations), #529 (typed exhaustion
//   sentinel), #530 (on_recovery routing gate), #551 (executor per-loop
//   advertised-tool enforcement, beta.149), and #566 (rule.Processor.Health/DataFlow
//   no longer mutate a shared cache under a read lock, beta.153) are all regression
//   guards. Of the MECHANICAL UPGRADES they enable, the #519 per-task ATTEMPT budget is now
//   ADOPTED (adopt-per-task-routing-budgets, with #568 length_gte): the retry/escalate routes
//   read $entity.triple.route.task.budget.value (the mirrored projected task.spec.budget)
//   instead of the constant 3. #529's reason-aware routing is ALSO now adopted
//   (adopt-reason-aware-escalate, via #569's agent.loop.terminal-reason fact —
//   TestTripwire569TerminalReasonFact): check_floors classifies the reason into the atomic
//   route.attempt.transient mirror flag, so a transient loop failure (model_error/handler_error)
//   gets bounded grace outside the convergence budget and the park carries the reason. Only #528's
//   per-task ITERATION budget (loop_max_iterations) remains a routing follow-up NOT yet adopted;
//   its guard's doc names it. The uniform iteration cap stays e2e-proven until #528 is taken. #551 and #566, by
//   contrast, were adopted on the bump alone — #551's already-scoped per-spawn `tools`
//   lists became load-bearing at execution, and #566's local-copy getters made the
//   -race e2e journeys reliably green — both with no rule/config change.
//
//   GAP-OPEN TRIPWIRES — when an upstream fix is STILL OPEN, the tripwire is the
//   INVERSE: it asserts the GAP is still present (green while open) and is meant to FIRE
//   (red) when the framework CLOSES it — the signal to adopt the real fix and FLIP the
//   tripwire to a regression guard. Currently open: none — #566 closed in beta.153 (the
//   getters now derive into a local copy under RLock; TestTripwireProcessorHealthRaceUnfixed
//   flipped to a regression guard, -race journeys re-enabled). LESSON from #551: the gap-open
//   form did NOT auto-trip when the fix landed (the fix's key name + admission seam were
//   outside the anchors' watch), so this polarity is a re-check HINT on a bump, not a
//   guarantee the close is caught.

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
// MECHANICAL UPGRADE — ADOPTED (adopt-per-task-routing-budgets, with #568 length_gte): the
// constant-3 attempt-budget literals in configs/rules/dev-from-task/{06c,06d,07b,07c} are now
// `$entity.triple.route.task.budget.value` — the route-mirror copies the projected
// task.spec.budget onto the firing loop and the retry/escalate routes substitute it, so the
// per-task budget is load-bearing (it was advisory before). The substitution reads the
// route-mirror predicate, not task.spec.budget directly, because .value is firing-entity-only
// and the routes fire on L_n / the review loop, not the run. See
// TestFloorsRouteTotalityAndSelfExtinguish / TestReviewRouteTotalityAndSelfExtinguish.
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

// TestTripwire568LengthGteLteOperators — semstreams #568 (the array/length operator family
// gains length_gte / length_lte, completing lt/lte/gt/gte to match the numeric family). A
// REGRESSION GUARD: the fix LANDED in beta.153. adopt-per-task-routing-budgets DEPENDS on
// length_gte — the escalate/park routes fire on `route.attempt.instance length_gte
// $entity.triple.route.task.budget.value`; without it the ≥B boundary has no clean operator
// (the design rejected the B-1/rule-split workarounds as cruft), so the retry/escalate
// partition cannot be expressed. Source-anchored on the compiled framework: the operator
// CONSTANTS in expression/types.go AND their registration/evaluation in evaluator.go. FIRES if
// a future beta drops either — the routing rules would then silently stop matching (a coerce
// error the evaluator swallows), stalling every not-clean attempt.
func TestTripwire568LengthGteLteOperators(t *testing.T) {
	types := readSemstreamsSource(t, "processor", "rule", "expression", "types.go")
	evaluator := readSemstreamsSource(t, "processor", "rule", "expression", "evaluator.go")
	for _, op := range []struct{ constName, literal string }{
		{"OpLengthGte", `"length_gte"`},
		{"OpLengthLte", `"length_lte"`},
	} {
		if !strings.Contains(types, op.constName) || !strings.Contains(types, op.literal) {
			t.Fatalf("REGRESSION (#568): expression/types.go no longer declares %s = %s — the "+
				"length-operator family lost a ≥/≤ variant. The escalate/park routes "+
				"(route.attempt.instance length_gte $…route.task.budget.value) can no longer express "+
				"the per-task budget boundary; restore upstream or re-open #568.", op.constName, op.literal)
		}
		// The constant existing but never registered in the evaluator's operator map would let a
		// rule reference it yet never match — worse than a hard absence. Anchor the registration too.
		if !strings.Contains(evaluator, op.constName) {
			t.Fatalf("REGRESSION (#568): expression/evaluator.go no longer references %s — the operator "+
				"constant exists but is not registered/evaluated, so a rule using it fails to match "+
				"(a swallowed coerce error → no-match). Restore upstream or re-open #568.", op.constName)
		}
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
// MECHANICAL UPGRADE — ADOPTED (adopt-reason-aware-escalate, with #569): the dev-loop routing
// is now reason-aware. The Go sentinel is not read by a rule directly (rules read facts, G2);
// instead #569 surfaces the classified reason as the graph fact agent.loop.terminal-reason
// (guarded below by TestTripwire569TerminalReasonFact), which check_floors classifies into the
// ATOMIC route.attempt.transient mirror flag (a rule-stamped collapse would race the convergence
// routes' exclusion — the pinned double-dispatch) so a transient model/handler error gets bounded
// grace outside the convergence budget and the escalate/park carries the reason. This sentinel
// guard stays: it protects the max_iterations classification the reason fact is derived from.
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

// TestTripwire569TerminalReasonFact — semstreams #569 (the loop's classified terminal reason
// stamped as a rule-readable graph fact, landed beta.153). A REGRESSION GUARD:
// adopt-reason-aware-escalate DEPENDS on it — a rule can only read facts (G2), not a Go
// errors.Is sentinel, so buildLoopFailureTriples surfacing LoopFailedEvent.Reason as
// agent.loop.terminal-reason is what lets check_floors classify WHY a developer loop failed
// (the atomic route.attempt.transient mirror flag) and the escalate/park messages quote it. Drop it and every loop failure collapses back to outcome=failed alone —
// transient grace silently stops working (no route.attempt.transient ever stamped) and a flaky
// endpoint burns the convergence budget again.
//
// Source-anchored on the compiled framework: the exported vocab constant AND the stamping site in
// graph_writer.go (guarded by `event.Reason != ""`). FIRES if either moves.
func TestTripwire569TerminalReasonFact(t *testing.T) {
	// Anchor 1: the vocab constant exists and is the canonical 3-seg predicate the rules bind.
	if agvocab.LoopTerminalReason != "agent.loop.terminal-reason" {
		t.Fatalf("REGRESSION (#569): agvocab.LoopTerminalReason = %q, want \"agent.loop.terminal-reason\" — "+
			"check_floors reads that predicate for the transient classification (compile-anchored via "+
			"agvocab) and the escalate/park messages substitute it; a rename breaks reason-aware "+
			"routing. Re-anchor the readers and this guard.", agvocab.LoopTerminalReason)
	}
	// Anchor 2: the stamping site still surfaces the classified reason from the failure event.
	src := readSemstreamsSource(t, "processor", "agentic-loop", "graph_writer.go")
	if !strings.Contains(src, "agvocab.LoopTerminalReason") || !strings.Contains(src, "event.Reason") {
		t.Fatal("REGRESSION (#569): graph_writer.go no longer stamps agvocab.LoopTerminalReason from " +
			"event.Reason — the classified terminal reason is not reaching the graph, so a rule cannot " +
			"read it. Reason-aware routing (transient grace + the reason-aware park) is inert. Restore " +
			"upstream or re-open #569.")
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
// tool-enforcement ask, semstreams #551 (semdev MEDIUM-3). Now a REGRESSION GUARD:
// the #551 fix LANDED in beta.149. It was a GAP-OPEN tripwire until this bump.
//
// THE FIX (beta.149, exactly the ask's option 1): the loop's per-spawn advertised
// `tools` set — cached at spawn from the rule's publish_agent `tools` list — is now
// enforced at EXECUTION, not just advertised to the model. agentic-loop's
// dispatchToolCall stamps agentic.MetadataKeyAdvertisedTools ("agent.tools.advertised")
// onto every ToolCall AUTHORITATIVELY from LoopManager.GetCachedTools (like the RunID
// stamp; deliberately NOT a DispatchEnforcedMetadataKeys member because it comes from the
// tools CACHE, not cached task metadata). The agentic-tools executor's admitToolCall then
// runs TWO layers: the global AllowedTools (unchanged), then the per-loop advertised set
// via agentic.AdvertisedToolsFromMetadata — key absent → no per-loop check (back-compat);
// present-but-empty/malformed → fail closed; name not in set → reject with
// ToolErrorPermission ("not permitted for this loop"), distinct from the global
// ToolErrorNotFound. So semdev's already-scoped `tools` lists became load-bearing at
// execution ON THE BUMP ALONE — no rule/config change (every semdev spawn advertises a
// non-empty set; TestEveryModelSpawnDeclaresToolsAllowlist).
//
// LESSON (why this flip was DRIVER-INITIATED, not tripwire-caught): the gap-open form did
// NOT auto-trip on beta.149. The fix used a metadata key (MetadataKeyAdvertisedTools) and
// an admitToolCall seam the anchors did not watch, was kept OUT of
// DispatchEnforcedMetadataKeys, and left isToolAllowed unchanged (now called inside
// admitToolCall). A gap-open tripwire is a hint to re-check on a bump, never a guarantee
// the close is caught — so this guard's coverage is behavioral, not gap-shaped.
//
// As a REGRESSION GUARD this asserts the landed capability is still present — the exported
// resolver's contract (behavioral), the metadata key, the executor's enforcement seam, and
// the dispatch stamp — and FIRES if a future beta drops any of them (which would silently
// re-open MEDIUM-3: semdev's scoped tools lists back to advertise-only).
func TestTripwireExecutorHonorsPerLoopToolAllowlist(t *testing.T) {
	// Anchor 1 (behavioral — the strongest): the exported resolver's present/absent/empty
	// contract, on which admitToolCall's back-compat-vs-fail-closed branching depends.
	if _, present := agentic.AdvertisedToolsFromMetadata(nil); present {
		t.Fatal("REGRESSION (#551): AdvertisedToolsFromMetadata(nil) reports an advertised set present — " +
			"the key-absent path is broken; the executor would spuriously enforce over nil metadata.")
	}
	if _, present := agentic.AdvertisedToolsFromMetadata(map[string]any{"unrelated": 1}); present {
		t.Fatal("REGRESSION (#551): AdvertisedToolsFromMetadata reports present when the advertised-tools " +
			"key is absent — back-compat (unrestricted) loops would be enforced against a phantom set.")
	}
	got, present := agentic.AdvertisedToolsFromMetadata(map[string]any{
		agentic.MetadataKeyAdvertisedTools: []any{"decide", "query_entity"},
	})
	if !present || len(got) != 2 || got[0] != "decide" || got[1] != "query_entity" {
		t.Fatalf("REGRESSION (#551): a stamped advertised set no longer resolves via "+
			"AdvertisedToolsFromMetadata — got %v present=%v; the executor cannot read the loop's "+
			"advertised tools, so per-loop enforcement is dead.", got, present)
	}
	// Present-but-empty MUST report (empty, true) so admitToolCall fails CLOSED (rejects all)
	// rather than degrading to permissive — the IsKnownFilesystemPolicy precedent.
	if empty, present := agentic.AdvertisedToolsFromMetadata(map[string]any{
		agentic.MetadataKeyAdvertisedTools: []any{},
	}); !present || len(empty) != 0 {
		t.Fatalf("REGRESSION (#551): a present-but-empty advertised set no longer reports (empty, true) — "+
			"got %v present=%v; the fail-closed contract is broken and a malformed security-control value "+
			"could degrade to permissive.", empty, present)
	}

	// Anchor 2: the executor's admission seam still ENFORCES the advertised set. admitToolCall
	// must exist, consult AdvertisedToolsFromMetadata, and reject with the per-loop permission
	// error. A drop back to a bare global-only gate re-opens the gap.
	comp := readSemstreamsSource(t, "processor", "agentic-tools", "component.go")
	for _, anchor := range []string{"admitToolCall", "AdvertisedToolsFromMetadata", "ToolErrorPermission"} {
		if !strings.Contains(comp, anchor) {
			t.Fatalf("REGRESSION (#551): agentic-tools component.go no longer references %q — the executor's "+
				"per-loop advertised-set enforcement (admitToolCall) was dropped; it is back to global-only "+
				"gating and semdev's scoped tools lists are advertise-only again. Restore upstream or re-open #551.", anchor)
		}
	}

	// Anchor 3: the DISPATCH side still stamps the advertised set from the loop's tools cache —
	// without this stamp the executor receives no set to enforce. Both tokens must be present in
	// the dispatchToolCall path.
	disp := readSemstreamsSource(t, "processor", "agentic-loop", "handlers.go")
	for _, anchor := range []string{"MetadataKeyAdvertisedTools", "GetCachedTools"} {
		if !strings.Contains(disp, anchor) {
			t.Fatalf("REGRESSION (#551): agentic-loop handlers.go no longer references %q — dispatch no longer "+
				"stamps the loop's advertised tool set onto tool calls, so the executor has nothing to enforce "+
				"and per-loop scoping is inert. Restore upstream or re-open #551.", anchor)
		}
	}
}

// TestTripwireProcessorHealthRaceFixed — semstreams #566 (rule.Processor.Health()
// and DataFlow() mutated shared struct fields while holding only a sync.RWMutex READ
// lock, so concurrent callers — the ComponentManager health-publish loop and any
// GetHealthyComponents() query — raced on the write). Now a REGRESSION GUARD: the fix
// LANDED in beta.153. It was a GAP-OPEN tripwire until this bump.
//
// The landed fix keeps the read lock but derives into a LOCAL copy: `health := rp.health`
// / `metrics := rp.flowMetrics`, then mutates the copy and returns it — so nothing writes
// the shared cache under RLock. Verified behaviorally: the -race e2e journeys, ~50% flaky
// while #566 was open (the race tripped requireAgenticHealthy's GetHealthyComponents poll
// during startup), ran green three times in a row on beta.153. `task e2e` runs -race again.
//
// As a REGRESSION GUARD this asserts, for BOTH getters, that (a) the local-copy idiom is
// present and (b) the racy shared-field WRITE is absent — and FIRES (red) if a future beta
// reintroduces the RLock+shared-write shape. Source-anchored on the compiled framework;
// fails loud if an anchor moves. CAVEAT: an alternative valid fix (RLock→Lock, keeping the
// shared write) would trip anchor (b) as a false positive — that is a deliberate lock-model
// change worth a hand re-verify + re-anchor, which the failure message calls out.
func TestTripwireProcessorHealthRaceFixed(t *testing.T) {
	src := readSemstreamsSource(t, "processor", "rule", "processor.go")

	// The beta.150 race shape was: RLock held while the body wrote the cached struct
	// (rp.health.LastCheck = …, rp.flowMetrics.<field> = …). The beta.153 fix copies the
	// cache into a local first, then mutates the local. Guard: local-copy present, racy
	// shared-write absent.
	for _, g := range []struct{ anchor, localCopy, racyWrite string }{
		{"func (rp *Processor) Health()", "health := rp.health", "rp.health.LastCheck"},
		{"func (rp *Processor) DataFlow()", "metrics := rp.flowMetrics", "rp.flowMetrics."},
	} {
		start := strings.Index(src, g.anchor)
		if start < 0 {
			t.Fatalf("rule.Processor getter %q not found in processor.go — the framework refactored it; "+
				"re-verify BY HAND whether the #566 race stays fixed (getters must not write the shared "+
				"health/flow cache under a read lock) and re-anchor.", g.anchor)
		}
		body := src[start:]
		if end := strings.Index(body, "\n}"); end >= 0 {
			body = body[:end]
		}
		if !strings.Contains(body, g.localCopy) {
			t.Fatalf("REGRESSION (#566): rule.Processor getter %q no longer derives into a local copy "+
				"(%q absent) — the fix that returns a copied struct instead of the shared cache may have "+
				"been reverted or refactored. Re-verify the getter cannot write the shared cache under "+
				"RLock, then re-anchor. Body:\n%s", g.anchor, g.localCopy, body)
		}
		if strings.Contains(body, g.racyWrite) {
			t.Fatalf("REGRESSION (#566): rule.Processor getter %q writes the shared cache (%q) again while "+
				"holding a read lock — the beta.150 data race is back and the -race e2e journeys will flake. "+
				"Restore the local-copy fix upstream or re-open #566. (If the lock was intentionally promoted "+
				"RLock→Lock, this write is safe — re-verify by hand and re-anchor.) Body:\n%s",
				g.anchor, g.racyWrite, body)
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
