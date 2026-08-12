// Package fixtures exercises the committed dev-loop fixtures against the sandbox
// substrate (group 2): it proves the go-health-class fixture is a REAL module with a
// REAL bug (compiles, but its own tests fail) whose declared image resolves + builds
// COLD, and that the cache-masked-fabrication variant is rejected cold. These are the
// artifacts the M0 dev loop and clean-room verify run on.
package fixtures

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/verify"
)

// fixtureDir returns the absolute path to a committed fixture, anchored off this file.
func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), name)
}

// goRun runs `go <args>` in dir, returning the exit code and combined output.
func goRun(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("go %v could not run in %s: %v", args, dir, err)
	return -1, ""
}

// resolveManifest assembles the fixture's run manifest the way the provisioning station
// will: locate the declared image, read a devcontainer's customizations if present, and
// overlay onto the Go convention.
func resolveManifest(t *testing.T, dir string) harness.Manifest {
	t.Helper()
	decl, err := cleanroom.LocateImageInDir(dir)
	if err != nil {
		t.Fatalf("locate image in %s: %v", dir, err)
	}
	var dcJSON []byte
	if decl.Devcontainer != "" {
		if dcJSON, err = os.ReadFile(filepath.Join(dir, decl.Devcontainer)); err != nil {
			t.Fatalf("read devcontainer: %v", err)
		}
	}
	m, err := harness.ResolveManifest(harness.ProfileGo, decl, dcJSON)
	if err != nil {
		t.Fatalf("resolve manifest: %v", err)
	}
	return m
}

// The fixture is a REAL module with a REAL bug: it compiles (so the baseline proves it
// cold), but its own test suite fails (the boundary bug the dev loop fixes). Offline.
func TestGoHealthClassCompilesButTestsFail(t *testing.T) {
	dir := fixtureDir(t, "go-health-class")
	if code, out := goRun(t, dir, "build", "./..."); code != 0 {
		t.Fatalf("go build exit %d, want 0 — the fixture must compile:\n%s", code, out)
	}
	if code, out := goRun(t, dir, "test", "./..."); code == 0 {
		t.Fatalf("go test exit 0, want non-zero — the fixture must carry a real failing test:\n%s", out)
	}
}

// The declared image + customizations.semdev block resolve into the expected run
// manifest (group 2's reader, exercised on the real fixture). Offline.
func TestGoHealthClassManifestResolves(t *testing.T) {
	dir := fixtureDir(t, "go-health-class")
	decl, err := cleanroom.LocateImageInDir(dir)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if decl.Devcontainer != ".devcontainer/devcontainer.json" {
		t.Errorf("located image = %+v, want the devcontainer declaration", decl)
	}
	m := resolveManifest(t, dir)
	if !slices.Equal(m.TestCmd, []string{"go", "test", "./..."}) {
		t.Errorf("test command = %v, want the customizations.semdev command", m.TestCmd)
	}
	if len(m.Tiers) != 1 || m.Tiers[0].Scope != harness.TierSandbox {
		t.Errorf("tiers = %+v, want a single sandbox tier", m.Tiers)
	}
	if got := harness.AssessReadiness(m.Tiers, harness.ClaimUnit); got != harness.ReadinessReady {
		t.Errorf("unit-claim readiness = %q, want ready", got)
	}
}

// Docker-gated: the operator-declared image builds the fixture and it resolves + builds
// COLD in a fresh container → Ready. The make-or-break, on the committed fixture.
func TestGoHealthClassBaselineReady(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	dir := fixtureDir(t, "go-health-class")
	m := resolveManifest(t, dir)
	b, err := coldproof.ProveBaseline(ctx, "docker", dir, m, nil)
	if err != nil {
		t.Fatalf("ProveBaseline: %v", err)
	}
	t.Cleanup(func() { _ = removeImages(ctx, b.Image) })
	if !b.Ready() {
		t.Errorf("baseline outcome = %q (not ready); failed checks: %v", b.Outcome, b.Verdict.FailedChecks())
	}
}

// Docker-gated: the cache-masked-fabrication variant declares a dependency that does not
// resolve cold, so the baseline is NOT ready (Fail) → park toward the operator. The real
// fabrication reject on the committed fixture.
func TestFabricatedVariantNotReady(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	dir := fixtureDir(t, "go-health-class-fabricated")
	m := resolveManifest(t, dir)
	b, err := coldproof.ProveBaseline(ctx, "docker", dir, m, nil)
	if err != nil {
		t.Fatalf("ProveBaseline: %v", err)
	}
	t.Cleanup(func() { _ = removeImages(ctx, b.Image) })
	if b.Ready() {
		t.Errorf("the fabricated variant must NOT prove ready; outcome = %q", b.Outcome)
	}
	if b.Outcome != verify.OutcomeFail {
		t.Errorf("outcome = %q, want fail (a genuine cold-resolution failure, not retry)", b.Outcome)
	}
}

func removeImages(ctx context.Context, img cleanroom.BuiltImage) error {
	return exec.CommandContext(ctx, "docker", "image", "rm", "-f", img.Ref, img.Digest).Run()
}
