package conformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// G8 — realistic fixtures. A dev-loop fixture must read like a genuine target repo: it
// must NOT coach the fix (comment markers pointing at the bug) or reveal the harness
// (semdev/orchestration vocabulary a real repo would never carry). Both predecessors
// leaked coaching/orchestration into their fixtures, which let the loop "succeed"
// against a scaffold rather than real code. This pin scans the committed fixtures for
// that vocabulary so a coached fixture cannot slip in.

// coachingMarkers are comment markers that point a reader at the defect/fix. Matched at
// word boundaries, case-insensitively (so "debug" is not a false hit on "BUG").
var coachingMarkers = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX|BUG)\b`)

// harnessVocab are SHARP semdev-specific terms a genuine upstream repo's source/docs
// would never carry — naming one reveals the meta-context and can coach the agent.
// Matched as case-insensitive substrings, so each term is deliberately unambiguous
// (semdev's own names). Ambiguous, domain-plausible terms are intentionally EXCLUDED —
// e.g. `task.spec` (a real k8s-shaped `task.Spec` field) or "dev loop"/"the harness"
// (ordinary prose) — because this lint FAILS the build, and a false-positive would
// block a legitimately realistic fixture (the opposite of G8's intent). Coaching that
// dodges these sharp terms is caught by the marker regex + reviewer judgment.
var harnessVocab = []string{
	"semdev", "openspec", "apply_patch",
	"dispatch-developer", "clean-room verify",
	"amelia", "quinn",
}

// bannedFixtureTerms returns the coaching/orchestration terms found in text (empty when
// clean). Pure, so a red-first test can feed synthetic coached content and prove the
// scan fires.
func bannedFixtureTerms(text string) []string {
	var found []string
	found = append(found, coachingMarkers.FindAllString(text, -1)...)
	lower := strings.ToLower(text)
	for _, term := range harnessVocab {
		if strings.Contains(lower, term) {
			found = append(found, term)
		}
	}
	return found
}

// The scanner catches planted coaching/orchestration vocabulary and passes clean,
// realistic code — the red-first proof it is not vacuous.
func TestBannedFixtureTermsScan(t *testing.T) {
	coached := []string{
		"// TODO: fix the boundary comparison below",
		"// FIXME the agent should change > to >=",
		"// dispatch-developer applies the patch here",
		"// apply_patch writes the fix here",
		"// this is the semdev clean-room verify fixture",
	}
	for _, c := range coached {
		if len(bannedFixtureTerms(c)) == 0 {
			t.Errorf("scanner missed coaching/orchestration vocabulary in %q", c)
		}
	}
	clean := []string{
		"// Classify reports a service's health from its resource pressure.",
		"case pressure > warningThreshold:",
		`import _ "example.com/telemetry/pulsecache"`,
		"// debugging note: pressure is the worse dimension", // "debug" must NOT trip BUG
		"if task.Spec.Replicas > 0 { return task.Spec }",     // realistic k8s-shaped code must NOT trip
	}
	for _, c := range clean {
		if got := bannedFixtureTerms(c); len(got) != 0 {
			t.Errorf("scanner false-flagged realistic code %q: %v", c, got)
		}
	}
}

// Every committed fixture is free of coaching/orchestration vocabulary (G8). Scans the
// .go and .md files inside each fixture subdirectory (not the fixtures test harness at
// the fixtures root, which legitimately discusses the harness).
func TestFixturesFreeOfOrchestrationVocabulary(t *testing.T) {
	fixturesDir := filepath.Join(repoRoot(t), "test", "fixtures")
	scanned := 0
	err := filepath.WalkDir(fixturesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(fixturesDir, path)
		// Skip files directly in the fixtures root (the test harness) — only fixture
		// subdirectories are artifacts the agent reads.
		if !strings.ContainsRune(filepath.ToSlash(rel), '/') {
			return nil
		}
		// Scan the artifact SOURCE + docs the agent reads (.go/.md). Config is out of
		// scope on purpose: a devcontainer.json's `customizations.semdev` block is the
		// operator's legitimate SB2 onboarding declaration (test command + tier), like
		// `customizations.vscode` — not fix-coaching. Only the source must read like
		// un-onboarded upstream code. (A coaching comment hidden in a Dockerfile/JSONC is
		// a residual gap the necessary-not-sufficient lint leaves to reviewer judgment.)
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".md" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		if terms := bannedFixtureTerms(string(src)); len(terms) != 0 {
			t.Errorf("%s carries coaching/orchestration vocabulary %v — fixtures must read like a real repo (G8)", rel, terms)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixtures: %v", err)
	}
	if scanned == 0 {
		t.Fatal("no fixture source files scanned — the G8 pin would pass vacuously")
	}
}
