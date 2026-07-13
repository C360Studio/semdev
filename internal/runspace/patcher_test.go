package runspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
)

func TestParseDiffTargets(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want []string
		err  bool
	}{
		{
			name: "single file, a/ b/ deduped",
			diff: "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1 +1 @@\n-x\n+y\n",
			want: []string{"pkg/f.txt"},
		},
		{
			name: "new file: /dev/null side skipped",
			diff: "--- /dev/null\n+++ b/pkg/new.txt\n@@ -0,0 +1 @@\n+hi\n",
			want: []string{"pkg/new.txt"},
		},
		{
			name: "trailing timestamp stripped",
			diff: "--- a/pkg/f.txt\t2026-07-11 10:00:00\n+++ b/pkg/f.txt\t2026-07-11 10:01:00\n@@ -1 +1 @@\n-x\n+y\n",
			want: []string{"pkg/f.txt"},
		},
		{
			name: "rename diff rejected",
			diff: "diff --git a/x b/y\nrename from x\nrename to y\n",
			err:  true,
		},
		{
			name: "symlink-creating diff rejected",
			diff: "diff --git a/link b/link\nnew file mode 120000\n--- /dev/null\n+++ b/link\n@@ -0,0 +1 @@\n+/etc/passwd\n",
			err:  true,
		},
		{
			name: "binary patch rejected",
			diff: "diff --git a/blob b/blob\nGIT binary patch\nliteral 4\n@@ stuff\n",
			err:  true,
		},
		{
			name: "content mentioning 120000 / rename is NOT a header (no false reject)",
			diff: "--- a/cfg.go\n+++ b/cfg.go\n@@ -1,2 +1,2 @@\n-timeoutMillis := 60000\n+timeoutMillis := 120000 // was for the old rename to path\n",
			want: []string{"cfg.go"},
		},
		{
			name: "git-format content diff accepted",
			diff: "diff --git a/pkg/f.go b/pkg/f.go\nindex abc1234..def5678 100644\n--- a/pkg/f.go\n+++ b/pkg/f.go\n@@ -1 +1 @@\n-x\n+y\n",
			want: []string{"pkg/f.go"},
		},
		{
			name: "empty new-file block (no hunk) rejected",
			diff: "diff --git a/empty.txt b/empty.txt\nnew file mode 100644\nindex 000..000\n",
			err:  true,
		},
		{
			name: "mode-only change (no hunk) rejected",
			diff: "diff --git a/x.sh b/x.sh\nold mode 100644\nnew mode 100755\n",
			err:  true,
		},
		{
			name: "mixed: content file + headerless mode block rejected (no unguarded write rides along)",
			diff: "diff --git a/pkg/f.go b/pkg/f.go\n--- a/pkg/f.go\n+++ b/pkg/f.go\n@@ -1 +1 @@\n-x\n+y\n" +
				"diff --git a/tool.sh b/tool.sh\nold mode 100644\nnew mode 100755\n",
			err: true,
		},
		{
			name: "no a/ b/ prefix rejected",
			diff: "--- f.txt\n+++ f.txt\n@@ -1 +1 @@\n-x\n+y\n",
			err:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseDiffTargets(c.diff)
			if c.err {
				if err == nil {
					t.Fatalf("want error, got targets %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("targets = %v, want %v", got, c.want)
			}
		})
	}
}

// materializedCheckout builds a Checkouts with one run's checkout seeded from a
// source tree {relpath: content}, returning the patcher and the run id. Every seeded
// file is in the approved target_files contract (so a diff touching any of them passes
// the apply-scope guard); use materializedCheckoutWithTargets to restrict the contract.
func materializedCheckout(t *testing.T, files map[string]string) (*Patcher, string) {
	t.Helper()
	targets := make([]string, 0, len(files))
	for rel := range files {
		targets = append(targets, rel)
	}
	return materializedCheckoutWithTargets(t, files, targets)
}

