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
	"sync"
)

// Checkouts materializes and tracks per-run target-repo checkouts. It implements the
// Workspace seam (Root) that measure_task and verify_artifact resolve the checkout root
// through. Safe for concurrent use.
type Checkouts struct {
	mu    sync.Mutex
	base  string
	roots map[string]string // runEntityID → absolute checkout root (the warm checkout)
	// verifyRoots holds the cold-verify CLONES — a SEPARATE fresh copy of a run's warm
	// checkout the clean-room final verify proves cold (SB4.3), keyed distinctly so it
	// never collides with the warm checkout. A clone is a throwaway per verify (a re-run
	// overwrites the run's entry); the prior clone dir is reaped so verifies do not leak.
	verifyRoots map[string]string // runEntityID → absolute cold-verify clone root
}

// NewCheckouts builds a Checkouts that materializes runs' checkouts under base. If base
// is empty, a process-scoped temp directory is created. base is created if absent.
func NewCheckouts(base string) (*Checkouts, error) {
	if base == "" {
		dir, err := os.MkdirTemp("", "semdev-checkouts-*")
		if err != nil {
			return nil, fmt.Errorf("runspace: create checkouts base: %w", err)
		}
		base = dir
	} else if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("runspace: create checkouts base %s: %w", base, err)
	}
	return &Checkouts{base: base, roots: map[string]string{}, verifyRoots: map[string]string{}}, nil
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
	if err := copyTree(ctx, absSource, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("runspace: materialize checkout for %s: %w", runEntityID, err)
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

	// Copy into a fresh dir fully BEFORE recording it (and outside the map mutation), so a
	// failed copy leaves any prior good clone intact — the Materialize discipline.
	dest, err := os.MkdirTemp(c.base, "verify-*")
	if err != nil {
		return "", fmt.Errorf("runspace: create verify clone dir: %w", err)
	}
	if err := copyTree(ctx, warm, dest); err != nil {
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
