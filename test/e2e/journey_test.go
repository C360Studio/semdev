//go:build e2e

// The mock-LLM spine journey (group 11). It boots the REAL shared runtime against
// the in-process mock LLM (zero paid tokens) and drives the issue→PR arc through
// it, growing one station at a time.
//
// Station 1 proved the live plumbing: the config points the agentic-model
// endpoint at the mock, the runtime assembles the agentic-execution plane, a
// coordinator TaskMessage published to the front door spawns a loop, and that
// loop reaches the mock.
//
// Station 2 (this slice) proves the coordinator actually ROUTES: the front-door
// wake is built exactly as the intake adapter will build it (via
// intake.CoordinatorTask — nil tools = global discovery, tool_choice=required,
// the closed decide-action allowlist), the seeded coordinator persona is live,
// and the mock's scripted decide turn lands a coordinator.decision.next_action
// triple on the loop entity. If the tool config, the persona seeding, the front
// door subject, or the decide registration is wrong, the loop reaches the model
// but never routes and no decision fact appears — so this is the regression guard
// for the whole route-a-decision path the dev-loop rail layers onto.
//
// Run via `task e2e`, which resets NATS first so the runtime loads the journey's
// patched config from file rather than a stale versioned-KV copy.

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semdev/internal/mockllm"
	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/service"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"
)

// journeyIssueRef is the host-neutral issue ref this journey admits. It is
// distinctive so the mock can key its scripted decide turn on it as a substring
// of the coordinator prompt (intake.CoordinatorTask always embeds the ref).
const journeyIssueRef = "c360studio/semdev-journey#1"

// journeyDecideAction is the action the mock's scripted decide returns — the
// terminal a coordinator picks for a newly admitted issue with no run yet.
const journeyDecideAction = "issue_intake"

// journeyChangeSlug is the slug the mock's scripted create_change authors. The
// change facts land on the run entity under openspec.change.<slug>.*.
const journeyChangeSlug = "journey-spine-change"

// entityStatesBucket is the graph fact-store KV bucket the loop's decide triple
// lands in (graph-ingest's kv-write output). The journey scans it for the
// coordinator's decision.
const entityStatesBucket = "ENTITY_STATES"

// wantAgenticHealthy are the components the coordinator loop needs live before the
// front-door message can be served: the agentic-execution plane plus the graph
// fact-store the loop's tools read/write.
var wantAgenticHealthy = []string{
	"graph-ingest", "graph-query", "rule",
	"agentic-tools", "agentic-model", "agentic-loop", "agentic-dispatch",
}

