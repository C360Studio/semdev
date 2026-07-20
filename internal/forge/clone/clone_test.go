package clone

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semstreams/message"
)

const runID = "org.plat.agent.chain.execution.run-1"

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// fakeReader returns a single run.issue.ref triple (or an error / nothing).
type fakeReader struct {
	ref string
	err error
}

func (f fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.ref == "" || prefix != issueRefPredicate {
		return nil, nil
	}
	return []message.Triple{{Predicate: issueRefPredicate, Object: f.ref}}, nil
}

// recordingRunner captures each invocation (env + args) and returns success WITHOUT shelling
// git — the no-argv-leak / URL-shape pins inspect what the runner was asked to run.
type recordingRunner struct{ calls []recordedCall }

type recordedCall struct {
	dir  string
	env  []string
	name string
	args []string
}

func (r *recordingRunner) Run(_ context.Context, dir, name string, args ...string) (cliexec.Result, error) {
	r.calls = append(r.calls, recordedCall{dir: dir, name: name, args: args})
	return cliexec.Result{}, nil
}

func (r *recordingRunner) RunWithEnv(_ context.Context, dir string, env []string, name string, args ...string) (cliexec.Result, error) {
	r.calls = append(r.calls, recordedCall{dir: dir, env: env, name: name, args: args})
	return cliexec.Result{}, nil
}

