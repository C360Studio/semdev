package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/conversationintent"
)

// The conversation classifier rule pack (nl-conversation-intent group 4, design
// D3/D5/D6): the rule-side half of the NL approval lane. Five rules under
// configs/rules/conversation/:
//
//   - 01 anchor: a run entity NEVER carries a bare agent.loop.run triple until
//     dev-from-task/01 stamps it at executing+approved, so an inherit spawn at
//     awaiting_approval would bind NO RunID and the classifier's
//     classify_intent would fault on a missing run id. The anchor is stamped in
//     its own rule (the dev-from-task/01 two-rule split: publish_agent inherit
//     reads the entity SNAPSHOT, so a same-rule add_triple is invisible to it).
//   - 02 spawn: pending message + gated phase + anchor + fire-once marker →
//     inherit-spawn the role=conversation classifier loop with the pending
//     body/author templated onto its prompt.
//   - 03a/03b intent routes: conversation.intent.value approve/reject on a
//     still-gated run (both gate facts absent, H4a) → publish the deterministic
//     apply consumer (the R6 station shape). `none` deliberately routes nowhere.
//   - 04 terminal-release: the classifier loop terminal clears the pending slot
//     and THEN the spawn marker, releasing the one-at-a-time serialization so
//     the NEXT authorized message can classify (journey 6.4 needs two
//     classifications on one run) — for BOTH a clean terminal and a fault (a
//     fault must not brick the NL lane, and must not respawn into a paid-token
//     storm either: clearing pending closes the respawn trigger).
const (
	anchorRuleID   = "conversation_anchor_gated_run"
	spawnRuleID    = "conversation_spawn_classifier"
	approveRouteID = "conversation_route_intent_approve"
	rejectRouteID  = "conversation_route_intent_reject"
	releaseRuleID  = "conversation_classifier_terminal_release"

	applySubject    = "component.conversation-apply.dispatch"
	runSubjectToken = "$entity.triple.agent.run.entity-id"
)

// classifierMarker is the spawn rule's fire-once marker — the domain package's
// constant so the rules, the tool's read-once binding check, and these pins
// share one source of truth.
const classifierMarker = conversationintent.ClassifierDispatchedPredicate

// uncapped reports whether the action explicitly opts out of the engine's
// per-action firing cap (max_iterations: 0). nil (field omitted) means the
// DEFAULT cap of 3 per rule+entity applies — fatal for a rule designed to
// re-fire indefinitely on one run entity (grp4-review H1).
func uncapped(a ruleAction) bool {
	return a.MaxIterations != nil && *a.MaxIterations == 0
}

var conversationRuleFiles = []string{
	"rules/conversation/01-anchor-gated-run.json",
	"rules/conversation/02-spawn-classifier.json",
	"rules/conversation/03a-route-intent-approve.json",
	"rules/conversation/03b-route-intent-reject.json",
	"rules/conversation/04-classifier-terminal-release.json",
}

// conversationPackRules returns the parsed rules under configs/rules/conversation/.
func conversationPackRules(t *testing.T) []ruleFile {
	t.Helper()
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	var out []ruleFile
	for _, r := range rules {
		if strings.Contains(filepath.ToSlash(r.path), "/rules/conversation/") {
			out = append(out, r)
		}
	}
	return out
}

// removesPredicateOn reports whether the rule has a remove_triple for predicate
// with the given subject.
func (r ruleFile) removesPredicateOn(subject, predicate string) bool {
	for _, a := range r.OnEnter {
		if a.Type == "remove_triple" && a.Subject == subject && a.Predicate == predicate {
			return true
		}
	}
	return false
}

