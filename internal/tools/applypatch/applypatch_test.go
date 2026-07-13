package applypatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakePatcher struct {
	touched  []string
	sha      string
	err      error
	gotDiff  string
	gotRunID string
}

func (f *fakePatcher) Apply(_ context.Context, runEntityID, diff string) ([]string, string, error) {
	f.gotRunID = runEntityID
	f.gotDiff = diff
	return f.touched, f.sha, f.err
}

// fakeWriter captures the last ReplaceTriples call so a test can assert the stamped
// attempt.commit. err makes ReplaceTriples fail (the retryable-transport posture).
type fakeWriter struct {
	err        error
	gotEntity  string
	gotTriples []message.Triple
	gotReplace []string
}

func (f *fakeWriter) ReplaceTriples(_ context.Context, entityID string, add []message.Triple, removePredicates []string) error {
	f.gotEntity = entityID
	f.gotTriples = add
	f.gotReplace = removePredicates
	return f.err
}

func (f *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
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
// checkout), reports the touched files, and stamps the committed attempt.commit — it
// measures no outcome (G3).
func TestApplyPassesDiffAndReportsFiles(t *testing.T) {
	p := &fakePatcher{touched: []string{"pkg/health/health.go"}, sha: "abc1234def"}
	w := &fakeWriter{}
	res, err := New(p, w, nil).Execute(context.Background(), call("--- a/x\n+++ b/x\n"))
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if p.gotRunID != runEntity {
		t.Errorf("patcher got run %q, want %q", p.gotRunID, runEntity)
	}
	if !strings.Contains(p.gotDiff, "+++ b/x") {
		t.Errorf("patcher got diff %q, want the authored diff", p.gotDiff)
	}
	if res.StopLoop {
		t.Error("apply_patch must NOT StopLoop — it runs inside Amelia's multi-turn loop (group 4); she continues to measure")
	}
	if !strings.Contains(res.Content, "pkg/health/health.go") {
		t.Errorf("result should report the touched files, got %q", res.Content)
	}
	// attempt.commit stamped on the RUN, latest-wins (replace-by-predicate), with the SHA.
	if w.gotEntity != runEntity {
		t.Errorf("attempt.commit stamped on %q, want the run %q", w.gotEntity, runEntity)
	}
	if len(w.gotTriples) != 1 || w.gotTriples[0].Predicate != CommitPredicate || w.gotTriples[0].Object != "abc1234def" {
		t.Errorf("stamped triples = %+v, want one %s=abc1234def", w.gotTriples, CommitPredicate)
	}
	if w.gotTriples[0].Source != Source {
		t.Errorf("attempt.commit Source = %q, want the single writer %q (G5)", w.gotTriples[0].Source, Source)
	}
	if len(w.gotReplace) != 1 || w.gotReplace[0] != CommitPredicate {
		t.Errorf("replace-predicates = %v, want [%s] (latest-wins)", w.gotReplace, CommitPredicate)
	}
}

// A stamp failure is retryable transport, NOT StopLoop — the loop re-applies rather than
// measuring an attempt whose snapshot pointer never landed (verify would clone the wrong tree).
func TestApplyStampFailureIsRetryableNotStopLoop(t *testing.T) {
	p := &fakePatcher{touched: []string{"x"}, sha: "deadbeef"}
	w := &fakeWriter{err: errors.New("kv unavailable")}
	res, err := New(p, w, nil).Execute(context.Background(), call("--- a/x\n+++ b/x\n"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a stamp failure must surface as a tool error")
	}
	if res.StopLoop {
		t.Error("a stamp failure must NOT StopLoop — the loop retries")
	}
}

// A rejected diff (path escape / malformed / conflict) surfaces as a tool error the
// developer re-authors from — never a silent success.
func TestApplyRejectionIsAToolError(t *testing.T) {
	p := &fakePatcher{err: errors.New("target \"../etc/x\" escapes the checkout")}
	res, err := New(p, &fakeWriter{}, nil).Execute(context.Background(), call("--- a/../etc/x\n+++ b/../etc/x\n"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a rejected diff must surface as a tool error")
	}
}

// Schema-only registration (nil patcher) fails loudly — never a silent skip.
func TestApplyFailsLoudlyWithoutPatcher(t *testing.T) {
	res, err := New(nil, nil, nil).Execute(context.Background(), call("--- a/x\n+++ b/x\n"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-patcher apply_patch must fail loudly")
	}
}

// A call with no run-entity id fails loudly — the tool cannot target a checkout.
func TestApplyMissingRunEntityFailsLoudly(t *testing.T) {
	res, err := New(&fakePatcher{}, &fakeWriter{}, nil).Execute(context.Background(), agentic.ToolCall{ID: "c1", Name: ToolName, Arguments: map[string]any{"diff": "x"}})
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