// TestSpineJourneyCoordinatorDecidesAgainstMock is the second spine slice:
// publish an admitted issue's coordinator wake to the front door and prove the
// coordinator loop calls decide and stamps its routing decision. Later slices
// grow the arc off that decision (mint the run → create_change → dev loop → …).
func TestSpineJourneyCoordinatorDecidesAgainstMock(t *testing.T) {
	// The mock scripts the arc's three sequential turns as a POSITIONAL sequence
	// (cursor advances per matched tool call). The turns are causally ordered —
	// each loop is spawned by a rule that fired on the prior loop's terminal — so
	// the cursor lands each fixture on its turn:
	//   1. C1 front-door coordinator → decide(issue_intake)   [mints the run]
	//   2. C2 re-woken coordinator   → decide(create_change)  [routes to author]
	//   3. A1 authoring coordinator  → create_change(<change>) [emits the change]
	// Every marker is the issue ref: it is present in all three prompts (each rule
	// threads the prior decision reason, which carries the ref), so the cursor —
	// not the marker — distinguishes the turns. An unscripted turn returns
	// mockllm.UnmatchedSentinel, failing loudly rather than green.
	mock := mockllm.New(
		mockllm.Fixture{
			Marker: journeyIssueRef,
			Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
				"action": journeyDecideAction,
				"reason": "new admitted issue " + journeyIssueRef + " needs a run",
			}},
		},
		mockllm.Fixture{
			Marker: journeyIssueRef,
			Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
				"action": "create_change",
				"reason": "author the change for " + journeyIssueRef,
			}},
		},
		mockllm.Fixture{
			Marker: journeyIssueRef,
			Tool:   &mockllm.ToolCall{Name: "create_change", Args: journeyChangeArgs()},
		},
	)
	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	defer func() { _ = mock.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rt, err := boot.NewRuntime(ctx, boot.RunOptions{
		ConfigPath: journeyConfigPath(t, mock.Endpoint()),
		// The patched config lives in a temp dir, so point persona seeding at the
		// repo's real fragment tree — otherwise the coordinator would route on the
		// framework default persona instead of Sarah's decision contract.
		PersonasDir: journeyPersonasDir(t),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		// Teardown is best-effort — graceful shutdown is subject to the known
		// semstreams ComponentManager deadlock (C360Studio/semstreams#508); Stop
		// bounds it so this returns rather than hanging.
		if stopErr := rt.Stop(5 * time.Second); stopErr != nil {
			t.Logf("runtime Stop (best-effort teardown): %v", stopErr)
		}
	}()

	requireAgenticHealthy(ctx, t, rt)
	taskID := publishCoordinatorWake(ctx, t)

	// Station 2 — the coordinator routed. Poll the graph until the coordinator's
	// decide triple lands (the proof it routed, not merely that it reached the
	// model), bound to THIS wake's task id so a stale prior-run decision can't
	// false-green.
	requireCoordinatorDecision(ctx, t, taskID, journeyDecideAction)
	t.Logf("station 2: coordinator routed via decide: task=%s next_action=%s (mock RequestCount=%d)", taskID, journeyDecideAction, mock.RequestCount())

	// Station 3 — the decision minted a run. The issue_intake spawn rule fires
	// publish_agent run_scope=new on the coordinator loop; the framework mints the
	// run (rooted at the coordinator's loop id) and the agent-run bridge advances
	// it dispatched→executing. Read the run anchor off THIS coordinator loop, then
	// assert the run entity reaches executing — proof the mint + bridge rules fired
	// live, not just that a decision was stamped.
	runEntityID := requireRunAnchor(ctx, t, taskID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("station 3: run %s minted and reached executing", runEntityID)

	// Station 4 — the coordinator re-woke, decided create_change, and a rule
	// spawned an authoring coordinator loop that called create_change. Assert the
	// change facts landed on the run entity: proof the create_change spawn rule
	// fired, the author inherited the run anchor, and the tool stamped
	// openspec.change.<slug>.* on the run (the create_change→validate→approval arc
	// hangs off these facts).
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	t.Logf("station 4: change %q authored onto run %s (mock RequestCount=%d)", journeyChangeSlug, runEntityID, mock.RequestCount())

	// Exactly three model turns drove the arc (C1 decide(issue_intake),
	// C2 decide(create_change), A1 create_change). create_change REPLACES its
	// owned facts idempotently, so a re-spawn/loop regression would re-stamp the
	// same facts and slip past requireChangeAuthored — RequestCount is the only
	// signal of an extra turn, so pin it (the mockllm contract: assert turns ==
	// expected). By here all three loops have terminated (StopLoop) and A1 spawns
	// nothing, so no fourth call is in flight.
	if got := mock.RequestCount(); got != 3 {
		t.Fatalf("expected exactly 3 model turns (C1 decide, C2 decide, A1 create_change), got %d — extra turns indicate a re-spawn/loop or an unscripted turn", got)
	}
}

// publishCoordinatorWake builds the front-door coordinator wake exactly as the
// intake adapter will (intake.CoordinatorTask), then publishes it to the front
// door (intake.FrontDoorSubject on the AGENT JetStream) BaseMessage-wrapped via
// PublishToStream — the real framework contract. A bare marshal or a core publish
// is silently dropped by the loop consumer, so this mirrors it precisely. Returns
// the wake's task id so the caller can bind its assertion to this run's loop
// (the framework stamps it on the loop entity as agent.loop.task).
func publishCoordinatorWake(ctx context.Context, t *testing.T) string {
	t.Helper()

	task, err := intake.CoordinatorTask(intake.Intake{Relevant: true, IssueRef: journeyIssueRef}, "mock")
	if err != nil {
		t.Fatalf("build coordinator wake: %v", err)
	}

	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	base := message.NewBaseMessage(task.Schema(), task, "journey-frontdoor")
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal coordinator wake envelope: %v", err)
	}
	if err := client.PublishToStream(ctx, intake.FrontDoorSubject, data); err != nil {
		t.Fatalf("publish coordinator wake: %v", err)
	}
	return task.TaskID
}

// requireCoordinatorDecision polls the ENTITY_STATES fact-store until THIS run's
// coordinator loop carries coordinator.decision.next_action == wantAction, or
// fails naming the likely cause. The loop id is minted by the framework, so the
// journey does not know the entity id up front — it scans (the deep-research
// scenario's pattern) and binds by the loop's spawn-stamped agent.loop.task
// (== the wake's task id), so a decision left by a prior run cannot false-green.
func requireCoordinatorDecision(ctx context.Context, t *testing.T, wantTaskID, wantAction string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 45*time.Second, func() bool {
		for _, e := range scanEntities(ctx, client) {
			if tripleString(e, agvocab.LoopRole) == "coordinator" &&
				tripleString(e, agvocab.LoopTask) == wantTaskID &&
				tripleString(e, agvocab.CoordinatorNextAction) == wantAction {
				return true
			}
		}
		return false
	}, "coordinator loop (task "+wantTaskID+") never stamped decision.next_action="+wantAction+
		" — check the front-door tool config (nil tools / tool_choice=required / allowlist), "+
		"persona seeding, the decide registration, or the mock decide fixture marker")
}

