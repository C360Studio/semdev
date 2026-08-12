package cleanroom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LocalRunner is the M0 Runner: it realizes clean-room isolation as cache-home
// freshness on the host (the universal G4 control, design D5) rather than a
// container. Up mints a fresh temp directory per cache-home env so the proof runs
// against a COLD package cache — enough to reject a cache-masked fabrication (a
// dependency only a warm cache made resolvable). Container/network isolation is an
// M2 Runner swapped in behind the same seam; the verify harness does not change.
type LocalRunner struct{}

// Up mints one fresh temp directory per cacheHomeEnvs entry and binds it in the
// sandbox environment (host environment + the fresh cache-home overrides). Every Up
// yields DISTINCT fresh homes, so two proofs never share a warm cache.
func (LocalRunner) Up(_ context.Context, workDir string, cacheHomeEnvs []string) (Sandbox, error) {
	env := environMap()
	homes := make([]string, 0, len(cacheHomeEnvs))
	for _, name := range cacheHomeEnvs {
		dir, err := os.MkdirTemp("", "semdev-cleanroom-cache-")
		if err != nil {
			// Provisioning fault (transport-class): tear down any homes already
			// minted so a partial Up leaves nothing behind.
			for _, h := range homes {
				_ = os.RemoveAll(h)
			}
			return Sandbox{}, fmt.Errorf("provision fresh cache home for %s: %w", name, err)
		}
		env[name] = dir
		homes = append(homes, dir)
	}
	return Sandbox{WorkDir: workDir, Env: env, CacheHomes: homes}, nil
}

// Exec runs argv in the sandbox with its fresh-cache environment. A completed
// process (any exit code) returns its Result and a nil error; only a genuine
// start/cancel failure returns a non-nil error (a transport fault the caller reads
// as "did not complete", never as a verdict). It follows internal/cliexec's exit-vs-
// error contract but EXTENDS it: any ctx cancellation (parent-cancel as well as
// deadline) is transport-class here, because a proof killed by its parent did not
// run to a trustworthy conclusion — the clean-room trust contract is stricter than
// the CLI oracle's.
func (LocalRunner) Exec(ctx context.Context, sb Sandbox, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("cleanroom: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = sb.WorkDir
	cmd.Env = environSlice(sb.Env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = cmd.ProcessState.ExitCode()
		return res, nil
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		if ctx.Err() != nil {
			// Killed by cancel/deadline: a transport fault, not a verdict.
			return res, fmt.Errorf("cleanroom: run %s: %w", argv[0], ctx.Err())
		}
		return res, nil
	default:
		// Could not start (binary missing, permission, cancel-before-start).
		return res, fmt.Errorf("cleanroom: run %s in %q: %w", argv[0], sb.WorkDir, err)
	}
}

// Down removes the fresh cache homes Up minted. Best-effort; a cleanup error is
// returned but does not corrupt any recorded verdict.
func (LocalRunner) Down(_ context.Context, sb Sandbox) error {
	var errs []error
	for _, h := range sb.CacheHomes {
		if err := os.RemoveAll(h); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// environMap returns the process environment as a map for override.
func environMap() map[string]string {
	pairs := os.Environ()
	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// environSlice renders an environment map as the "K=V" slice exec expects.
func environSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
