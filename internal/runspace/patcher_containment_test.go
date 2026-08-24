package runspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/floors"
)

// RED-FIRST PIN P1 (security-forge-containment 1.1): working-tree residue — a file NO
// applied diff introduced — must NOT reach the attempt commit. Today `commit()` stages
// `git add -A`, so anything sitting in the tree (a prior attempt's test artifact, scratch
// state) is silently absorbed into the committed snapshot that measurement, review, cold
// verify, and finally the delivered PR all consume. The commit tree must contain exactly
// the base tree plus contract-authorized diff content.
func TestPatcherCommitExcludesWorkingTreeResidue(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "alpha\nbeta\ngamma\n"})
	ctx := context.Background()
	root, err := p.checkouts.Root(ctx, run)
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// Residue: untracked, non-gitignored, introduced by no diff.
	if err := os.WriteFile(filepath.Join(root, "residue.out"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"
	_, sha, err := p.Apply(ctx, run, diff)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	tree := gitOut(t, root, "ls-tree", "-r", "--name-only", sha)
	if strings.Contains(tree, "residue.out") {
		t.Fatalf("the attempt commit contains residue no diff introduced — unauthorized content would ride the delivered PR:\n%s", tree)
	}
	// The authored change itself IS committed.
	if !strings.Contains(tree, "pkg/f.txt") {
		t.Fatalf("the committed tree lost the authored target file:\n%s", tree)
	}
	// And the attempt ends resolvable: the tree matches the commit (no dirty carry-over
	// for the clean-tree floor to reject on a residue the model cannot remove).
	if out := gitOut(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("checkout dirty after apply+commit (residue left to doom every later attempt): %q", out)
	}
}

// RED-FIRST PIN P2 (security-forge-containment 1.2): the laundering shape. Attempt N's
// measurement leaves a non-ignored artifact in the working tree → CleanTree rejects
// (correct). Today the retry's `git add -A` then absorbs that artifact into attempt N+1's
// commit: the floor goes green BECAUSE the unauthorized file was committed, and that
// laundered sha is exactly what delivery pushes. The pin drives the real resolver
// (Attempts.Resolve → floors.CleanTree) across both attempts and requires the retry's
// commit to be residue-free AND floor-clean.
func TestPatcherLaunderingClosedAcrossAttempts(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "alpha\nbeta\ngamma\n"})
	ctx := context.Background()
	root, err := p.checkouts.Root(ctx, run)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	resolver := NewAttempts(targetFilesReader([]string{"pkg/f.txt"}), p.checkouts)

	// Attempt N: a legitimate authored change lands and is committed.
	diffN := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"
	if _, _, err := p.Apply(ctx, run, diffN); err != nil {
		t.Fatalf("apply attempt N: %v", err)
	}

	// Measurement side effect: the in-container test run writes a non-ignored artifact.
	if err := os.WriteFile(filepath.Join(root, "testdata.golden"), []byte("regen\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The clean-tree floor rejects attempt N — the working tree diverged from the commit.
	attemptN, err := resolver.Resolve(ctx, run, 0)
	if err != nil {
		t.Fatalf("resolve attempt N: %v", err)
	}
	if f := floors.CleanTree(attemptN); f.Passed {
		t.Fatalf("CleanTree passed a dirty tree — the pin's premise is broken: %s", f.Detail)
	}

	// The retry: a fresh authored change. The residue must NOT ride its commit.
	diffRetry := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n BETA\n-gamma\n+GAMMA\n"
	_, shaRetry, err := p.Apply(ctx, run, diffRetry)
	if err != nil {
		t.Fatalf("apply retry: %v", err)
	}
	tree := gitOut(t, root, "ls-tree", "-r", "--name-only", shaRetry)
	if strings.Contains(tree, "testdata.golden") {
		t.Fatalf("the retry's commit absorbed the prior attempt's residue (the laundering shape) — the floor would now pass on unauthorized committed content:\n%s", tree)
	}

	// And the retry is floor-clean the HONEST way: residue gone, tree == commit.
	attemptRetry, err := resolver.Resolve(ctx, run, 0)
	if err != nil {
		t.Fatalf("resolve retry: %v", err)
	}
	if f := floors.CleanTree(attemptRetry); !f.Passed {
		t.Fatalf("the retry should be floor-clean without absorbing residue: %s", f.Detail)
	}
}

// injectAfterApplyRunner delegates to the real runner and, ONCE, drops a residue file
// into the checkout immediately after the `git apply` invocation — i.e. between D2's
// reset and D1's staging — so targets-only staging is guarded INDEPENDENTLY of the
// pre-apply reset (go-review M1: without this, reverting `add -- <targets>` to `add -A`
// is caught by no pin because reset cleans the pre-planted residue first).
type injectAfterApplyRunner struct {
	inner cliexec.Runner
	root  string
	done  bool
}

func (r *injectAfterApplyRunner) Run(ctx context.Context, dir, name string, args ...string) (cliexec.Result, error) {
	res, err := r.inner.Run(ctx, dir, name, args...)
	if !r.done && name == gitBin && len(args) > 0 && args[0] == "apply" {
		if werr := os.WriteFile(filepath.Join(r.root, "midapply-residue.out"), []byte("mid\n"), 0o644); werr != nil {
			return res, werr
		}
		r.done = true
	}
	return res, err
}

// Targets-only staging (D1) holds on its own: residue arriving AFTER the reset — between
// apply and commit — is still excluded from the commit tree.
func TestPatcherCommitStagesOnlyTargetsIndependentOfReset(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "alpha\nbeta\ngamma\n"})
	ctx := context.Background()
	root, err := p.checkouts.Root(ctx, run)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	inject := NewPatcher(p.checkouts, &injectAfterApplyRunner{inner: cliexec.OSRunner{}, root: root},
		targetFilesReader([]string{"pkg/f.txt"}))

	diff := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"
	_, sha, err := inject.Apply(ctx, run, diff)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	tree := gitOut(t, root, "ls-tree", "-r", "--name-only", sha)
	if strings.Contains(tree, "midapply-residue.out") {
		t.Fatalf("mid-apply residue rode the commit — staging is not targets-only:\n%s", tree)
	}
	// The residue stays visible to the clean-tree floor (tree != commit), never absorbed.
	if out := gitOut(t, root, "status", "--porcelain"); !strings.Contains(out, "midapply-residue.out") {
		t.Fatalf("mid-apply residue not surfaced by git status: %q", out)
	}
}

