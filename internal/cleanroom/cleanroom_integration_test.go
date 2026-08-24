//go:build integration

// Docker-gated integration tests for cleanroom, split out under the `integration`
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

package cleanroom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration: a real per-run container round-trips Up → Exec (verdict) → Down, with
// a fresh cache volume that is distinct and torn down. Skips when docker is absent so
// unit CI stays green; runs in the docker-backed environment (the journey already
// needs docker for NATS).
func TestContainerRunnerRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable, skipping container integration: %v", err)
	}

	// A real checkout dir with a file the container will read back through the bind mount.
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "marker.txt"), []byte("bound"), 0o644); err != nil {
		t.Fatalf("seed workdir: %v", err)
	}

	c := NewContainerRunner("alpine:latest")
	sb, err := c.Up(ctx, workDir, []string{"GOMODCACHE"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() {
		if derr := c.Down(ctx, sb); derr != nil {
			t.Errorf("Down: %v", derr)
		}
	}()

	if sb.Handle == "" {
		t.Fatal("Up returned no container handle")
	}
	if len(sb.CacheHomes) != 1 || sb.CacheHomes[0] == "" {
		t.Fatalf("expected one fresh cache volume, got %v", sb.CacheHomes)
	}
	if sb.Env["GOMODCACHE"] != "/caches/GOMODCACHE" {
		t.Errorf("GOMODCACHE env = %q, want /caches/GOMODCACHE", sb.Env["GOMODCACHE"])
	}

	// The bind-mounted checkout is visible in the container.
	res, err := c.Exec(ctx, sb, []string{"cat", "marker.txt"})
	if err != nil {
		t.Fatalf("Exec cat: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "bound" {
		t.Errorf("cat marker: exit=%d stdout=%q, want exit 0 / 'bound'", res.ExitCode, res.Stdout)
	}

	// The fresh cache env points at a writable mounted volume.
	if res, err = c.Exec(ctx, sb, []string{"sh", "-c", "echo ok > $GOMODCACHE/probe && cat $GOMODCACHE/probe"}); err != nil {
		t.Fatalf("Exec cache write: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "ok" {
		t.Errorf("cache write: exit=%d stdout=%q, want 0 / 'ok'", res.ExitCode, res.Stdout)
	}

	// A non-zero exit is a VERDICT (nil error), not a transport fault (the seam contract).
	res, err = c.Exec(ctx, sb, []string{"sh", "-c", "exit 7"})
	if err != nil {
		t.Fatalf("Exec exit-7 returned a transport error, want a verdict: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7 (a verdict, not an error)", res.ExitCode)
	}
}

// Integration: once the container is gone (torn down), a non-zero docker exec is a
// TRANSPORT fault (park), not a red verdict — the reviewer's blocking scenario (a
// dead sandbox must never read as "the command ran and failed", SB5).

func TestContainerRunnerDeadContainerIsTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	c := NewContainerRunner("alpine:latest")
	sb, err := c.Up(ctx, t.TempDir(), []string{"GOMODCACHE"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	// Kill the container out from under Exec, then run a command that would exit
	// non-zero IF it ran — docker exec returns 1/137, which must classify as transport.
	if derr := c.Down(ctx, sb); derr != nil {
		t.Fatalf("Down (to kill the container): %v", derr)
	}
	_, err = c.Exec(ctx, sb, []string{"sh", "-c", "exit 1"})
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("exec against a dead container: got %v, want a transport fault (ErrDockerUnavailable), NOT a red verdict", err)
	}
}

// Integration: two Ups yield DISTINCT fresh cache volumes — caches are never shared
// across runs (the semteams/semspec cache-masking sin, SB4).

func TestContainerRunnerFreshCachePerRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	c := NewContainerRunner("alpine:latest")

	sb1, err := c.Up(ctx, t.TempDir(), []string{"GOMODCACHE"})
	if err != nil {
		t.Fatalf("Up 1: %v", err)
	}
	defer func() { _ = c.Down(ctx, sb1) }()
	sb2, err := c.Up(ctx, t.TempDir(), []string{"GOMODCACHE"})
	if err != nil {
		t.Fatalf("Up 2: %v", err)
	}
	defer func() { _ = c.Down(ctx, sb2) }()

	if sb1.CacheHomes[0] == sb2.CacheHomes[0] {
		t.Errorf("two runs share a cache volume %q — caches must be fresh per run", sb1.CacheHomes[0])
	}
	if sb1.Handle == sb2.Handle {
		t.Errorf("two runs share a container %q — containers must be fresh per run", sb1.Handle)
	}
}

// Docker-gated: BuildImage builds a real tiny image and returns its digest-pinned
// sha256 ref (the pin). Skips when docker is absent (unit tests need no daemon).
func TestBuildImageReal(t *testing.T) {
	ctx := context.Background()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Dockerfile"), "FROM busybox:latest\nRUN true\n")

	decl, err := LocateImageInDir(root)
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	img, err := BuildImage(ctx, "docker", root, decl)
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	t.Cleanup(func() {
		_ = exec2(ctx, "docker", "image", "rm", "-f", img.Ref)
		_ = exec2(ctx, "docker", "image", "rm", "-f", img.Digest)
	})

	// Digest is the immutable sha256 pin; Ref is a non-dangling semdev-scoped tag.
	if !strings.HasPrefix(img.Digest, "sha256:") {
		t.Errorf("built digest = %q, want a sha256: pin", img.Digest)
	}
	if !strings.HasPrefix(img.Ref, imageTagRepo+":") {
		t.Errorf("built ref = %q, want a %s: tag (non-dangling)", img.Ref, imageTagRepo)
	}
	// Both the pin and the tag resolve to a real, runnable image.
	if err := exec2(ctx, "docker", "image", "inspect", img.Ref); err != nil {
		t.Errorf("built image ref %q not inspectable: %v", img.Ref, err)
	}
	if err := exec2(ctx, "docker", "image", "inspect", img.Digest); err != nil {
		t.Errorf("built image digest %q not inspectable: %v", img.Digest, err)
	}
}

// A declared-but-absent Dockerfile fails closed (ErrNoImage) BEFORE any docker call —
// the operator committed a broken declaration, provable offline (no docker gate).

// Docker-gated: a secret injected via NewContainerRunnerWithSecrets is present in the
// container at exec time, but is NOT baked into the container's persistent config (it
// rides `docker exec -e`, never `docker run -e`) — so `docker inspect` never carries it
// (SB2c). Skips when docker is absent.
func TestContainerRunnerInjectsSecretExecOnly(t *testing.T) {
	ctx := context.Background()
	if err := DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	c := NewContainerRunnerWithSecrets("busybox:latest", map[string]string{"TOKEN": "sekret-value-xyz"})
	sb, err := c.Up(ctx, t.TempDir(), []string{"CACHE"})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	defer func() { _ = c.Down(ctx, sb) }()

	// The secret is visible to a command run in the sandbox.
	res, err := c.Exec(ctx, sb, []string{"sh", "-c", "echo $TOKEN"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "sekret-value-xyz") {
		t.Errorf("secret not injected into the exec env; stdout = %q", res.Stdout)
	}

	// ...but it is NOT in the container's persistent config (exec-only, not `docker run -e`).
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Config.Env}}", sb.Handle).Output()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if strings.Contains(string(out), "sekret-value-xyz") {
		t.Errorf("secret leaked into the container's persistent config: %s", out)
	}
}