// requireRunAnchor polls until THIS wake's coordinator loop carries an
// agent.run.entity_id triple — the run anchor stamped by publish_agent
// run_scope=new — and returns that run entity id. Its presence is the proof the
// issue_intake spawn rule fired and minted a run rooted at the coordinator loop.
func requireRunAnchor(ctx context.Context, t *testing.T, wantTaskID string) string {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	var runEntityID string
	requireEventually(t, 45*time.Second, func() bool {
		entities := scanEntities(ctx, client)
		for _, e := range entities {
			if tripleString(e, agvocab.LoopTask) != wantTaskID {
				continue
			}
			if id := tripleString(e, agvocab.LoopRunEntityID); id != "" {
				runEntityID = id
				return true
			}
		}
		return false
	}, "coordinator loop (task "+wantTaskID+") never gained an agent.run.entity_id anchor "+
		"— the issue_intake spawn rule (run_scope=new) did not fire; check the rule conditions "+
		"(coordinator role / next_action=issue_intake / no prior run anchor) and that the lifecycle manager is wired")
	return runEntityID
}

// requireRunPhase polls the run entity until agent.run.phase == wantPhase, or
// fails. Reaching `executing` proves the agent-run bridge (handoff-marker →
// dispatched-to-executing) advanced the freshly-minted run.
func requireRunPhase(ctx context.Context, t *testing.T, runEntityID, wantPhase string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 45*time.Second, func() bool {
		entities := scanEntities(ctx, client)
		e, ok := entities[runEntityID]
		if !ok {
			return false
		}
		return tripleString(e, agentrun.PhasePredicate) == wantPhase
	}, "run entity "+runEntityID+" never reached agent.run.phase="+wantPhase+
		" — the agent-run bridge did not advance it (check the handoff-marker rule fired on rule.spawned_task "+
		"and the dispatched→executing rule on the run entity)")
}

// requireChangeAuthored polls the run entity until it carries at least one
// openspec.change.<slug>.* triple — the proof the authoring loop's create_change
// call stamped the change package on the run (it targets the run entity via the
// inherited agent.run_entity_id). Fails naming the likely cause.
func requireChangeAuthored(ctx context.Context, t *testing.T, runEntityID, slug string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	prefix := "openspec.change." + slug + "."
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		for _, tr := range e.Triples {
			if strings.HasPrefix(tr.Predicate, prefix) {
				return true
			}
		}
		return false
	}, "run entity "+runEntityID+" never gained openspec.change."+slug+".* facts — the create_change "+
		"spawn rule did not fire, the author did not inherit the run anchor (agent.run_entity_id), or the "+
		"create_change tool call was not scripted/advertised")
}

// journeyChangeArgs is a minimal VALID create_change payload the mock's authoring
// turn emits: a slug, a proposal with intent, one spec delta with an ADDED
// RFC-2119 requirement carrying a Given/When/Then scenario, and one task section.
// Mirrors the create_change tool's own test fixture so it passes the schema.
func journeyChangeArgs() map[string]any {
	return map[string]any{
		"slug":     journeyChangeSlug,
		"proposal": map[string]any{"intent": "make the spine journey author a real change", "scope_in": []any{"the spine"}},
		"deltas": []any{map[string]any{
			"capability": "spine",
			"added": []any{map[string]any{
				"name":      "Spine authors a change",
				"statement": "The system SHALL author an OpenSpec change from an intaken issue.",
				"scenarios": []any{map[string]any{
					"name": "Issue intaken",
					"steps": []any{
						map[string]any{"kw": "WHEN", "text": "an admitted issue is routed to create_change"},
						map[string]any{"kw": "THEN", "text": "openspec.change facts land on the run"},
					},
				}},
			}},
		}},
		"tasks": []any{map[string]any{
			"section": "1. Spine",
			"items":   []any{map[string]any{"number": "1.1", "text": "emit the change onto the run"}},
		}},
	}
}