// failOnRunner delegates until it sees the given git subcommand, then reports exit 1.
type failOnRunner struct {
	inner cliexec.Runner
	sub   string
}

func (r *failOnRunner) Run(ctx context.Context, dir, name string, args ...string) (cliexec.Result, error) {
	if name == gitBin && len(args) > 0 && args[0] == r.sub {
		return cliexec.Result{ExitCode: 1, Stderr: "scripted " + r.sub + " failure"}, nil
	}
	return r.inner.Run(ctx, dir, name, args...)
}

// A tree that cannot be reset is not trusted: reset failure fails the apply closed
// (go-review L2).
func TestPatcherFailsClosedWhenResetFails(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "alpha\n"})
	failing := NewPatcher(p.checkouts, &failOnRunner{inner: cliexec.OSRunner{}, sub: "reset"},
		targetFilesReader([]string{"pkg/f.txt"}))

	diff := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1 +1 @@\n-alpha\n+ALPHA\n"
	_, _, err := failing.Apply(context.Background(), run, diff)
	if err == nil {
		t.Fatal("apply must fail closed when the pre-apply reset fails")
	}
	if !strings.Contains(err.Error(), "reset") {
		t.Errorf("want the reset failure surfaced, got: %v", err)
	}
}

// RED-FIRST PIN (go-review H1): STAGED residue must not survive into the commit either.
// Measurement runs arbitrary repo test code in-container over the bind-mounted checkout
// with git present — one `git add residue` / `git rm tracked` from a build hook is enough
// to poison the INDEX, and `git commit` commits the whole index. A reset that only
// restores the worktree from the index (`git checkout -- .`) launders exactly this shape;
// the reset must be index-and-worktree to HEAD (`git reset --hard`).
func TestPatcherCommitExcludesStagedResidueAndRestoresStagedDeletion(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{
		"pkg/f.txt": "alpha\nbeta\ngamma\n",
		"other.txt": "keep me\n",
	})
	ctx := context.Background()
	root, err := p.checkouts.Root(ctx, run)
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// Hostile measurement side effects: a staged new file AND a staged deletion.
	if err := os.WriteFile(filepath.Join(root, "residue.out"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "residue.out")
	gitOut(t, root, "rm", "-q", "other.txt")

	diff := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"
	_, sha, err := p.Apply(ctx, run, diff)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	tree := gitOut(t, root, "ls-tree", "-r", "--name-only", sha)
	if strings.Contains(tree, "residue.out") {
		t.Fatalf("staged residue rode the attempt commit (the index-laundering shape):\n%s", tree)
	}
	if !strings.Contains(tree, "other.txt") {
		t.Fatalf("a staged deletion no diff authored deleted other.txt from the commit:\n%s", tree)
	}
	if out := gitOut(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("checkout dirty after apply+commit: %q", out)
	}
}

