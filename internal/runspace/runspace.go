// Package runspace resolves the per-run target-repo CHECKOUT and everything the dev-loop
// and clean-room tools read from it — the concrete implementations of the Workspace /
// Manifests / Attempts seams the tools declared nil at M0 (design SB4, group 4).
//
// The checkout is the artifact a run develops. At M0 it is a local working COPY of the
// in-repo Go fixture — the design's M0 "checkout" (Open Questions: the M0 verify clones
// the fixture-with-the-applied-diff into a fresh dir); forge-io's real `--recursive`
// clone of a live PR lands at M2 behind this same seam. A run's checkout is run-scoped
// INFRA state (a materialized directory + its mapping), like a container handle — NOT
// domain state, so it lives in memory and is re-materialized idempotently on restart,
// never a graph fact holding a host path.
//
// The seams FAIL CLOSED: a tool that asks for a run's checkout before one is materialized
// gets an error (the run parks toward the human), never a silent host-path guess (SB5).
package runspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/c360studio/semdev/internal/cliexec"
)

// Harness git identity — the fixed author every base + attempt commit is made under, set
// repo-local at init so the run path never depends on the operator's global git config
// (CI machines often have none) and every commit is attributable to the harness, not a
// person (G7). A checkout is a throwaway working copy, so a shared identity is correct.
const (
	harnessGitName  = "semdev harness"
	harnessGitEmail = "harness@semdev.local"
)

// baseRef is the custom in-repo ref that records a run's DIFF BASE — the prepare-time tip
// (the fixture's pristine baseline commit, or a forge clone's default-branch tip). Diff
// reads `refs/semdev/base..HEAD`. It is written with `git update-ref` (NOT a tag, which
// would land under refs/tags/ and not resolve as refs/semdev/base) and lives inside the
// checkout, so it is exactly as durable as the checkout dir — Diff holds no in-memory base
// map, and restart reconstruction stays the pre-existing gap (design D2 / task 2.6).
const baseRef = "refs/semdev/base"

// Checkouts materializes and tracks per-run target-repo checkouts. It implements the
// Workspace seam (Root) that measure_task and verify_artifact resolve the checkout root
// through. Each checkout is a real git repository (git-init at materialize, one commit per
// applied attempt) so the cold verify proves an IMMUTABLE committed snapshot, never the
// mutable warm working tree (G4/G7). Safe for concurrent use.
type Checkouts struct {
	mu     sync.Mutex
	base   string
	runner cliexec.Runner    // shells git for init/commit/clone (OSRunner in prod)
	roots  map[string]string // runEntityID → absolute checkout root (the warm checkout)
	// verifyRoots holds the cold-verify CLONES — a SEPARATE git clone of a run's warm
	// checkout AT ITS COMMITTED HEAD that the clean-room final verify proves cold (SB4.3),
	// keyed distinctly so it never collides with the warm checkout. A clone is a throwaway
	// per verify (a re-run overwrites the run's entry); the prior clone dir is reaped so
	// verifies do not leak.
	verifyRoots map[string]string // runEntityID → absolute cold-verify clone root
}

// NewCheckouts builds a Checkouts that materializes runs' checkouts under base, shelling
// git through runner (cliexec.OSRunner in production). If base is empty, a process-scoped
// temp directory is created. base is created if absent.
func NewCheckouts(base string, runner cliexec.Runner) (*Checkouts, error) {
	if runner == nil {
		return nil, fmt.Errorf("runspace: NewCheckouts needs a git runner")
	}
	if base == "" {
		dir, err := os.MkdirTemp("", "semdev-checkouts-*")
		if err != nil {
			return nil, fmt.Errorf("runspace: create checkouts base: %w", err)
		}
		base = dir
	} else if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("runspace: create checkouts base %s: %w", base, err)
	}
	return &Checkouts{base: base, runner: runner, roots: map[string]string{}, verifyRoots: map[string]string{}}, nil
}

