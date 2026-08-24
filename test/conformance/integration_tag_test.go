package conformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A test that SKIPS ITSELF when infrastructure is absent is an integration test wearing
// a unit test's clothes, and it must carry the `integration` build tag.
//
// The convention predates this pin: internal/boot/runtime_integration_test.go has always
// been tagged, its comment explaining that plain `go test ./...` must never need a live
// NATS. The problem was that nothing ENFORCED it. Eleven docker-backed tests across
// internal/cleanroom, internal/coldproof and internal/tools/provisionsandbox sat untagged
// for as long as they existed, and the skip guard is exactly what hid them: on a machine
// with docker down they vanished (so the unit suite looked clean), and on a machine with
// docker up they ran and looked like fast unit tests.
//
// CI is where that stopped being free. `go test ./...` runs packages in PARALLEL, so
// three of them stood up containers at once, exhausted a per-user kernel resource, and
// failed with an ENOSPC that read as a full disk — with 84G free. Two wrong fixes went in
// before the mislabelling was the thing anyone looked at.
//
// So the rule is mechanical now: gate on infrastructure, carry the tag.

// infraSkipMarkers are the infrastructure probes whose failure a test may legitimately
// skip on. Deliberately narrow: each names a real availability check this repo makes, so
// a match is evidence of an infra dependency rather than a guess from a keyword.
var infraSkipMarkers = []string{
	"DockerAvailable",
	"nats.Connect",
}

// funcSplit finds top-level func declarations so a marker can be attributed to the test
// that contains it, rather than to any file that merely mentions the symbol. That
// distinction is load-bearing: TestDockerAvailableFailsClosed names DockerAvailable and
// is a genuine unit test — it passes a path that does not exist and asserts the
// fail-closed error, needing no daemon at all.
var funcSplit = regexp.MustCompile(`(?m)^func\s+(\w+)`)

// gatedTestsIn returns the names of tests in src that skip on an infrastructure probe.
// Pure, so the pin below can feed it synthetic source and prove it is not vacuous.
func gatedTestsIn(src string) []string {
	var gated []string
	idx := funcSplit.FindAllStringSubmatchIndex(src, -1)
	for i, m := range idx {
		name := src[m[2]:m[3]]
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		end := len(src)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := src[m[1]:end]
		if !strings.Contains(body, "t.Skip") {
			continue
		}
		for _, marker := range infraSkipMarkers {
			if strings.Contains(body, marker) {
				gated = append(gated, name)
				break
			}
		}
	}
	return gated
}

// The detector catches a skip-on-infrastructure test and passes a unit test that merely
// names the probe — the red-first proof it is neither vacuous nor trigger-happy.
func TestGatedTestDetector(t *testing.T) {
	gated := `
func TestNeedsDocker(t *testing.T) {
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
}`
	if got := gatedTestsIn(gated); len(got) != 1 || got[0] != "TestNeedsDocker" {
		t.Errorf("detector missed a skip-on-docker test: got %v", got)
	}

	// A unit test that names the probe and asserts its fail-closed behaviour, with no
	// skip — this is the real shape of TestDockerAvailableFailsClosed and MUST NOT trip.
	unit := `
func TestDockerAvailableFailsClosed(t *testing.T) {
	err := DockerAvailable(context.Background(), filepath.Join(t.TempDir(), "no-such-docker"))
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("want ErrDockerUnavailable")
	}
}`
	if got := gatedTestsIn(unit); len(got) != 0 {
		t.Errorf("detector false-flagged a fail-closed unit test: %v", got)
	}
}

// Every test that gates on infrastructure lives behind the `integration` build tag.
func TestInfraGatedTestsCarryTheIntegrationTag(t *testing.T) {
	root := repoRoot(t)
	scanned, tagged := 0, 0
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		text := string(src)
		hasTag := strings.Contains(text, "//go:build integration")
		if hasTag {
			tagged++
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if gated := gatedTestsIn(text); len(gated) != 0 {
			t.Errorf("%s: %v gate on infrastructure but the file lacks `//go:build integration` — "+
				"an infra-gated test in the default build makes `task test` need a daemon, and its "+
				"skip hides that on any machine where the daemon is down", filepath.ToSlash(rel), gated)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
	if scanned == 0 {
		t.Fatal("no test files scanned — this pin would pass vacuously")
	}
	// The convention must have at least one live example, or the walk above is only ever
	// proving a negative and would keep passing if the tag stopped being used entirely.
	if tagged == 0 {
		t.Error("no `//go:build integration` file found under internal/ — the tag is the " +
			"mechanism this pin enforces; if it is genuinely unused, delete the pin deliberately")
	}
}
