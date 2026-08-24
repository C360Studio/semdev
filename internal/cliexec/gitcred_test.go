package cliexec

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The GIT_ASKPASS helper writeGitAskpass produces actually emits the token from the
// subprocess env — byte-for-byte, no trailing newline — even for a token with
// shell-hostile characters. This converts the credential path from "reasoned correct" to
// regression-guarded (review M3; moved from the clone package with the helper,
// security-forge-containment 2.1).
func TestAskpassEmitsTokenFromEnv(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX sh not available")
	}
	const token = `p@ss w/%s $x "q" 'r'`
	path, cleanup, err := writeGitAskpass()
	if err != nil {
		t.Fatalf("writeGitAskpass: %v", err)
	}
	defer cleanup()

	got, err := OSRunner{}.RunWithEnv(context.Background(), "", []string{GitTokenEnv + "=" + token}, path, "Password for 'https://x-access-token@github.com':")
	if err != nil {
		t.Fatalf("run askpass: %v", err)
	}
	if got.Stdout != token {
		t.Errorf("askpass emitted %q, want the token %q verbatim (no newline)", got.Stdout, token)
	}
}

// GitCredEnv without a token is prompt-suppression only: no askpass, no token env, a
// non-nil no-op cleanup — and with a token, the assembled env never carries the raw
// token under any name but GitTokenEnv (the askpass script itself must not contain it).
func TestGitCredEnvShapes(t *testing.T) {
	env, cleanup, err := GitCredEnv("")
	if err != nil {
		t.Fatalf("GitCredEnv(\"\"): %v", err)
	}
	cleanup()
	if len(env) != 1 || env[0] != "GIT_TERMINAL_PROMPT=0" {
		t.Errorf("tokenless env = %v, want exactly [GIT_TERMINAL_PROMPT=0]", env)
	}

	const token = "sekrit-token-value"
	env, cleanup, err = GitCredEnv(token)
	if err != nil {
		t.Fatalf("GitCredEnv(token): %v", err)
	}
	defer cleanup()
	var askpass string
	for _, e := range env {
		if rest, ok := strings.CutPrefix(e, "GIT_ASKPASS="); ok {
			askpass = rest
		}
	}
	if askpass == "" {
		t.Fatalf("token env carries no GIT_ASKPASS: %v", env)
	}
	script, err := os.ReadFile(askpass)
	if err != nil {
		t.Fatalf("read askpass helper: %v", err)
	}
	if strings.Contains(string(script), token) {
		t.Error("the askpass script contains the raw token — it must only reference the env var")
	}

	// cleanup actually removes the helper from disk.
	cleanup()
	if _, err := os.Stat(askpass); !os.IsNotExist(err) {
		t.Errorf("askpass helper still on disk after cleanup: %v", err)
	}
}
