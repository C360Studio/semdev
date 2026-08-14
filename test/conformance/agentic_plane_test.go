package conformance

import (
	"slices"
	"testing"
)

// The agentic-execution plane is the four framework processors that actually run
// agent loops: agentic-tools (executes tool calls), agentic-model (calls the
// LLM), agentic-loop (drives the request/response/tool cycle), and
// agentic-dispatch (the front-door / completion handler). semdev declares them
// in configs/semdev-bootstrap.json so the shared runtime brings them up; the live
// boot smoke test proves they reach healthy (internal/boot/runtime_integration_test.go).
// This offline pin guards the DECLARATION so the plane cannot be silently dropped
// from the config — a deletion that would boot green (no error) but run no agents.

// wantAgenticComponents maps each required component's config map-key to the
// framework factory `name` it must select. The map key is arbitrary; the `name`
// is what the component-manager resolves to a factory, so the pin checks the name.
var wantAgenticComponents = map[string]string{
	"agentic-tools":    "agentic-tools",
	"agentic-model":    "agentic-model",
	"agentic-loop":     "agentic-loop",
	"agentic-dispatch": "agentic-dispatch",
}

// wantAgenticStreams are the JetStream streams the plane's ports bind to. The
// loop/model/tools/dispatch ports reference these by stream_name; a missing
// stream declaration means a consumer binds to a stream that must auto-provision
// (or fails), so semdev declares them explicitly and pins their presence.
// TOOL is deliberately the NARROWED pair (beta.160 discovery cutover): the
// tool.list request moved to a nats-request port at discovery.tool.list, and a
// stream capturing `tool.>` would swallow those requests. The census asserts the
// narrowing so a broadening regression cannot silently re-capture discovery.
var wantAgenticStreams = map[string][]string{
	"AGENT": {"agent.>"},
	"TOOL":  {"tool.execute.>", "tool.result.>"},
	"USER":  {"user.>", "semdev.park-post.request"},
}

// TestBootstrapDeclaresAgenticExecutionPlane — the bootstrap config declares all
// four agentic-execution components, each enabled and bound to its framework
// factory, and the AGENT/TOOL/USER streams they run over. Red-first: this fails
// until the plane is added to the config (it was deferred from the shared-runtime
// path), and would fail again if any component or stream were removed.
func TestBootstrapDeclaresAgenticExecutionPlane(t *testing.T) {
	cfg, err := loadBootstrap(repoRoot(t))
	if err != nil {
		t.Fatalf("load bootstrap: %v", err)
	}

	got := map[string]agenticComponent{
		"agentic-tools":    {Name: cfg.Components.AgenticTools.Name, Enabled: cfg.Components.AgenticTools.Enabled},
		"agentic-model":    cfg.Components.AgenticModel,
		"agentic-loop":     cfg.Components.AgenticLoop,
		"agentic-dispatch": cfg.Components.AgenticDispatch,
	}
	for key, wantName := range wantAgenticComponents {
		c := got[key]
		if c.Name != wantName {
			t.Errorf("component %q: name = %q, want %q (the factory selector)", key, c.Name, wantName)
		}
		if !c.Enabled {
			t.Errorf("component %q must be enabled — a disabled agentic component runs no agents", key)
		}
	}

	for name, wantSubjects := range wantAgenticStreams {
		s, ok := cfg.Streams[name]
		if !ok {
			t.Errorf("stream %q not declared; the plane's ports bind to it by stream_name", name)
			continue
		}
		if !slices.Equal(s.Subjects, wantSubjects) {
			t.Errorf("stream %q subjects = %v, want %v", name, s.Subjects, wantSubjects)
		}
	}
}
