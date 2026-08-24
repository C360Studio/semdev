package conformance

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/pkg/projection"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/standards"
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

// No AUTHORED PROMPT may contain the standards injection prefix — not a persona
// fragment, and not a rule action's `prompt` field.
//
// A prompt that shows a specimen standard — even as an illustration of the format —
// puts a line into every brief for that role which is byte-indistinguishable from a
// standard the target repository actually declared. The persona is told to treat those
// lines as the repo's law and, for the reviewer, to cite them in findings. So an example
// becomes a constraint the run enforces against work no repository asked to be judged
// that way, and a reviewer citing it blocks approval on a rule that does not exist.
//
// Found by running the D9 journey and PRINTING what each brief carried, not by reading
// the fragments: the first draft of both standards fragments opened with a fenced
// example, and the developer and reviewer briefs duly arrived carrying forged ids.
// Describe the format in prose; never spell a specimen.
//
// BOTH surfaces are scanned because both are brief text. `04-dispatch-developer.json`'s
// on_enter prompt IS Amelia's user message verbatim, so a specimen there forges a
// standard on every run of that station with exactly the same indistinguishability.
// (The UNTRUSTED channels — issue text reaching task.spec, a diff read via read_diff,
// a file read via read_workspace, a finding's quoted command output — cannot be closed
// by a scan. Those are bounded in the persona fragments themselves, which now teach
// that a standard arrives only inside the brief's lessons block and a bracketed id met
// anywhere else carries no authority.)
func TestNoAuthoredPromptForgesAStandard(t *testing.T) {
	root := repoRoot(t)

	fragments := filepath.Join(root, "configs", "personas", "fragments")
	scannedFragments := 0
	err := filepath.WalkDir(fragments, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		scannedFragments++
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), standards.InjectionPrefix) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("persona fragment %s contains the standards injection prefix %q — every brief for "+
				"that role would carry a standard no target repository declared, and the persona cannot "+
				"distinguish it from a real one. Describe the format in prose instead of showing a specimen",
				rel, standards.InjectionPrefix)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk persona fragments: %v", err)
	}
	if scannedFragments == 0 {
		t.Fatal("no persona fragments were scanned; the fragment half of this pin would pass vacuously")
	}

	rules, err := loadRules(root)
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	scannedPrompts := 0
	for _, r := range rules {
		for _, a := range append(append(append(append([]ruleAction{}, r.OnEnter...), r.OnExit...), r.WhileTrue...), r.OnRecovery...) {
			if a.Prompt == "" {
				continue
			}
			scannedPrompts++
			if strings.Contains(a.Prompt, standards.InjectionPrefix) {
				t.Errorf("rule %q dispatches a prompt containing the standards injection prefix %q — a spawn "+
					"prompt is brief text, so this forges a standard on every run of that station",
					r.ID, standards.InjectionPrefix)
			}
		}
	}
	if scannedPrompts == 0 {
		t.Fatal("no rule action prompts were scanned; the rule half of this pin would pass vacuously")
	}
	t.Logf("scanned %d persona fragments and %d rule action prompts for forged standards",
		scannedFragments, scannedPrompts)
}