// Targets-only staging must keep supporting every diff shape the parser admits: a
// create-with-content and a delete-with-content diff both stage through
// `git add -- :(literal)<path>` (semstreams-review MEDIUM — design asserted it; this
// pins it at the Apply→ls-tree level where a staging regression would surface).
func TestPatcherStagesNewFileAndDeletionDiffs(t *testing.T) {
	p, run := materializedCheckoutWithTargets(t,
		map[string]string{"pkg/f.txt": "alpha\n", "pkg/old.txt": "obsolete\n"},
		[]string{"pkg/f.txt", "pkg/old.txt", "pkg/sub/new.txt"})
	ctx := context.Background()
	root, err := p.checkouts.Root(ctx, run)
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// One diff: create a file in a NEW directory and delete an existing file.
	diff := "--- /dev/null\n+++ b/pkg/sub/new.txt\n@@ -0,0 +1 @@\n+fresh\n" +
		"--- a/pkg/old.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-obsolete\n"
	_, sha, err := p.Apply(ctx, run, diff)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	tree := gitOut(t, root, "ls-tree", "-r", "--name-only", sha)
	if !strings.Contains(tree, "pkg/sub/new.txt") {
		t.Errorf("created file missing from the commit tree:\n%s", tree)
	}
	if strings.Contains(tree, "pkg/old.txt") {
		t.Errorf("deleted file still in the commit tree:\n%s", tree)
	}
	if out := gitOut(t, root, "status", "--porcelain"); out != "" {
		t.Errorf("tree not clean after create+delete attempt: %q", out)
	}
}

// RED-FIRST PIN for D2 (security-forge-containment 1.4): every apply starts from the
// committed snapshot. A dirty tracked file (a failed prior apply, manual mutation, or a
// test that edited the tree) must not change what a diff applies against: the sandbox
// spec says the diff applies to the committed snapshot. Today the stale edit makes
// `git apply` fail on context mismatch and the attempt dies on state the developer
// never authored.
func TestPatcherApplyResetsToCommittedSnapshot(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "alpha\nbeta\ngamma\n"})
	ctx := context.Background()
	root, err := p.checkouts.Root(ctx, run)
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	// A stale uncommitted mutation that conflicts with the committed content the
	// developer's diff was authored against.
	if err := os.WriteFile(filepath.Join(root, "pkg", "f.txt"), []byte("alpha\nMUTATED\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The diff is authored against the COMMITTED snapshot (beta, not MUTATED).
	diff := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"
	_, sha, err := p.Apply(ctx, run, diff)
	if err != nil {
		t.Fatalf("apply against the committed snapshot must succeed regardless of stale working-tree state: %v", err)
	}
	if sha == "" {
		t.Fatal("no commit SHA returned")
	}
	got, err := os.ReadFile(filepath.Join(root, "pkg", "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("applied content = %q, want the diff applied over the committed snapshot", string(got))
	}
}
