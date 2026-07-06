package createchange

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

// fakeWriter records replace calls and returns a scripted prior owned package, so
// the replace-by-predicate path is testable without a live NATS graph.
type fakeWriter struct {
	owned    []string // what ReadOwnedPredicates returns (the prior package)
	replaces []replaceCall
}

type replaceCall struct {
	add    []message.Triple
	remove []string
}

func (f *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, remove []string) error {
	f.replaces = append(f.replaces, replaceCall{add: add, remove: remove})
	return nil
}
func (f *fakeWriter) ReadOwnedPredicates(_ context.Context, _ string, _ string) ([]string, error) {
	return f.owned, nil
}

const runEntity = "org.plat.agent.chain.execution.run-1"

func sampleCall() agentic.ToolCall {
	return agentic.ToolCall{
		ID:       "c1",
		Name:     ToolName,
		Metadata: map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{
			"slug":     "fix-null-deref",
			"proposal": map[string]any{"intent": "fix the crash", "scope_in": []any{"the handler"}},
			"deltas": []any{map[string]any{
				"capability": "handler",
				"added": []any{map[string]any{
					"name":      "Nil guard",
					"statement": "The system SHALL guard nil input.",
					"scenarios": []any{map[string]any{
						"name": "Nil input",
						"steps": []any{
							map[string]any{"kw": "WHEN", "text": "input is nil"},
							map[string]any{"kw": "THEN", "text": "no panic occurs"},
						},
					}},
				}},
			}},
			"tasks": []any{map[string]any{
				"section": "1. Fix",
				"items":   []any{map[string]any{"number": "1.1", "text": "add the guard", "done": false}},
			}},
		},
	}
}

// The tool stamps openspec.change.* facts, all on the RUN entity (D15), tagged
// with the vocab writer Source, and never an outcome fact.
func TestCreateChangeStampsFactsOnRunEntity(t *testing.T) {
	w := &fakeWriter{}
	res, err := New(w, nil).Execute(context.Background(), sampleCall())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("author result should set StopLoop so a re-author is not auto-triggered in-loop")
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected one replace mutation, got %d", len(w.replaces))
	}
	triples := w.replaces[0].add
	if len(triples) == 0 {
		t.Fatal("no triples stamped")
	}

	var sawIntent, sawReq, sawTask bool
	for _, tr := range triples {
		if tr.Subject != runEntity {
			t.Errorf("triple subject = %q, want the run entity %q (D15)", tr.Subject, runEntity)
		}
		if !strings.HasPrefix(tr.Predicate, "openspec.change.") {
			t.Errorf("predicate %q is not under openspec.change.*", tr.Predicate)
		}
		if tr.Source != Source {
			t.Errorf("triple Source = %q, want the vocab writer %q (G5)", tr.Source, Source)
		}
		if strings.Contains(tr.Predicate, "outcome") || strings.HasSuffix(tr.Predicate, ".validated") || strings.HasSuffix(tr.Predicate, ".pass") {
			t.Errorf("tool stamped an outcome-shaped fact %q (G3 violation)", tr.Predicate)
		}
		switch {
		case tr.Predicate == "openspec.change.fix-null-deref.proposal.intent":
			sawIntent = tr.Object == "fix the crash"
		case strings.Contains(tr.Predicate, ".delta.handler.nil-guard.statement"):
			sawReq = true
		case strings.Contains(tr.Predicate, ".task.0.text"):
			sawTask = true
		}
	}
	if !sawIntent {
		t.Error("proposal intent fact not stamped")
	}
	if !sawReq {
		t.Error("requirement delta fact not stamped")
	}
	if !sawTask {
		t.Error("task fact not stamped")
	}
}