// git runs a git subcommand in dir and returns its trimmed stdout, failing closed on any
// non-zero exit (the stderr travels in the error so the caller can surface the real cause).
func (c *Checkouts) git(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := c.runner.Run(ctx, dir, gitBin, args...)
	if err != nil {
		return "", fmt.Errorf("runspace: run git %v: %w", args, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("runspace: git %v failed (exit %d): %s", args, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// gitStatusPorcelain returns one entry per path in `git status --porcelain` over dir's
// checkout — empty when the working tree matches HEAD. Each line is the porcelain status
// (e.g. " M health.go", "?? residue.txt"); the clean-tree floor only needs their presence
// and count, so the raw lines are returned verbatim.
func (c *Checkouts) gitStatusPorcelain(ctx context.Context, dir string) ([]string, error) {
	out, err := c.git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			dirty = append(dirty, line)
		}
	}
	return dirty, nil
}

// configHarnessIdentity sets the repo-local harness author on dir's checkout. Repo-local
// (not global) so the run path never depends on the operator's git config; required on BOTH
// the git-init path AND the clone path — `git clone` copies no identity and CI machines have
// no global one, so omitting it fails apply_patch's commit with "Author identity unknown".
func (c *Checkouts) configHarnessIdentity(ctx context.Context, dir string) error {
	if _, err := c.git(ctx, dir, "config", "user.email", harnessGitEmail); err != nil {
		return err
	}
	if _, err := c.git(ctx, dir, "config", "user.name", harnessGitName); err != nil {
		return err
	}
	return nil
}

// recordBase writes baseRef at dir's current HEAD — the run's diff base (the prepare-time
// tip). See baseRef's doc for why it is a custom ref rather than a tag or an in-memory map.
func (c *Checkouts) recordBase(ctx context.Context, dir string) error {
	_, err := c.git(ctx, dir, "update-ref", baseRef, "HEAD")
	return err
}

// initCommit turns a freshly-copied (non-git) checkout dir into a git repository with the
// harness identity and one base commit of the pristine source, so every later attempt commit
// has a parent and `git diff refs/semdev/base..HEAD` is the cumulative authored change. It is
// the immutable-snapshot foundation: what verify clones is a commit, not a mutable tree. The
// fixture path; a source that already carries history takes cloneCheckout instead.
func (c *Checkouts) initCommit(ctx context.Context, dir string) error {
	if _, err := c.git(ctx, dir, "init", "-q"); err != nil {
		return err
	}
	if err := c.configHarnessIdentity(ctx, dir); err != nil {
		return err
	}
	if _, err := c.git(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	// --no-gpg-sign: a run must never block on an operator's signing key; the harness
	// identity is the attribution (G7), not a cryptographic signature.
	if _, err := c.git(ctx, dir, "commit", "-q", "--no-gpg-sign", "-m", "base: pristine checkout"); err != nil {
		return err
	}
	// The pristine base commit IS the diff base for the fixture path.
	return c.recordBase(ctx, dir)
}

// cloneCheckout materializes a forge-clone source into dest by git-cloning it, PRESERVING
// its history — so the run develops the REAL target and delivery's PR diffs cleanly against
// the target's base (design D2). --no-hardlinks keeps dest's object store independent of the
// per-run source clone (which is reaped). The harness identity is re-configured (clone copies
// none — the H2 fix) and refs/semdev/base pins the cloned tip as the diff base. The run stays
// on the cloned default branch: delivery pushes the recorded attempt sha to its OWN remote
// head (delivery.BranchPrefix + runSuffix), so the local branch name is irrelevant.
func (c *Checkouts) cloneCheckout(ctx context.Context, absSource, dest string) error {
	// dest is a fresh empty dir (MkdirTemp); git clone accepts an existing empty target.
	if _, err := c.git(ctx, "", "clone", "-q", "--no-hardlinks", absSource, dest); err != nil {
		return err
	}
	// An EMPTY target (a git repo with zero commits — a freshly created remote, like an
	// unseeded semdev-test) clones with NO HEAD: there is nothing to develop and no tip to
	// record as the diff base. Fail CLOSED with an operator-facing cause (SB5) rather than the
	// opaque "HEAD: not a valid SHA1" that recordBase's update-ref would raise, and never a
	// guessed base. `rev-parse -q --verify HEAD` exits non-zero on an unborn HEAD.
	if _, err := c.git(ctx, dest, "rev-parse", "-q", "--verify", "HEAD"); err != nil {
		return fmt.Errorf("source %s is a git repository with no commits (empty target) — seed it with at least one commit before provisioning", absSource)
	}
	if err := c.configHarnessIdentity(ctx, dest); err != nil {
		return err
	}
	return c.recordBase(ctx, dest)
}

// sourceHasGit reports whether absSource is itself a git repository (has a .git entry) — a
// forge clone whose history must be preserved, rather than a plain fixture directory.
func sourceHasGit(absSource string) bool {
	_, err := os.Stat(filepath.Join(absSource, ".git"))
	return err == nil
}

// Materialize creates a fresh working copy of sourceDir for the run and records it,
// returning the checkout root. It is idempotent per run: re-materializing replaces the
// prior copy so the checkout always reflects a clean source (never a mix of runs). The
// copy runs OUTSIDE the lock (it can be slow — the M2 clone — and must not block other
// runs' Root/Remove) and BEFORE the prior checkout is touched, so a failed copy leaves
// the run's existing good checkout intact rather than destroying it.
func (c *Checkouts) Materialize(ctx context.Context, runEntityID, sourceDir string) (string, error) {
	if runEntityID == "" {
		return "", fmt.Errorf("runspace: materialize needs a run entity id")
	}
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", fmt.Errorf("runspace: resolve source %s: %w", sourceDir, err)
	}
	if info, err := os.Stat(absSource); err != nil || !info.IsDir() {
		return "", fmt.Errorf("runspace: source %s is not a directory: %w", absSource, err)
	}

	// c.base is set once at construction and never mutated, so it is safe to read
	// without the lock; the copy into a fresh dir happens fully before any map mutation.
	dest, err := os.MkdirTemp(c.base, "run-*")
	if err != nil {
		return "", fmt.Errorf("runspace: create checkout dir: %w", err)
	}
	// Prepare the fresh dir into the run's checkout BEFORE recording it, so a failed prepare
	// leaves the prior good checkout intact (the copy-before-record discipline). Two paths by
	// whether the source carries history (design D2): a forge clone is git-cloned so its
	// history is preserved (a real, clean-diff PR); a plain fixture is copied and git-init'd
	// with a pristine base commit. Both configure the harness identity and record
	// refs/semdev/base, so the checkout is always a snapshot-able repo with a defined diff base.
	if sourceHasGit(absSource) {
		if err := c.cloneCheckout(ctx, absSource, dest); err != nil {
			_ = os.RemoveAll(dest)
			return "", fmt.Errorf("runspace: clone checkout for %s: %w", runEntityID, err)
		}
	} else {
		if err := copyTree(ctx, absSource, dest); err != nil {
			_ = os.RemoveAll(dest)
			return "", fmt.Errorf("runspace: materialize checkout for %s: %w", runEntityID, err)
		}
		if err := c.initCommit(ctx, dest); err != nil {
			_ = os.RemoveAll(dest)
			return "", fmt.Errorf("runspace: git-init checkout for %s: %w", runEntityID, err)
		}
	}

	// Only now that the new copy is complete: record it and remove any prior copy.
	c.mu.Lock()
	prior, hadPrior := c.roots[runEntityID]
	c.roots[runEntityID] = dest
	c.mu.Unlock()
	if hadPrior {
		_ = os.RemoveAll(prior)
	}
	return dest, nil
}

// Root returns the run's materialized checkout root — the Workspace seam. It fails CLOSED
// when no checkout has been materialized for the run: the tool surfaces the error and the
// run parks, never a silent guess at where the artifact lives (SB5).
func (c *Checkouts) Root(_ context.Context, runEntityID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	root, ok := c.roots[runEntityID]
	if !ok {
		return "", fmt.Errorf("runspace: no checkout materialized for run %s — cannot resolve the workspace (park toward the human)", runEntityID)
	}
	return root, nil
}

// CloneForVerify makes a fresh COPY of the run's warm checkout into a new dir and returns
// it — the clean-room final verify's "--recursive clone of the committed artifact" at M0
// (design SB4.3 / Open Questions: clone the fixture-with-the-applied-diff into a fresh
// dir). It is deliberately NON-DESTRUCTIVE of the warm checkout (it never touches
// roots[runEntityID]): calling Materialize for verify would wipe the applied diff the warm
// container measured over (the group-4 carry-forward (b) trap). The clone is what the cold
// verify builds the image and runs tests FROM, so the COMMITTED bytes — not the warm
// container's environment — are what's proven; combined with a fresh cache home (the cold
// container's own anonymous volumes) this is what makes a cache-masked fabrication or a
// harness-only fixup FAIL here (SB3, the semspec grave). Fails CLOSED if no warm checkout
// exists (nothing to prove → the run parks). A prior clone for the run is reaped so
// re-verifies do not leak.
func (c *Checkouts) CloneForVerify(ctx context.Context, runEntityID string) (string, error) {
	if runEntityID == "" {
		return "", fmt.Errorf("runspace: clone-for-verify needs a run entity id")
	}
	c.mu.Lock()
	warm, ok := c.roots[runEntityID]
	c.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("runspace: no checkout materialized for run %s — cannot clone for cold verify (park toward the human)", runEntityID)
	}

	// git-clone the warm repo into a fresh dir fully BEFORE recording it (and outside the
	// map mutation), so a failed clone leaves any prior good clone intact — the Materialize
	// discipline. `git clone` copies only COMMITTED objects and checks out HEAD, so the
	// clone's working tree is exactly the latest committed attempt (attempt.commit in the
	// strictly-serial M0 chain) — an uncommitted warm-tree change (test residue, tampering)
	// is structurally excluded. This is what makes verify prove an immutable snapshot, not a
	// copy of the mutable tree (G4/G7, the semspec grave). --no-hardlinks keeps the clone's
	// object store physically independent of the warm repo's.
	dest, err := os.MkdirTemp(c.base, "verify-*")
	if err != nil {
		return "", fmt.Errorf("runspace: create verify clone dir: %w", err)
	}
	// git permits cloning into an existing EMPTY directory (which MkdirTemp guarantees), and
	// dest becomes the clone's worktree root. warm/dest are absolute, so the runner's cwd
	// (dir="") is irrelevant.
	if _, err := c.git(ctx, "", "clone", "-q", "--no-hardlinks", warm, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("runspace: clone checkout for verify of %s: %w", runEntityID, err)
	}

	c.mu.Lock()
	prior, hadPrior := c.verifyRoots[runEntityID]
	c.verifyRoots[runEntityID] = dest
	c.mu.Unlock()
	if hadPrior {
		_ = os.RemoveAll(prior)
	}
	return dest, nil
}

// Diff returns the unified diff of everything the run has authored so far: the run's diff
// base (refs/semdev/base — the prepare-time tip recorded at materialize) through its current
// HEAD. Under the M0 one-in-flight serialization invariant a run develops exactly one task
// with attempts applied strictly serially, so HEAD IS the latest committed attempt
// (attempt.commit, apply_patch's stamped pointer) — base..HEAD is exactly the cumulative
// authored change. It is the read_diff seam: the reviewer (Quinn) reads this instead of
// re-deriving the diff from raw file contents. Fails CLOSED when no checkout is materialized
// (Root's fail-closed posture) or git itself fails. It bases on refs/semdev/base, NOT the
// repo's root commit: a forge clone's root is the target's ORIGINAL commit, which would fold
// the entire pre-existing history into the "authored" diff — for the fixture, refs/semdev/base
// IS the pristine baseline commit, so the diff is byte-identical to the pre-change behavior.
func (c *Checkouts) Diff(ctx context.Context, runEntityID string) (string, error) {
	root, err := c.Root(ctx, runEntityID)
	if err != nil {
		return "", err
	}
	// `git diff` exits 0 whether or not there is output (a base==HEAD run authored nothing
	// yet, which is a legitimate empty diff, not an error); c.git already fails closed on a
	// non-zero exit (a malformed ref, a corrupt repo), so no separate exit-code handling is
	// needed here.
	diff, err := c.git(ctx, root, "diff", baseRef+"..HEAD")
	if err != nil {
		return "", fmt.Errorf("runspace: diff %s..HEAD for %s: %w", baseRef, runEntityID, err)
	}
	return diff, nil
}

// Remove tears down a run's checkout AND any cold-verify clone (best-effort). A no-op when
// nothing is materialized.
func (c *Checkouts) Remove(runEntityID string) error {
	c.mu.Lock()
	root, ok := c.roots[runEntityID]
	delete(c.roots, runEntityID)
	verifyRoot, hadVerify := c.verifyRoots[runEntityID]
	delete(c.verifyRoots, runEntityID)
	c.mu.Unlock()
	if hadVerify {
		_ = os.RemoveAll(verifyRoot)
	}
	if !ok {
		return nil
	}
	return os.RemoveAll(root)
}

// copyTree recursively copies src into dst (which must already exist), preserving file
// modes. Symlinks are skipped (a checkout is regular source; a symlink out of the tree is
// a path-escape surface a materialized copy must not carry).
func copyTree(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			// A checkout is a WORKING copy (apply_patch writes into it), so directories
			// are created writable (0755) rather than mirroring a possibly read-only
			// source dir mode — else a read-only source dir would block child writes.
			return os.MkdirAll(target, 0o755)
		case d.Type()&os.ModeSymlink != 0:
			return nil // skip symlinks
		default:
			return copyFile(path, target)
		}
	})
}

// copyFile copies a single regular file, preserving its mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
