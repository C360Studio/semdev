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
// source tree {relpath: content}, returning the patcher and the run id.
func materializedCheckout(t *testing.T, files map[string]string) (*Patcher, string) {
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
	return NewPatcher(checkouts, cliexec.OSRunner{}), run
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
	p := NewPatcher(checkouts, cliexec.OSRunner{})
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
