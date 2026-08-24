package conformance

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/standards"
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

// fixtureScanKind is one class of fixture file the G8 pin opens. Declaring a kind here is
// what makes it scanned — and the reach pin plants `specimen` for EVERY entry, so a kind
// cannot be declared without proving the walk actually reaches it.
type fixtureScanKind struct {
	// label names the kind in failure messages and keys the per-kind scan counter.
	label string
	// matches reports whether a fixture-relative slash path belongs to this kind.
	matches func(rel string) bool
	// specimen is the fixture-relative path the reach pin plants. It MUST satisfy matches
	// and sit in a subdirectory; the reach pin asserts both, so a mis-specified specimen
	// fails loudly instead of quietly proving nothing.
	specimen string
	// treeFloor says whether at least one real committed fixture file must be this kind.
	// A STRING with no valid zero value, not a bool, on purpose: a bool's zero value
	// silently means "exempt", so a kind whose author never considered the floor is
	// indistinguishable from one deliberately waived — the same quiet-default shape as
	// declaring a kind and never proving reach. The reach pin fatals on the empty value,
	// which forces the choice to be made rather than defaulted.
	treeFloor treeFloorPolicy
}

// treeFloorPolicy classifies a kind's tree floor. Deliberately has no valid zero value.
type treeFloorPolicy string

const (
	treeFloorRequired treeFloorPolicy = "required"
	treeFloorExempt   treeFloorPolicy = "exempt"
)

// fixtureScanKinds is the whole scanning policy, in one place.
//
// The line drawn is CONTENT PUSHED INTO A BRIEF UNBIDDEN — not "content the agent reads".
// read_workspace serves Amelia any repo-relative path with no extension filter
// (internal/tools/readworkspace) and is advertised in every developer dispatch, so by the
// reading test every fixture file qualifies and the distinction collapses. What is special
// about the standards file is that its text is pushed into every developer and reviewer
// brief VERBATIM without anyone asking — minted as agent.lesson.injection-form
// (internal/standards/sync.go) and rendered verbatim by the framework. That is the B10
// channel: coaching smuggled there rides into every brief.
//
// Scoped to standards.Path rather than to all .yaml/.yml on purpose. The product reads
// standards from exactly that fixed path, so no other YAML can ever BE a standards file,
// and scanning the rest breaks realistic fixtures: a .golangci.yml enabling godox declares
// `keywords: [TODO, FIXME, HACK, BUG]` — the repo configuring the very linter that bans
// coaching — and a .github/ISSUE_TEMPLATE/bug_report.yml is little but the word "bug".
// This lint FAILS THE BUILD, so a false positive blocks a legitimately realistic fixture:
// the opposite of G8's intent.
//
// .json stays out for a stronger reason than taste. semdev's own SB2 contract MANDATES the
// literal token `semdev` as the devcontainer customizations key (internal/harness), so any
// fixture declaring a devcontainer is structurally required to carry a harnessVocab term.
// Scanning .json is permanently unsatisfiable, not merely unnecessary.
var fixtureScanKinds = []fixtureScanKind{
	{
		label:     "artifact source (.go)",
		matches:   func(rel string) bool { return filepath.Ext(rel) == ".go" },
		specimen:  "go-health-class/service.go",
		treeFloor: treeFloorRequired,
	},
	{
		label:    "fixture docs (.md)",
		matches:  func(rel string) bool { return filepath.Ext(rel) == ".md" },
		specimen: "go-health-class/README.md",
		// No fixture ships docs today, and the per-kind guard is what PROVED it: the
		// original pin declared .md yet had never opened one, hidden behind three .go
		// files by a single whole-walk total. The kind stays declared as live forward
		// coverage — the reach pin keeps proving the walk opens it — but requiring a real
		// .md would only false-alarm on a tree that is correct as it stands.
		treeFloor: treeFloorExempt,
	},
	{
		label: "the standards declaration (" + standards.Path + ")",
		// Case-folded: on a case-insensitive host FS the product's
		// filepath.Join(checkoutRoot, Path) would open a case-variant file, so the pin
		// must scan one too. standards.Path is already lower-case.
		matches: func(rel string) bool {
			lower := strings.ToLower(rel)
			return lower == standards.Path || strings.HasSuffix(lower, "/"+standards.Path)
		},
		specimen:  "go-health-class/" + standards.Path,
		treeFloor: treeFloorRequired,
	},
}

// fixtureKindOf returns the declared kind a fixture-relative slash path belongs to.
func fixtureKindOf(slashRel string) (fixtureScanKind, bool) {
	for _, kind := range fixtureScanKinds {
		if kind.matches(slashRel) {
			return kind, true
		}
	}
	return fixtureScanKind{}, false
}

