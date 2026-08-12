package brownfield

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/specfacts"
	"github.com/c360studio/semdev/internal/vocab"
	"github.com/c360studio/semstreams/vocabulary"
)

// writeSpec lays out specsDir/<cap>/spec.md with the given content.
func writeSpec(t *testing.T, specsDir, capability, content string) {
	t.Helper()
	dir := filepath.Join(specsDir, capability)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write spec.md: %v", err)
	}
}

const cleanSpec = `# Auth Specification

## Purpose
Guard the front door.

## Requirements

### Requirement: Token check
The system SHALL reject an expired token.

#### Scenario: Expired token
- WHEN a request carries an expired token
- THEN it is rejected
`

// ProjectSpecs reads a repo's living specs into ONE canonical openspec.spec.document blob
// per capability, deterministically, under one owner, with the raw bytes retained by
// reference (source_ref inside the blob) — and no model in the loop.
func TestProjectSpecsSeedsDocumentWithProvenance(t *testing.T) {
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "auth", cleanSpec)

	p, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(p.Docs) != 1 {
		t.Fatalf("want one capability document, got %d", len(p.Docs))
	}
	d := p.Docs[0]
	if d.Capability != "auth" {
		t.Errorf("capability = %q, want auth", d.Capability)
	}

	// The document unmarshals to the parsed spec — the whole capability in one scalar.
	doc, err := specfacts.UnmarshalDocument(d.Document)
	if err != nil {
		t.Fatalf("unmarshal document: %v", err)
	}
	if doc.Spec == nil {
		t.Fatal("document carries no spec")
	}
	if doc.Spec.Title != "Auth Specification" {
		t.Errorf("spec title = %q, want %q", doc.Spec.Title, "Auth Specification")
	}
	if doc.Spec.Purpose == "" {
		t.Error("no purpose in the projected spec")
	}
	if len(doc.Spec.Requirements) == 0 || doc.Spec.Requirements[0].Statement == "" {
		t.Error("no requirement statement in the projected spec")
	}
	// Diagnostics do not travel in the content blob (the projector clears them).
	if doc.Spec.Warnings != nil {
		t.Errorf("spec Warnings leaked into the content blob: %v", doc.Spec.Warnings)
	}

	// Provenance: source_ref inside the blob points at the retained raw bytes.
	if doc.SourceRef == "" {
		t.Fatal("no source_ref provenance in the document")
	}
	if d.Source.Ref != doc.SourceRef {
		t.Errorf("retained artifact ref %q != document source_ref %q", d.Source.Ref, doc.SourceRef)
	}
	if string(d.Source.Bytes) != cleanSpec {
		t.Error("retained artifact bytes are not the source file's raw bytes")
	}
}

// The projection is deterministic: same input → identical document objects (byte-for-byte),
// in sorted-capability order, so the ingest path is reproducible and model-free.
func TestProjectSpecsIsDeterministic(t *testing.T) {
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "billing", cleanSpec)
	writeSpec(t, specsDir, "auth", cleanSpec)

	a, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project a: %v", err)
	}
	b, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project b: %v", err)
	}
	if len(a.Docs) != len(b.Docs) {
		t.Fatalf("doc count differs across runs: %d vs %d", len(a.Docs), len(b.Docs))
	}
	for i := range a.Docs {
		if a.Docs[i].Capability != b.Docs[i].Capability || a.Docs[i].Document != b.Docs[i].Document {
			t.Errorf("doc %d differs across runs (non-deterministic projection)", i)
		}
	}
	// Sorted-capability order regardless of write order (auth before billing).
	if len(a.Docs) != 2 || a.Docs[0].Capability != "auth" || a.Docs[1].Capability != "billing" {
		t.Errorf("capabilities not in sorted order: %+v", a.Docs)
	}
}

