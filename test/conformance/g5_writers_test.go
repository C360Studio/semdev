package conformance

import (
	"testing"

	"github.com/c360studio/semdev/internal/vocab"
)

// G5 — single writer per fact. Every predicate maps to exactly one writer in the
// checked-in table. semspec's audit found four multi-writer facts, one whose
// "sole writer" claim was a false comment; here the property is a census, not a
// comment.
func TestSingleWriterPerPredicate(t *testing.T) {
	if len(vocab.Predicates) == 0 {
		t.Fatal("vocabulary is empty; the writers census would pass vacuously")
	}
	for _, v := range singleWriterViolations(vocab.Predicates) {
		t.Error(v)
	}
	for _, v := range duplicateNameViolations(vocab.Predicates) {
		t.Error(v)
	}
	for _, v := range namespaceWriterViolations(vocab.Predicates) {
		t.Error(v)
	}
}

// Red-first: the namespace census must flag a concrete predicate under a
// declared namespace that declares a different writer.
func TestNamespaceWriterCensusCatchesConflict(t *testing.T) {
	preds := []vocab.Predicate{
		{Name: "openspec.change.*", Writer: "author-tool", Capability: "openspec-io", IntroducedBy: "m0-walking-skeleton-spine"},
		{Name: "openspec.change.title", Writer: "some-other-writer", Capability: "openspec-io", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	if len(namespaceWriterViolations(preds)) == 0 {
		t.Error("census passed a namespace member with a conflicting writer; G5 namespace pin does not fire")
	}
}

// Red-first: the census must catch a predicate stamped by two writers — the
// exact shape that bred semspec's wedge families — and a writer-less predicate.
func TestSingleWriterCensusCatchesViolations(t *testing.T) {
	multiWriter := []vocab.Predicate{
		{Name: "x.y", Writer: "alpha", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"},
		{Name: "x.y", Writer: "beta", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	if len(singleWriterViolations(multiWriter)) == 0 {
		t.Error("census passed a predicate with two writers; G5 pin does not fire")
	}
	noWriter := []vocab.Predicate{{Name: "x.y", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"}}
	if len(singleWriterViolations(noWriter)) == 0 {
		t.Error("census passed a writer-less predicate; G5 pin does not fire")
	}
}
