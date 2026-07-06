package conformance

import (
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// 4.7 — round-trip fidelity is the OpenSpec compatibility test, and semdev's own
// m0 change is the first fixture. The graph-authoritative content — the spec
// deltas (requirements + scenarios) and the tasks — must survive Facts∘FromFacts
// unchanged, so a change hydrated from graph facts reproduces what was ingested.
//
// Boundary (documented, not a bug): the engine projects the graph-authoritative
// SPEC content (proposal intent/scope, specs, tasks) to facts; freeform proposal/
// design prose under non-canonical headings lands in ExtraSections, which are not
// fact-projected. semdev's own proposal.md/design.md use narrative headings
// ("## Why", "## Decisions"), so this test asserts the specs+tasks round-trip —
// the content the graph owns. semdev-GENERATED changes emit the canonical modeled
// fields by construction (create_change), so they round-trip fully; the donor
// add-mfa fixture proves that full-content path in internal/openspec.
func TestM0ChangeSpecContentRoundTrips(t *testing.T) {
	changeDir := filepath.Join(repoRoot(t), "openspec", "changes", activeChangeSlug)

	c1, err := openspec.ReadChange(changeDir)
	if err != nil {
		t.Fatalf("read change %s: %v", changeDir, err)
	}
	if len(c1.Deltas) == 0 {
		t.Fatal("m0 change parsed zero spec deltas; the round-trip pin would pass vacuously")
	}
	if c1.Tasks == nil || len(c1.Tasks.Sections) == 0 {
		t.Fatal("m0 change parsed no tasks; the round-trip pin would pass vacuously")
	}

	facts := c1.Facts()
	c2 := openspec.ChangeFromFacts(activeChangeSlug, facts)

	// Deltas: requirements + scenarios must be identical (Title/Warnings are not
	// fact-modeled and are expected to differ).
	if diff := cmp.Diff(c1.Deltas, c2.Deltas,
		cmpopts.IgnoreFields(openspec.Delta{}, "Title", "Warnings")); diff != "" {
		t.Errorf("spec deltas did not round-trip through facts (-ingested +hydrated):\n%s", diff)
	}

	// Tasks: sections, numbers, text, done state must be identical.
	if diff := cmp.Diff(c1.Tasks, c2.Tasks,
		cmpopts.IgnoreFields(openspec.Tasks{}, "Title")); diff != "" {
		t.Errorf("tasks did not round-trip through facts (-ingested +hydrated):\n%s", diff)
	}
}
