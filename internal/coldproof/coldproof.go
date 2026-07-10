// Package coldproof is the shared cold-build core (design SB4): it provisions ONE
// fresh-isolation sandbox (a fresh cache home per proof — the universal G4 control) and
// runs an artifact's own resolve step then a prove step, folding the results into
// neutral, harness-measured (G3) Evidence. Two consumers map that evidence:
//
//   - the clean-room verify (verify_artifact) — the prove step is the artifact's TESTS,
//     mapped to verify.Input → verify.Decide → the terminal verify.result.
//   - the provision-time BASELINE (ProveBaseline, SB2/SB4.1) — the prove step is a cold
//     BUILD, proving the operator-declared image resolves the repo's base deps and
//     builds it cold BEFORE the dev loop relies on it.
//
// Drawing the transport-vs-genuine line ONCE, here, is the point: a fabrication (a
// dependency that only a warm cache masks) must read identically whether it surfaces in
// the baseline or the final verify. Gather sets Completed=false for ANY transport/infra
// fault (a sandbox that would not provision, a step that could not run, a resolve
// failure the classifier reads as network-class) so the consumer treats it as Retry,
// never a terminal reject of a good artifact; a GENUINE resolve failure is Resolved=false.
package coldproof

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/verify"
)

// stepTimeout bounds one cold step (resolve or prove).
const stepTimeout = 15 * time.Minute

// Evidence is the neutral, harness-measured (G3) result of a cold resolve+prove run in
// one fresh-isolation sandbox. It is deliberately neutral about what the prove step IS
// (tests vs build) so the two consumers apply their own semantics; the transport line is
// already drawn.
type Evidence struct {
	// Completed is true once the run reached a definitive artifact conclusion (the
	// resolve genuinely failed, or the prove step ran). False = a transport/infra fault
	// (Transport set) — the consumer reads it as Retry, never a verdict.
	Completed bool
	// Transport carries the reason the run could not complete (empty when Completed).
	Transport string
	// FreshCacheHomes are the per-proof cache homes Up minted (the G4 isolation proof).
	FreshCacheHomes []string
	// CacheDetail is a human summary of the fresh cache homes.
	CacheDetail string
	// Resolved is the base-dependency resolution proof: the artifact's OWN declarations
	// resolved cold. A fabricated/missing coordinate a warm cache would mask fails here.
	Resolved      bool
	ResolveDetail string
	// ProvePassed is the prove step's outcome: it ran and exited 0 (tests passed / built
	// cold). ProveDetail carries the excerpt.
	ProvePassed bool
	ProveDetail string
}

// Gather provisions a fresh cold sandbox and runs resolveCmd then proveCmd inside it,
// returning neutral Evidence. cacheEnvs are the cache-home envs Up freshens per proof
// (the G4 control). A provisioning fault, a step that could not run, or a
// transport-class resolve failure all set Completed=false; a genuine resolve failure is
// Resolved=false and short-circuits before the prove step (running it would be moot).
func Gather(ctx context.Context, runner cleanroom.Runner, root string, cacheEnvs, resolveCmd, proveCmd []string) Evidence {
	sb, err := runner.Up(ctx, root, cacheEnvs)
	if err != nil {
		return Evidence{Completed: false, Transport: "could not provision cold isolation: " + err.Error()}
	}
	defer func() { _ = runner.Down(ctx, sb) }()

	ev := Evidence{
		FreshCacheHomes: sb.CacheHomes,
		CacheDetail:     fmt.Sprintf("%d fresh cache home(s): %s", len(sb.CacheHomes), strings.Join(sb.CacheHomes, ", ")),
	}

	// Resolve step. A run error is transport; a non-zero exit is classified
	// transport-vs-genuine (the one line verify.Decide cannot draw).
	resolveRes, err := execStep(ctx, runner, sb, resolveCmd)
	if err != nil {
		ev.Completed = false
		ev.Transport = "resolve step could not run: " + err.Error()
		return ev
	}
	switch class, detail := cleanroom.ClassifyResolve(resolveRes); class {
	case cleanroom.ResolveTransport:
		ev.Completed = false
		ev.Transport = detail
		return ev
	case cleanroom.ResolveFailed:
		ev.Completed = true
		ev.Resolved = false
		ev.ResolveDetail = detail + excerpt(resolveRes)
		return ev
	default: // ResolveOK
		ev.Resolved = true
		ev.ResolveDetail = detail
	}

	// Prove step (tests or cold build), in the same fresh isolation.
	proveRes, err := execStep(ctx, runner, sb, proveCmd)
	if err != nil {
		ev.Completed = false
		ev.Transport = "prove step could not run: " + err.Error()
		return ev
	}
	ev.Completed = true
	ev.ProvePassed = proveRes.ExitCode == 0
	ev.ProveDetail = excerptOr(proveRes, "the prove step ran in fresh isolation")
	return ev
}

// ToVerifyInput maps neutral Evidence onto the clean-room verify.Input, where the prove
// step is the artifact's tests. The verify gate and the baseline both route through
// verify.Decide, so they share one fail-closed decision.
func (e Evidence) ToVerifyInput() verify.Input {
	return verify.Input{
		Completed:       e.Completed,
		TransportError:  e.Transport,
		FreshCacheHome:  len(e.FreshCacheHomes) > 0,
		CacheHomeDetail: e.CacheDetail,
		Resolved:        e.Resolved,
		ResolveDetail:   e.ResolveDetail,
		TestsPassed:     e.ProvePassed,
		TestsDetail:     e.ProveDetail,
	}
}

// execStep runs one manifest step under a per-step timeout. An empty command is a
// harness misconfiguration (transport-class: the step could not run).
func execStep(ctx context.Context, runner cleanroom.Runner, sb cleanroom.Sandbox, argv []string) (cleanroom.Result, error) {
	if len(argv) == 0 {
		return cleanroom.Result{}, fmt.Errorf("manifest declares an empty command")
	}
	stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	return runner.Exec(stepCtx, sb, argv)
}

// excerpt returns a short trailing excerpt of a step's output for the detail.
func excerpt(r cleanroom.Result) string {
	out := strings.TrimSpace(r.Stderr)
	if out == "" {
		out = strings.TrimSpace(r.Stdout)
	}
	if out == "" {
		return ""
	}
	const maxLen = 400
	if len(out) > maxLen {
		out = strings.ToValidUTF8(out[len(out)-maxLen:], "")
	}
	return ": " + out
}

func excerptOr(r cleanroom.Result, fallback string) string {
	if e := excerpt(r); e != "" {
		return strings.TrimPrefix(e, ": ")
	}
	return fallback
}