// scanFixtureTree walks root exactly as the G8 pin does, returning the banned vocabulary
// found per relative path plus a count of files scanned per kind label. Taking root as a
// parameter is what lets the reach pin drive it with a synthetic tree: proving the regex
// matches a planted term is NOT evidence the walk ever opened a file of that kind.
//
// When err is non-nil both maps are PARTIAL — the walk stopped early, so a caller must not
// read them as a clean result. Both pins t.Fatalf on err for exactly that reason.
func scanFixtureTree(root string) (map[string][]string, map[string]int, error) {
	violations := map[string][]string{}
	scanned := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		slashRel := filepath.ToSlash(rel)
		// Skip files directly in the fixtures root: the harness at that level legitimately
		// discusses the harness (test/fixtures/fixtures_test.go carries banned terms by
		// design), so this skip is load-bearing, not cosmetic.
		if !strings.ContainsRune(slashRel, '/') {
			return nil
		}
		kind, ok := fixtureKindOf(slashRel)
		if !ok {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned[kind.label]++
		if terms := bannedFixtureTerms(string(src)); len(terms) != 0 {
			violations[slashRel] = terms
		}
		return nil
	})
	return violations, scanned, err
}

// Every committed fixture is free of coaching/orchestration vocabulary (G8), across every
// kind the pin declares.
func TestFixturesFreeOfOrchestrationVocabulary(t *testing.T) {
	fixturesDir := filepath.Join(repoRoot(t), "test", "fixtures")
	violations, scanned, err := scanFixtureTree(fixturesDir)
	if err != nil {
		t.Fatalf("walk fixtures: %v", err)
	}
	// Sorted: map order is not stable, and a failing CI log should diff run to run.
	for _, rel := range slices.Sorted(maps.Keys(violations)) {
		t.Errorf("%s carries coaching/orchestration vocabulary %v — fixtures must read like a real repo (G8)", rel, violations[rel])
	}
	// Per-kind vacuity guard. A single whole-walk total cannot tell "the standards file was
	// scanned and is clean" from "it moved, so that half of the pin silently stopped
	// running" — the green-that-never-ran shape.
	for _, kind := range fixtureScanKinds {
		if kind.treeFloor == treeFloorRequired && scanned[kind.label] == 0 {
			t.Errorf("no fixture file of kind %q was scanned — that half of the G8 pin passes vacuously", kind.label)
		}
	}
}

// The walk REACHES every kind it declares, and still ignores the config deliberately left
// out. The plants are DERIVED from fixtureScanKinds, so a newly declared kind cannot go
// unproven — both reviewers independently found the earlier hardcoded version green with
// three unreached extensions declared. Group 6's review found the same shape in the persona
// fragments: both files could be deleted with every test still green, because nothing
// pinned that they reached a brief at all.
func TestFixtureScanReachesEveryDeclaredKind(t *testing.T) {
	root := t.TempDir()
	// One marker for every specimen, so this pin measures REACHABILITY alone. Which terms
	// bannedFixtureTerms recognises is TestBannedFixtureTermsScan's job; coupling the two
	// would let a vocabulary edit turn this pin red for an unrelated reason.
	const plant = "// TODO: fix the boundary comparison\n"
	seen := map[string]bool{}
	for _, kind := range fixtureScanKinds {
		if kind.matches == nil {
			t.Fatalf("kind %q declares no matcher — fixtureKindOf would panic mid-walk, from a different test", kind.label)
		}
		// Labels key the scan counter, so two kinds sharing one silently MERGE counts: the
		// later kind's reach and tree-floor guards are then satisfied by the earlier kind's
		// files, re-opening the exact vacuity this pin exists to close. Copy-an-entry is the
		// authoring motion for a new kind, which makes the slip a likely one.
		if seen[kind.label] {
			t.Fatalf("duplicate kind label %q — labels key the scan counter, so two kinds sharing one would merge counts and prove nothing", kind.label)
		}
		seen[kind.label] = true
		if kind.treeFloor != treeFloorRequired && kind.treeFloor != treeFloorExempt {
			t.Fatalf("kind %q declares treeFloor %q — every kind must be classified required or exempt; the zero value is not a decision", kind.label, kind.treeFloor)
		}
		if !kind.matches(kind.specimen) {
			t.Fatalf("kind %q declares specimen %q that its own matcher rejects — the plant would prove nothing", kind.label, kind.specimen)
		}
		if !strings.ContainsRune(kind.specimen, '/') {
			t.Fatalf("kind %q specimen %q must sit in a fixture subdirectory — the walk skips the root", kind.label, kind.specimen)
		}
		writeFixtureFile(t, root, kind.specimen, plant)
	}
	// Out of scope on purpose. Carries banned terms so a green negative control means "the
	// walk never opened it", not "there was nothing to find".
	const ignored = "go-health-class/.devcontainer/devcontainer.json"
	writeFixtureFile(t, root, ignored, `{"customizations":{"semdev":{"test":"TODO"}}}`)

	violations, scanned, err := scanFixtureTree(root)
	if err != nil {
		t.Fatalf("scan synthetic tree: %v", err)
	}
	for _, kind := range fixtureScanKinds {
		if scanned[kind.label] == 0 {
			t.Errorf("kind %q had no file attributed to it — check whether an earlier kind's matcher claims its specimen first (fixtureKindOf returns the FIRST match)", kind.label)
		}
		if len(violations[kind.specimen]) == 0 {
			t.Errorf("planted coaching in %s was not flagged — the walk never opens kind %q", kind.specimen, kind.label)
		}
	}
	// Assert on what was OPENED, not only on what was flagged. A violations-only check on
	// the control passes for the wrong reason the moment its path stops matching — one typo
	// from vacuous. The total is the real claim: the walk opened the planted specimens and
	// NOTHING else, so whatever the control is named, it was not read.
	opened := 0
	for _, n := range scanned {
		opened += n
	}
	if opened != len(fixtureScanKinds) {
		t.Errorf("walk opened %d files; exactly the %d planted specimens must be opened and every out-of-scope control skipped — scanned=%v", opened, len(fixtureScanKinds), scanned)
	}
	if kind, ok := fixtureKindOf(ignored); ok {
		t.Errorf("%s matched declared kind %q — that config must stay out of scope", ignored, kind.label)
	}
	if terms := violations[ignored]; len(terms) != 0 {
		t.Errorf("%s was scanned but that config kind is deliberately out of scope: %v", ignored, terms)
	}
}

