package conformance

import (
	"slices"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/pkg/projection"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/vocab"
)

// RED-FIRST PIN (standards-via-lessons 1.1a): the repo-standards source-entity
// vocabulary — the change's ENTIRE G9 cost. Three predicates, one writer, one
// capability, provenance to this change.
func TestRepoStandardsVocabDeclared(t *testing.T) {
	want := map[string]bool{
		"repo.standards.digest": false,
		"repo.standards.path":   false,
		"repo.standards.repo":   false,
	}
	for _, p := range vocab.Predicates {
		if _, ok := want[p.Name]; !ok {
			continue
		}
		want[p.Name] = true
		if p.Writer != "standards-sync" {
			t.Errorf("%s writer = %q, want standards-sync", p.Name, p.Writer)
		}
		if p.Capability != "repo-standards" {
			t.Errorf("%s capability = %q, want repo-standards", p.Name, p.Capability)
		}
		if p.IntroducedBy != "standards-via-lessons" {
			t.Errorf("%s introduced-by = %q, want standards-via-lessons", p.Name, p.IntroducedBy)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("predicate %s not declared in vocab", name)
		}
	}
}

// RED-FIRST PIN (standards-via-lessons 1.1b): the standards-sync owner derives a
// strict-birth contract on the source entity class — the same Create lane as the
// admission record (conflict = the idempotent duplicate signal).
func TestStandardsSyncContractDerived(t *testing.T) {
	if !graphown.IsCreateOwner("standards-sync") {
		t.Fatal("standards-sync is not a create owner — the source entity must be born on the strict Create lane")
	}
	all, err := graphown.Contracts()
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	for _, oc := range all {
		if oc.Owner != "standards-sync" {
			continue
		}
		c := oc.Contract
		if c.EntityPattern != "*.*.repo.standards.source.*" {
			t.Errorf("standards-sync entity pattern = %q, want *.*.repo.standards.source.*", c.EntityPattern)
		}
		wantBirth := []string{"repo.standards.digest", "repo.standards.path", "repo.standards.repo"}
		got := slices.Clone(c.BirthPredicates)
		slices.Sort(got)
		if !slices.Equal(got, wantBirth) {
			t.Errorf("standards-sync birth predicates = %v, want %v", got, wantBirth)
		}
		if len(c.Groups) != 0 {
			t.Errorf("standards-sync contract carries reconcile groups %v — a create owner owns nothing post-birth", c.Groups)
		}
		return
	}
	t.Fatal("no derived contract for owner standards-sync")
}

// RED-FIRST PIN (standards-via-lessons 1.1c): the HAND-MIRRORED lesson-record
// contract. The framework's authoritative copy lives in an internal package
// (semstreams internal/builtinprojection/contracts.go — LessonRecordContractName /
// LessonLifecycleGroupName) that semdev cannot import, so this pin locks semdev's
// mirror to the upstream literals; the standards journey is the behavioral proof
// the mirror matches the wire. On a semstreams bump, re-verify against that file.
func TestLessonRecordContractMirror(t *testing.T) {
	all, err := graphown.AllContracts()
	if err != nil {
		t.Fatalf("AllContracts: %v", err)
	}
	for _, oc := range all {
		if oc.Contract.Name != "agentic.lesson-record" {
			continue
		}
		c := oc.Contract
		if oc.Owner != "ops-lesson-curator" {
			t.Errorf("mirror owner = %q, want the framework curator source ops-lesson-curator", oc.Owner)
		}
		if c.MessageType != agentic.AgentLessonMessageType().Key() {
			t.Errorf("mirror message type = %q, want %q", c.MessageType, agentic.AgentLessonMessageType().Key())
		}
		if c.EntityPattern != "*.*.agent.lesson.record.*" {
			t.Errorf("mirror entity pattern = %q", c.EntityPattern)
		}
		wantBirth := []string{
			agvocab.LessonCategory, agvocab.LessonPolarity, agvocab.LessonSeverity,
			agvocab.LessonCreatedAt, agvocab.LessonSummary, agvocab.LessonDetail,
			agvocab.LessonInjectionForm, agvocab.LessonEvidence, agvocab.LessonAppliesTo,
			agvocab.LessonObservedRole, agvocab.ActionExecutedBy,
		}
		gotBirth := slices.Clone(c.BirthPredicates)
		slices.Sort(gotBirth)
		slices.Sort(wantBirth)
		if !slices.Equal(gotBirth, wantBirth) {
			t.Errorf("mirror birth predicates = %v, want %v", gotBirth, wantBirth)
		}
		if len(c.Groups) != 1 || c.Groups[0].Name != "lesson-lifecycle" {
			t.Fatalf("mirror groups = %+v, want exactly the lesson-lifecycle group", c.Groups)
		}
		if c.Groups[0].Mode != projection.ModeReconcile {
			t.Errorf("lesson-lifecycle mode = %v, want ModeReconcile", c.Groups[0].Mode)
		}
		wantGroup := []string{agvocab.LessonStatus, agvocab.LessonSupersededBy, agvocab.LessonRetiredAt}
		gotGroup := slices.Clone(c.Groups[0].Predicates)
		slices.Sort(gotGroup)
		slices.Sort(wantGroup)
		if !slices.Equal(gotGroup, wantGroup) {
			t.Errorf("lesson-lifecycle group = %v, want %v", gotGroup, wantGroup)
		}
		return
	}
	t.Fatal("no agentic.lesson-record contract in the graphown contract set — the CLAUDE.md mirror precondition is unmet")
}
