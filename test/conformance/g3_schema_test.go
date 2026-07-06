package conformance

import (
	"context"
	"testing"

	"github.com/c360studio/semdev/internal/boot"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/processor/agentic-tools/executors"
)

// semdevToolRegistry builds the tool registry through boot.RegisterTools — the
// SAME seam production uses — so the census and production registration cannot
// drift. At M0 that is framework builtins only (bash, http_request, web_search;
// semdev owns none yet); when semdev's first tool lands (task 7.1) it registers
// in boot.RegisterTools and this census covers it automatically, no test edit.
func semdevToolRegistry(t *testing.T) *agentictools.ExecutorRegistry {
	t.Helper()
	reg := agentictools.NewExecutorRegistry()
	// Empty githubToken → each host tool deterministically takes its schema-only
	// nil path regardless of the ambient GITHUB_TOKEN, so the census is hermetic.
	if err := boot.RegisterTools(context.Background(), reg, executors.ToolDependencies{}, ""); err != nil {
		t.Fatalf("boot.RegisterTools: %v", err)
	}
	return reg
}

// G3 — no LLM-supplied outcome facts. No registered tool's input schema may
// accept an outcome-shaped field (pass/passed/exit_code/success/resolved/
// outcome) from the caller; the harness that runs the command stamps the
// outcome, the model may only claim. A measurement tool that took `pass` from
// the model — semteams' one structural gap — fails the build.
func TestNoToolSchemaAcceptsOutcomeField(t *testing.T) {
	tools := semdevToolRegistry(t).ListTools()
	if len(tools) == 0 {
		t.Fatal("tool registry is empty; the G3 census would pass vacuously")
	}
	for _, def := range tools {
		for _, msg := range outcomeFieldViolations(def.Name, def.Parameters) {
			t.Error(msg)
		}
	}
}

// Red-first: the census must flag a schema that accepts an outcome field
// (including one nested inside an object), and must not false-flag a clean
// schema.
func TestG3CensusCatchesOutcomeField(t *testing.T) {
	topLevel := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"pass":    map[string]any{"type": "boolean"},
		},
	}
	if len(outcomeFieldViolations("fake_measure", topLevel)) == 0 {
		t.Error("census missed a top-level outcome field; G3 pin does not fire")
	}

	nested := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"exit_code": map[string]any{"type": "integer"},
				},
			},
		},
	}
	if len(outcomeFieldViolations("fake_nested", nested)) == 0 {
		t.Error("census missed a nested outcome field; G3 pin does not recurse")
	}

	clean := map[string]any{
		"type":       "object",
		"properties": map[string]any{"command": map[string]any{"type": "string"}},
	}
	if len(outcomeFieldViolations("bash", clean)) != 0 {
		t.Error("census false-flagged a clean schema")
	}
}
