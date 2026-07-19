package createchange

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/changefacts"
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

// stampedDocument runs the tool and decodes the stamped openspec.change.document
// blob (beta.147 D3: create_change owns exactly {document,slug,revision} — no more
// slug-scoped triple tree). Returns the decoded document plus the raw triples for
// Source/subject/outcome-shape assertions.
func stampedDocument(t *testing.T, call agentic.ToolCall) (changefacts.ChangeDocument, []message.Triple) {
	t.Helper()
	w := &fakeWriter{}
	res, err := New(w, testPlatform, nil).Execute(context.Background(), call)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("expected one replace, got %d", len(w.replaces))
	}
	triples := w.replaces[0].add
	raw := objectOf(triples, changefacts.DocumentPredicate)
	doc, err := changefacts.UnmarshalDocument(raw)
	if err != nil {
		t.Fatalf("decode stamped document: %v", err)
	}
	return doc, triples
}

// The tool stamps exactly the three flat openspec.change.* facts (D3: document
// blob, slug, revision), all on the RUN entity (D15), tagged with the vocab writer
// Source, and never an outcome fact. The document decodes back to the authored
// content.
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
	if len(triples) != 3 {
		t.Fatalf("expected exactly 3 flat facts (document/slug/revision), got %d: %+v", len(triples), triples)
	}

	var doc string
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
		if tr.Predicate == changefacts.DocumentPredicate {
			doc, _ = tr.Object.(string)
		}
	}
	if doc == "" {
		t.Fatal("no document blob stamped")
	}

	decoded, err := changefacts.UnmarshalDocument(doc)
	if err != nil {
		t.Fatalf("decode stamped document: %v", err)
	}
	if decoded.Change == nil {
		t.Fatal("decoded document has a nil Change")
	}
	if decoded.Change.Proposal == nil || decoded.Change.Proposal.Intent != "fix the crash" {
		t.Errorf("decoded proposal intent = %+v, want %q", decoded.Change.Proposal, "fix the crash")
	}
	if len(decoded.Change.Deltas) != 1 || len(decoded.Change.Deltas[0].Added) != 1 || decoded.Change.Deltas[0].Added[0].Statement != "The system SHALL guard nil input." {
		t.Errorf("decoded delta requirement missing/wrong: %+v", decoded.Change.Deltas)
	}
	if decoded.Change.Tasks == nil || len(decoded.Change.Tasks.Sections) != 1 || len(decoded.Change.Tasks.Sections[0].Tasks) != 1 || decoded.Change.Tasks.Sections[0].Tasks[0].Text != "add the guard" {
		t.Errorf("decoded task missing/wrong: %+v", decoded.Change.Tasks)
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

// Re-author REPLACES the owned package: beta.147 D3 fixed the owned set to exactly
// three flat predicates (document/slug/revision) — create_change no longer discovers
// what to clear via ReadOwnedPredicates (there is no slug-scoped tree to enumerate),
// it clears the FIXED set unconditionally on every execute. That means a shrunk or
// renamed re-author can never leave a phantom fact, independent of whatever a prior
// author wrote.
func TestCreateChangeReAuthorReplacesPackage(t *testing.T) {
	w := &fakeWriter{}
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
	want := []string{changefacts.DocumentPredicate, SlugPredicate, RevisionPredicate}
	for _, p := range want {
		if !contains(got, p) {
			t.Errorf("re-author did not clear owned predicate %q — a phantom fact would linger", p)
		}
	}
	if len(got) != len(want) {
		t.Errorf("remove-list = %v, want exactly the fixed owned package %v", got, want)
	}
}

// The run-level slug pointer (openspec.change.slug = slug) is stamped in the same
// atomic run-facts replace so the approval-triggered projection rule can thread
// this run's slug into project_tasks. It is added to removePredicates so a
// re-author overwrites it.
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
		t.Errorf("slug pointer %s must be in removePredicates so a re-author overwrites it", SlugPredicate)
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

// D15 #0: create_change stamps the flat content revision (openspec.change.revision,
// single-change at M0 — beta.147 D1 dropped the slug from the key) on the run —
// project_tasks binds it, and validate_change echoes it into
// openspec.change.validated — with the openspec.change.* writer Source and a
// sha256 prefix (so the rule engine never compares it numerically).
func TestCreateChangeStampsContentRevision(t *testing.T) {
	w := &fakeWriter{}
	if res, err := New(w, testPlatform, nil).Execute(context.Background(), sampleCall()); err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	triples := w.replaces[0].add
	rev := objectOf(triples, RevisionPredicate)
	if rev == "" {
		t.Errorf("%s not stamped — project_tasks cannot bind content", RevisionPredicate)
	}
	if !strings.HasPrefix(rev, "sha256:") {
		t.Errorf("revision %q must be sha256-prefixed so the rule engine never compares it numerically", rev)
	}
	for _, tr := range triples {
		if tr.Predicate == RevisionPredicate && tr.Source != Source {
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
		return objectOf(w.replaces[0].add, RevisionPredicate)
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
// from execution markers and gate facts (dev-from-task spec), not authored. The
// completion state now lives inside the decoded document's Task.Done (D3 — no
// standalone task.<i>.done fact anymore).
func TestCreateChangeIgnoresAuthoredTaskCompletion(t *testing.T) {
	call := sampleCall()
	// Inject done:true into the authored task args.
	items := call.Arguments["tasks"].([]any)[0].(map[string]any)["items"].([]any)
	items[0].(map[string]any)["done"] = true

	doc, _ := stampedDocument(t, call)
	if doc.Change.Tasks == nil || len(doc.Change.Tasks.Sections) == 0 || len(doc.Change.Tasks.Sections[0].Tasks) == 0 {
		t.Fatal("no task decoded from the stamped document")
	}
	if doc.Change.Tasks.Sections[0].Tasks[0].Done {
		t.Error("authored done:true was honored — task completion must be derived, not authored (G3)")
	}
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

// TestSchemaTargetFilesNamesTheIncludesTestContract pins the contract-
// communication lane the FIRST REAL-LLM RUN failed on (first-real-llm-journey,
// run 1, 2026-07-19): the projector REJECTS a task whose target_files carry no
// *_test.go (projecttasks — "the developer could not author the test that
// measures its own work"), but the schema's target_files description never
// TOLD the model. An enforced-but-uncommunicated contract is a paid-run
// failure by construction: the model authored an otherwise-valid change whose
// only task listed just the source file, and the run died at projection. The
// model-facing description must name the contract.
func TestSchemaTargetFilesNamesTheIncludesTestContract(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) == 0 {
		t.Fatal("ListTools returned no definitions")
	}
	raw, err := json.Marshal(defs[0].Parameters)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	// Walk to the tasks item properties' target_files description.
	desc := ""
	var walk func(v any)
	walk = func(v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if tf, ok := m["target_files"].(map[string]any); ok {
			if d, ok := tf["description"].(string); ok {
				desc = d
			}
		}
		for _, child := range m {
			walk(child)
		}
	}
	walk(schema)
	if desc == "" {
		t.Fatal("schema carries no target_files description at all")
	}
	if !strings.Contains(desc, "_test.go") {
		t.Errorf("target_files description does not name the *_test.go requirement — the projector enforces it, so the model must be told; got: %q", desc)
	}
	if !strings.Contains(desc, "reject") && !strings.Contains(desc, "REJECT") {
		t.Errorf("target_files description does not say the projector REJECTS a task without its test — the consequence is part of the contract; got: %q", desc)
	}
}
