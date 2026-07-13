package coldproof

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/verify"
)

// The baseline maps the shared cold-proof outcome to readiness: an all-green proof is
// Ready; a genuine cold failure (the warm-only-resolvable dep that fails cold) is Fail
// and NOT ready (park toward the operator); a transport fault is Retry. Offline,
// deterministic — the real cache-masked-fabrication fixture is group 3.
func TestBaselineFromEvidence(t *testing.T) {
	img := cleanroom.BuiltImage{Ref: "semdev-sandbox:abc", Digest: "sha256:abc"}

	// All green → Ready.
	pass := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}}, {Result: cleanroom.Result{ExitCode: 0}},
	}})
	if b := baselineFromEvidence(img, pass); !b.Ready() || b.Outcome != verify.OutcomePass {
		t.Errorf("all-green baseline = %+v, want Ready/pass", b)
	}

	// A dependency that only resolves warm fails cold → Fail, NOT ready → park operator.
	coldFail := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "go: example.com/fabricated@v9.9.9: no matching versions"}},
	}})
	if b := baselineFromEvidence(img, coldFail); b.Ready() || b.Outcome != verify.OutcomeFail {
		t.Errorf("cold-fabrication baseline = %+v, want NOT ready / fail", b)
	}

	// A transport fault → Retry, not a terminal park.
	transient := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "connection refused"}},
	}})
	if b := baselineFromEvidence(img, transient); b.Ready() || b.Outcome != verify.OutcomeRetry {
		t.Errorf("transport baseline = %+v, want NOT ready / retry", b)
	}
}

// ProveBaseline fails closed (returns an error, no Baseline) when the manifest declares
// no image — offline-provable.
func TestProveBaselineNoImageFailsClosed(t *testing.T) {
	m := harness.GoProfile() // complete run fields, but Image is empty (undeclared)
	if _, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil); err == nil {
		t.Fatal("expected an error proving a baseline with no declared image")
	}
}

// A manifest requiring a creds-ref the store cannot provide fails CLOSED before building
// anything (SB2c) — no baseline, no warm fallback. Offline-provable.
func TestProveBaselineMissingSecretFailsClosed(t *testing.T) {
	m := harness.GoProfile()
	m.Image = harness.ImageDecl{Dockerfile: "Dockerfile"}
	m.SecretRefs = []string{"GITHUB_PACKAGES_TOKEN"}
	// nil store → the required ref cannot resolve → park toward the operator.
	if _, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil); err == nil {
		t.Fatal("expected an error: a required creds-ref with no store must park, not proceed")
	}
}

// An incomplete manifest (missing a run field) is a DECLARATION fault → error up front
// (park toward the operator), never a silent degrade to infra-Retry. Offline-provable.
func TestProveBaselineIncompleteManifestFailsClosed(t *testing.T) {
	base := harness.GoProfile()
	base.Image = harness.ImageDecl{Dockerfile: "Dockerfile"}
	for _, tc := range []struct {
		name string
		mut  func(*harness.Manifest)
	}{
		{"no-build", func(m *harness.Manifest) { m.BuildCmd = nil }},
		{"no-resolve", func(m *harness.Manifest) { m.ResolveCmd = nil }},
		{"no-cache-home", func(m *harness.Manifest) { m.CacheHomeEnvs = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.mut(&m)
			if _, err := ProveBaseline(context.Background(), "docker", "/repo", m, nil); err == nil {
				t.Fatalf("expected an error for an incomplete manifest (%s)", tc.name)
			}
		})
	}
}

// The baseline's terminal check reads "build", not "tests" (SB4.1) — the operator-facing
// evidence must match what the baseline proved (a cold build). This also guards the
// relabel against a rename of verify's terminal check.
func TestBaselineRelabelsBuildCheck(t *testing.T) {
	buildFail := gather(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}},                        // resolve ok
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "build broke"}}, // cold build fails
	}})
	b := baselineFromEvidence(cleanroom.BuiltImage{Ref: "x"}, buildFail)
	var names []string
	for _, c := range b.Verdict.Checks {
		names = append(names, c.Name)
		if c.Name == "tests" {
			t.Errorf("baseline verdict carries a %q check — it must be relabelled to build (SB4.1)", c.Name)
		}
	}
	found := false
	for _, c := range b.Verdict.FailedChecks() {
		if c.Name == "build" {
			found = true
		}
	}
	if !found {
		t.Errorf("baseline build-failure did not surface a failed \"build\" check; checks = %v", names)
	}
}

// Docker-gated: ProveBaseline builds a real golang image and proves a trivial Go module
// resolves + builds COLD in a fresh container → Ready. This is the make-or-break
// end-to-end (build image → per-run container → cold resolve+build), proven for real.
func TestProveBaselineRealReady(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	writeGoModule(t, root, "package app\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM golang:1.26\nWORKDIR /work\n")

	m := goManifest(t, root)
	b, err := ProveBaseline(ctx, "docker", root, m, nil)
	if err != nil {
		t.Fatalf("ProveBaseline: %v", err)
	}
	t.Cleanup(func() { _ = exec.CommandContext(ctx, "docker", "image", "rm", "-f", b.Image.Ref, b.Image.Digest).Run() })

	if !b.Ready() {
		t.Errorf("baseline outcome = %q (not ready); failed checks: %v", b.Outcome, b.Verdict.FailedChecks())
	}
}

