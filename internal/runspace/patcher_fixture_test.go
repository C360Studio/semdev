package runspace

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
)

// fixtureFixDiff is the developer's authored fix for the go-health-class fixture's
// real boundary bug: the doc says "at or above the warning threshold is Degraded" but
// the code uses `>` instead of `>=`, so the 0.75 boundary reads Healthy and the
// warning-boundary test fails. This one-line diff is exactly what a developer would
// emit through apply_patch.
const fixtureFixDiff = "--- a/health.go\n" +
	"+++ b/health.go\n" +
	"@@ -28,7 +28,7 @@ func Classify(cpu, mem float64) Status {\n" +
	" \tswitch {\n" +
	" \tcase pressure >= criticalThreshold:\n" +
	" \t\treturn Unhealthy\n" +
	"-\tcase pressure > warningThreshold:\n" +
	"+\tcase pressure >= warningThreshold:\n" +
	" \t\treturn Degraded\n" +
	" \tdefault:\n" +
	" \t\treturn Healthy\n"

// TestPatcherFixesFixtureRedToGreen is the strongest g6 proof, minus the container:
// apply_patch authors the REAL fix to the REAL fixture and the previously-FAILING
// `go test` goes green. It grounds the tool against theater — the change is applied
// for real and the outcome is the go toolchain's real exit code (G3), not a claim.
// This is the exact g7 loop mechanic (author → measure) run on the host.
func TestPatcherFixesFixtureRedToGreen(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not available")
	}
	ctx := context.Background()
	runner := cliexec.OSRunner{}

	checkouts, err := NewCheckouts(t.TempDir(), runner)
	if err != nil {
		t.Fatalf("new checkouts: %v", err)
	}
	const run = "org.p.agent.chain.execution.run-1"
	root, err := checkouts.Materialize(ctx, run, fixtureModuleDir(t))
	if err != nil {
		t.Fatalf("materialize fixture: %v", err)
	}

	// RED: the fixture's own test fails before the fix (the real boundary bug).
	if res, err := runner.Run(ctx, root, "go", "test", "./..."); err != nil {
		t.Fatalf("run go test (pre-fix): %v", err)
	} else if res.ExitCode == 0 {
		t.Fatal("fixture tests unexpectedly PASSED before the fix — the seeded bug is gone, the red→green proof is vacuous")
	}

	// Author the fix through the real apply_patch seam (health.go is in the contract).
	touched, _, err := NewPatcher(checkouts, runner, targetFilesReader([]string{"health.go"})).Apply(ctx, run, fixtureFixDiff)
	if err != nil {
		t.Fatalf("apply fix diff: %v", err)
	}
	if len(touched) != 1 || touched[0] != "health.go" {
		t.Fatalf("apply touched %v, want [health.go]", touched)
	}

	// GREEN: the same test passes after the authored fix — a real outcome, not a claim.
	res, err := runner.Run(ctx, root, "go", "test", "./...")
	if err != nil {
		t.Fatalf("run go test (post-fix): %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("fixture tests still FAIL after the fix (exit %d): %s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// fixtureModuleDir returns the committed go-health-class fixture module root.
func fixtureModuleDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/runspace → repo root is two levels up.
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "test", "fixtures", "go-health-class")
}