// Re-author REPLACES the owned package: the prior predicates are cleared (passed
// as removePredicates) so a shrunk/renamed re-author leaves no phantom facts.
func TestCreateChangeReAuthorReplacesPackage(t *testing.T) {
	prior := []string{
		"openspec.change.fix-null-deref.task.9.text", // a task that no longer exists
		"openspec.change.fix-null-deref.delta.handler.old-req.statement",
	}
	w := &fakeWriter{owned: prior}
	res, err := New(w, nil).Execute(context.Background(), sampleCall())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected one replace mutation, got %d", len(w.replaces))
	}
	got := w.replaces[0].remove
	for _, p := range prior {
		if !contains(got, p) {
			t.Errorf("re-author did not clear prior owned predicate %q — a phantom fact would linger", p)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// The author tool must NOT honor a model-supplied task-completion field: a
// freshly authored change's tasks are never pre-checked. Task status is DERIVED
// from execution markers and gate facts (dev-from-task spec), not authored.
func TestCreateChangeIgnoresAuthoredTaskCompletion(t *testing.T) {
	call := sampleCall()
	// Inject done:true into the authored task args.
	items := call.Arguments["tasks"].([]any)[0].(map[string]any)["items"].([]any)
	items[0].(map[string]any)["done"] = true

	w := &fakeWriter{}
	res, err := New(w, nil).Execute(context.Background(), call)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	for _, tr := range w.replaces[0].add {
		if strings.HasSuffix(tr.Predicate, ".task.0.done") {
			if tr.Object != "false" {
				t.Errorf("authored done:true was honored — %s = %v, want \"false\" (status must be derived, not authored)", tr.Predicate, tr.Object)
			}
			return
		}
	}
	t.Error("no task.0.done fact stamped")
}

// A traversal slug is rejected at the authoring source and writes no facts, so a
// model cannot seed an openspec.change.* namespace (or a downstream changes/<slug>/
// path) that escapes the tree.
func TestCreateChangeRejectsUnsafeSlug(t *testing.T) {
	for _, bad := range []string{"../../etc/passwd", "a/b", ".", "..", "foo/../bar"} {
		call := sampleCall()
		call.Arguments["slug"] = bad
		w := &fakeWriter{}
		res, _ := New(w, nil).Execute(context.Background(), call)
		if res.Error == "" {
			t.Errorf("slug %q was accepted; want a rejection", bad)
		}
		if len(w.replaces) != 0 {
			t.Errorf("slug %q wrote %d fact mutations; want none (rejected before any write)", bad, len(w.replaces))
		}
	}
}

// Missing run-entity metadata fails loudly (the silent-subject trap).
func TestCreateChangeFailsWithoutRunEntity(t *testing.T) {
	call := sampleCall()
	call.Metadata = nil
	res, _ := New(&fakeWriter{}, nil).Execute(context.Background(), call)
	if res.Error == "" {
		t.Error("expected an error when agent.run_entity_id is missing")
	}
}

// A nil writer fails loudly rather than dropping facts.
func TestCreateChangeFailsWithoutWriter(t *testing.T) {
	res, _ := New(nil, nil).Execute(context.Background(), sampleCall())
	if res.Error == "" {
		t.Error("expected an error when no owned-fact writer is wired")
	}
}

// The schema advertises content-only input — no outcome field (G3) and no
// task-completion field anywhere (an authoring tool must not let the model
// pre-complete tasks).
func TestSchemaHasNoOutcomeOrCompletionField(t *testing.T) {
	defs := New(nil, nil).ListTools()
	if len(defs) != 1 || defs[0].Name != ToolName {
		t.Fatalf("want one tool %q, got %+v", ToolName, defs)
	}
	forbidden := []string{"pass", "passed", "exit_code", "success", "outcome", "validated", "resolved", "done", "completed", "complete"}
	for _, name := range forbidden {
		if schemaHasProperty(defs[0].Parameters, name) {
			t.Errorf("schema exposes forbidden field %q (G3 / status-is-derived)", name)
		}
	}
}

// schemaHasProperty reports whether name appears as a property key anywhere in a
// JSON schema (recursing into properties, items, and composition keywords).
func schemaHasProperty(schema map[string]any, name string) bool {
	if props, ok := schema["properties"].(map[string]any); ok {
		for k, v := range props {
			if k == name {
				return true
			}
			if sub, ok := v.(map[string]any); ok && schemaHasProperty(sub, name) {
				return true
			}
		}
	}
	for _, key := range []string{"items", "anyOf", "allOf", "oneOf"} {
		switch v := schema[key].(type) {
		case map[string]any:
			if schemaHasProperty(v, name) {
				return true
			}
		case []any:
			for _, e := range v {
				if m, ok := e.(map[string]any); ok && schemaHasProperty(m, name) {
					return true
				}
			}
		}
	}
	return false
}
