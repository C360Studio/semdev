package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/conversationchannel"
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
	"rules/conversation/05-classifier-fault-note.json",
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
	if !spawn.hasAbsenceGuard("run.change.decision") {
		t.Error("spawn must require the gate UNDECIDED (run.change.decision length_eq 0) — a decided run's classification is a guaranteed-dead paid turn")
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
		if !route.hasAbsenceGuard("run.change.decision") {
			t.Errorf("%s must require the gate UNDECIDED (run.change.decision length_eq 0, H4a) — and the decision landing is what extinguishes the trigger", id)
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

// TestClassifierFaultPostsFallbackNote pins the fault-surfacing lane
// (nl-conversation-intent task 5.4, design D9 / semstreams HIGH-3).
//
// A classifier FAULT is not a confident `none`. A loop that errors or truncates
// stamps NO conversation.intent, so every downstream route stays silent — and the
// human who wrote "ship it" sees NOTHING happen, forever. That is the silent
// human-facing dead-end the design bars. A faulted terminal therefore POSTS a
// fallback note telling them to use the exact command (or re-type).
//
// WHY THIS RULE MUST NOT USE THE PARK LANE: the park rules stamp
// run.awaiting.human, which run-lifecycle/02 reads as a RESUME BLOCKER
// (`run.awaiting.human length_eq 0`). Routing a classifier fault through the park
// lane would therefore wedge the run — a LATER, successful approval could never
// resume it. The note rides its own user.note.* subject on the existing USER
// stream and stamps no fact at all: it is a message to a human, not a lifecycle
// event (G9 — no new vocabulary for a post).
//
// The rule fires on the classifier LOOP (conditions can only read the FIRING
// entity's own facts, so the run's intent facts are unreachable here) and
// discriminates on agent.loop.outcome == "failed" — the framework's terminal
// outcome for an errored/truncated loop (agentic.OutcomeFailed). A `success`
// terminal means classify_intent ran (tool_choice is required on the spawn), and
// `cancelled` means the run itself is going away — neither warrants a note.
func TestClassifierFaultPostsFallbackNote(t *testing.T) {
	var note *ruleFile
	pack := conversationPackRules(t)
	for i := range pack {
		if pack[i].ID == "conversation_classifier_fault_note" {
			note = &pack[i]
			break
		}
	}
	if note == nil {
		t.Fatal("missing conversation_classifier_fault_note rule — a faulted classifier is a SILENT dead-end for the human (HIGH-3)")
	}
	if c, ok := note.condition("agent.loop.role"); !ok || c.Operator != "eq" || c.Value != "conversation" {
		t.Error("the fault note must be scoped to the conversation role — every other role's failures have their own lanes")
	}
	// THE DISCRIMINATOR, and the reason this pin exists in its current form. Keying
	// on agent.loop.outcome == "failed" is WRONG and shipped broken: a tool that
	// returns a ToolResult error does NOT fail its loop, so a classifier that
	// deliberately refused to classify (the read-once binding fault) terminated
	// outcome=success and the human was told nothing. The complete test is the
	// ABSENCE of a recorded classification.
	// hasAbsenceGuard checks length_eq AND Value == 0; the hand-rolled operator-only
	// check passed a polarity INVERSION (length_eq 1 = note-on-success,
	// silence-on-fault) — grp6-review M5.
	if !note.hasAbsenceGuard(conversationintent.ClassifierRecordedPredicate) {
		t.Errorf("the fault note must fire on the ABSENCE of %s (length_eq 0) — that is the only condition covering every no-reading terminal (binding fault, model error, truncation, cap exhaustion). An agent.loop.outcome-keyed condition NEVER fires for a refusing classifier, because a tool error does not fail its loop",
			conversationintent.ClassifierRecordedPredicate)
	}
	// A terminal must still be required, and a cancelled run must not draw a note.
	var sawTerminal, sawNotCancelled bool
	for _, c := range note.Conditions {
		if c.Field != "agent.loop.outcome" || c.Operator != "ne" {
			continue
		}
		if c.Value == "" {
			sawTerminal = true
		}
		if c.Value == "cancelled" {
			sawNotCancelled = true
		}
	}
	if !sawTerminal {
		t.Error(`the fault note must require a TERMINAL (agent.loop.outcome ne "") — otherwise it fires on a classifier still in flight`)
	}
	if !sawNotCancelled {
		t.Error(`the fault note must exclude the cancelled terminal (agent.loop.outcome ne "cancelled") — the run is going away, so a note is noise`)
	}
	if c, ok := note.condition("agent.run.entity-id"); !ok || c.Operator != "ne" {
		t.Error("the fault note needs the run anchor present (ne \"\") — the note consumer resolves the thread through it")
	}

	// The note must NOT stamp run.awaiting.human (or anything else): that fact is
	// the resume rule's blocker, so parking here would wedge a later approval.
	for _, a := range note.OnEnter {
		if a.Type == "add_triple" {
			t.Errorf("the fault note stamps %q — it must stamp NOTHING; run.awaiting.human in particular would block run-lifecycle/02 and wedge a later approval", a.Predicate)
		}
	}
	var publishes []string
	for _, a := range note.OnEnter {
		if a.Type == "publish" {
			publishes = append(publishes, a.Subject)
		}
	}
	if len(publishes) != 1 {
		t.Fatalf("the fault note must publish EXACTLY one subject, got %v", publishes)
	}
	published := publishes[0]
	if !strings.HasPrefix(published, "user.note.") {
		t.Errorf("the fault note publishes %q, want a user.note.* subject (its own lane on the existing USER stream, NOT the park lane)", published)
	}
}

// TestApplyLaneIsTheDeliberateNonParkingStation is the B1 structural guard.
//
// Every other station routes a retries-exhausted dispatch to
// `station.dispatch.failed`, which run-lifecycle/05 converts into
// `run.awaiting.human`. For the conversation-apply lane that would be
// UNRECOVERABLE, because its run is sitting AT the change-approval gate and BOTH
// release rules require `run.awaiting.human` ABSENT:
//
//	run-lifecycle/02 (resume): run.awaiting.human length_eq 0
//	run-lifecycle/07 (cancel): run.awaiting.human length_eq 0
//
// and NOTHING in the repo ever removes that predicate (eight park rules add it,
// zero remove it — a resume-from-park rule is still unbuilt). A park here would
// therefore wedge the run forever: never approvable, never cancellable, and even
// `/semdev approve` would die stamping a fact no rule would consume.
//
// So the apply lane notifies the human and leaves the gate open instead. This pin
// fails if anyone re-wires it to the park lane — a change that would look locally
// reasonable ("be consistent with the other six stations") and be catastrophic.
func TestApplyLaneIsTheDeliberateNonParkingStation(t *testing.T) {
	root := repoRoot(t)

	// (a) The apply lane's own code must never reference the park predicate.
	dir := filepath.Join(root, "internal", "conversationchannel")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		// The park predicate reaches the graph either via the station const or the
		// literal; catch both.
		body := string(src)
		bannedTokens := []string{"DispatchFailedPredicate", `"station.dispatch.failed"`}
		// The predicate that ACTUALLY wedges the gate, banned in every file EXCEPT
		// the one legitimate reader. Scoping it to apply.go alone was too narrow
		// (grp5-review NEW-10): a helper added in a NEW file in this package could
		// write the wedge predicate directly and name neither station token, so it
		// would sail through both bans. parkpost.go:70 legitimately READS it to
		// post a park message. No repo-wide pin covers this — TestOnlySanctionedParkWriters
		// walks RULES only, never Go.
		if e.Name() != "parkpost.go" {
			bannedTokens = append(bannedTokens, `"run.awaiting.human"`)
		}
		for _, banned := range bannedTokens {
			// Allow it inside comments explaining WHY the lane must not park.
			for _, line := range strings.Split(body, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if strings.Contains(line, banned) {
					t.Errorf("%s references %s in CODE — the conversation-apply lane must NEVER stamp a station park: its run is at the change-approval gate, both release rules require run.awaiting.human absent, and nothing ever removes it (the run would be wedged forever). Notify the human and leave the gate open instead.", e.Name(), banned)
				}
			}
		}
	}

	// (b) The park rule that WOULD have covered it stays intact for the other
	// stations — this pin is about the apply lane opting out, not about weakening
	// the park subsystem.
	if _, ok := runLifecycleRules(t)["run_park_station_failure_run"]; !ok {
		t.Error("run-lifecycle/05 (the run-fired park half) is missing — the other RUN-dispatched stations lost their terminal-failure park")
	}
}

// TestConversationLanePortsMatchConfigs closes the silent void both reviewers
// flagged (go M2 / semstreams M6): the routing rules PUBLISH to
// component.conversation-apply.dispatch, and if the consuming port were dropped
// from a shipped config the rule would fire, the stream would store, and nothing
// would consume — green everywhere, dead in production. Nothing tied the Go consts
// to the config until now. The subject match is also load-bearing for the
// serialization knob (component.go keys max_ack_pending on an EXACT subject
// compare, so the station-convention wildcard would silently revert the lane to
// the default 8).
func TestConversationLanePortsMatchConfigs(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"semdev-bootstrap.json", "semdev-live-gemini.json"} {
		raw, err := os.ReadFile(filepath.Join(root, "configs", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var cfg struct {
			Streams    map[string]struct{ Subjects []string } `json:"streams"`
			Components struct {
				ConversationChannel struct {
					Config struct {
						Ports struct {
							Inputs []struct {
								Name       string `json:"name"`
								Type       string `json:"type"`
								Subject    string `json:"subject"`
								StreamName string `json:"stream_name"`
							} `json:"inputs"`
						} `json:"ports"`
					} `json:"config"`
				} `json:"conversation-channel"`
			} `json:"components"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}

		byName := map[string]struct{ subject, stream string }{}
		for _, in := range cfg.Components.ConversationChannel.Config.Ports.Inputs {
			byName[in.Name] = struct{ subject, stream string }{in.Subject, in.StreamName}
		}
		for _, want := range []struct{ port, subject, stream string }{
			{"apply_dispatch", conversationchannel.ApplyDispatchSubject, conversationchannel.ApplyStreamName},
			{"user_notes", conversationchannel.UserNoteSubject, "USER"},
		} {
			got, ok := byName[want.port]
			if !ok {
				t.Errorf("%s: conversation-channel declares no %q input — its producer is a RULE, so the lane would publish into a void with every test still green", name, want.port)
				continue
			}
			if got.subject != want.subject {
				t.Errorf("%s: %s subject = %q, want %q (the Go const); an exact match is required — component.go keys the serialization knob on it", name, want.port, got.subject, want.subject)
			}
			if got.stream != want.stream {
				t.Errorf("%s: %s stream_name = %q, want %q", name, want.port, got.stream, want.stream)
			}
		}

		// The apply dispatch's stream must be declared, or Start fails loudly
		// (AutoCreate:false). Declared NARROW on purpose.
		stream, ok := cfg.Streams[conversationchannel.ApplyStreamName]
		if !ok {
			t.Errorf("%s: no %q stream declared — the apply consumer cannot start (AutoCreate is false)", name, conversationchannel.ApplyStreamName)
			continue
		}
		if len(stream.Subjects) != 1 || !strings.HasPrefix(stream.Subjects[0], "component."+conversationchannel.ApplyStationName) {
			t.Errorf("%s: %s subjects = %v, want exactly the narrow component.%s.> — a broader subject would silently persist the other six stations' fire-and-forget dispatches", name, conversationchannel.ApplyStreamName, stream.Subjects, conversationchannel.ApplyStationName)
		}
	}
}

// TestShippedConfigVersionsAreParseableSemver guards a SILENT config-loading
// failure (nl-conversation-intent grp5-review, semstreams HIGH-1).
//
// The framework stores each config in a versioned NATS KV and, on boot, compares
// the file's version to the KV's. config.CompareVersions strconv.Atoi's every
// dot-separated segment, so a decorated value like "0.29.0-live-gemini" FAILS TO
// PARSE — and the manager treats a comparison error as "sync from KV". The file
// then loses on every boot against a populated KV, silently, forever.
//
// The failure is split and therefore very hard to spot: EnsureStreams runs off the
// FILE (so newly declared streams appear and look healthy), while the component
// manager reads the KV-synced config (so newly declared PORTS do not exist). The
// result is a stream filling with nobody consuming it, with no error anywhere.
// semdev-live-gemini.json carried a suffixed version for its whole life; group 5
// is the first change to put a rule-published lane behind it.
func TestShippedConfigVersionsAreParseableSemver(t *testing.T) {
	root := repoRoot(t)
	versions := map[string]string{}
	for _, name := range []string{"semdev-bootstrap.json", "semdev-live-gemini.json"} {
		raw, err := os.ReadFile(filepath.Join(root, "configs", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var cfg struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if cfg.Version == "" {
			t.Errorf("%s declares no version", name)
			continue
		}
		versions[name] = cfg.Version
		parts := strings.Split(cfg.Version, ".")
		if len(parts) != 3 {
			t.Errorf("%s version %q is not MAJOR.MINOR.PATCH — CompareVersions cannot parse it, so this file silently loses to the KV on every boot", name, cfg.Version)
			continue
		}
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				t.Errorf("%s version %q has non-numeric segment %q — CompareVersions strconv.Atoi's each segment, the comparison ERRORS, and the manager falls back to the KV: this file's component blocks (ports included) are silently ignored while EnsureStreams still runs off the file", name, cfg.Version, p)
			}
		}
	}

	// INVARIANT 2 — the MOCK config must win any stale-KV race (grp5-review,
	// BLOCKING). Both files share ONE KV entry: the bucket ("semstreams_config")
	// and key ("version") are hardcoded globals, NOT platform-derived, and both
	// configs declare the IDENTICAL platform identity (c360/semdev-bootstrap/
	// development) — so the framework's gh#459 cross-app detach guard cannot fire
	// and VERSION ALONE decides which file wins.
	//
	// PushToKV writes model_registry. So if the LIVE config could ever win, a
	// later mock-config boot would take the `cmp < 0` branch, syncFromKV, and
	// silently adopt gemini endpoints — the free ladder spending real tokens
	// behind one WARN line, with the `conversation` capability (mock-only) dropped
	// so the classifier falls through to gemini too.
	//
	// Keeping mock STRICTLY GREATER makes every stale-KV race resolve toward the
	// FREE config; the live lane still loads because its Taskfile entry mandates
	// `task nats:reset`. NOTE this ordering was briefly INVERTED while fixing the
	// unparseable-suffix bug: the suffix had accidentally made live un-pushable,
	// and making it parseable removed that protection. Hence this pin.
	mock, live := versions["semdev-bootstrap.json"], versions["semdev-live-gemini.json"]
	if mock == "" || live == "" {
		t.Fatalf("could not read both versions (mock=%q live=%q)", mock, live)
	}
	if compareSemver(t, mock, live) <= 0 {
		t.Errorf("mock config version %q must be STRICTLY GREATER than the live config's %q — they share one KV entry and one platform identity, so version alone decides which file wins; if live wins, a later mock boot adopts the gemini model_registry and the FREE ladder spends real tokens", mock, live)
	}
}

// compareSemver compares two plain MAJOR.MINOR.PATCH strings the same way the
// framework's config.CompareVersions does (segment-wise integer compare).
func compareSemver(t *testing.T, a, b string) int {
	t.Helper()
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3 && i < len(as) && i < len(bs); i++ {
		ai, err := strconv.Atoi(as[i])
		if err != nil {
			t.Fatalf("non-numeric segment in %q", a)
		}
		bi, err := strconv.Atoi(bs[i])
		if err != nil {
			t.Fatalf("non-numeric segment in %q", b)
		}
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return 0

}

// frameworkMaxConfigString mirrors component.MaxStringLength (registry.go:507) —
// the framework's per-string cap in component config validation.
const frameworkMaxConfigString = 1024

// TestComponentConfigStringsWithinFrameworkLimit guards a SILENT DEAD-LANE class
// that a verbose description in this very change actually triggered.
//
// component.ValidateConfig rejects any config string longer than MaxStringLength
// (1024). A component whose config fails validation is NOT created — and the
// runtime still boots, still reports the agentic plane healthy, and still drives
// every RULE-owned station, because rules live in a different component. So the
// only symptom is that one component's lanes silently never run. In the case that
// prompted this pin, an over-long `apply_dispatch` port description meant
// conversation-channel never started: no poller, no approval lane, no NL lane —
// and the offline suite was entirely green, because nothing offline builds a
// component from the shipped config.
//
// The trap is that documentation quality and component liveness are in direct
// tension here: the more carefully a port is explained, the closer it creeps to a
// limit whose only feedback is an ERROR line in a boot log nobody greps. This pin
// converts that into a test failure at authoring time. Long-form rationale belongs
// in the Go doc comments and design.md; the config carries a summary and a pointer.
func TestComponentConfigStringsWithinFrameworkLimit(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"semdev-bootstrap.json", "semdev-live-gemini.json"} {
		raw, err := os.ReadFile(filepath.Join(root, "configs", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var cfg struct {
			Components map[string]any `json:"components"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch node := v.(type) {
			case map[string]any:
				for k, child := range node {
					walk(path+"."+k, child)
				}
			case []any:
				for i, child := range node {
					walk(fmt.Sprintf("%s[%d]", path, i), child)
				}
			case string:
				if len(node) > frameworkMaxConfigString {
					t.Errorf("%s: %s is %d bytes, over the framework's %d-byte config-string limit — the component would FAIL validation and never be created, and the runtime would boot green with that component's lanes silently dead",
						name, path, len(node), frameworkMaxConfigString)
				}
			}
		}
		for comp, v := range cfg.Components {
			walk(comp, v)
		}
	}
}

// TestNatsPortMatchesComposeDefault guards the nastiest failure mode on a shared
// docker host: semdev's compose publishing one host port while its configs dial
// another.
//
// This machine runs many sem* stacks, and SIX of them (semdocs, semdragon,
// semsage, semspec and its two UI variants, and semdev until now) all defaulted
// their NATS to 4222. Nothing arbitrates that. If the compose file and the configs
// drift apart, semdev does NOT fail to connect — it connects to WHOEVER holds the
// port it dialed. It then reads that stack's config KV, writes its facts into that
// stack's JetStream, and behaves in ways that look like deep application bugs:
// entities that vanish, a config version that keeps reverting, runs that never
// appear. The symptom never mentions ports.
//
// semdev therefore claims 24222/28222 (unused by any other c360 stack — semstreams
// holds the 34222/44222/54222/61222/64222 family, semsource 14222) and pins the
// two sides together here. An intentional move updates both and this test; an
// accidental one-sided edit fails loudly, here, offline.
func TestNatsPortMatchesComposeDefault(t *testing.T) {
	root := repoRoot(t)

	composeRaw, err := os.ReadFile(filepath.Join(root, "docker", "compose", "nats.yml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	// e.g.  - "${SEMDEV_NATS_PORT:-24222}:4222"
	m := regexp.MustCompile(`\$\{SEMDEV_NATS_PORT:-(\d+)\}:4222`).FindSubmatch(composeRaw)
	if m == nil {
		t.Fatal("could not find the client port mapping in docker/compose/nats.yml — the pin cannot verify config/compose agreement (did the mapping change shape?)")
	}
	composePort := string(m[1])
	if composePort == "4222" {
		t.Error("semdev's compose default is back to 4222 — the single most contended port on this host (six c360 stacks default to it); semdev must publish a port it owns")
	}

	// The compose project name is the other half of the isolation: without it,
	// Compose derives the project from the parent dir ("compose"), which every
	// repo laid out as docker/compose/*.yml also derives — making them ONE project,
	// so `down -v` from either side destroys the other's containers and volumes.
	if !regexp.MustCompile(`(?m)^name:\s*semdev\s*$`).Match(composeRaw) {
		t.Error(`docker/compose/nats.yml must declare "name: semdev" — without it the project name is derived from the parent directory ("compose"), colliding with every other repo laid out the same way`)
	}

	// The SERVER image is pinned here too (migrate-semstreams-beta159 D7). beta.159's
	// ownership substrate (OWNER_PRESENCE TTL keys, the epoch bucket) runs on the
	// house 2.14 line; silently drifting back to 2.10 would surface as ownership
	// bind/heartbeat misbehavior at boot, not as a config error — and never offline.
	// A floating tag — including the MINOR line nats:2.14-alpine, which moves with
	// every 2.14.x patch — is rejected for the same reason the
	// nats-box sidecar is pinned: an unpinned substrate makes a green run
	// unreproducible.
	if m := regexp.MustCompile(`(?m)^\s*image:\s*nats:(\S+)\s*$`).FindSubmatch(composeRaw); m == nil {
		t.Error("could not find the nats server image in docker/compose/nats.yml — the version pin cannot verify it")
	} else if got := string(m[1]); got != "2.14.4-alpine" {
		t.Errorf("nats server image = %q, want %q — beta.159's ownership substrate is pinned to the house 2.14 line (D7); a drift shows up as bind/heartbeat misbehavior at boot, never offline", got, "2.14.4-alpine")
	}

	// The operator sidecar's image must not float: `nats-box:latest` silently
	// changes the CLI under a paid run's watch commands.
	taskfile, err := os.ReadFile(filepath.Join(root, "Taskfile.yml"))
	if err != nil {
		t.Fatalf("read Taskfile.yml: %v", err)
	}
	if regexp.MustCompile(`natsio/nats-box:(latest|main)\b`).Match(taskfile) {
		t.Error("Taskfile.yml uses a FLOATING natsio/nats-box tag — pin it, or the sidecar CLI changes under a paid run without a commit (D7)")
	}

	for _, name := range []string{"semdev-bootstrap.json", "semdev-live-gemini.json"} {
		raw, err := os.ReadFile(filepath.Join(root, "configs", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var cfg struct {
			NATS struct {
				URLs []string `json:"urls"`
			} `json:"nats"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if len(cfg.NATS.URLs) == 0 {
			t.Errorf("%s declares no nats.urls", name)
			continue
		}
		want := "nats://localhost:" + composePort
		if cfg.NATS.URLs[0] != want {
			t.Errorf("%s dials %q but compose publishes %s — on this host that does not fail to connect, it connects to WHICHEVER stack owns the dialed port, and semdev then reads and writes another project's KV and streams",
				name, cfg.NATS.URLs[0], want)
		}
	}
}

// TestLiveConfigCarriesTheNLLane pins task 6.6. The NL lane needs THREE things in
// the live config, and each one fails DIFFERENTLY silent if missing:
//
//   - the conversation rules_files: absent → no rule fires, so a human's message
//     is bridged onto the run and then nothing at all happens;
//   - classify_intent in allowed_tools: absent → the classifier loop boots and
//     dies at runtime with "tool not allowed", burning a paid turn per message;
//   - a `conversation` model_registry capability: absent → NOT an error. An
//     unknown capability silently falls back to defaults.model, so the classifier
//     runs on whatever the default is, misrouted rather than dead.
//
// The bootstrap census pins the MOCK config only, so without this the paid lane
// could ship dead while every offline test and every journey stayed green.
func TestLiveConfigCarriesTheNLLane(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "configs", "semdev-live-gemini.json"))
	if err != nil {
		t.Fatalf("read live config: %v", err)
	}
	var cfg struct {
		ModelRegistry struct {
			Capabilities map[string]struct {
				Preferred []string `json:"preferred"`
			} `json:"capabilities"`
		} `json:"model_registry"`
		Components struct {
			Rule struct {
				Config struct {
					RulesFiles []string `json:"rules_files"`
				} `json:"config"`
			} `json:"rule"`
			AgenticTools struct {
				Config struct {
					AllowedTools []string `json:"allowed_tools"`
				} `json:"config"`
			} `json:"agentic-tools"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode live config: %v", err)
	}

	listed := make(map[string]bool)
	for _, rel := range cfg.Components.Rule.Config.RulesFiles {
		listed[filepath.ToSlash(rel)] = true
	}
	for _, rel := range conversationRuleFiles {
		if !listed[rel] {
			t.Errorf("live config does not load %q — the NL lane is DEAD in the paid config: a human's message bridges onto the run and nothing fires", rel)
		}
	}
	if !slices.Contains(cfg.Components.AgenticTools.Config.AllowedTools, "classify_intent") {
		t.Error("live config does not allow classify_intent — the classifier loop boots and dies with \"tool not allowed\", burning a paid turn per message")
	}
	if _, ok := cfg.ModelRegistry.Capabilities["conversation"]; !ok {
		t.Error("live config declares no `conversation` model capability — this does NOT fail loudly: an unknown capability silently falls back to defaults.model, so the classifier runs misrouted rather than dead")
	}
}
