package cleanroom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/secrets"
)

// ErrDockerUnavailable is the sentinel a caller reads as "the sandbox substrate is
// absent" — it MUST fail closed (park toward the human), never a silent skip. This
// is the semspec grave: with the sandbox absent, semspec skipped verify/floors and
// stamped "execution verified" over zero executions (design SB5).
var ErrDockerUnavailable = errors.New("cleanroom: docker unavailable")

// ErrNoImage is returned when a ContainerRunner has no image to run — the operator
// must declare one (a Dockerfile/devcontainer built into a ref); semdev never
// guesses (design SB2).
var ErrNoImage = errors.New("cleanroom: container runner has no image")

// containerWorkDir is the in-container mount point for the artifact checkout. The
// host checkout is bind-mounted here, so the container's writes land back on the
// host tree (the apply_patch diff and the floors read the same bytes).
const containerWorkDir = "/work"

// cacheMountRoot is where fresh per-run cache volumes mount inside the container.
const cacheMountRoot = "/caches"

// cleanupTimeout bounds best-effort teardown run on a detached context.
const cleanupTimeout = 30 * time.Second

// dockerExecTransportSignatures are docker's own error phrasings that mean the
// command could NOT be run (a transport fault the caller parks on), regardless of the
// exit code. Exit codes are unreliable for `docker exec`: a stopped/absent container
// returns 1 and a container killed mid-exec returns 137 — indistinguishable from a
// genuine in-container command exit. So we key on docker's stderr, plus a liveness
// check (containerAlive) for the case where a killed container leaves stderr empty.
var dockerExecTransportSignatures = []string{
	"Error response from daemon:",         // container is not running / No such container
	"Cannot connect to the Docker daemon", // daemon unreachable
	"OCI runtime exec failed",             // bad workdir / missing binary in the container
	"unable to start container process",   // OCI exec setup failure
}

// dockerExecTransport reports whether a docker-exec stderr carries a docker-level
// "could not run" signature — a transport fault, not the command's verdict.
func dockerExecTransport(stderr string) bool {
	for _, sig := range dockerExecTransportSignatures {
		if strings.Contains(stderr, sig) {
			return true
		}
	}
	return false
}

// keepAliveArgv holds the container process open so exec can run against it across
// dev-loop iterations; the image's CMD is overridden with this. `tail -f /dev/null`
// is portable across busybox (alpine) and coreutils (debian/golang) images, unlike
// `sleep infinity` (coreutils only).
var keepAliveArgv = []string{"tail", "-f", "/dev/null"}

// ContainerRunner is the M0 run-path Runner (design SB1, revising D5): each Up stands
// up a FRESH per-run docker container from the operator-declared image, with the
// checkout bind-mounted and a FRESH cache volume per cache-home env — never shared
// across runs (the semteams/semspec cache-masking sin). It shells the docker CLI via
// os/exec (no new deps; mirrors LocalRunner), and semdev owns the run lifecycle
// (fresh docker run, not `devcontainer up` warm-reuse). The image is set at
// construction (the provisioning station resolves + builds the declared image first).
type ContainerRunner struct {
	// Image is the built, ideally digest-pinned image ref the sandbox runs.
	Image string
	// docker is the CLI binary; "docker" unless overridden (tests).
	docker string
	// secretEnv are resolved governed creds-refs (name→value) injected at EXEC time only
	// (SB2c): present for cold resolution, but NEVER in the container's persistent config
	// (no `docker run -e`), image layer, or the artifact — so `docker inspect` of the
	// running container never carries a secret. nil for the common no-secret run.
	secretEnv map[string]string
}

// NewContainerRunner builds a ContainerRunner for a declared, already-built image.
func NewContainerRunner(image string) *ContainerRunner {
	return &ContainerRunner{Image: image, docker: "docker"}
}

// NewContainerRunnerWithSecrets builds a ContainerRunner that injects resolved governed
// creds-refs (SB2c) into every exec's environment — the run-time secret channel. The
// caller resolves the refs (fail-closed) and scrubs any surfaced evidence; this runner
// only injects the values at exec time. A nil/empty map is equivalent to NewContainerRunner.
func NewContainerRunnerWithSecrets(image string, secretEnv map[string]string) *ContainerRunner {
	return &ContainerRunner{Image: image, docker: "docker", secretEnv: secretEnv}
}

var _ Runner = (*ContainerRunner)(nil)

