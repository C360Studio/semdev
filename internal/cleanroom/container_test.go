package cleanroom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// buildRunArgs assembles a deterministic `docker run -d` line: detached, checkout
// bind-mounted at /work, one fresh cache volume + its env per cache-home name in
// order, then the image and the keep-alive.
func TestBuildRunArgs(t *testing.T) {
	got := buildRunArgs("img@sha256:abc", "/host/checkout",
		[]string{"GOMODCACHE", "GOCACHE"},
		map[string]string{"GOMODCACHE": "vol-mod", "GOCACHE": "vol-build"})
	want := []string{
		"run", "-d", "-w", "/work",
		"--mount", "type=bind,source=/host/checkout,target=/work",
		"-v", "vol-mod:/caches/GOMODCACHE", "-e", "GOMODCACHE=/caches/GOMODCACHE",
		"-v", "vol-build:/caches/GOCACHE", "-e", "GOCACHE=/caches/GOCACHE",
		"img@sha256:abc", "tail", "-f", "/dev/null",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildRunArgs:\n got %v\nwant %v", got, want)
	}
}

// dockerExecTransport keys on docker's own stderr signatures, NOT the exit code: a
// stopped/absent container returns exit 1 and a killed one 137, indistinguishable
// from a genuine command exit. Red-first proof of the reviewer's blocking finding —
// these docker faults must classify as transport, a real command failure must not.
func TestDockerExecTransportSignatures(t *testing.T) {
	transport := []string{
		"Error response from daemon: Container abc123 is not running",
		"Error response from daemon: No such container: abc123",
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
		"OCI runtime exec failed: exec failed: unable to start container process: exec: \"go\": executable file not found in $PATH",
	}
	for _, s := range transport {
		if !dockerExecTransport(s) {
			t.Errorf("stderr %q must classify as a docker transport fault", s)
		}
	}
	verdict := []string{
		"--- FAIL: TestFoo (0.00s)\n    foo_test.go:10: want 2 got 1",
		"go: build failed",
		"", // a bare non-zero exit with no docker signature is the command's verdict
	}
	for _, s := range verdict {
		if dockerExecTransport(s) {
			t.Errorf("stderr %q is a command verdict, must NOT be transport", s)
		}
	}
}

// execArgs prefixes `docker exec` with the workdir and the cache-home env in sorted
// (deterministic) order, then the container handle.
func TestExecArgs(t *testing.T) {
	c := &ContainerRunner{Image: "img"}
	sb := Sandbox{
		WorkDir: "/work",
		Env:     map[string]string{"GOCACHE": "/caches/GOCACHE", "GOMODCACHE": "/caches/GOMODCACHE"},
		Handle:  "container-123",
	}
	got := c.execArgs(sb)
	want := []string{
		"exec", "-w", "/work",
		"-e", "GOCACHE=/caches/GOCACHE",
		"-e", "GOMODCACHE=/caches/GOMODCACHE",
		"container-123",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("execArgs:\n got %v\nwant %v", got, want)
	}
}

// Up with no declared image fails with ErrNoImage — semdev never guesses (SB2).
func TestUpNoImageFailsClosed(t *testing.T) {
	c := &ContainerRunner{Image: "  "}
	_, err := c.Up(context.Background(), t.TempDir(), []string{"GOMODCACHE"})
	if !errors.Is(err, ErrNoImage) {
		t.Errorf("Up with empty image: got %v, want ErrNoImage", err)
	}
}

// A relative workDir masks that the checkout is absent (docker reads it as a named
// volume) — Up rejects it (SB5 fail-closed, the reviewer's MEDIUM).
func TestUpRejectsRelativeWorkDir(t *testing.T) {
	c := &ContainerRunner{Image: "img", docker: filepath.Join(t.TempDir(), "docker")}
	_, err := c.Up(context.Background(), "relative/checkout", []string{"GOMODCACHE"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("Up with relative workDir: got %v, want an absolute-path error", err)
	}
}

// Exec on a sandbox with no container handle is a transport fault, not a verdict.
func TestExecNoHandleIsTransport(t *testing.T) {
	c := &ContainerRunner{Image: "img"}
	_, err := c.Exec(context.Background(), Sandbox{}, []string{"go", "test"})
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("Exec with no handle: got %v, want ErrDockerUnavailable", err)
	}
}

// DockerAvailable fails CLOSED when the docker binary cannot be reached — the caller
// reads this as "sandbox absent" and parks (SB5), never a silent skip.
func TestDockerAvailableFailsClosed(t *testing.T) {
	// A docker binary that does not exist forces the daemon probe to fail.
	err := DockerAvailable(context.Background(), filepath.Join(t.TempDir(), "no-such-docker"))
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("DockerAvailable with missing binary: got %v, want ErrDockerUnavailable", err)
	}
}

// Up also fails closed when docker is unavailable (before touching volumes).
func TestUpFailsClosedWithoutDocker(t *testing.T) {
	c := &ContainerRunner{Image: "img", docker: filepath.Join(t.TempDir(), "no-such-docker")}
	_, err := c.Up(context.Background(), t.TempDir(), []string{"GOMODCACHE"})
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("Up without docker: got %v, want ErrDockerUnavailable", err)
	}
}

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