// materializedCheckoutWithTargets is materializedCheckout with an explicit approved
// target_files contract (which may be a SUBSET of the seeded files), so a test can prove
// an out-of-contract path is rejected.
func materializedCheckoutWithTargets(t *testing.T, files map[string]string, targets []string) (*Patcher, string) {
	t.Helper()
	requireGit(t) // Materialize now git-inits the checkout
	src := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	checkouts, err := NewCheckouts(t.TempDir(), cliexec.OSRunner{})
	if err != nil {
		t.Fatalf("new checkouts: %v", err)
	}
	const run = "org.p.agent.chain.execution.run-1"
	if _, err := checkouts.Materialize(context.Background(), run, src); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return NewPatcher(checkouts, cliexec.OSRunner{}, targetFilesReader(targets)), run
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// Apply lands a real diff on the checkout (git apply), changing the file for real —
// the mechanical author step. The measured outcome is separate (G3).
func TestPatcherApplyChangesCheckout(t *testing.T) {
	requireGit(t)
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "alpha\nbeta\ngamma\n"})
	diff := "--- a/pkg/f.txt\n+++ b/pkg/f.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"

	touched, sha, err := p.Apply(context.Background(), run, diff)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(touched, []string{"pkg/f.txt"}) {
		t.Errorf("touched = %v, want [pkg/f.txt]", touched)
	}
	// Apply commits the attempt and returns the new commit SHA (the immutable snapshot).
	if sha == "" {
		t.Error("Apply returned an empty commit SHA — the attempt was not committed")
	}
	root, _ := p.checkouts.Root(context.Background(), run)
	got, _ := os.ReadFile(filepath.Join(root, "pkg", "f.txt"))
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Errorf("file content = %q, want the applied change", string(got))
	}
	// The applied change is COMMITTED: git status is clean and HEAD is the returned SHA.
	if out := gitOut(t, root, "status", "--porcelain"); out != "" {
		t.Errorf("checkout not clean after apply+commit: %q", out)
	}
	if head := gitOut(t, root, "rev-parse", "HEAD"); head != sha {
		t.Errorf("HEAD = %q, want the returned commit SHA %q", head, sha)
	}
}

// gitOut runs a git command in dir and returns trimmed stdout, failing the test on error.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// RED-FIRST PIN (task 3.1): a diff touching a real file INSIDE the checkout but OUTSIDE the
// approved task.spec.0.target_files is rejected — the unreviewed workflow/build/config
// escape Codex flagged. The rejection is ATOMIC: it fires before git apply, so the
// in-contract file the same diff touches is NOT partially applied either.
func TestPatcherRejectsOutOfContractPath(t *testing.T) {
	// Seed two real files; approve ONLY health.go. A diff touching the CI workflow (in the
	// checkout, safeJoin-legal) must be rejected because it is outside the contract.
	p, run := materializedCheckoutWithTargets(t,
		map[string]string{
			"health.go":                "package health\n\nfunc F() int { return 1 }\n",
			".github/workflows/ci.yml": "on: push\njobs: {}\n",
		},
		[]string{"health.go"}, // approved contract: source only, NOT the workflow
	)
	// A well-formed diff that modifies the out-of-contract workflow file.
	diff := "--- a/.github/workflows/ci.yml\n+++ b/.github/workflows/ci.yml\n@@ -1,2 +1,2 @@\n-on: push\n+on: [push, pull_request]\n jobs: {}\n"

	_, _, err := p.Apply(context.Background(), run, diff)
	if err == nil {
		t.Fatal("a diff touching a file outside target_files must be rejected (the unreviewed config escape)")
	}
	if !strings.Contains(err.Error(), "out-of-contract") && !strings.Contains(err.Error(), "outside the approved") {
		t.Errorf("want an out-of-contract rejection, got: %v", err)
	}
	// Atomic: the workflow file on disk is UNCHANGED (no partial application).
	root, _ := p.checkouts.Root(context.Background(), run)
	got, _ := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if string(got) != "on: push\njobs: {}\n" {
		t.Errorf("out-of-contract file was modified despite rejection — apply was not atomic: %q", got)
	}
}

