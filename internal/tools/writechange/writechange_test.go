package writechange

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// fakeReader serves the triples create_change would have stamped, honoring the
// scoping prefix like the real reader.
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

// tempResolver resolves the change dir under a fixed root, standing in for the
// forge-io/clean-room checkout the real resolver will wrap.
type tempResolver struct {
	root string
	err  error
}

func (r tempResolver) ChangeDir(_ context.Context, _ string, slug string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return filepath.Join(r.root, "openspec", "changes", slug), nil
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
		Deltas: []openspec.Delta{{
			Capability: "handler",
			Added: []openspec.Requirement{{
				Name:      "Nil guard",
				Statement: "The system SHALL guard nil input.",
				Scenarios: []openspec.Scenario{{Name: "Nil input", Steps: []openspec.Step{{Keyword: "WHEN", Text: "input is nil"}, {Keyword: "THEN", Text: "no panic"}}}},
			}},
		}},
		Tasks: &openspec.Tasks{Sections: []openspec.TaskSection{{Name: "1. Fix", Tasks: []openspec.Task{{Number: "1.1", Text: "add the guard"}}}}},
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

// The tool hydrates the change from the run's facts and writes a real OpenSpec
// change folder into the workspace: proposal.md, tasks.md, and the per-capability
// delta spec — and it round-trips (ReadChange of what was written is semantically
// equal to the hydrated change).
func TestWriteChangeMaterializesFolder(t *testing.T) {
	root := t.TempDir()
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	res, err := New(r, tempResolver{root: root}, nil).Execute(context.Background(), call("fix-null-deref"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	if !res.StopLoop {
		t.Error("write is one action per turn; want StopLoop")
	}

	dir := filepath.Join(root, "openspec", "changes", "fix-null-deref")
	for _, rel := range []string{"proposal.md", "tasks.md", filepath.Join("specs", "handler", "spec.md")} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}

	// What landed on disk re-reads to the same change (the write is faithful).
	readBack, err := openspec.ReadChange(dir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if a, b := openspec.RenderChangeFolder(readBack), openspec.RenderChangeFolder(sampleChange()); a != b {
		t.Errorf("written change is not semantically equal to the hydrated one.\n--- disk ---\n%s\n--- want ---\n%s", a, b)
	}
}

// A run with no authored change document at all must not materialize a hollow
// change folder. Beta.147 D3 collapsed the document to ONE flat predicate at M0
// (single-change) — Hydrate no longer scopes the read by slug, so the pre-D3
// "wrong slug on an otherwise-populated run" case has no analogue.
func TestWriteChangeFailsOnEmptyChange(t *testing.T) {
	root := t.TempDir()
	r := &fakeReader{} // no document fact at all
	res, _ := New(r, tempResolver{root: root}, nil).Execute(context.Background(), call("never-authored"))
	if res.Error == "" {
		t.Fatal("expected an error writing a run with no authored change")
	}
	if _, err := os.Stat(filepath.Join(root, "openspec", "changes", "never-authored")); !os.IsNotExist(err) {
		t.Error("an empty change must not create a folder on disk")
	}
}

// A traversal slug is rejected before any dir resolution or filesystem write, so
// write_change cannot escape the checkout even if a resolver would naively join it.
func TestWriteChangeRejectsUnsafeSlug(t *testing.T) {
	root := t.TempDir()
	r := &fakeReader{triples: stamped(runEntity, sampleChange())}
	for _, bad := range []string{"../../etc", "a/b", "..", "foo/../bar"} {
		res, _ := New(r, tempResolver{root: root}, nil).Execute(context.Background(), call(bad))
		if res.Error == "" {
			t.Errorf("slug %q was accepted; want a rejection", bad)
		}
	}
	// Nothing escaped the temp root (no openspec tree created for a bad slug).
	if _, err := os.Stat(filepath.Join(root, "openspec")); !os.IsNotExist(err) {
		t.Error("a rejected slug still touched the filesystem")
	}
}

func TestWriteChangeFailsWithoutRunEntity(t *testing.T) {
	c := call("fix-null-deref")
	c.Metadata = nil
	res, _ := New(&fakeReader{}, tempResolver{root: t.TempDir()}, nil).Execute(context.Background(), c)
	if res.Error == "" {
		t.Error("expected an error when agent.run_entity_id is missing")
	}
}

func TestWriteChangeFailsWithoutReaderOrResolver(t *testing.T) {
	if res, _ := New(nil, tempResolver{root: t.TempDir()}, nil).Execute(context.Background(), call("fix-null-deref")); res.Error == "" {
		t.Error("expected an error when no fact reader is wired")
	}
	if res, _ := New(&fakeReader{}, nil, nil).Execute(context.Background(), call("fix-null-deref")); res.Error == "" {
		t.Error("expected an error when no workspace resolver is wired")
	}
}

func TestWriteChangeRequiresSlug(t *testing.T) {
	res, _ := New(&fakeReader{}, tempResolver{root: t.TempDir()}, nil).Execute(context.Background(), call(""))
	if res.Error == "" {
		t.Error("expected an error when slug is empty")
	}
}

// The schema advertises content-only input — slug, no outcome-shaped field (G3).
func TestSchemaIsContentOnly(t *testing.T) {
	defs := New(nil, nil, nil).ListTools()
	if len(defs) != 1 || defs[0].Name != ToolName {
		t.Fatalf("want one tool %q, got %+v", ToolName, defs)
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if _, ok := props["slug"]; !ok {
		t.Error("schema is missing the slug property")
	}
	for _, forbidden := range []string{"pass", "passed", "exit_code", "success", "outcome", "validated", "resolved", "done", "dir", "path"} {
		if _, ok := props[forbidden]; ok {
			t.Errorf("schema exposes forbidden field %q (G3 / no model-supplied path)", forbidden)
		}
	}
}
