package runspace

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/c360studio/semdev/internal/cleanroom"
)

// Sandboxes tracks each run's WARM dev-loop sandbox container — the one the
// bounded loop's measure_task (and, later, check_floors) Exec into across
// apply→measure→retry iterations (design SB4, "warm dev iterations"). It is the
// concrete implementation behind provision_sandbox's Warmers seam and
// measure_task's Sandboxes seam.
//
// The warm container is stood up ONCE, by provision_sandbox, AFTER the cold
// baseline proved the declared image builds the repo cold — over the SAME host
// checkout the developer's apply_patch writes (the container bind-mounts it at
// /work), so measure reads the PATCHED bytes, never a stale copy. It stays Up
// across the run's iterations (unlike the cold baseline and the cold final verify,
// which are throwaway Up→prove→Down — three instances, ONE warm) and is torn down
// best-effort by the runtime reaper (CloseAll on Stop) — NOT a lifecycle
// transition (G2), and NOT a graph fact: a warm container is run-scoped INFRA, like
// the checkout, held in memory and re-provisioned idempotently, never a stamped
// host handle (B1).
//
// The seam FAILS CLOSED: measure asking for a run's warm sandbox before one is
// provisioned gets an error (the run parks toward the human), never a silent
// host-exec fallback that would measure over an unproven environment (SB5 — the
// semspec disease).
type Sandboxes struct {
	mu   sync.Mutex
	warm map[string]warmSandbox
	// newRunner builds the Runner a Provision stands the warm container up with. A
	// field so a unit test can inject a MockRunner (no docker); production builds a
	// ContainerRunner for the declared image.
	newRunner func(image string) cleanroom.Runner
}

// warmSandbox pairs a run's warm container handle with the Runner that owns it, so
// Resolve can hand measure both (Exec needs the Runner AND its Sandbox), and Down
// tears down the exact container that was stood up.
type warmSandbox struct {
	runner cleanroom.Runner
	sb     cleanroom.Sandbox
}

// NewSandboxes builds an empty warm-sandbox registry whose Provision stands up a
// real docker ContainerRunner per run.
func NewSandboxes() *Sandboxes {
	return &Sandboxes{
		warm:      map[string]warmSandbox{},
		newRunner: func(image string) cleanroom.Runner { return cleanroom.NewContainerRunner(image) },
	}
}

// Provision stands up a fresh warm container from image over workDir (the run's
// checkout root, bind-mounted at the container's /work) with one fresh cache home
// per cacheEnvs entry — the universal G4 control, never shared across runs — and
// records it for the run. A prior warm sandbox for the same run is torn down first
// (a re-provision replaces it), so a run never accumulates containers. A non-nil
// error is a provisioning transport fault the caller parks on (SB5); nothing is
// recorded on failure.
func (s *Sandboxes) Provision(ctx context.Context, runEntityID, image, workDir string, cacheEnvs []string) (cleanroom.Sandbox, error) {
	if runEntityID == "" {
		return cleanroom.Sandbox{}, fmt.Errorf("runspace: provision needs a run entity id")
	}
	runner := s.newRunner(image)
	sb, err := runner.Up(ctx, workDir, cacheEnvs)
	if err != nil {
		return cleanroom.Sandbox{}, fmt.Errorf("runspace: stand up warm sandbox for %s: %w", runEntityID, err)
	}

	s.mu.Lock()
	prior, hadPrior := s.warm[runEntityID]
	s.warm[runEntityID] = warmSandbox{runner: runner, sb: sb}
	s.mu.Unlock()

	// Tear down any replaced container OUTSIDE the lock and on a detached context (the
	// caller's ctx may already be dead) — a re-provision must never leak the prior.
	if hadPrior {
		_ = prior.runner.Down(context.WithoutCancel(ctx), prior.sb)
	}
	return sb, nil
}

// Resolve returns the run's warm Runner + Sandbox — the pair measure_task Execs
// through. It FAILS CLOSED when nothing is provisioned: the tool surfaces the error
// and the run parks, never a silent host-exec fallback (SB5).
func (s *Sandboxes) Resolve(_ context.Context, runEntityID string) (cleanroom.Runner, cleanroom.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.warm[runEntityID]
	if !ok {
		return nil, cleanroom.Sandbox{}, fmt.Errorf("runspace: no warm sandbox for run %s — provision it first (park toward the human)", runEntityID)
	}
	return w.runner, w.sb, nil
}

// Down tears down and forgets one run's warm sandbox (best-effort). A no-op when
// nothing is provisioned.
//
// M0 status: this is a forward per-run reap hook — it is NOT yet wired to a run
// terminal, so within one process lifetime warm containers accumulate until Stop
// (CloseAll) reaps them all. Fine for the single-run M0 journey; a per-run reap on
// the run's terminal is itself G2-sensitive (it must not become a B3/B7 reconciler),
// so wiring it is deferred with the run-complete station. Tracked in the design's
// group-7 carry-forwards.
func (s *Sandboxes) Down(ctx context.Context, runEntityID string) error {
	s.mu.Lock()
	w, ok := s.warm[runEntityID]
	if ok {
		delete(s.warm, runEntityID)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return w.runner.Down(ctx, w.sb)
}

// CloseAll tears down every tracked warm sandbox — the runtime reaper the boot path
// calls on Stop, so a SIGINT/shutdown does not leak a docker container + cache
// volumes per run. Best-effort; every teardown runs even if an earlier one errs.
func (s *Sandboxes) CloseAll(ctx context.Context) error {
	s.mu.Lock()
	all := s.warm
	s.warm = map[string]warmSandbox{}
	s.mu.Unlock()

	var errs []error
	for id, w := range all {
		if err := w.runner.Down(ctx, w.sb); err != nil {
			errs = append(errs, fmt.Errorf("down warm sandbox %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
