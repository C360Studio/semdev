package conformance

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot returns the semdev repository root, anchored off this test file's own
// location (runtime.Caller) and walked up to the directory holding go.mod. Pins
// read checked-in artifacts (specs, docs, source) by absolute path, so they must
// not depend on the process working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod from %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// activeChangeSlug is the founding OpenSpec change — the reference change whose
// spec deltas introduce the founding vocabulary (the openspec round-trip pin uses
// it). The capability set is unioned across all active changes (see capabilities).
const activeChangeSlug = "m0-walking-skeleton-spine"

// capabilities returns the set of capability names declared by any ACTIVE change's
// spec deltas — the authoritative list a predicate's Capability must belong to. It
// unions the spec directories across every non-archived change (a capability is
// real if any active change declares it: m0 declares the founding capabilities, the
// containerized-sandbox change declares `sandbox`), derived from the directories
// rather than hand-listed so it cannot drift from the specs it mirrors.
func capabilities(t *testing.T) map[string]bool {
	t.Helper()
	changesDir := filepath.Join(repoRoot(t), "openspec", "changes")
	changes, err := os.ReadDir(changesDir)
	if err != nil {
		t.Fatalf("read changes dir %s: %v", changesDir, err)
	}
	caps := make(map[string]bool)
	for _, ch := range changes {
		if !ch.IsDir() || ch.Name() == "archive" {
			continue
		}
		specsDir := filepath.Join(changesDir, ch.Name(), "specs")
		entries, err := os.ReadDir(specsDir)
		if err != nil {
			continue // a change with no spec deltas declares no capabilities
		}
		for _, e := range entries {
			if e.IsDir() {
				caps[e.Name()] = true
			}
		}
	}
	if len(caps) == 0 {
		t.Fatalf("no capability spec dirs under any active change in %s — the pin would pass vacuously", changesDir)
	}
	return caps
}

// validChangeSlugs returns the set of OpenSpec change slugs that resolve to a
// real change directory, active or archived — the authoritative set a
// predicate's IntroducedBy must belong to.
func validChangeSlugs(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRoot(t)
	out := make(map[string]bool)
	for _, base := range []string{
		filepath.Join(root, "openspec", "changes"),
		filepath.Join(root, "openspec", "changes", "archive"),
	} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && e.Name() != "archive" {
				out[e.Name()] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no OpenSpec change directories found; the provenance pin would pass vacuously")
	}
	return out
}