// scanEntities reads every entity in ENTITY_STATES into a map keyed by entity id.
// Returns an empty map on any transient read error (bucket not yet created, no
// keys yet) so callers poll rather than fail on a not-yet-populated graph.
func scanEntities(ctx context.Context, client *natsclient.Client) map[string]graph.EntityState {
	out := map[string]graph.EntityState{}
	bucket, err := client.GetKeyValueBucket(ctx, entityStatesBucket)
	if err != nil {
		return out
	}
	kv := client.NewKVStore(bucket)
	keys, err := kv.Keys(ctx)
	if err != nil {
		return out
	}
	for _, key := range keys {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}
		var e graph.EntityState
		if err := json.Unmarshal(entry.Value, &e); err != nil {
			continue
		}
		out[e.ID] = e
	}
	return out
}

// tripleString returns the first string object of the entity's triple for
// predicate, or "" if absent / non-string.
func tripleString(e graph.EntityState, predicate string) string {
	for _, tr := range e.Triples {
		if tr.Predicate == predicate {
			if s, ok := tr.Object.(string); ok {
				return s
			}
		}
	}
	return ""
}

// connectFrontDoor opens a NATS client to the local runtime for publishing the
// wake and scanning the fact-store.
func connectFrontDoor(ctx context.Context, t *testing.T) *natsclient.Client {
	t.Helper()
	client, err := natsclient.NewClient("nats://localhost:4222")
	if err != nil {
		t.Fatalf("NATS client: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	if err := client.WaitForConnection(ctx); err != nil {
		t.Fatalf("NATS wait for connection: %v", err)
	}
	return client
}

// requireAgenticHealthy polls the component-manager until the agentic-execution
// plane + graph substrate report healthy, or fails naming what is missing.
func requireAgenticHealthy(ctx context.Context, t *testing.T, rt *boot.Runtime) {
	t.Helper()
	svc, ok := rt.ServiceManager().GetService("component-manager")
	if !ok || svc == nil {
		t.Fatal("component-manager service not present after Start")
	}
	cm, ok := svc.(*service.ComponentManager)
	if !ok {
		t.Fatalf("component-manager service is %T, want *service.ComponentManager", svc)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		healthy := map[string]bool{}
		for _, n := range cm.GetHealthyComponents() {
			healthy[n] = true
		}
		var missing []string
		for _, n := range wantAgenticHealthy {
			if !healthy[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agentic plane not healthy within deadline: missing %v", missing)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled waiting for component health: %v", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// requireEventually polls cond until true or the timeout elapses, failing with msg.
func requireEventually(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// journeyConfigPath writes a copy of the real bootstrap config to a temp file with
// two edits: the mock model endpoint URL points at the in-process mock, and the
// rule pack paths are rewritten to absolute (so the temp-dir config still resolves
// the repo's rules). Returns the temp path. The version is left as-is; `task e2e`
// resets NATS so the file loads fresh regardless of the KV version.
func journeyConfigPath(t *testing.T, mockURL string) string {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "configs", "semdev-bootstrap.json"))
	if err != nil {
		t.Fatalf("read bootstrap config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode bootstrap config: %v", err)
	}

	// Point the mock endpoint at the in-process server.
	mustMap(t, mustMap(t, mustMap(t, cfg, "model_registry"), "endpoints"), "mock")["url"] = mockURL

	// Rewrite rules_files to absolute repo paths (the temp config lives elsewhere).
	ruleCfg := mustMap(t, mustMap(t, mustMap(t, cfg, "components"), "rule"), "config")
	files, _ := ruleCfg["rules_files"].([]any)
	abs := make([]any, len(files))
	for i, f := range files {
		abs[i] = filepath.Join(root, "configs", f.(string))
	}
	ruleCfg["rules_files"] = abs

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// journeyPersonasDir is the repo's real persona fragment tree, passed explicitly
// because the journey's patched config lives in a temp dir where the convention
// path (<configDir>/personas/fragments) would not resolve.
func journeyPersonasDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "configs", "personas", "fragments")
}

func mustMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("journey config: %q is not a JSON object", key)
	}
	return v
}

// repoRoot walks up from this test file to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod from %s", filepath.Dir(file))
		}
		dir = parent
	}
}
