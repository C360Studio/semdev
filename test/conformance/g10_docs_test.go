package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/registry"
	"github.com/c360studio/semdev/internal/vocab"
)

const architectureDoc = "docs/architecture.md"

// G10 — docs tell the truth. The architecture doc's fact-vocabulary table is a
// projection of the code registry and is pinned to it: every predicate row must
// match `internal/vocab.Predicates` on (name, writer, capability), in both
// directions. semspec's CLAUDE.md described ten components that did not exist;
// this makes a doc that drifts from the registry fail the build.
func TestDocsVocabularyMatchesRegistry(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), architectureDoc))
	if err != nil {
		t.Fatalf("read %s: %v", architectureDoc, err)
	}
	rows := markdownTableRows(src, "Fact vocabulary")
	if len(rows) == 0 {
		t.Fatalf("no fact-vocabulary table found in %s; the G10 pin would pass vacuously", architectureDoc)
	}

	type triple struct{ name, writer, capability string }
	docSet := make(map[triple]bool)
	for _, cells := range rows {
		if len(cells) < 3 {
			t.Errorf("malformed vocabulary row (want 3 cells): %v", cells)
			continue
		}
		docSet[triple{cells[0], cells[1], cells[2]}] = true
	}

	codeSet := make(map[triple]bool)
	for _, p := range vocab.Predicates {
		codeSet[triple{p.Name, p.Writer, p.Capability}] = true
	}

	for tr := range codeSet {
		if !docSet[tr] {
			t.Errorf("vocabulary entry %+v is in the code registry but not in %s (docs drift, G10)", tr, architectureDoc)
		}
	}
	for tr := range docSet {
		if !codeSet[tr] {
			t.Errorf("vocabulary row %+v is in %s but not in the code registry (stale doc, G10)", tr, architectureDoc)
		}
	}
}

// G10 — the architecture doc's Components table is a projection of the code's
// component/tool registry (internal/registry.Entries) and is pinned to it: every
// row must match on (name, kind, capability, alignment note), in both directions.
// semdev's own change registered four openspec-io tools while the doc still said
// "None yet"; this pin makes that stale-topology drift fail the build.
func TestDocsComponentsMatchRegistry(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), architectureDoc))
	if err != nil {
		t.Fatalf("read %s: %v", architectureDoc, err)
	}
	rows := markdownTableRows(src, "Components")
	if len(rows) == 0 {
		t.Fatalf("no Components table found in %s; the G10 pin would pass vacuously (did the section revert to prose?)", architectureDoc)
	}

	type entry struct{ name, kind, capability, note string }
	docSet := make(map[entry]bool)
	for _, cells := range rows {
		if len(cells) < 4 {
			t.Errorf("malformed Components row (want 4 cells): %v", cells)
			continue
		}
		docSet[entry{cells[0], cells[1], cells[2], cells[3]}] = true
	}

	codeSet := make(map[entry]bool)
	for _, e := range registry.Entries {
		codeSet[entry{e.Name, string(e.Kind), e.Capability, e.AlignmentNote}] = true
	}

	for e := range codeSet {
		if !docSet[e] {
			t.Errorf("registry entry %+v is in internal/registry but not in %s Components table (docs drift, G10)", e, architectureDoc)
		}
	}
	for e := range docSet {
		if !codeSet[e] {
			t.Errorf("Components row %+v is in %s but not in the code registry (stale/phantom doc row, G10)", e, architectureDoc)
		}
	}
}

// Red-first: the Components census must catch drift in either direction through
// the real markdown parser, so a phantom doc row or a missing tool cannot slip by.
func TestDocsComponentsCensusCatchesDrift(t *testing.T) {
	doc := []byte("# X\n\n## Components\n\nprose intro.\n\n" +
		"| Name | Kind | Capability | Alignment note |\n" +
		"|------|------|------------|----------------|\n" +
		"| `create_change` | tool | openspec-io | `create-change-author-tool` |\n" +
		"| `phantom_tool` | tool | openspec-io | `phantom-note` |\n")

	rows := markdownTableRows(doc, "Components")
	if len(rows) != 2 {
		t.Fatalf("parser returned %d rows, want 2 (prose-before-table + separator handling): %v", len(rows), rows)
	}

	type entry struct{ name, kind, capability, note string }
	docSet := make(map[entry]bool)
	for _, cells := range rows {
		if len(cells) < 4 {
			t.Fatalf("parser produced a short row: %v", cells)
		}
		docSet[entry{cells[0], cells[1], cells[2], cells[3]}] = true
	}
	// Code set: create_change matches; validate_change is absent from the doc;
	// phantom_tool is in the doc but not the code.
	codeSet := map[entry]bool{
		{"create_change", "tool", "openspec-io", "create-change-author-tool"}:    true,
		{"validate_change", "tool", "openspec-io", "validate-change-cli-oracle"}: true,
	}

	var missingFromDoc, phantomInDoc int
	for e := range codeSet {
		if !docSet[e] {
			missingFromDoc++
		}
	}
	for e := range docSet {
		if !codeSet[e] {
			phantomInDoc++
		}
	}
	if missingFromDoc == 0 || phantomInDoc == 0 {
		t.Errorf("Components drift census under-fires: missingFromDoc=%d phantomInDoc=%d (want both >0)", missingFromDoc, phantomInDoc)
	}
}

// Red-first: the docs pin must catch drift in either direction, driving the REAL
// markdown parser (markdownTableRows) so a parser bug — mishandling the
// separator, backticks, or a fence — cannot silently mask a drift.
func TestDocsVocabularyCensusCatchesDrift(t *testing.T) {
	doc := []byte("# X\n\n## Fact vocabulary\n\n" +
		"| Predicate | Writer | Capability |\n" +
		"|-----------|--------|------------|\n" +
		"| `a.b` | right-writer | forge-io |\n" +
		"| `c.d` | WRONG-writer | forge-io |\n")

	rows := markdownTableRows(doc, "Fact vocabulary")
	if len(rows) != 2 {
		t.Fatalf("parser returned %d rows, want 2 (separator/header handling): %v", len(rows), rows)
	}

	type triple struct{ name, writer, capability string }
	docSet := make(map[triple]bool)
	for _, cells := range rows {
		if len(cells) < 3 {
			t.Fatalf("parser produced a short row: %v", cells)
		}
		docSet[triple{cells[0], cells[1], cells[2]}] = true
	}
	// Code set: a.b matches; c.d's writer differs (doc drifted); e.f is absent
	// from the doc entirely.
	codeSet := map[triple]bool{
		{"a.b", "right-writer", "forge-io"}: true,
		{"c.d", "right-writer", "forge-io"}: true,
		{"e.f", "w", "forge-io"}:            true,
	}

	var missingFromDoc, staleInDoc int
	for tr := range codeSet {
		if !docSet[tr] {
			missingFromDoc++
		}
	}
	for tr := range docSet {
		if !codeSet[tr] {
			staleInDoc++
		}
	}
	if missingFromDoc == 0 || staleInDoc == 0 {
		t.Errorf("drift census under-fires through the real parser: missingFromDoc=%d staleInDoc=%d (want both >0)", missingFromDoc, staleInDoc)
	}
}
