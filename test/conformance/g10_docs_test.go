package conformance

import (
	"os"
	"path/filepath"
	"testing"

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