// seedBareRemote creates a bare remote at <remotesDir>/<owner>/<repo>.git seeded with one
// commit (existing.go) — a forge double reachable over file:// transport.
func seedBareRemote(t *testing.T, remotesDir, owner, repo string) {
	t.Helper()
	work := t.TempDir()
	runGit := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(work, "init", "-q")
	runGit(work, "config", "user.email", "seed@example.com")
	runGit(work, "config", "user.name", "seed")
	if err := os.WriteFile(filepath.Join(work, "existing.go"), []byte("package p\n\nfunc Existing() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(work, "add", "-A")
	runGit(work, "commit", "-q", "--no-gpg-sign", "-m", "seed")
	bare := filepath.Join(remotesDir, owner, repo+".git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", "--bare", "-q", work, bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}
}

// Resolve reads run.issue.ref, parses owner/repo, and clones that target from the configured
// base — the happy path over a local bare remote (no credential needed on file:// transport).
func TestResolveClonesTargetFromCoordinate(t *testing.T) {
	requireGit(t)
	remotes := t.TempDir()
	seedBareRemote(t, remotes, "acme", "widget")

	src, err := NewSource(fakeReader{ref: "acme/widget#7"}, cliexec.OSRunner{}, t.TempDir(),
		Config{BaseURL: "file://" + remotes}, nil) // empty TokenEnv → unauthenticated
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	dir, err := src.Resolve(context.Background(), runID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("resolved source is not a git clone (no .git): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "existing.go")); err != nil {
		t.Errorf("resolved source missing the target's file: %v", err)
	}
}

// Every unresolvable input fails CLOSED (an error → the run parks), never a guessed source.
func TestResolveFailsClosed(t *testing.T) {
	requireGit(t)
	cases := []struct {
		name   string
		reader fakeReader
	}{
		{"no coordinate", fakeReader{ref: ""}},
		{"unparseable ref", fakeReader{ref: "not-a-ref"}},
		{"reader fault", fakeReader{err: errors.New("graph down")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := NewSource(tc.reader, cliexec.OSRunner{}, t.TempDir(),
				Config{BaseURL: "file:///nonexistent"}, nil)
			if err != nil {
				t.Fatalf("NewSource: %v", err)
			}
			if dir, err := src.Resolve(context.Background(), runID); err == nil {
				t.Errorf("expected fail-closed error, got dir %q", dir)
			}
		})
	}
}

// A valid coordinate whose repo does not exist fails CLOSED at the clone.
func TestResolveUnknownRepoFailsClosed(t *testing.T) {
	requireGit(t)
	remotes := t.TempDir() // empty — no bare remote seeded
	src, err := NewSource(fakeReader{ref: "acme/missing#1"}, cliexec.OSRunner{}, t.TempDir(),
		Config{BaseURL: "file://" + remotes}, nil)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	if dir, err := src.Resolve(context.Background(), runID); err == nil {
		t.Errorf("expected a clone failure for a missing repo, got dir %q", dir)
	}
}

// With a token configured, the token rides the subprocess ENV via GIT_ASKPASS and appears in
// NO command argument (design D3 no-argv-leak); the URL carries only the non-secret
// x-access-token username.
func TestResolveTokenRidesEnvNotArgv(t *testing.T) {
	const secret = "s3cr3t-token-value"
	t.Setenv("SEMDEV_TEST_TOKEN", secret)
	rec := &recordingRunner{}
	src, err := NewSource(fakeReader{ref: "acme/widget#1"}, rec, t.TempDir(),
		Config{BaseURL: "https://github.com", TokenEnv: "SEMDEV_TEST_TOKEN"}, nil)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	if _, err := src.Resolve(context.Background(), runID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("want exactly 1 git call, got %d", len(rec.calls))
	}
	call := rec.calls[0]

	for _, a := range call.args {
		if strings.Contains(a, secret) {
			t.Errorf("token leaked into argv: %q", a)
		}
	}
	if !hasEnv(call.env, tokenEnvName+"="+secret) {
		t.Errorf("token not injected via env %s; env=%v", tokenEnvName, call.env)
	}
	if !hasEnvPrefix(call.env, "GIT_ASKPASS=") {
		t.Errorf("GIT_ASKPASS not set; env=%v", call.env)
	}
	if joined := strings.Join(call.args, " "); !strings.Contains(joined, "x-access-token@github.com/acme/widget.git") {
		t.Errorf("clone URL missing x-access-token username: %v", call.args)
	}
}

// The coordinate maps to <base>/<owner>/<repo>.git across realistic owner/repo shapes.
func TestResolveCoordinateToURLMapping(t *testing.T) {
	cases := []struct {
		ref        string
		wantSuffix string
	}{
		{"my-org/my-repo#3", "https://github.com/my-org/my-repo.git"},
		{"org.with.dots/repo#9", "https://github.com/org.with.dots/repo.git"},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			rec := &recordingRunner{}
			src, err := NewSource(fakeReader{ref: tc.ref}, rec, t.TempDir(),
				Config{BaseURL: "https://github.com"}, nil) // unauthenticated → no x-access-token in the URL
			if err != nil {
				t.Fatalf("NewSource: %v", err)
			}
			if _, err := src.Resolve(context.Background(), runID); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			joined := strings.Join(rec.calls[0].args, " ")
			if !strings.Contains(joined, tc.wantSuffix) {
				t.Errorf("ref %q → args %v, want URL %q", tc.ref, rec.calls[0].args, tc.wantSuffix)
			}
		})
	}
}

// The GIT_ASKPASS helper writeAskpass produces actually emits the token from the subprocess
// env — byte-for-byte, no trailing newline — even for a token with shell-hostile characters.
// This converts the credential path from "reasoned correct" to regression-guarded (review M3).
func TestAskpassEmitsTokenFromEnv(t *testing.T) {
	requireGit(t) // needs a POSIX sh to run the helper
	const token = `p@ss w/%s $x "q" 'r'`
	path, cleanup, err := writeAskpass()
	if err != nil {
		t.Fatalf("writeAskpass: %v", err)
	}
	defer cleanup()

	got, err := cliexec.OSRunner{}.RunWithEnv(context.Background(), "", []string{tokenEnvName + "=" + token}, path, "Password for 'https://x-access-token@github.com':")
	if err != nil {
		t.Fatalf("run askpass: %v", err)
	}
	if got.Stdout != token {
		t.Errorf("askpass emitted %q, want the token %q verbatim (no newline)", got.Stdout, token)
	}
}

// An empty TokenEnv stays UNAUTHENTICATED even when GITHUB_TOKEN is exported — a stray ambient
// token must not silently authenticate the clone (review M1). Proven by the URL carrying no
// x-access-token username and the env carrying no token.
func TestEmptyTokenEnvStaysUnauthenticatedDespiteAmbientGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ambient-should-not-be-used")
	rec := &recordingRunner{}
	src, err := NewSource(fakeReader{ref: "acme/widget#1"}, rec, t.TempDir(),
		Config{BaseURL: "https://github.com"}, nil) // TokenEnv empty
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	if _, err := src.Resolve(context.Background(), runID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	call := rec.calls[0]
	if strings.Contains(strings.Join(call.args, " "), "x-access-token") {
		t.Errorf("unauthenticated clone must not add the x-access-token username: %v", call.args)
	}
	for _, e := range call.env {
		if strings.HasPrefix(e, tokenEnvName+"=") {
			t.Errorf("unauthenticated clone injected a token env: %q", e)
		}
	}
}

func hasEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func hasEnvPrefix(env []string, prefix string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}
