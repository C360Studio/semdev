package createchange

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/types"
)

// testPlatform matches the org/platform of runEntity so a loop entity id built
// from a test LoopID shares the same 6-part prefix.
var testPlatform = types.PlatformMeta{Org: "org", Platform: "plat"}

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
	res, err := New(w, testPlatform, nil).Execute(context.Background(), sampleCall())
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

// With a loop_id present, the tool stamps the authored marker
// (openspec.change.authored = slug) on the AUTHORING LOOP entity as a SECOND
// mutation after the run facts — the slug-independent signal the validate station
// fires on. It carries the vocab writer Source and upserts (removePredicates
// clears any prior marker).
func TestCreateChangeStampsAuthoredMarkerOnLoop(t *testing.T) {
	w := &fakeWriter{}
	call := sampleCall()
	call.LoopID = "loop-1"
	res, err := New(w, testPlatform, nil).Execute(context.Background(), call)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(w.replaces) != 2 {
		t.Fatalf("expected two mutations (run facts + loop marker), got %d", len(w.replaces))
	}
	marker := w.replaces[1]
	if len(marker.add) != 1 {
		t.Fatalf("marker mutation should add exactly one triple, got %d", len(marker.add))
	}
	tr := marker.add[0]
	if tr.Predicate != AuthoredPredicate {
		t.Errorf("marker predicate = %q, want %q", tr.Predicate, AuthoredPredicate)
	}
	if tr.Object != "fix-null-deref" {
		t.Errorf("marker object = %v, want the slug %q", tr.Object, "fix-null-deref")
	}
	if tr.Source != Source {
		t.Errorf("marker Source = %q, want the vocab writer %q (G5)", tr.Source, Source)
	}
	// Stamped on the LOOP entity (not the run) — the same loop-execution entity the
	// spawn identity (agent.loop.role, agent.run) lives on, so the validate rule can
	// match role + marker + inherit the run anchor on one entity.
	if !strings.HasSuffix(tr.Subject, ".execution.loop-1") || tr.Subject == runEntity {
		t.Errorf("marker subject = %q, want the authoring loop entity (…execution.loop-1)", tr.Subject)
	}
	if !contains(marker.remove, AuthoredPredicate) {
		t.Errorf("marker mutation must clear the prior %q (upsert), remove=%v", AuthoredPredicate, marker.remove)
	}
}

// Without a loop_id (a degenerate unit-test-only case), the marker is skipped but
// the change facts still land — the tool does not fail an otherwise-authored change.
func TestCreateChangeSkipsMarkerWithoutLoopID(t *testing.T) {
	w := &fakeWriter{}
	res, err := New(w, testPlatform, nil).Execute(context.Background(), sampleCall())
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected only the run-facts mutation (no marker without loop_id), got %d", len(w.replaces))
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
	res, err := New(w, testPlatform, nil).Execute(context.Background(), sampleCall())
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

// The run-level slug pointer (openspec.change.slug = slug) is stamped in the same
// atomic run-facts replace so the approval-triggered projection rule can thread
// this run's slug into project_tasks (the other change facts are slug-scoped, so a
// rule cannot wildcard the slug out of the key). It is added to removePredicates so
// a re-author overwrites it (it sits outside the slug-scoped owned prefix).
func TestCreateChangeStampsRunSlugPointer(t *testing.T) {
	w := &fakeWriter{}
	res, err := New(w, testPlatform, nil).Execute(context.Background(), sampleCall())
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	run := w.replaces[0]
	if got := objectOf(run.add, SlugPredicate); got != "fix-null-deref" {
		t.Errorf("run slug pointer %s = %q, want the slug %q", SlugPredicate, got, "fix-null-deref")
	}
	if !contains(run.remove, SlugPredicate) {
		t.Errorf("slug pointer %s must be in removePredicates so a re-author overwrites it (it is outside the slug-scoped owned prefix)", SlugPredicate)
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

// objectOf returns the object stamped for a predicate in a replace batch, or "".
func objectOf(triples []message.Triple, predicate string) string {
	for _, tr := range triples {
		if tr.Predicate == predicate {
			s, _ := tr.Object.(string)
			return s
		}
	}
	return ""
}

// D15 #0: create_change stamps the slug-scoped content revision
// (openspec.change.<slug>.revision) on the run — project_tasks binds it, and
// validate_change echoes it into openspec.validated — with the openspec.change.*
// writer Source and a sha256 prefix (so the rule engine never compares it
// numerically).
func TestCreateChangeStampsContentRevision(t *testing.T) {
	w := &fakeWriter{}
	if res, err := New(w, testPlatform, nil).Execute(context.Background(), sampleCall()); err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	triples := w.replaces[0].add
	slugRevPredicate := SlugRevisionPredicate("fix-null-deref")
	slugRev := objectOf(triples, slugRevPredicate)
	if slugRev == "" {
		t.Errorf("slug-scoped %s not stamped — project_tasks cannot bind slug+content", slugRevPredicate)
	}
	if !strings.HasPrefix(slugRev, "sha256:") {
		t.Errorf("revision %q must be sha256-prefixed so the rule engine never compares it numerically", slugRev)
	}
	for _, tr := range triples {
		if tr.Predicate == slugRevPredicate && tr.Source != Source {
			t.Errorf("revision Source = %q, want the vocab writer %q (G5)", tr.Source, Source)
		}
	}
}

// D15 #0 red-first: the revision changes iff the content changes — the property
// the whole freshness contract relies on. Re-authoring with a different intent
// yields a different revision; identical content yields the same one.
func TestCreateChangeRevisionTracksContent(t *testing.T) {
	revFor := func(mutate func(agentic.ToolCall)) string {
		w := &fakeWriter{}
		c := sampleCall()
		if mutate != nil {
			mutate(c)
		}
		if res, err := New(w, testPlatform, nil).Execute(context.Background(), c); err != nil || res.Error != "" {
			t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
		}
		return objectOf(w.replaces[0].add, SlugRevisionPredicate("fix-null-deref"))
	}

	base := revFor(nil)
	same := revFor(nil)
	if base != same {
		t.Errorf("identical content must yield the same revision, got %q and %q", base, same)
	}
	changed := revFor(func(c agentic.ToolCall) {
		c.Arguments["proposal"] = map[string]any{"intent": "a DIFFERENT intent", "scope_in": []any{"the handler"}}
	})
	if changed == base {
		t.Errorf("changed content must yield a different revision, but both were %q — a re-author would not self-invalidate a stale validation", base)
	}
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
	res, err := New(w, testPlatform, nil).Execute(context.Background(), call)
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
		res, _ := New(w, testPlatform, nil).Execute(context.Background(), call)
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
	res, _ := New(&fakeWriter{}, testPlatform, nil).Execute(context.Background(), call)
	if res.Error == "" {
		t.Error("expected an error when agent.run_entity_id is missing")
	}
}

// A nil writer fails loudly rather than dropping facts.
func TestCreateChangeFailsWithoutWriter(t *testing.T) {
	res, _ := New(nil, testPlatform, nil).Execute(context.Background(), sampleCall())
	if res.Error == "" {
		t.Error("expected an error when no owned-fact writer is wired")
	}
}

// The schema advertises content-only input — no outcome field (G3) and no
// task-completion field anywhere (an authoring tool must not let the model
// pre-complete tasks).
func TestSchemaHasNoOutcomeOrCompletionField(t *testing.T) {
	defs := New(nil, testPlatform, nil).ListTools()
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