// 4.1 — the classifier rule pack is present, bootstrapped, and structurally
// sound. A silently-unloaded rule is the pinned failure shape
// (TestEveryRuleFileIsBootstrapped's class): a rule file that parses green in
// every census but is missing from configs/semdev-bootstrap.json never fires.
func TestBootstrapWiresConversationClassifierRules(t *testing.T) {
	root := repoRoot(t)

	// Every pack file is LISTED in the bootstrap rules_files (the runtime loads
	// only listed files).
	cfg, err := loadBootstrap(root)
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}
	listed := make(map[string]bool)
	for _, rel := range cfg.Components.Rule.Config.RulesFiles {
		listed[filepath.ToSlash(rel)] = true
	}
	for _, rel := range conversationRuleFiles {
		if !listed[rel] {
			t.Errorf("conversation rule file %q is not in the bootstrap rules_files — the runtime never loads it and the NL lane silently never fires", rel)
		}
	}

	// The spawned classify_intent tool must be in the agentic-tools allowlist or
	// the classifier loop boots and dies at runtime with "tool not allowed".
	if !slices.Contains(cfg.Components.AgenticTools.Config.AllowedTools, "classify_intent") {
		t.Error("classify_intent missing from the bootstrap agentic-tools allowed_tools — the spawned classifier loop would fail at runtime")
	}

	// The model capability the spawn names must exist in the model_registry:
	// Resolve() on an unknown capability silently falls back to defaults.model,
	// which works under the mock but mis-routes the classifier unnoticed in a
	// real deployment (the OQ4 tier decision needs a named knob to land on).
	var reg struct {
		ModelRegistry struct {
			Capabilities map[string]json.RawMessage `json:"capabilities"`
		} `json:"model_registry"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "configs", "semdev-bootstrap.json"))
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("parse bootstrap model_registry: %v", err)
	}
	if _, ok := reg.ModelRegistry.Capabilities["conversation"]; !ok {
		t.Error("model_registry.capabilities has no \"conversation\" entry — the classifier spawn would silently fall back to defaults.model")
	}

	// Pack-wide invariants: enabled, entity-pattern-bearing (required to fire on
	// the entity-state lane since beta.147), NO lifecycle transition anywhere
	// (G2 — task 4.2), and the spawn is the pack's ONLY model-turn source.
	pack := conversationPackRules(t)
	if len(pack) != len(conversationRuleFiles) {
		t.Fatalf("expected %d rules under configs/rules/conversation/, found %d", len(conversationRuleFiles), len(pack))
	}
	for _, r := range pack {
		if !r.Enabled {
			t.Errorf("rule %s is disabled", r.ID)
		}
		if r.Entity.Pattern != "*.*.*.*.*.*" {
			t.Errorf("rule %s has entity.pattern %q — a rule without the entity pattern never fires on the entity-state lane", r.ID, r.Entity.Pattern)
		}
		if r.firesTransition() {
			t.Errorf("rule %s fires a lifecycle_transition — the conversation pack owns NO transition (G2); the reject→cancel rule is run-lifecycle's (group 5)", r.ID)
		}
		spawns := false
		for _, a := range r.OnEnter {
			if a.Type == "publish_agent" {
				spawns = true
			}
		}
		if spawns && r.ID != spawnRuleID {
			t.Errorf("rule %s spawns a model loop — only %s may (one model turn per distinct message, D9)", r.ID, spawnRuleID)
		}
	}
}

// 01 anchor + 02 spawn: the two-rule inherit split. The anchor stamps the bare
// agent.loop.run on the GATED run so the spawn's run_scope=inherit can bind (a
// run entity otherwise never carries it before dev-from-task/01 fires at
// executing+approved); the spawn conditions on it and dispatches the classifier.
func TestClassifierSpawnRuleContract(t *testing.T) {
	rules := runLifecycleRules(t)

	anchor, ok := rules[anchorRuleID]
	if !ok {
		t.Fatalf("missing %s rule", anchorRuleID)
	}
	if c, ok := anchor.condition("agent.run.phase"); !ok || c.Operator != "eq" || c.Value != "awaiting_approval" {
		t.Errorf("anchor must fire at agent.run.phase == awaiting_approval, got %+v", c)
	}
	if !anchor.hasAbsenceGuard("agent.loop.run") {
		t.Error("anchor must guard on agent.loop.run length_eq 0 (fire-once; dev-from-task/01 then simply never fires, same value)")
	}
	if !anchor.hasTriple("agent.loop.run") {
		t.Error("anchor must add_triple agent.loop.run on the run — publish_agent inherit reads exactly this")
	}
	for _, a := range anchor.OnEnter {
		if a.Type == "add_triple" && a.Predicate == "agent.loop.run" {
			if a.Subject != "$entity.id" || a.Object != "$entity.instance" {
				t.Errorf("anchor must stamp agent.loop.run = $entity.instance on $entity.id (the bare run id, the dev-from-task/01 shape), got subject=%q object=%q", a.Subject, a.Object)
			}
		}
	}

	// 02 spawn: pending + gated + ANCHORED + marker-guarded → inherit-spawn the
	// classifier with the pending body/author templated onto the prompt.
	spawn, ok := rules[spawnRuleID]
	if !ok {
		t.Fatalf("missing %s rule", spawnRuleID)
	}
	if c, ok := spawn.condition("conversation.pending.message-id"); !ok || c.Operator != "ne" || c.Value != "" {
		t.Errorf("spawn must require a pending message (conversation.pending.message-id ne \"\"), got %+v", c)
	}
	if c, ok := spawn.condition("agent.run.phase"); !ok || c.Operator != "eq" || c.Value != "awaiting_approval" {
		t.Errorf("spawn must be phase-gated to awaiting_approval (classification is scoped to the approval gate), got %+v", c)
	}
	if c, ok := spawn.condition("agent.loop.run"); !ok || c.Operator != "ne" || c.Value != "" {
		t.Errorf("spawn must require the agent.loop.run anchor (ne \"\") — without it run_scope=inherit binds NO RunID and classify_intent faults on a missing run id, got %+v", c)
	}
	if !spawn.hasAbsenceGuard(classifierMarker) || !spawn.hasTriple(classifierMarker) || !spawn.markerBeforePublish(classifierMarker) {
		t.Errorf("spawn must be self-extinguishing: %s length_eq 0 guard + add_triple stamped BEFORE the publish (publish_agent is not idempotent; the run is long-lived and replay-exposed)", classifierMarker)
	}
	// Gate-facts guards (grp4-review M1): a run that already carries a gate fact
	// while still at awaiting_approval (the D7 both-facts park; the
	// publish→stamp window) must not spend a classifier turn whose intent can
	// never route.
	if !spawn.hasAbsenceGuard("run.change.approved") || !spawn.hasAbsenceGuard("run.change.rejected") {
		t.Error("spawn must require BOTH gate facts absent (run.change.approved/rejected length_eq 0) — a gate-fact-bearing run's classification is a guaranteed-dead paid turn")
	}
	// Firing-cap opt-out (grp4-review H1): the engine's default per-action cap
	// (3 per rule+entity, RULE_STATE-persisted) would silently skip the marker
	// stamp AND the spawn on the run's 4th authorized message — pending stamped,
	// no classifier, no fault, no note. Every action of the ONE rule in the repo
	// designed to re-fire indefinitely on one entity must opt out explicitly.
	for _, a := range spawn.OnEnter {
		if !uncapped(a) {
			t.Errorf("spawn action %q must carry explicit max_iterations: 0 — the default per-action firing cap (3) silently kills the NL lane on the run's 4th message", a.Type)
		}
	}
	for _, a := range spawn.OnEnter {
		if a.Type == "add_triple" && a.Predicate == classifierMarker {
			if a.Object != "$entity.triple.conversation.pending.message-id" {
				t.Errorf("spawn marker object must be the pending message id (D5: the marker names WHICH message a classifier is in flight for), got %q", a.Object)
			}
		}
	}
	var spawnAgent *ruleAction
	for i, a := range spawn.OnEnter {
		if a.Type == "publish_agent" {
			spawnAgent = &spawn.OnEnter[i]
		}
	}
	if spawnAgent == nil {
		t.Fatal("spawn rule has no publish_agent action")
	}
	if spawnAgent.Role != "conversation" {
		t.Errorf("classifier spawn role must be \"conversation\" (the persona fragment binding key), got %q", spawnAgent.Role)
	}
	if spawnAgent.RunScope != "inherit" {
		t.Errorf("classifier spawn must use run_scope=inherit (binds the loop to the gated run so classify_intent resolves it), got %q", spawnAgent.RunScope)
	}
	if !slices.Equal(spawnAgent.Tools, []string{"classify_intent"}) {
		t.Errorf("classifier spawn must advertise EXACTLY [classify_intent] (the beta.149 executor enforces the advertised set — the scoped list is the whole tool surface), got %v", spawnAgent.Tools)
	}
	if spawnAgent.ToolChoice.Mode != "required" {
		t.Errorf("classifier spawn must force a tool call (tool_choice mode=required — a weak model must not terminate text-only, D2), got %q", spawnAgent.ToolChoice.Mode)
	}
	for _, token := range []string{
		"$entity.triple.conversation.pending.author",
		"$entity.triple.conversation.pending.body",
	} {
		if !strings.Contains(spawnAgent.Prompt, token) {
			t.Errorf("classifier prompt must template %s (D3: the single triggering message's author + body are the classifier's whole input)", token)
		}
	}
}

// 03a/03b intent routes: approve/reject on a still-gated run → publish the
// apply consumer. Gated on BOTH gate facts absent (H4a: two racing
// classifications cannot both stamp; the consumer's gate-still-open re-check
// is the serializer). The gate fact itself extinguishes the trigger, so no
// marker — the publish is idempotent at the consumer (Post-then-stamp
// at-least-once, M6), the coordinator/03 station posture.
func TestIntentRoutesDispatchApplyConsumer(t *testing.T) {
	rules := runLifecycleRules(t)
	for id, want := range map[string]string{approveRouteID: "approve", rejectRouteID: "reject"} {
		route, ok := rules[id]
		if !ok {
			t.Fatalf("missing %s rule", id)
		}
		c, ok := route.condition("conversation.intent.value")
		if !ok || c.Operator != "eq" || c.Value != want {
			t.Errorf("%s must fire on conversation.intent.value eq %q, got %+v", id, want, c)
		}
		if c.Required {
			t.Errorf("%s intent condition must be required:false — a scalar eq with required:true ERRORS on every entity lacking the field", id)
		}
		if c, ok := route.condition("agent.run.phase"); !ok || c.Operator != "eq" || c.Value != "awaiting_approval" {
			t.Errorf("%s must be phase-gated to awaiting_approval, got %+v", id, c)
		}
		if !route.hasAbsenceGuard("run.change.approved") || !route.hasAbsenceGuard("run.change.rejected") {
			t.Errorf("%s must require BOTH gate facts absent (run.change.approved/rejected length_eq 0, H4a) — and the gate fact landing is what extinguishes the trigger", id)
		}
		if !route.publishesTo(applySubject) {
			t.Errorf("%s must publish %s (the deterministic apply consumer; the run is the dispatch entity_id — the rule fires on the run)", id, applySubject)
		}
		// Firing-cap opt-out (grp4-review H1): the route legitimately re-fires on
		// one run across the gate lifetime (each fresh classification is an edge);
		// the default cap of 3 would silently stop routing on the 4th.
		for _, a := range route.OnEnter {
			if a.Type == "publish" && !uncapped(a) {
				t.Errorf("%s publish must carry explicit max_iterations: 0 — the default per-action firing cap (3) silently stops routing on the run's 4th classification", id)
			}
		}
	}
}

// 04 terminal-release: the classifier loop terminal (clean OR faulted) clears
// the pending slot and the spawn marker on the RUN, re-arming the spawn for the
// NEXT message. ORDER IS LOAD-BEARING: every pending removal must precede the
// marker removal — the engine writes each action as its own KV revision, so
// marker-first would expose a revision where pending is still present and the
// marker absent, and the spawn rule would re-fire on the ALREADY-classified
// message (a duplicate paid turn).
func TestClassifierTerminalReleaseContract(t *testing.T) {
	rules := runLifecycleRules(t)

	release, ok := rules[releaseRuleID]
	if !ok {
		t.Fatalf("missing %s rule", releaseRuleID)
	}
	if c, ok := release.condition("agent.loop.role"); !ok || c.Operator != "eq" || c.Value != "conversation" {
		t.Errorf("release must fire on the classifier loop (agent.loop.role eq conversation), got %+v", c)
	}
	if c, ok := release.condition("agent.loop.outcome"); !ok || c.Operator != "ne" || c.Value != "" {
		t.Errorf("release must fire on ANY terminal (agent.loop.outcome ne \"\") — a FAULTED classifier must release the lane too (a fault must not brick NL for the run), got %+v", c)
	}
	if c, ok := release.condition("agent.run.entity-id"); !ok || c.Operator != "ne" || c.Value != "" {
		t.Errorf("release must require agent.run.entity-id (ne \"\") — its removals subject-override to the run, and substitution fails open on absence, got %+v", c)
	}
	pendingPreds := []string{
		conversationintent.PendingMessageIDPredicate,
		conversationintent.PendingAuthorPredicate,
		conversationintent.PendingBodyPredicate,
	}
	for _, pred := range pendingPreds {
		if !release.removesPredicateOn(runSubjectToken, pred) {
			t.Errorf("release must remove_triple %s on %s — a surviving pending slot re-triggers the spawn the moment the marker clears (an infinite paid-classification loop)", pred, runSubjectToken)
		}
	}
	if !release.removesPredicateOn(runSubjectToken, classifierMarker) {
		t.Errorf("release must remove_triple %s on %s — the one-at-a-time release; without it the FIRST classification permanently closes the NL lane for the run (journey 6.4 needs two)", classifierMarker, runSubjectToken)
	}
	// The removal order is doubly load-bearing (grp4-review M2): each action is
	// its own KV revision, so (a) the message-id removal must precede the marker
	// removal (marker-first exposes the exact spawn-trigger revision — pending id
	// present, marker absent — re-spawning a classifier for the already-classified
	// message), and (b) the author + body removals must precede the message-id
	// removal (id-first lets a concurrent bridge write be TORN by the trailing
	// author/body removals into an id-present/author-absent slot that spawns a
	// classifier guaranteed to fault).
	idx := func(pred string) int {
		for i, a := range release.OnEnter {
			if a.Type == "remove_triple" && a.Predicate == pred {
				return i
			}
		}
		return -1
	}
	markerIdx := idx(classifierMarker)
	idIdx := idx(conversationintent.PendingMessageIDPredicate)
	if markerIdx != -1 && idIdx != -1 && markerIdx < idIdx {
		t.Error("release must remove conversation.pending.message-id BEFORE the marker — marker-first exposes an intermediate revision (pending id present, marker absent) that re-spawns a classifier for the already-classified message")
	}
	for _, pred := range []string{conversationintent.PendingAuthorPredicate, conversationintent.PendingBodyPredicate} {
		if p := idx(pred); p != -1 && idIdx != -1 && p > idIdx {
			t.Errorf("release must remove %s BEFORE conversation.pending.message-id — id-first tears a concurrent bridge write into an id-present/author-absent slot that spawns a fault-only classifier", pred)
		}
	}
}

// The routing-rule arm of the conversation-intent drift census (grp1 task 1.3,
// deferred to land with the rules): the values ANY rule routes on via
// conversation.intent.value are EXACTLY the canonical closed taxonomy minus
// `none`. An off-taxonomy route value could never fire (classify_intent
// validates before stamping) — a silently dead lane; a routed `none` would act
// on "no directive"; a canonical directive with no route would classify and
// then silently stall the human's answer. Bidirectional, mirroring the persona
// census — and swept over ALL packs (the coordinator taxonomy census's scope
// discipline): a conversation.intent.value route added in another pack would
// otherwise escape both arms and double-dispatch under a green census.
func TestConversationRoutingRulesMatchTaxonomy(t *testing.T) {
	rules, err := loadRules(repoRoot(t))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	routed := make(map[string]int)
	for _, r := range rules {
		for _, c := range r.Conditions {
			if c.Field != "conversation.intent.value" || c.Operator != "eq" {
				continue
			}
			v, ok := c.Value.(string)
			if !ok {
				t.Errorf("rule %s routes conversation.intent.value on a non-string value %v", r.ID, c.Value)
				continue
			}
			routed[v]++
		}
	}
	if len(routed) == 0 {
		t.Fatal("no conversation rule routes on conversation.intent.value — the census would pass vacuously")
	}
	for v, n := range routed {
		if !conversationintent.Valid(v) {
			t.Errorf("routing rule matches intent %q — not in the canonical closed taxonomy (a dead route: classify_intent never stamps it)", v)
		}
		if v == "none" {
			t.Error("a routing rule matches intent \"none\" — no directive must route nowhere (the run stays gated, D1)")
		}
		if n != 1 {
			t.Errorf("intent %q is routed by %d rules — each directive intent has exactly one route (a duplicate double-dispatches the apply)", v, n)
		}
	}
	for _, v := range conversationintent.Names() {
		if v == "none" {
			continue
		}
		if routed[v] == 0 {
			t.Errorf("canonical intent %q has no routing rule — a classified directive would silently stall at the gate", v)
		}
	}
}
