package applypatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakePatcher struct {
	touched  []string
	err      error
	gotDiff  string
	gotRunID string
}

func (f *fakePatcher) Apply(_ context.Context, runEntityID, diff string) ([]string, error) {
	f.gotRunID = runEntityID
	f.gotDiff = diff
	return f.touched, f.err
}

func call(diff string) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Arguments: map[string]any{"diff": diff},
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

// The tool passes the developer's diff through to the patcher (targeting THIS run's
// checkout) and reports the touched files — it measures no outcome (G3).
func TestApplyPassesDiffAndReportsFiles(t *testing.T) {
	p := &fakePatcher{touched: []string{"pkg/health/health.go"}}
	res, err := New(p, nil).Execute(context.Background(), call("--- a/x\n+++ b/x\n"))
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if p.gotRunID != runEntity {
		t.Errorf("patcher got run %q, want %q", p.gotRunID, runEntity)
	}
	if !strings.Contains(p.gotDiff, "+++ b/x") {
		t.Errorf("patcher got diff %q, want the authored diff", p.gotDiff)
	}
	if !res.StopLoop {
		t.Error("apply_patch must end the authoring turn (StopLoop)")
	}
	if !strings.Contains(res.Content, "pkg/health/health.go") {
		t.Errorf("result should report the touched files, got %q", res.Content)
	}
}

// A rejected diff (path escape / malformed / conflict) surfaces as a tool error the
// developer re-authors from — never a silent success.
func TestApplyRejectionIsAToolError(t *testing.T) {
	p := &fakePatcher{err: errors.New("target \"../etc/x\" escapes the checkout")}
	res, err := New(p, nil).Execute(context.Background(), call("--- a/../etc/x\n+++ b/../etc/x\n"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a rejected diff must surface as a tool error")
	}
}

// Schema-only registration (nil patcher) fails loudly — never a silent skip.
func TestApplyFailsLoudlyWithoutPatcher(t *testing.T) {
	res, err := New(nil, nil).Execute(context.Background(), call("--- a/x\n+++ b/x\n"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-patcher apply_patch must fail loudly")
	}
}

// A call with no run-entity id fails loudly — the tool cannot target a checkout.
func TestApplyMissingRunEntityFailsLoudly(t *testing.T) {
	res, err := New(&fakePatcher{}, nil).Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolName, Arguments: map[string]any{"diff": "x"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a call with no run entity id must fail loudly")
	}
}

// G3: the schema exposes only `diff` and no outcome field — the developer authors,
// it cannot assert its change works.
func TestApplySchemaTakesOnlyDiff(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 1 {
		t.Fatalf("schema exposes %d properties, want exactly 1 (diff): %v", len(props), props)
	}
	if _, ok := props["diff"]; !ok {
		t.Errorf("schema must expose a `diff` property, got %v", props)
	}
	for _, banned := range []string{"passed", "outcome", "result", "success", "pass"} {
		if _, ok := props[banned]; ok {
			t.Errorf("schema exposes an outcome-like field %q — apply_patch takes no outcome (G3)", banned)
		}
	}
}
