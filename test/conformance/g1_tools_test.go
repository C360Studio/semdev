package conformance

import (
	"context"
	"testing"

	"github.com/c360studio/semdev/internal/registry"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/processor/agentic-tools/executors"
)

func toolNameSet(reg *agentictools.ExecutorRegistry) map[string]bool {
	out := make(map[string]bool)
	for _, def := range reg.ListTools() {
		out[def.Name] = true
	}
	return out
}

// frameworkToolNames is the baseline set registered by the framework builtins
// alone — the tools semdev does not own.
func frameworkToolNames(t *testing.T) map[string]bool {
	t.Helper()
	reg := agentictools.NewExecutorRegistry()
	if err := executors.RegisterBuiltins(context.Background(), reg, executors.ToolDependencies{}); err != nil {
		t.Fatalf("register builtin tools: %v", err)
	}
	return toolNameSet(reg)
}

// G1 (tools) — every agentic tool semdev registers beyond the framework baseline
// must have a KindTool registry entry (and thus an alignment note). A tool wired
// into boot.RegisterTools with no registry entry — invisible to G1 unless it
// happens to trip G3 — fails the build. At M0 semdev owns no tools; the pin
// guards the boundary as tools land (task 5.x/7.x).
func TestSemdevToolsAreRegistered(t *testing.T) {
	framework := frameworkToolNames(t)
	full := toolNameSet(semdevToolRegistry(t))

	added := make(map[string]bool)
	for name := range full {
		if !framework[name] {
			added[name] = true
		}
	}

	declared := registry.ToolNames()
	for _, msg := range undeclaredRegistrations("tool", added, declared) {
		t.Error(msg)
	}
	// No phantom tool entries: every declared tool must actually be registered.
	for name := range declared {
		if !full[name] {
			t.Errorf("registry declares tool %q that boot.RegisterTools does not register", name)
		}
	}
}