// The narrowing is load-bearing: idiomatic config a realistic Go repo ships must not trip
// the scan. Each body here carries banned vocabulary and is nonetheless legitimate — a
// .golangci.yml enabling godox literally declares the coaching keywords, because it is
// configuring the linter that bans them. Scanning all .yaml/.yml flagged every one of these
// and would have failed the build on a correct fixture, the opposite of G8's intent
// (docs/constitution.md permits a fixture to document what a real repo documents).
func TestRealisticRepoConfigStaysOutOfScope(t *testing.T) {
	realistic := map[string]string{
		"go-health-class/.golangci.yml":                   "linters-settings:\n  godox:\n    keywords: [TODO, FIXME, HACK, BUG]\n",
		"go-health-class/.github/ISSUE_TEMPLATE/bug.yml":  "name: Bug report\ndescription: File a bug report\n",
		"go-health-class/.devcontainer/devcontainer.json": `{"customizations":{"semdev":{"test":"go test ./..."}}}`,
	}
	root := t.TempDir()
	for _, rel := range slices.Sorted(maps.Keys(realistic)) {
		body := realistic[rel]
		// Belt: each body really does carry banned vocabulary, so a green below means "not
		// scanned" rather than "nothing to find".
		if len(bannedFixtureTerms(body)) == 0 {
			t.Errorf("%s carries no banned vocabulary — it cannot prove the scope boundary", rel)
		}
		writeFixtureFile(t, root, rel, body)
	}

	// Drive the REAL walk, not only the classifier. Asserting on fixtureKindOf alone leaves
	// the pin blind to scope widened INSIDE scanFixtureTree — verified: bypassing
	// fixtureKindOf there left every pin green.
	violations, scanned, err := scanFixtureTree(root)
	if err != nil {
		t.Fatalf("scan synthetic tree: %v", err)
	}
	opened := 0
	for _, n := range scanned {
		opened += n
	}
	if opened != 0 {
		t.Errorf("the walk opened %d realistic config files; all must stay out of scope — scanned=%v", opened, scanned)
	}
	for _, rel := range slices.Sorted(maps.Keys(violations)) {
		t.Errorf("%s was scanned and flagged, but realistic repo config must stay out of scope: %v", rel, violations[rel])
	}
	for _, rel := range slices.Sorted(maps.Keys(realistic)) {
		if kind, ok := fixtureKindOf(rel); ok {
			t.Errorf("%s matched declared kind %q — realistic repo config must stay out of scope", rel, kind.label)
		}
	}
}

func writeFixtureFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("seed %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
