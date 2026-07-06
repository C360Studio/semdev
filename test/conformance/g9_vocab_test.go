package conformance

import (
	"testing"

	"github.com/c360studio/semdev/internal/vocab"
)

// G9 — minimal vocabulary. Every predicate points at the OpenSpec change that
// introduced it (and that change exists), and is owned by a declared capability.
// semspec accreted 224 predicates with no provenance; semdev starts from zero
// and a fact with no introducing change — the shape of speculative accretion —
// fails the build.
func TestVocabularyProvenance(t *testing.T) {
	if len(vocab.Predicates) == 0 {
		t.Fatal("vocabulary is empty; the exhaustiveness census would pass vacuously")
	}
	validChanges := validChangeSlugs(t)
	validCaps := capabilities(t)
	for _, v := range provenanceViolations(vocab.Predicates, validChanges, validCaps) {
		t.Error(v)
	}
	for _, v := range duplicateNameViolations(vocab.Predicates) {
		t.Error(v)
	}
}

// Red-first: the census must catch a predicate with no introducing change, one
// whose change does not exist, and one owned by an undeclared capability.
func TestVocabularyProvenanceCatchesViolations(t *testing.T) {
	validChanges := map[string]bool{"m0-walking-skeleton-spine": true}
	validCaps := map[string]bool{"forge-io": true}

	bad := []vocab.Predicate{
		{Name: "a.b", Writer: "w", Capability: "forge-io", IntroducedBy: ""},
		{Name: "c.d", Writer: "w", Capability: "forge-io", IntroducedBy: "no-such-change"},
		{Name: "e.f", Writer: "w", Capability: "phantom-capability", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	got := provenanceViolations(bad, validChanges, validCaps)
	if len(got) < 3 {
		t.Errorf("provenance census caught %d of 3 planted violations; G9 pin under-fires: %v", len(got), got)
	}
}
