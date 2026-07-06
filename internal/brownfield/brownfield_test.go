package brownfield

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/vocab"
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

// ProjectSpecs reads a repo's living specs into openspec.spec.<cap>.* facts
// deterministically, under one owner, with the raw bytes retained by reference —
// and no model in the loop.
func TestProjectSpecsSeedsFactsWithProvenance(t *testing.T) {
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "auth", cleanSpec)

	p, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	// Content facts are projected under the capability's openspec.spec.* subtree.
	facts := factMap(p.Facts)
	if got := facts["openspec.spec.auth.title"]; got != "Auth Specification" {
		t.Errorf("title fact = %q, want %q", got, "Auth Specification")
	}
	if _, ok := facts["openspec.spec.auth.purpose"]; !ok {
		t.Error("no purpose fact projected")
	}
	var sawRequirement bool
	for pred := range facts {
		if strings.HasPrefix(pred, "openspec.spec.auth.requirement.") && strings.HasSuffix(pred, ".statement") {
			sawRequirement = true
		}
	}
	if !sawRequirement {
		t.Error("no requirement statement fact projected")
	}

	// Provenance: a source_ref fact points at the retained raw bytes.
	ref, ok := facts["openspec.spec.auth.source_ref"]
	if !ok || ref == "" {
		t.Fatal("no source_ref provenance fact projected")
	}
	if len(p.Sources) != 1 {
		t.Fatalf("want one retained source artifact, got %d", len(p.Sources))
	}
	src := p.Sources[0]
	if src.Ref != ref {
		t.Errorf("source_ref fact %q does not match retained artifact ref %q", ref, src.Ref)
	}
	if string(src.Bytes) != cleanSpec {
		t.Error("retained artifact bytes are not the source file's raw bytes")
	}
}

// The projection is deterministic: same input → identical facts (byte-for-byte),
// so the ingest path is reproducible and model-free.
func TestProjectSpecsIsDeterministic(t *testing.T) {
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "auth", cleanSpec)
	writeSpec(t, specsDir, "billing", cleanSpec)

	a, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project a: %v", err)
	}
	b, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project b: %v", err)
	}
	if len(a.Facts) != len(b.Facts) {
		t.Fatalf("fact count differs across runs: %d vs %d", len(a.Facts), len(b.Facts))
	}
	for i := range a.Facts {
		if a.Facts[i] != b.Facts[i] {
			t.Errorf("fact %d differs across runs: %+v vs %+v", i, a.Facts[i], b.Facts[i])
		}
	}
}

// A spec using bullet/heading-case variance and a malformed block parses
// leniently: it still yields facts, and the lossy case is surfaced as a warning
// (never a hard failure), with provenance intact.
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

	facts := factMap(p.Facts)
	// Heading-case variance ("## purpose", "## REQUIREMENTS") is tolerated: the
	// content still projects.
	if _, ok := facts["openspec.spec.payments.purpose"]; !ok {
		t.Error("lower-case '## purpose' heading was not tolerated")
	}
	var sawReq bool
	for pred, obj := range facts {
		if strings.Contains(pred, ".requirement.") && strings.HasSuffix(pred, ".statement") && strings.Contains(obj, "at most once") {
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
	if _, ok := facts["openspec.spec.payments.source_ref"]; !ok {
		t.Error("provenance dropped on a lenient parse")
	}
}

// A repo with no openspec/specs/ dir is not an error — onboarding a repo that has
// none yields an empty projection.
func TestProjectSpecsAbsentDirIsEmptyNotError(t *testing.T) {
	p, err := ProjectSpecs(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("absent specs dir must not error: %v", err)
	}
	if len(p.Facts) != 0 || len(p.Sources) != 0 {
		t.Errorf("expected an empty projection, got %+v", p)
	}
}

// Triples stamps the single owner (== the vocab writer for openspec.spec.*) on
// every fact, so the whole ingest is attributable and G5-verifiable.
func TestTriplesStampSingleOwnerMatchingVocab(t *testing.T) {
	specsDir := t.TempDir()
	writeSpec(t, specsDir, "auth", cleanSpec)
	p, err := ProjectSpecs(specsDir)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	const subject = "org.plat.openspec.spec.capability.auth"
	triples := Triples(subject, p, time.Unix(0, 0).UTC())
	if len(triples) != len(p.Facts) {
		t.Fatalf("Triples produced %d, want %d (one per fact)", len(triples), len(p.Facts))
	}

	writer, ok := vocab.WriterOf("openspec.spec.auth.title")
	if !ok {
		t.Fatal("openspec.spec.* has no vocab writer")
	}
	if Source != writer {
		t.Errorf("projector Source %q != vocab writer %q for openspec.spec.* — G5 unverifiable drift", Source, writer)
	}
	for _, tr := range triples {
		if tr.Subject != subject {
			t.Errorf("triple subject = %q, want %q", tr.Subject, subject)
		}
		if tr.Source != Source {
			t.Errorf("triple Source = %q, want the single owner %q", tr.Source, Source)
		}
		if !strings.HasPrefix(tr.Predicate, "openspec.spec.") {
			t.Errorf("projected predicate %q is not under openspec.spec.*", tr.Predicate)
		}
	}
}

// factMap flattens a fact list to predicate→object for lookup. First write wins,
// matching the engine's index semantics.
func factMap(facts []openspec.Fact) map[string]string {
	m := make(map[string]string, len(facts))
	for _, f := range facts {
		if _, ok := m[f.Predicate]; !ok {
			m[f.Predicate] = f.Object
		}
	}
	return m
}