// Docker-gated: a module declaring a fabricated dependency does NOT resolve cold →
// baseline NOT ready (Fail) → park toward the operator. The real cache-masked-
// fabrication reject, proven end-to-end (not a mock).
func TestProveBaselineRealFabricationNotReady(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	// A require + import of a module the Go proxy cannot resolve — fabricated, fails cold.
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/fab\n\ngo 1.26\n\nrequire example.com/definitely-not-real v1.2.3\n")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n\nimport _ \"example.com/definitely-not-real\"\n")
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM golang:1.26\nWORKDIR /work\n")

	m := goManifest(t, root)
	b, err := ProveBaseline(ctx, "docker", root, m, nil)
	if err != nil {
		t.Fatalf("ProveBaseline: %v", err)
	}
	t.Cleanup(func() { _ = exec.CommandContext(ctx, "docker", "image", "rm", "-f", b.Image.Ref, b.Image.Digest).Run() })

	if b.Ready() {
		t.Errorf("a fabricated dependency must NOT prove ready; outcome = %q", b.Outcome)
	}
	if b.Outcome != verify.OutcomeFail {
		t.Errorf("outcome = %q, want fail (a genuine cold-resolution failure, not retry)", b.Outcome)
	}
}

// Docker-gated: ProveArtifact builds a real golang image and proves a Go module
// resolves + builds + PASSES ITS TESTS cold in a fresh throwaway container → Pass. This
// is the clean-room final verify end-to-end (SB4.3) — the distinct-from-baseline proof:
// verify runs the artifact's TESTS, not just the build.
func TestProveArtifactRealPass(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(root, "app_test.go"), "package app\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM golang:1.26\nWORKDIR /work\n")

	v, err := ProveArtifact(ctx, "docker", root, goManifest(t, root), nil)
	if err != nil {
		t.Fatalf("ProveArtifact: %v", err)
	}
	cleanupImages(t, ctx, root)
	if v.Outcome != verify.OutcomePass {
		t.Errorf("verify outcome = %q, want pass; failed checks: %v", v.Outcome, v.FailedChecks())
	}
}

// Docker-gated: the DISTINGUISHING proof — a module that BUILDS but whose TESTS FAIL
// cold → verify FAIL (Fail, a genuine artifact failure), where the provision-time
// baseline (which proves only build) would read Ready. This is why the final verify is a
// separate instance running TESTS, not the baseline's build.
func TestProveArtifactRealTestsFailIsFail(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Add(a, b int) int { return a + b }\n")
	// Compiles fine (baseline would be Ready) but the assertion is wrong → tests fail cold.
	writeFile(t, filepath.Join(root, "app_test.go"), "package app\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 4 {\n\t\tt.Fatal(\"boundary\")\n\t}\n}\n")
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM golang:1.26\nWORKDIR /work\n")

	v, err := ProveArtifact(ctx, "docker", root, goManifest(t, root), nil)
	if err != nil {
		t.Fatalf("ProveArtifact: %v", err)
	}
	cleanupImages(t, ctx, root)
	if v.Outcome != verify.OutcomeFail {
		t.Errorf("verify outcome = %q, want fail (tests failed cold — the artifact does not pass its own tests)", v.Outcome)
	}
}

// Docker-gated: a fabricated dependency does NOT resolve cold → verify FAIL, identically
// to the baseline (the shared cold-proof core). The final verify catches a cache-masked
// fabrication a warm dev cache would have let survive to measure.
func TestProveArtifactRealFabricationIsFail(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/fab\n\ngo 1.26\n\nrequire example.com/definitely-not-real v1.2.3\n")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n\nimport _ \"example.com/definitely-not-real\"\n")
	writeFile(t, filepath.Join(root, "Dockerfile"), "FROM golang:1.26\nWORKDIR /work\n")

	v, err := ProveArtifact(ctx, "docker", root, goManifest(t, root), nil)
	if err != nil {
		t.Fatalf("ProveArtifact: %v", err)
	}
	cleanupImages(t, ctx, root)
	if v.Outcome != verify.OutcomeFail {
		t.Errorf("verify outcome = %q, want fail (fabricated dependency does not resolve cold)", v.Outcome)
	}
}

// cleanupImages removes the semdev-sandbox image built from root (best-effort), so the
// docker-gated tests do not accumulate images. The image ref is deterministic from the
// declared Dockerfile digest, so a fresh BuildImage recomputes it.
func cleanupImages(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	img, err := cleanroom.BuildImage(ctx, "docker", root, goManifest(t, root).Image)
	if err != nil {
		return
	}
	t.Cleanup(func() { _ = exec.CommandContext(ctx, "docker", "image", "rm", "-f", img.Ref, img.Digest).Run() })
}

// goManifest resolves the Go convention manifest for a repo root that declares a
// Dockerfile — the shape the provisioning station assembles.
func goManifest(t *testing.T, root string) harness.Manifest {
	t.Helper()
	decl, err := cleanroom.LocateImageInDir(root)
	if err != nil {
		t.Fatalf("locate image: %v", err)
	}
	m, err := harness.ResolveManifest(harness.ProfileGo, decl, nil)
	if err != nil {
		t.Fatalf("resolve manifest: %v", err)
	}
	return m
}

func writeGoModule(t *testing.T, root, appGo string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module semdev.test/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "app.go"), appGo)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
