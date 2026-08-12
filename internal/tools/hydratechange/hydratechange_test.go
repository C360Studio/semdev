package hydratechange

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeReader serves the triples create_change would have stamped for a change.
type fakeReader struct {
	triples []message.Triple
}

func (f *fakeReader) ReadFacts(_ context.Context, _ string, prefix string) ([]message.Triple, error) {
	var out []message.Triple
	for _, t := range f.triples {
		if strings.HasPrefix(t.Predicate, prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

// stamped builds the single document blob triple create_change would have
// written on the run entity (beta.147 D3: the whole change serialized into ONE
// scalar, not a slug-scoped triple tree).
func stamped(runEntityID string, c *openspec.Change) []message.Triple {
	raw, err := changefacts.MarshalDocument(changefacts.ChangeDocument{Change: c})
	if err != nil {
		panic("marshal fixture document: " + err.Error()) // test fixture construction only
	}
	return []message.Triple{{Subject: runEntityID, Predicate: changefacts.DocumentPredicate, Object: raw}}
}

func sampleChange() *openspec.Change {
	return &openspec.Change{
		Slug:     "fix-null-deref",
		Proposal: &openspec.Proposal{Intent: "fix the crash"},
		Tasks: &openspec.Tasks{Sections: []openspec.TaskSection{{
			Name:  "1. Fix",
			Tasks: []openspec.Task{{Number: "1.1", Text: "add the guard"}},
		}}},
	}
}

func call(slug string) agentic.ToolCall {
	return agentic.ToolCall{
		ID:        "c1",
		Name:      ToolName,
		Metadata:  map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
		Arguments: map[string]any{"slug": slug},
	}
}

// The tool renders the change from the run's facts: the returned document carries
// the proposal, tasks, and their file-path markers — a projection of the graph.
func TestRenderOpenspecProjectsFacts(t *testing.T) {
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	res, err := New(r, nil).Execute(context.Background(), call("fix-null-deref"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("a render is terminal for the turn; want StopLoop")
	}
	for _, want := range []string{
		"openspec/changes/fix-null-deref/proposal.md",
		"fix the crash",
		"openspec/changes/fix-null-deref/tasks.md",
		"1.1 add the guard",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("rendered document missing %q:\n%s", want, res.Content)
		}
	}
}

// A run with no authored change document at all must fail loudly, not return a
// hollow header — an un-authored change cannot masquerade as a rendered one
// (honest evidence). Beta.147 D3 collapsed the document to ONE flat predicate at
// M0 (single-change) — Hydrate no longer scopes the read by slug, so the pre-D3
// "wrong slug on an otherwise-populated run" case has no analogue.
func TestRenderOpenspecFailsOnEmptyChange(t *testing.T) {
	r := &fakeReader{} // no document fact at all
	res, _ := New(r, nil).Execute(context.Background(), call("never-authored"))
	if res.Error == "" {
		t.Error("expected an error rendering a run with no authored change")
	}
}

// Missing run-entity metadata fails loudly (the silent-subject trap).
func TestRenderOpenspecFailsWithoutRunEntity(t *testing.T) {
	c := call("fix-null-deref")
	c.Metadata = nil
	res, _ := New(&fakeReader{}, nil).Execute(context.Background(), c)
	if res.Error == "" {
		t.Error("expected an error when agent.run_entity_id is missing")
	}
}

// A nil reader fails loudly rather than pretending an empty render.
func TestRenderOpenspecFailsWithoutReader(t *testing.T) {
	res, _ := New(nil, nil).Execute(context.Background(), call("fix-null-deref"))
	if res.Error == "" {
		t.Error("expected an error when no fact reader is wired")
	}
}

func TestRenderOpenspecRequiresSlug(t *testing.T) {
	c := call("")
	res, _ := New(&fakeReader{}, nil).Execute(context.Background(), c)
	if res.Error == "" {
		t.Error("expected an error when slug is empty")
	}
}

// The schema advertises read-only content input — only the slug, and no
// outcome-shaped field (G3, trivially — hydrate stamps nothing).
func TestSchemaIsContentOnly(t *testing.T) {
	defs := New(nil, nil).ListTools()
	if len(defs) != 1 || defs[0].Name != ToolName {
		t.Fatalf("want one tool %q, got %+v", ToolName, defs)
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if _, ok := props["slug"]; !ok {
		t.Error("schema is missing the slug property")
	}
	for _, forbidden := range []string{"pass", "passed", "exit_code", "success", "outcome", "validated", "resolved", "done"} {
		if _, ok := props[forbidden]; ok {
			t.Errorf("schema exposes forbidden field %q (G3)", forbidden)
		}
	}
}
