package runspace

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/c360studio/semdev/internal/cliexec"
)

// gitBin is the git CLI the patcher shells to apply the unified diff. git is a hard
// dependency of the run path anyway (the g8 `--recursive` clone), and the operator
// image carries it.
const gitBin = "git"

// Patcher applies a developer-authored unified diff to a run's CHECKOUT — the
// SB6 code-authoring mechanism semdev lacked. It is the concrete apply_patch seam:
// the tool passes the diff, this resolves the run's checkout root, PATH-GUARDS every
// file the diff touches to inside that checkout (never the host — the sandbox==nil /
// harness-fixup class both predecessors died on), and applies it with `git apply`.
//
// The checkout is the host directory bind-mounted into the run's container at /work,
// so applying on the host checkout root IS applying to the container's /work — the
// dev loop (measure_task/floors, in-container at g7) then sees exactly what was
// authored. The apply is mechanical only: it writes the change and reports whether
// it applied; the OUTCOME (does the task's test pass) is measured separately by the
// harness (G3), never supplied by whoever authored the diff.
type Patcher struct {
	checkouts *Checkouts
	runner    cliexec.Runner
}

// NewPatcher builds the apply_patch seam over the run-checkout registry and a
// local-command runner (cliexec.OSRunner in production; a scripted runner in tests).
func NewPatcher(checkouts *Checkouts, runner cliexec.Runner) *Patcher {
	return &Patcher{checkouts: checkouts, runner: runner}
}