// DockerAvailable probes the docker daemon; a non-nil error (wrapping
// ErrDockerUnavailable) means the sandbox substrate is absent and the caller MUST
// park (fail closed, SB5). Cheap and side-effect-free (`docker version`).
func DockerAvailable(ctx context.Context, docker string) error {
	if docker == "" {
		docker = "docker"
	}
	cmd := exec.CommandContext(ctx, docker, "version", "--format", "{{.Server.Version}}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %v: %s", ErrDockerUnavailable, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Up stands up a fresh container from the declared image: it creates one fresh
// (anonymous) docker volume per cacheHomeEnvs entry, bind-mounts the host workDir at
// /work, mounts each cache volume, and runs the image detached with a keep-alive so
// Exec can run against it. A non-nil error is a provisioning transport fault; a
// partial Up tears down whatever it created so nothing leaks.
func (c *ContainerRunner) Up(ctx context.Context, workDir string, cacheHomeEnvs []string) (Sandbox, error) {
	docker := c.dockerBin()
	if strings.TrimSpace(c.Image) == "" {
		return Sandbox{}, ErrNoImage
	}
	// A relative source makes docker's bind a NAMED VOLUME (empty /work), and a missing
	// absolute source is silently auto-created empty — either masks that the checkout
	// is absent. Require an absolute path and bind via --mount (fails loud on a missing
	// source), SB5 fail-closed.
	if !filepath.IsAbs(workDir) {
		return Sandbox{}, fmt.Errorf("cleanroom: workDir %q must be an absolute path", workDir)
	}
	if err := DockerAvailable(ctx, docker); err != nil {
		return Sandbox{}, err
	}

	env := make(map[string]string, len(cacheHomeEnvs))
	mounts := make(map[string]string, len(cacheHomeEnvs))
	var volumes []string
	// Roll back any created volumes on a later failure so a partial Up leaks nothing.
	// Teardown runs on a DETACHED context so a cancelled/timed-out Up still removes the
	// volumes it created (the caller's ctx is already dead here).
	rollback := func() {
		cctx, cancel := detachedCleanupCtx(ctx)
		defer cancel()
		for _, v := range volumes {
			_ = exec.CommandContext(cctx, docker, "volume", "rm", "-f", v).Run()
		}
	}

	for _, name := range cacheHomeEnvs {
		vol, err := c.createVolume(ctx)
		if err != nil {
			rollback()
			return Sandbox{}, fmt.Errorf("provision fresh cache volume for %s: %w", name, err)
		}
		volumes = append(volumes, vol)
		mounts[name] = vol
		env[name] = cacheMountRoot + "/" + name
	}

	id, err := c.runDetached(ctx, buildRunArgs(c.Image, workDir, cacheHomeEnvs, mounts))
	if err != nil {
		rollback()
		return Sandbox{}, fmt.Errorf("start sandbox container from %q: %w", c.Image, err)
	}
	return Sandbox{WorkDir: containerWorkDir, Env: env, CacheHomes: volumes, Handle: id}, nil
}

// detachedCleanupCtx returns a bounded context detached from ctx's cancellation, so
// best-effort teardown (volume/container removal) still runs after the caller's ctx
// is cancelled or timed out.
func detachedCleanupCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// buildRunArgs assembles the `docker run -d` args: detached, checkout bind-mounted at
// /work via --mount (fails loud on a missing source, unlike -v), one fresh cache
// volume mounted + its env exported per cache-home name (in the given order), then the
// image and keep-alive. Pure — unit-testable without docker.
func buildRunArgs(image, workDir string, cacheEnvs []string, mounts map[string]string) []string {
	args := []string{
		"run", "-d", "-w", containerWorkDir,
		"--mount", "type=bind,source=" + workDir + ",target=" + containerWorkDir,
	}
	for _, name := range cacheEnvs {
		mountPath := cacheMountRoot + "/" + name
		args = append(args, "-v", mounts[name]+":"+mountPath, "-e", name+"="+mountPath)
	}
	args = append(args, image)
	return append(args, keepAliveArgv...)
}

// Exec runs argv inside the sandbox container via `docker exec`, injecting the fresh
// cache-home env. A completed process returns its Result and a nil error (any exit
// code is a verdict); a docker-level failure to run the command (daemon/container gone
// or killed mid-exec) or a ctx cancellation is a transport fault (non-nil error) the
// caller reads as "did not complete", never as an artifact verdict (the seam
// contract). Transport is detected by docker's stderr signatures plus a container-
// liveness check — NOT by exit code, which is unreliable for docker exec (a dead
// container returns 1, a killed one 137, both indistinguishable from a real command).
func (c *ContainerRunner) Exec(ctx context.Context, sb Sandbox, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("cleanroom: empty command")
	}
	if sb.Handle == "" {
		return Result{}, fmt.Errorf("%w: sandbox has no container handle", ErrDockerUnavailable)
	}
	full := append(c.execArgs(sb), argv...)
	cmd := exec.CommandContext(ctx, c.dockerBin(), full...)
	// Governed secret VALUES ride the docker process's own environment (paired with the
	// `-e NAME` pass-through flags in execArgs), never its argv — so the value lives only
	// in owner-only /proc/<pid>/environ, not world-readable /proc/<pid>/cmdline (SB2c).
	if len(c.secretEnv) > 0 {
		cmd.Env = append(os.Environ(), secrets.ExecEnvKV(c.secretEnv)...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		// docker exec returns 0 ONLY when the command ran and exited 0 — a dead/absent
		// container makes docker exec non-zero, so exit 0 is always a genuine verdict
		// (no liveness check needed; this is why a dead sandbox cannot false-GREEN).
		res.ExitCode = cmd.ProcessState.ExitCode()
		return res, nil
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		if ctx.Err() != nil {
			return res, fmt.Errorf("cleanroom: exec %s: %w", argv[0], ctx.Err())
		}
		// A non-zero docker-exec is EITHER the command's real verdict OR a docker/
		// container transport fault (exit 1 for a stopped/absent container, 137 for one
		// killed mid-exec — both indistinguishable from a real command code). Read
		// docker's stderr signatures first, then confirm with a liveness check: the
		// container's keep-alive process persists across execs, so a not-running
		// container means it died and the exit code is NOT this command's verdict —
		// park (SB5 fail-closed), never record it as a red regression.
		// Under ambiguity this seam biases toward TRANSPORT (Retry), never verdict: a
		// signature collision (a command whose own stderr prints a docker phrasing) or a
		// liveness TOCTOU (container dies just after a real non-zero exit) yields a
		// spurious retry, never a false-green or a false terminal-reject — the design's
		// preferred safe direction (classify.go / SB5).
		if dockerExecTransport(res.Stderr) {
			return res, fmt.Errorf("cleanroom: docker could not exec %s: %w: %s", argv[0], ErrDockerUnavailable, firstLine(res.Stderr))
		}
		if alive, ierr := c.containerAlive(ctx, sb.Handle); ierr != nil || !alive {
			return res, fmt.Errorf("cleanroom: sandbox container %s not running after exec %s (exit %d): %w", sb.Handle, argv[0], res.ExitCode, ErrDockerUnavailable)
		}
		return res, nil
	default:
		return res, fmt.Errorf("cleanroom: exec %s: %w", argv[0], err)
	}
}

// containerAlive reports whether the sandbox container's main (keep-alive) process is
// still running. A docker error (e.g. "No such container") or Running=false means the
// container is gone — the caller treats a non-zero exec as transport, not a verdict.
func (c *ContainerRunner) containerAlive(ctx context.Context, handle string) (bool, error) {
	out, err := c.output(ctx, "inspect", "-f", "{{.State.Running}}", handle)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// firstLine returns the first non-empty line of s (a legible one-line error tail).
func firstLine(s string) string {
	for ln := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// Down force-removes the sandbox container and its fresh cache volumes. Best-effort;
// a cleanup error does not corrupt any recorded verdict.
func (c *ContainerRunner) Down(ctx context.Context, sb Sandbox) error {
	// Detach from the caller's ctx: teardown most often runs AFTER a run deadline, and
	// removal must still happen or the container/volumes leak (the reviewer's HIGH).
	ctx, cancel := detachedCleanupCtx(ctx)
	defer cancel()
	docker := c.dockerBin()
	var errs []error
	if sb.Handle != "" {
		if err := exec.CommandContext(ctx, docker, "rm", "-f", sb.Handle).Run(); err != nil {
			errs = append(errs, fmt.Errorf("remove container %s: %w", sb.Handle, err))
		}
	}
	for _, vol := range sb.CacheHomes {
		if err := exec.CommandContext(ctx, docker, "volume", "rm", "-f", vol).Run(); err != nil {
			errs = append(errs, fmt.Errorf("remove cache volume %s: %w", vol, err))
		}
	}
	return errors.Join(errs...)
}

// execArgs builds the `docker exec` prefix (before the user argv): the container
// handle, workdir, the fresh cache-home env injections, and any governed secret env
// (SB2c — injected here at exec, never at `docker run`, so a secret is never in the
// container's persistent/inspectable config). Pure — unit-testable without docker.
func (c *ContainerRunner) execArgs(sb Sandbox) []string {
	args := []string{"exec", "-w", firstNonEmpty(sb.WorkDir, containerWorkDir)}
	for _, k := range sortedKeys(sb.Env) {
		args = append(args, "-e", k+"="+sb.Env[k])
	}
	// Secret env rides the PASS-THROUGH `-e NAME` form (value in the process env, not the
	// argv) — see Exec (SB2c).
	args = append(args, secrets.ExecEnvFlags(c.secretEnv)...)
	args = append(args, sb.Handle)
	return args
}

func (c *ContainerRunner) dockerBin() string {
	if c.docker == "" {
		return "docker"
	}
	return c.docker
}

// createVolume creates a fresh anonymous docker volume and returns its id.
func (c *ContainerRunner) createVolume(ctx context.Context) (string, error) {
	out, err := c.output(ctx, "volume", "create")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", errors.New("docker volume create returned no id")
	}
	return id, nil
}

// runDetached runs `docker <args>` (a detached `run -d`) and returns the container id.
func (c *ContainerRunner) runDetached(ctx context.Context, args []string) (string, error) {
	out, err := c.output(ctx, args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", errors.New("docker run -d returned no container id")
	}
	return id, nil
}

// output runs a docker subcommand and returns trimmed stdout, folding stderr into the
// error so a failure is legible.
func (c *ContainerRunner) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.dockerBin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sortedKeys returns a map's keys in sorted order (deterministic exec args).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
