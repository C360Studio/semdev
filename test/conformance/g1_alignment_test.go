package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/registry"
)

// alignmentNotesDoc is the checked-in record of every G1 addition's justification.
const alignmentNotesDoc = "docs/alignment-notes.md"

func alignmentAnchors(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot(t), alignmentNotesDoc))
	if err != nil {
		t.Fatalf("read %s: %v", alignmentNotesDoc, err)
	}
	return markdownH2Anchors(src)
}

// G1 / alignment notes — every semdev component or tool in the registry links a
// framework-alignment note (which primitive was considered, why it cannot do
// this), and that note must exist. At M0 the registry is empty; the pin holds
// the format and guards the boundary as Go lands.
func TestRegistryEntriesHaveAlignmentNotes(t *testing.T) {
	anchors := alignmentAnchors(t)
	for _, msg := range alignmentNoteViolations(registry.Entries, anchors) {
		t.Error(msg)
	}
}

// The alignment-notes doc itself must exist and carry the format section, so the
// discipline is documented before the first note is needed (G10).
func TestAlignmentNotesDocIsPresent(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), alignmentNotesDoc))
	if err != nil {
		t.Fatalf("read %s: %v", alignmentNotesDoc, err)
	}
	anchors := markdownH2Anchors(src)
	if !anchors["Note format"] {
		t.Errorf("%s is missing its 'Note format' section; the G1 note discipline is undocumented", alignmentNotesDoc)
	}
}

// Red-first: the census must flag an entry with a missing note and an entry
// whose note does not resolve to a heading.
func TestAlignmentCensusCatchesViolations(t *testing.T) {
	anchors := map[string]bool{"measurement-tool": true}

	noNote := []registry.Entry{{Name: "x", Kind: registry.KindTool}}
	if len(alignmentNoteViolations(noNote, anchors)) == 0 {
		t.Error("census passed an entry with no alignment note; G1 pin does not fire")
	}
	danglingNote := []registry.Entry{{Name: "y", Kind: registry.KindTool, AlignmentNote: "no-such-anchor"}}
	if len(alignmentNoteViolations(danglingNote, anchors)) == 0 {
		t.Error("census passed an entry whose note has no heading; G1 pin does not fire")
	}
}