// A spec using bullet/heading-case variance and a malformed block parses leniently: it
// still yields a document, and the lossy case is surfaced as a warning (never a hard
// failure), with provenance intact.
func TestProjectSpecsLenientVarianceKeepsProvenance(t *testing.T) {
	const variant = `# Payments

## purpose
Move money.

#### Scenario: Orphan before any requirement
* WHEN this scenario has no requirement
* THEN the parser drops it and warns

## REQUIREMENTS

### Requirement: Idempotent charge
The system SHALL charge at most once.

#### Scenario: Retry
+ WHEN a charge is retried
+ THEN no second charge occurs
`
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "payments", variant)

	p, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("lenient parse must not hard-fail: %v", err)
	}
	if len(p.Docs) != 1 {
		t.Fatalf("want one document, got %d", len(p.Docs))
	}
	doc, err := specfacts.UnmarshalDocument(p.Docs[0].Document)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Heading-case variance ("## purpose", "## REQUIREMENTS") is tolerated: content projects.
	if doc.Spec == nil || doc.Spec.Purpose == "" {
		t.Error("lower-case '## purpose' heading was not tolerated")
	}
	var sawReq bool
	for _, r := range doc.Spec.Requirements {
		if strings.Contains(r.Statement, "at most once") {
			sawReq = true
		}
	}
	if !sawReq {
		t.Error("requirement under an upper-case '## REQUIREMENTS' heading was not projected")
	}
	// The orphan scenario is surfaced as a warning, not lost silently.
	if len(p.Warnings) == 0 {
		t.Error("expected a warning for the scenario before any requirement")
	}
	for _, w := range p.Warnings {
		if !strings.HasPrefix(w, "payments: ") {
			t.Errorf("warning is not attributed to its capability: %q", w)
		}
	}
	// Provenance survives a lenient parse.
	if doc.SourceRef == "" {
		t.Error("provenance dropped on a lenient parse")
	}
}

// A repo with no openspec/specs/ dir is not an error — onboarding a repo that has none
// yields an empty projection.
func TestProjectSpecsAbsentDirIsEmptyNotError(t *testing.T) {
	p, err := ProjectSpecs(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("absent specs dir must not error: %v", err)
	}
	if len(p.Docs) != 0 {
		t.Errorf("expected an empty projection, got %+v", p)
	}
}

// Triples stamps ONE canonical openspec.spec.document triple per capability under the
// single owner (== the vocab writer), on that capability's spec entity — G5-verifiable —
// and the predicate is CANONICAL, so the beta.150 fail-closed graph-write gate accepts it.
func TestTriplesStampSingleOwnerMatchingVocab(t *testing.T) {
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "auth", cleanSpec)
	p, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(p.Docs) != 1 {
		t.Fatalf("want one document, got %d", len(p.Docs))
	}

	const subject = "org.plat.openspec.spec.capability.auth"
	triples := Triples(subject, p.Docs[0], time.Unix(0, 0).UTC())
	if len(triples) != 1 {
		t.Fatalf("Triples produced %d, want 1 (one document per capability entity)", len(triples))
	}
	tr := triples[0]

	writer, ok := vocab.WriterOf(specfacts.DocumentPredicate)
	if !ok {
		t.Fatalf("%s has no vocab writer", specfacts.DocumentPredicate)
	}
	if Source != writer {
		t.Errorf("projector Source %q != vocab writer %q for %s — G5 unverifiable drift", Source, writer, specfacts.DocumentPredicate)
	}
	if tr.Subject != subject {
		t.Errorf("triple subject = %q, want %q", tr.Subject, subject)
	}
	if tr.Source != Source {
		t.Errorf("triple Source = %q, want the single owner %q", tr.Source, Source)
	}
	if tr.Predicate != specfacts.DocumentPredicate {
		t.Errorf("triple predicate = %q, want %q", tr.Predicate, specfacts.DocumentPredicate)
	}
	// The whole point of the flatten (semstreams beta.150): the predicate is CANONICAL, so
	// the fail-closed graph-ingest gate would accept the write instead of rejecting it.
	if !vocabulary.IsValidPredicate(tr.Predicate) {
		t.Errorf("projected predicate %q is NOT canonical — beta.150 graph-ingest would reject it", tr.Predicate)
	}
}
