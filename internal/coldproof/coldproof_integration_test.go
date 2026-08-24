//go:build integration

// Docker-gated integration tests for coldproof, split out under the `integration`
// build tag so plain `go test ./...` never needs a docker daemon — the same
// convention internal/boot/runtime_integration_test.go established for a live NATS.
//
// These stand up REAL containers and build REAL ~1.29GB images. They previously
// lived untagged alongside the unit tests, where a `DockerAvailable` skip made them
// invisible on a machine with docker down and silently promoted them into the unit
// suite on a machine with docker up. On CI, several such packages ran in PARALLEL and
// exhausted a per-user kernel resource, which surfaced as an ENOSPC nobody could place.
//
// Run them with `task test:integration`.

package coldproof

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/verify"
)

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

// The build-file tripwire (SB3), NON-docker: a committed Dockerfile that fetches from a
// raw URL at build time is a non-self-contained Fail — and ProveArtifact SHORT-CIRCUITS to
// that Fail before building any image (the scan is pure/offline), so this red-first pin
// needs no docker. This is the "a fix that builds only via a would-be harness fixup fails
// cold" guard: a hidden runtime download dodges the cold-resolution proof, and the tripwire
// catches it statically.