// Atomicity under a MIXED diff: when one diff touches an in-contract file (health.go) AND
// an out-of-contract file (the workflow), the WHOLE diff is rejected and the in-contract
// file is left UNwritten too — the contract check fires before git apply, so there is no
// partial application of the allowed hunk.
func TestPatcherRejectsMixedDiffAtomically(t *testing.T) {
	p, run := materializedCheckoutWithTargets(t,
		map[string]string{
			"health.go":                "package health\n\nfunc F() int { return 1 }\n",
			".github/workflows/ci.yml": "on: push\njobs: {}\n",
		},
		[]string{"health.go"}, // only health.go is approved
	)
	// One diff, two files: an allowed edit to health.go AND a sneaky edit to the workflow.
	mixed := "--- a/health.go\n+++ b/health.go\n@@ -1,3 +1,3 @@\n package health\n \n-func F() int { return 1 }\n+func F() int { return 2 }\n" +
		"--- a/.github/workflows/ci.yml\n+++ b/.github/workflows/ci.yml\n@@ -1,2 +1,2 @@\n-on: push\n+on: [push, pull_request]\n jobs: {}\n"

	if _, _, err := p.Apply(context.Background(), run, mixed); err == nil {
		t.Fatal("a mixed diff with any out-of-contract path must be rejected wholesale")
	}
	root, _ := p.checkouts.Root(context.Background(), run)
	// The ALLOWED file is unchanged — the reject fired before git apply, so no hunk landed.
	if got, _ := os.ReadFile(filepath.Join(root, "health.go")); string(got) != "package health\n\nfunc F() int { return 1 }\n" {
		t.Errorf("in-contract file was partially applied despite a wholesale reject: %q", got)
	}
	// So is the out-of-contract file.
	if got, _ := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml")); string(got) != "on: push\njobs: {}\n" {
		t.Errorf("out-of-contract file was modified despite reject: %q", got)
	}
}

// A diff whose EVERY path is in the contract applies (the enforcement doesn't over-block).
func TestPatcherAllowsInContractPaths(t *testing.T) {
	p, run := materializedCheckoutWithTargets(t,
		map[string]string{"health.go": "package health\n\nfunc F() int { return 1 }\n"},
		[]string{"health.go"},
	)
	diff := "--- a/health.go\n+++ b/health.go\n@@ -1,3 +1,3 @@\n package health\n \n-func F() int { return 1 }\n+func F() int { return 2 }\n"
	if _, _, err := p.Apply(context.Background(), run, diff); err != nil {
		t.Fatalf("an in-contract diff must apply, got: %v", err)
	}
}

// Red-first (6.2): a diff targeting a path OUTSIDE the checkout is rejected — never a
// host write. The guard fires before git runs, and nothing is created outside.
func TestPatcherRejectsPathEscape(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"pkg/f.txt": "x\n"})
	escape := "--- a/../../../../tmp/semdev-pwn.txt\n+++ b/../../../../tmp/semdev-pwn.txt\n@@ -0,0 +1 @@\n+pwned\n"

	if _, _, err := p.Apply(context.Background(), run, escape); err == nil {
		t.Fatal("apply must reject a diff that escapes the checkout")
	} else if !strings.Contains(err.Error(), "escapes the checkout") {
		t.Errorf("want an escape rejection, got: %v", err)
	}
	if _, err := os.Stat("/tmp/semdev-pwn.txt"); err == nil {
		t.Error("escape write landed on the host — path guard failed")
		_ = os.Remove("/tmp/semdev-pwn.txt")
	}
}

// An empty diff and a diff with no targets fail closed.
func TestPatcherRejectsEmptyAndTargetless(t *testing.T) {
	p, run := materializedCheckout(t, map[string]string{"f.txt": "x\n"})
	if _, _, err := p.Apply(context.Background(), run, "   \n"); err == nil {
		t.Error("empty diff must fail closed")
	}
	if _, _, err := p.Apply(context.Background(), run, "not a diff, no headers\n"); err == nil {
		t.Error("a diff with no file headers must fail closed")
	}
}

// No checkout for the run → fail closed (park), never a silent host write.
func TestPatcherFailsClosedWithoutCheckout(t *testing.T) {
	requireGit(t)
	checkouts, err := NewCheckouts(t.TempDir(), cliexec.OSRunner{})
	if err != nil {
		t.Fatal(err)
	}
	p := NewPatcher(checkouts, cliexec.OSRunner{}, targetFilesReader([]string{"f.txt"}))
	diff := "--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-x\n+y\n"
	if _, _, err := p.Apply(context.Background(), "org.p.agent.chain.execution.no-such-run", diff); err == nil {
		t.Error("apply on a run with no materialized checkout must fail closed")
	}
}

// A diff that does not apply cleanly (stale context) is a real git-apply failure, not
// a silent success — the developer must re-author.
func TestPatcherReportsApplyConflict(t *testing.T) {
	requireGit(t)
	p, run := materializedCheckout(t, map[string]string{"f.txt": "actual\n"})
	stale := "--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-expected-something-else\n+new\n"
	if _, _, err := p.Apply(context.Background(), run, stale); err == nil {
		t.Error("a diff that does not apply cleanly must fail, not silently succeed")
	}
}
