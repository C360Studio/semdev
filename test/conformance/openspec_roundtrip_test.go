package conformance

import (
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/google/go-cmp/cmp"
)

// 4.7 — round-trip fidelity is the OpenSpec compatibility test, and semdev's own
// m0 change is the first fixture. The graph-authoritative content — the spec
// deltas (requirements + scenarios) and the tasks — must survive projection to
// the graph and hydration back unchanged, so what render_openspec shows a human
// is what create_change ingested.
//
// The projection under test is the CANONICAL one: the whole change as ONE
// openspec.change.document blob (beta.147 D3, internal/changefacts) — the shape
// create_change writes and Hydrate reads. The earlier fine-grained
// Facts/ChangeFromFacts triple-tree adapter was retired with the blob flatten
// (it had no product caller; two projections of one artifact is drift surface),
// so this pin rides Marshal∘Unmarshal of the real document. The blob carries
// the WHOLE change — proposal and design included, not just the spec deltas the
// old fine-grained projection modeled — so the comparison is the full Change,
// strict, no ignored fields. (The all-fields schema pin, covering Modified/
// Removed/Design shapes the m0 fixture doesn't exercise, lives in
// internal/changefacts's document round-trip test.)
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

	blob, err := changefacts.MarshalDocument(changefacts.ChangeDocument{Change: c1})
	if err != nil {
		t.Fatalf("marshal change document: %v", err)
	}
	doc, err := changefacts.UnmarshalDocument(blob)
	if err != nil {
		t.Fatalf("unmarshal change document: %v", err)
	}
	c2 := doc.Change
	if c2 == nil {
		t.Fatal("hydrated document carries no change")
	}

	// The FULL change — slug, proposal, design, deltas, tasks — must be
	// identical after the round-trip.
	if diff := cmp.Diff(c1, c2); diff != "" {
		t.Errorf("change did not round-trip through the document blob (-ingested +hydrated):\n%s", diff)
	}
}
