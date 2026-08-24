package cliexec

import (
	"fmt"
	"os"
)

// GitTokenEnv is the fixed subprocess env var the GIT_ASKPASS helper reads the token
// from — an INTERNAL name, independent of the operator's chosen token env var, so the
// child env is deterministic. The operator's token value is copied into it for the git
// subprocess only.
const GitTokenEnv = "SEMDEV_FORGE_TOKEN"

// GitCredEnv assembles the extra subprocess environment for a non-interactive,
// credential-contained git invocation (the design-D3 channel clone proved and delivery
// shares): GIT_TERMINAL_PROMPT=0 always — an auth-requiring remote fails fast instead of
// blocking on a prompt — and, when a token is configured, a throwaway GIT_ASKPASS helper
// plus GitTokenEnv carrying the token, so the credential rides the environment and NEVER
// a command argument. The returned env is for EnvRunner.RunWithEnv; cleanup removes the
// helper (call it after the git run, token or not — it is never nil).
func GitCredEnv(token string) (env []string, cleanup func(), err error) {
	env = []string{"GIT_TERMINAL_PROMPT=0"}
	if token == "" {
		return env, func() {}, nil
	}
	askpass, cleanup, err := writeGitAskpass()
	if err != nil {
		return nil, func() {}, err
	}
	return append(env, "GIT_ASKPASS="+askpass, GitTokenEnv+"="+token), cleanup, nil
}

// writeGitAskpass writes a throwaway GIT_ASKPASS helper that echoes the token from the
// subprocess env — never a file containing the token, never argv. Returns its path and a
// cleanup func.
func writeGitAskpass() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "semdev-askpass-*.sh")
	if err != nil {
		return "", func() {}, fmt.Errorf("cliexec: create askpass helper: %w", err)
	}
	name := f.Name()
	remove := func() { _ = os.Remove(name) }
	// Echo the token ENV VAR (not the value) — git calls this for the token user's
	// password prompt. The token is never written into this script.
	if _, err := f.WriteString("#!/bin/sh\nprintf '%s' \"$" + GitTokenEnv + "\"\n"); err != nil {
		_ = f.Close()
		remove()
		return "", func() {}, fmt.Errorf("cliexec: write askpass helper: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", func() {}, fmt.Errorf("cliexec: close askpass helper: %w", err)
	}
	if err := os.Chmod(name, 0o700); err != nil {
		remove()
		return "", func() {}, fmt.Errorf("cliexec: chmod askpass helper: %w", err)
	}
	return name, remove, nil
}
