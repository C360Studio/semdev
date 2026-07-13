package runspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semstreams/message"
)

// targetFilesReader returns a fakeReader whose task.spec.0.target_files fact declares the
// given approved write contract — the shape the Patcher enforces apply scope against.
func targetFilesReader(targets []string) fakeReader {
	b, _ := json.Marshal(targets)
	return fakeReader{triples: []message.Triple{
		{Predicate: devtask.TaskSpecKeyPrefix(0) + devtask.FactTargetFiles, Object: string(b)},
	}}
}

const runID = "org.plat.agent.chain.execution.run-1"

// fixture returns the absolute path to a committed test fixture.
func fixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "test", "fixtures", name)
}

// fakeReader returns triples whose predicate starts with the requested prefix.
type fakeReader struct{ triples []message.Triple }

func (f fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	var out []message.Triple
	for _, tr := range f.triples {
		if strings.HasPrefix(tr.Predicate, prefix) {
			out = append(out, tr)
		}
	}
	return out, nil
}

func newCheckouts(t *testing.T) *Checkouts {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	c, err := NewCheckouts(t.TempDir(), cliexec.OSRunner{})
	if err != nil {
		t.Fatalf("NewCheckouts: %v", err)
	}
	return c
}

// commitWarm stages and commits everything in a git-backed checkout under the harness
// identity Materialize configured repo-local — the test stand-in for apply_patch's
// commit-per-attempt, so CloneForVerify has a committed tree to clone.
func commitWarm(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "--no-gpg-sign", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// Materialize copies the fixture into a fresh per-run checkout; Root resolves it, and
// fails closed before materialize / after Remove.
func TestCheckoutsMaterializeRootRemove(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()

	if _, err := c.Root(ctx, runID); err == nil {
		t.Fatal("Root before materialize should fail closed")
	}

	root, err := c.Materialize(ctx, runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got, err := c.Root(ctx, runID); err != nil || got != root {
		t.Fatalf("Root = (%q, %v), want the materialized root %q", got, err, root)
	}
	// The copy carries the source files (a real working copy, not an empty dir).
	if _, err := os.Stat(filepath.Join(root, "health.go")); err != nil {
		t.Errorf("materialized checkout missing health.go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".devcontainer", "devcontainer.json")); err != nil {
		t.Errorf("materialized checkout missing the declared devcontainer: %v", err)
	}
	// It is a COPY, not the source (mutations must not touch the committed fixture).
	if root == fixture(t, "go-health-class") {
		t.Error("checkout root is the source itself — must be a copy")
	}

	if err := c.Remove(runID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := c.Root(ctx, runID); err == nil {
		t.Error("Root after Remove should fail closed")
	}
}

// Re-materializing a run replaces its checkout (fresh source, never a mix).
func TestCheckoutsRematerializeReplaces(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()
	first, err := c.Materialize(ctx, runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize 1: %v", err)
	}
	second, err := c.Materialize(ctx, runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize 2: %v", err)
	}
	if first == second {
		t.Error("re-materialize returned the same dir; want a fresh checkout")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("prior checkout %s not removed on re-materialize", first)
	}
}

// A FAILED re-materialize (bad source) must leave the run's existing good checkout
// intact and still resolvable — never destroy it (the copy-before-remove contract).
func TestMaterializeFailureKeepsPriorCheckout(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()
	good, err := c.Materialize(ctx, runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize good: %v", err)
	}
	if _, err := c.Materialize(ctx, runID, filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("re-materialize from a missing source should error")
	}
	got, err := c.Root(ctx, runID)
	if err != nil || got != good {
		t.Errorf("after a failed re-materialize, Root = (%q, %v), want the intact prior checkout %q", got, err, good)
	}
	if _, err := os.Stat(filepath.Join(good, "health.go")); err != nil {
		t.Errorf("prior checkout was destroyed by a failed re-materialize: %v", err)
	}
}

// CloneForVerify makes a FRESH copy of the warm checkout for the cold verify, WITHOUT
// touching the warm checkout (the applied-diff bytes the loop measured must survive) —
// the SB4.3 third-instance clone.
func TestCloneForVerifyIsFreshAndNonDestructive(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()
	warm, err := c.Materialize(ctx, runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// Simulate apply_patch mutating AND COMMITTING the warm checkout (the committed
	// attempt). CloneForVerify clones the committed tree, so only committed bytes appear.
	patched := filepath.Join(warm, "health.go")
	if err := os.WriteFile(patched, []byte("package health\n// PATCHED\n"), 0o644); err != nil {
		t.Fatalf("mutate warm checkout: %v", err)
	}
	commitWarm(t, warm, "attempt: patch health.go")

	clone, err := c.CloneForVerify(ctx, runID)
	if err != nil {
		t.Fatalf("CloneForVerify: %v", err)
	}
	// The clone is a DISTINCT dir, not the warm checkout.
	if clone == warm {
		t.Fatal("clone is the warm checkout itself — must be a fresh copy")
	}
	// The clone carries the PATCHED bytes (it copies the committed artifact, not the source).
	got, err := os.ReadFile(filepath.Join(clone, "health.go"))
	if err != nil || string(got) != "package health\n// PATCHED\n" {
		t.Errorf("clone health.go = %q (err %v), want the patched bytes", got, err)
	}
	// The warm checkout is UNTOUCHED and still resolvable (CloneForVerify never
	// re-materializes — the applied diff survives for a retry).
	if root, err := c.Root(ctx, runID); err != nil || root != warm {
		t.Errorf("warm checkout Root = (%q, %v), want the intact warm root %q — CloneForVerify must not touch it", root, err, warm)
	}
	if _, err := os.Stat(patched); err != nil {
		t.Errorf("warm checkout's patched file was destroyed by CloneForVerify: %v", err)
	}
}

// RED-FIRST PIN (group 2, task 2.1): the mutable-tree leak. Cold verify must prove an
// IMMUTABLE snapshot — the bytes at a commit — never the mutable warm working tree. A
// file written into the warm checkout but NEVER committed (post-measure `go test`
// residue, or outright tampering) must not reach the cold-verify clone; if it does,
// verify proves bytes that exist in no commit (G4/G7 — the semspec grave). Against the
// pre-git copyTree implementation this FAILS (the uncommitted file is copied into the
// clone); once Materialize git-inits + apply_patch commits + CloneForVerify clones at the
// committed tree, it passes.
func TestCloneForVerifyExcludesUncommittedWarmTreeMutation(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()
	warm, err := c.Materialize(ctx, runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// A file in the warm tree that was NEVER committed — the leak. In the real flow this
	// is what a container-side `go test` or a tampering step leaves behind after the
	// committed attempt was measured.
	leak := filepath.Join(warm, "UNCOMMITTED_LEAK.txt")
	if err := os.WriteFile(leak, []byte("bytes that are in no commit\n"), 0o644); err != nil {
		t.Fatalf("write leak file: %v", err)
	}

	clone, err := c.CloneForVerify(ctx, runID)
	if err != nil {
		t.Fatalf("CloneForVerify: %v", err)
	}
	// The committed baseline IS present — the clone is the real artifact, not an empty dir.
	if _, err := os.Stat(filepath.Join(clone, "health.go")); err != nil {
		t.Errorf("cold-verify clone missing the committed health.go: %v", err)
	}
	// The uncommitted leak is ABSENT — verify proves the immutable commit, not the warm tree.
	if _, err := os.Stat(filepath.Join(clone, "UNCOMMITTED_LEAK.txt")); !os.IsNotExist(err) {
		t.Errorf("cold-verify clone carried an UNCOMMITTED warm-tree file (stat err=%v) — the mutable-tree leak: verify would prove bytes absent from any commit", err)
	}
}

// CloneForVerify fails CLOSED when the run has no warm checkout — nothing to prove, so
// the run parks rather than verifying over a guessed path (SB5). And a re-clone reaps the
// prior clone (no leak).
func TestCloneForVerifyFailsClosedAndReaps(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()
	if _, err := c.CloneForVerify(ctx, runID); err == nil {
		t.Fatal("CloneForVerify with no warm checkout should fail closed")
	}
	if _, err := c.Materialize(ctx, runID, fixture(t, "go-health-class")); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	first, err := c.CloneForVerify(ctx, runID)
	if err != nil {
		t.Fatalf("clone 1: %v", err)
	}
	second, err := c.CloneForVerify(ctx, runID)
	if err != nil {
		t.Fatalf("clone 2: %v", err)
	}
	if first == second {
		t.Error("re-clone returned the same dir; want a fresh clone")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("prior verify clone %s not reaped on re-clone", first)
	}
	// Remove reaps the outstanding clone too.
	if err := c.Remove(runID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Errorf("Remove did not reap the verify clone %s", second)
	}
}

// Manifests resolves the fixture's declared image + customizations run fields.
func TestManifestsResolve(t *testing.T) {
	c := newCheckouts(t)
	root, err := c.Materialize(context.Background(), runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	m, err := Manifests{}.Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if m.Profile != harness.ProfileGo {
		t.Errorf("profile = %q, want go", m.Profile)
	}
	if m.Image.Devcontainer == "" {
		t.Errorf("manifest carries no declared image: %+v", m.Image)
	}
	if !slices.Equal(m.TestCmd, []string{"go", "test", "./..."}) {
		t.Errorf("test cmd = %v, want the customizations command", m.TestCmd)
	}
	if len(m.Tiers) != 1 || m.Tiers[0].Scope != harness.TierSandbox {
		t.Errorf("tiers = %+v, want one sandbox tier", m.Tiers)
	}
}

// Manifests fails closed on an unrecognized ecosystem and on an undeclared image.
func TestManifestsResolveFailsClosed(t *testing.T) {
	noMarker := t.TempDir()
	if err := os.WriteFile(filepath.Join(noMarker, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Manifests{}).Resolve(context.Background(), noMarker); err == nil {
		t.Error("a repo with no profile marker must fail closed")
	}

	noImage := t.TempDir()
	if err := os.WriteFile(filepath.Join(noImage, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Manifests{}).Resolve(context.Background(), noImage); err == nil {
		t.Error("a repo that declares no image must fail closed (ErrNoImage)")
	}
}

// Attempts resolves the task's target files + their checkout contents into a floors.Attempt.
func TestAttemptsResolve(t *testing.T) {
	c := newCheckouts(t)
	root, err := c.Materialize(context.Background(), runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	reader := fakeReader{triples: []message.Triple{
		{Predicate: devtask.TaskSpecKeyPrefix(0) + devtask.FactTargetFiles, Object: `["health.go","health_test.go"]`},
	}}
	att, err := NewAttempts(reader, c).Resolve(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !slices.Equal(att.TargetFiles, []string{"health.go", "health_test.go"}) {
		t.Errorf("target files = %v", att.TargetFiles)
	}
	if len(att.Files) != 2 {
		t.Fatalf("resolved %d files, want 2 authored target files", len(att.Files))
	}
	for _, f := range att.Files {
		if f.Content == "" {
			t.Errorf("file %s has empty content — the checkout copy was not read", f.Path)
		}
	}
	// A freshly materialized checkout is committed pristine, so the working tree is CLEAN.
	if len(att.DirtyPaths) != 0 {
		t.Errorf("fresh checkout has dirty paths %v — Materialize's base commit should leave a clean tree", att.DirtyPaths)
	}
	_ = root
}

// RED-FIRST PIN (task 2.5): a working-tree mutation AFTER the committed attempt (the shape
// of container-side test residue or tampering during measure) is captured as DirtyPaths, so
// the clean-tree floor rejects. Without the git-status capture DirtyPaths is always empty
// and the floor never fires — floors would green a tree that diverges from what verify proves.
func TestAttemptsResolveCapturesDirtyTree(t *testing.T) {
	c := newCheckouts(t)
	root, err := c.Materialize(context.Background(), runID, fixture(t, "go-health-class"))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// Mutate a committed file WITHOUT committing — the post-measure divergence.
	if err := os.WriteFile(filepath.Join(root, "health.go"), []byte("package health\n// TAMPERED POST-COMMIT\n"), 0o644); err != nil {
		t.Fatalf("mutate checkout: %v", err)
	}
	reader := fakeReader{triples: []message.Triple{
		{Predicate: devtask.TaskSpecKeyPrefix(0) + devtask.FactTargetFiles, Object: `["health.go","health_test.go"]`},
	}}
	att, err := NewAttempts(reader, c).Resolve(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(att.DirtyPaths) == 0 {
		t.Fatal("Resolve did not capture the dirty working tree — the clean-tree floor cannot fire")
	}
	if f := floors.CleanTree(att); f.Passed {
		t.Error("the clean-tree floor must REJECT a checkout mutated after its commit")
	}
}

// A declared-but-absent target file is listed in TargetFiles but absent from Files (a
// floor sees it as not-authored), not a resolve error.
func TestAttemptsMissingTargetIsAbsentNotError(t *testing.T) {
	c := newCheckouts(t)
	if _, err := c.Materialize(context.Background(), runID, fixture(t, "go-health-class")); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	reader := fakeReader{triples: []message.Triple{
		{Predicate: devtask.TaskSpecKeyPrefix(0) + devtask.FactTargetFiles, Object: `["health.go","not_authored_yet.go"]`},
	}}
	att, err := NewAttempts(reader, c).Resolve(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(att.Files) != 1 || att.Files[0].Path != "health.go" {
		t.Errorf("Files = %+v, want only the authored health.go", att.Files)
	}
	if len(att.TargetFiles) != 2 {
		t.Errorf("TargetFiles = %v, want both declared targets", att.TargetFiles)
	}
}

// Attempts fails closed: no checkout, no target_files, and a path escape.
func TestAttemptsFailsClosed(t *testing.T) {
	c := newCheckouts(t)
	ctx := context.Background()

	// No checkout materialized.
	noTargets := fakeReader{triples: []message.Triple{
		{Predicate: devtask.TaskSpecKeyPrefix(0) + devtask.FactTargetFiles, Object: `["health.go"]`},
	}}
	if _, err := NewAttempts(noTargets, c).Resolve(ctx, runID, 0); err == nil {
		t.Error("no checkout should fail closed")
	}

	if _, err := c.Materialize(ctx, runID, fixture(t, "go-health-class")); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// No target_files projected.
	if _, err := NewAttempts(fakeReader{}, c).Resolve(ctx, runID, 0); err == nil {
		t.Error("no target_files should fail closed")
	}

	// A path that escapes the checkout.
	escape := fakeReader{triples: []message.Triple{
		{Predicate: devtask.TaskSpecKeyPrefix(0) + devtask.FactTargetFiles, Object: `["../../../etc/passwd"]`},
	}}
	if _, err := NewAttempts(escape, c).Resolve(ctx, runID, 0); err == nil {
		t.Error("a path escaping the checkout must be rejected")
	}
}
