// Package cleanroom is semdev's clean-room isolation seam (G4, design D5): the thin
// Runner the verify harness uses to prove a delivered artifact cold, plus the
// evidence classifier that draws the one line verify.Decide cannot — transport
// fault vs genuine artifact failure.
//
// semstreams ships only an HTTP client to an external sandbox and a git-diff
// tripwire (ADR-067, detection not containment); pkg/sandbox is proposal-only. So
// semdev owns a thin Runner seam (Up/Exec/Down, mirroring semteams' sandboxmanager)
// with swappable implementations — a MockRunner for tests and a LocalRunner for M0.
// The seam is deliberately DISTINCT from internal/cliexec (the trusted CLI-oracle
// exec for `openspec validate`): this one PROVES an untrusted artifact in fresh
// isolation, a different trust and isolation contract.
//
// The one universal G4 control is cache-home freshness (D5): every ecosystem has a
// package cache that can mask a broken build, so Up MINTS a fresh cache home per
// proof (orthogonal to the container choice). M0's LocalRunner realizes isolation
// as cache-home freshness on the host; container/network isolation is an M2 Runner
// implementation swapped in behind this same seam.
package cleanroom

import "context"

// Sandbox is a provisioned clean-room environment. WorkDir is the artifact
// workspace the resolve/build/test commands run in; Env is the environment they run
// under (the host environment plus the fresh cache-home overrides); CacheHomes are
// the fresh per-proof cache-home directories Up minted (the universal G4 control),
// exposed so the harness can assert isolation and Down can tear them down.
type Sandbox struct {
	WorkDir    string
	Env        map[string]string
	CacheHomes []string
}

// Result is one command invocation's captured outcome inside the sandbox. ExitCode
// is the real process exit status (harness-measured — G3, never model-supplied); a
// non-zero exit is DATA (a verdict), not a run error.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner provisions clean-room isolation and runs commands in it. Implementations:
// MockRunner (tests), LocalRunner (M0 cache-home isolation), and — behind the same
// seam at M2 — a container/CLI runner.
//
// The contract that makes the verify gate correct: Exec returns a NIL error whenever
// the command RAN to completion (any exit code lands in Result.ExitCode); a non-nil
// error means the command could NOT be run at all — a transport/infrastructure fault
// the caller must read as "did not complete" (verify Completed=false → Retry), never
// as an artifact verdict. Up's error is likewise a provisioning transport fault.
type Runner interface {
	// Up provisions a fresh sandbox rooted at workDir and mints one fresh cache-home
	// directory per name in cacheHomeEnvs (e.g. GOMODCACHE, GOCACHE), binding each in
	// the returned Sandbox.Env. A non-nil error is a provisioning transport fault.
	Up(ctx context.Context, workDir string, cacheHomeEnvs []string) (Sandbox, error)
	// Exec runs argv in the sandbox. A completed process returns its Result and a nil
	// error; a non-nil error means the command could not be run (transport fault).
	Exec(ctx context.Context, sb Sandbox, argv []string) (Result, error)
	// Down tears the sandbox down (removes the minted cache homes). Best-effort.
	Down(ctx context.Context, sb Sandbox) error
}