// Apply path-guards and applies diff to runEntityID's checkout, COMMITS the result under
// the harness identity, and returns the repo-relative files it touched plus the new commit
// SHA. Committing per attempt is what makes the checkout snapshot-able: the cold verify
// clones this exact commit and read_diff (group 4) diffs base..commit, so what is reviewed
// and proven is an immutable tree, never the mutable warm working copy (G4/G7). It FAILS
// CLOSED at every step: no checkout for the run (park), an empty/target-less/rename diff, a
// path that escapes the checkout, a git-apply or commit failure — none silently succeed. The
// returned error is the reason the developer's authored change did not land; the measured
// pass/fail of the change is a SEPARATE harness measurement (G3), not derived here.
func (p *Patcher) Apply(ctx context.Context, runEntityID, diff string) (touched []string, commitSHA string, err error) {
	if strings.TrimSpace(diff) == "" {
		return nil, "", fmt.Errorf("runspace: apply_patch got an empty diff")
	}
	root, err := p.checkouts.Root(ctx, runEntityID)
	if err != nil {
		return nil, "", err // fail-closed: no checkout materialized (park toward the human)
	}
	targets, err := parseDiffTargets(diff)
	if err != nil {
		return nil, "", err
	}
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("runspace: diff declares no file targets (no --- / +++ headers)")
	}
	// PATH-GUARD every target to inside the checkout BEFORE touching the filesystem —
	// the primary containment. `git apply` also rejects escapes (belt), but a rule
	// that relies on the tool's own protection alone is the pattern the constitution
	// bars; safeJoin is the explicit fail-closed guard.
	for _, t := range targets {
		if _, err := safeJoin(root, t); err != nil {
			return nil, "", err
		}
	}

	// The diff is the patch SOURCE, not a checkout target, so it lives in a temp file
	// OUTSIDE the checkout; `git apply` reads it and applies relative to the checkout
	// root (dir), stripping the a/ b/ prefix (-p1).
	tmp, err := os.CreateTemp("", "semdev-patch-*.diff")
	if err != nil {
		return nil, "", fmt.Errorf("runspace: create patch temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(diff); err != nil {
		_ = tmp.Close()
		return nil, "", fmt.Errorf("runspace: write patch temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, "", fmt.Errorf("runspace: close patch temp file: %w", err)
	}

	res, err := p.runner.Run(ctx, root, gitBin, "apply", "-p1", tmp.Name())
	if err != nil {
		return nil, "", fmt.Errorf("runspace: run git apply: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, "", fmt.Errorf("runspace: git apply did not apply the diff (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	// Commit the applied attempt under the harness identity Materialize configured
	// repo-local, so the checkout carries an immutable snapshot the cold verify clones and
	// read_diff diffs against base. Commit failure is fail-closed (the developer re-authors):
	// a change that applied but could not be committed is not a landed attempt.
	sha, err := p.commit(ctx, root)
	if err != nil {
		return nil, "", err
	}
	return targets, sha, nil
}

// commit stages the whole checkout and commits it, returning the new commit SHA. The
// harness identity is the repo-local config Materialize set at init, so no per-commit
// identity is needed. --no-gpg-sign: a run never blocks on an operator signing key.
func (p *Patcher) commit(ctx context.Context, root string) (string, error) {
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "--no-gpg-sign", "-m", "attempt: apply_patch"}} {
		res, err := p.runner.Run(ctx, root, gitBin, args...)
		if err != nil {
			return "", fmt.Errorf("runspace: run git %v: %w", args, err)
		}
		if res.ExitCode != 0 {
			// git reports "nothing to commit" on STDOUT (a diff that applied but changed no
			// TRACKED path — e.g. it touched only gitignored files), so surface both streams
			// or the caller gets an empty reason. A no-op attempt fails closed here.
			return "", fmt.Errorf("runspace: git %v failed (exit %d): %s", args, res.ExitCode, strings.TrimSpace(res.Stdout+" "+res.Stderr))
		}
	}
	res, err := p.runner.Run(ctx, root, gitBin, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("runspace: run git rev-parse: %w", err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("runspace: git rev-parse HEAD failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	sha := strings.TrimSpace(res.Stdout)
	if sha == "" {
		return "", fmt.Errorf("runspace: git rev-parse HEAD returned empty SHA")
	}
	return sha, nil
}

// parseDiffTargets extracts the repo-relative files a unified diff touches from its
// `--- `/`+++ ` headers, stripping the leading a/ b/ segment (-p1) — the exact paths
// `git apply -p1` will write, so the caller's containment guard covers precisely
// git's write set. The invariant it upholds: EVERY path git writes is either extracted
// here (and guarded by the caller) OR the diff is rejected — git apply's own escape
// rejection is a backstop, never the sole guard. It fails closed on inputs whose write
// targets it cannot fully enumerate: rename/copy/symlink/binary headers (isUnsupported
// DiffHeader), and — crucially — a `diff --git` block with NO `--- `/`+++ ` hunk (an
// empty-file create, mode-only change, or empty deletion writes without a hunk this
// parser would see, so a mixed diff could otherwise smuggle an unguarded second path).
func parseDiffTargets(diff string) ([]string, error) {
	seen := map[string]bool{}
	var targets []string
	// A `diff --git` block that names a file but carries no content hunk still makes git
	// write (create/chmod/delete) — a write this parser does not extract. Require every
	// such block to carry a `--- `/`+++ ` hunk; reject one that does not, so no unguarded
	// write can ride alongside a guarded one.
	inGitBlock, blockHasContent := false, false
	flushBlock := func() error {
		if inGitBlock && !blockHasContent {
			return fmt.Errorf("runspace: a `diff --git` block declares no in-place content change (no --- / +++ hunk) — empty-file/mode-only/deletion-only changes are unsupported at M0; author a content diff")
		}
		return nil
	}

	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if err := flushBlock(); err != nil {
				return nil, err
			}
			inGitBlock, blockHasContent = true, false
			continue
		}
		// Reject — LINE-ANCHORED so a content line (+/-/space) that merely mentions a
		// keyword or the number 120000 is not misread as a header — diff features whose
		// write set this text parser cannot fully enumerate/vet:
		//   - rename/copy: extended git headers with new paths we don't parse.
		//   - symlink mode (120000): would write a link inside the checkout whose target
		//     could point out of the tree (the materialized checkout carries none —
		//     copyTree strips symlinks — so a diff must not reintroduce one).
		//   - binary patch: opaque write content we cannot path-verify.
		if isUnsupportedDiffHeader(line) {
			return nil, fmt.Errorf("runspace: unsupported diff feature %q — author a plain in-place text unified diff (rename/copy/symlink/binary are M0-out of scope)", strings.TrimSpace(line))
		}

		var raw string
		switch {
		case strings.HasPrefix(line, "--- "):
			raw = line[len("--- "):]
		case strings.HasPrefix(line, "+++ "):
			raw = line[len("+++ "):]
		default:
			continue
		}
		blockHasContent = true                         // a content hunk header: git applies via a path we guard
		if i := strings.IndexByte(raw, '\t'); i >= 0 { // drop a trailing timestamp
			raw = raw[:i]
		}
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "/dev/null" { // a new/deleted side: the other header names the file
			continue
		}
		rel, ok := stripP1(raw)
		if !ok {
			return nil, fmt.Errorf("runspace: diff header %q lacks an a/ b/ prefix (apply_patch needs -p1 unified-diff format)", raw)
		}
		if !seen[rel] {
			seen[rel] = true
			targets = append(targets, rel)
		}
	}
	if err := flushBlock(); err != nil {
		return nil, err
	}
	return targets, nil
}

// isUnsupportedDiffHeader reports whether line is a git extended header for a diff
// feature apply_patch does not support at M0 (rename/copy/symlink/binary). It is
// anchored to header shapes — a content line (starting with +/-/space) is never a
// header — so a code line like `+timeout := 120000` or one mentioning "rename" is not
// misclassified.
func isUnsupportedDiffHeader(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case '+', '-', ' ', '@':
		return false // diff content / hunk header, never an extended file header
	}
	if line == "GIT binary patch" {
		return true
	}
	for _, p := range []string{"rename from ", "rename to ", "copy from ", "copy to "} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	// git symlink modes: `new file mode 120000`, `old mode 120000`, `new mode 120000`,
	// `deleted file mode 120000` — a mode header ending in the symlink mode.
	if strings.HasSuffix(line, "mode 120000") {
		return true
	}
	return false
}

// stripP1 removes the first path segment (the a/ or b/ prefix `git apply -p1`
// strips). ok is false when there is no segment to strip (a bare path with no prefix
// is not the -p1 format the applier expects).
func stripP1(p string) (rel string, ok bool) {
	i := strings.IndexByte(p, '/')
	if i < 0 {
		return "", false
	}
	return p[i+1:], true
}
