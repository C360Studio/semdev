//go:build e2e

// The mock-LLM spine journey (group 11). It boots the REAL shared runtime against
// the in-process mock LLM (zero paid tokens) and drives the issue→PR arc through
// it, growing one station at a time. This first slice proves the load-bearing live
// plumbing every later station depends on: the config points the agentic-model
// endpoint at the in-process mock, the runtime assembles the agentic-execution
// plane, a coordinator TaskMessage published to the front door actually spawns a
// loop, and that loop reaches the mock. If the front-door subject, the BaseMessage
// envelope, the stream wiring, or the model endpoint override is wrong, the loop
// never runs and RequestCount stays 0 — so this is the regression guard for the
// whole live path before the dev-loop rail layers onto it.
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
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/mockllm"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/service"
)

// journeyMarker is a distinctive substring of the coordinator prompt this journey
// publishes; the mock keys its scripted response on it so an unscripted turn fails
// loudly (mockllm.UnmatchedSentinel) rather than passing green on the framework
// mock's default output.
const journeyMarker = "JOURNEY_SPINE_SMOKE"

// wantAgenticHealthy are the components the coordinator loop needs live before the
// front-door message can be served: the agentic-execution plane plus the graph
// fact-store the loop's tools read/write.
var wantAgenticHealthy = []string{
	"graph-ingest", "graph-query", "rule",
	"agentic-tools", "agentic-model", "agentic-loop", "agentic-dispatch",
}

// TestSpineJourneyCoordinatorRunsAgainstMock is the first spine slice: publish a
// coordinator TaskMessage to the front door and prove the agentic plane runs a loop
// that reaches the mock LLM. Later slices assert the resulting decision fact and
// grow the arc (create_change → approval → dev loop → …).
func TestSpineJourneyCoordinatorRunsAgainstMock(t *testing.T) {
	mock := mockllm.New(mockllm.Fixture{
		Marker:  journeyMarker,
		Content: "Acknowledged — coordinator smoke turn.",
	})
	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	defer func() { _ = mock.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rt, err := boot.NewRuntime(ctx, boot.RunOptions{ConfigPath: journeyConfigPath(t, mock.Endpoint())})
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
	publishCoordinatorTask(ctx, t, journeyMarker)

	// The loop is served asynchronously off the AGENT stream; poll until it reaches
	// the mock. RequestCount rising off zero proves the whole live path end to end.
	requireEventually(t, 45*time.Second, func() bool { return mock.RequestCount() >= 1 },
		"coordinator loop never reached the mock LLM (RequestCount stayed 0) — check the front-door subject, the BaseMessage envelope, or the model endpoint override")

	t.Logf("coordinator loop reached the mock LLM: RequestCount=%d", mock.RequestCount())
}

// publishCoordinatorTask publishes a coordinator TaskMessage to the front door
// (agent.task.coordinator on the AGENT JetStream) exactly as semdev's intake
// adapter will: BaseMessage-wrapped and PublishToStream'd. A bare marshal or a core
// publish is silently dropped by the loop consumer, so this mirrors the framework
// contract precisely.
func publishCoordinatorTask(ctx context.Context, t *testing.T, marker string) {
	t.Helper()
	client, err := natsclient.NewClient("nats://localhost:4222")
	if err != nil {
		t.Fatalf("front-door NATS client: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("front-door connect: %v", err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	if err := client.WaitForConnection(ctx); err != nil {
		t.Fatalf("front-door wait for connection: %v", err)
	}

	task := &agentic.TaskMessage{
		TaskID: "journey-coordinator-1",
		Role:   "coordinator",
		Model:  "mock",
		Prompt: marker + ": a new admitted issue needs a run. Decide the next action.",
		Tools:  []agentic.ToolDefinition{},
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("TaskMessage invalid: %v", err)
	}
	base := message.NewBaseMessage(task.Schema(), task, "journey-frontdoor")
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal TaskMessage envelope: %v", err)
	}
	if err := client.PublishToStream(ctx, "agent.task.coordinator", data); err != nil {
		t.Fatalf("publish coordinator TaskMessage: %v", err)
	}
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
