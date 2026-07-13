package coldproof

import (
	"context"
	"fmt"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"
)

// Baseline is the provision-time cold-proof result (SB2/SB4.1): built the operator's
// declared image, then proved the repo resolves its base dependencies and BUILDS cold in
// a fresh per-run container, BEFORE the dev loop relies on it. The Outcome reuses the
// clean-room taxonomy so both proofs share one fail-closed decision:
//
//   - Pass  (Ready) — the image resolved + built the repo cold; the dev loop may proceed.
//   - Fail  — a GENUINE failure: the declared image cannot resolve the repo's base deps
//     or build it cold → park toward the OPERATOR (fix/declare the image), never a guess.
//   - Retry — an infra/transport fault (daemon, provisioning, network) → re-run; a
//     persistent retry parks toward the human.
type Baseline struct {
	// Image is the built, digest-pinned image the baseline proved (and the dev loop runs).
	Image cleanroom.BuiltImage
	// Outcome is the cold-proof classification (pass/fail/retry).
	Outcome verify.Outcome
	// Verdict carries the per-check detail (completion/isolation/resolution/build) for the
	// operator when the baseline blocks the run. Its fourth check reuses verify.Decide's
	// "tests" slot, which here carries the cold BUILD result (ProveDetail).
	Verdict verify.Verdict
}

// Ready reports whether the baseline proved the image builds the repo cold — the gate
// the dev loop proceeds on (SB5: only a proven sandbox advances; anything else parks).
func (b Baseline) Ready() bool { return b.Outcome == verify.OutcomePass }

// ProveBaseline builds the operator-declared image (m.Image) and proves the repo at
// repoRoot resolves its base dependencies and builds COLD inside a fresh per-run
// container (SB4.1). It fails closed two ways, distinctly:
//
//   - a returned ERROR is an infra/declaration fault the caller PARKS on before any
//     verdict exists: an undeclared/unbuildable image, an absent docker daemon. There is
//     no Baseline to stamp.
//   - a returned Baseline with a non-Pass Outcome is a definitive proof result: the image
//     BUILT but the repo did not prove cold (Fail → park toward the operator) or an infra
//     fault interrupted the proof itself (Retry). The caller stamps it and routes.
//
// Governed creds-refs (m.SecretRefs, SB2c) are resolved from store and injected into the
// proof's container at run time; a missing required ref fails CLOSED toward the operator
// (a returned error — register the secret, no warm fallback). The image is built once +
// digest-pinned; the proof runs the manifest's own ResolveCmd then BuildCmd (never a
// model-supplied command — G3) in a fresh cache home; the returned Baseline's evidence
// is SCRUBBED of any injected secret value (G7).
func ProveBaseline(ctx context.Context, docker, repoRoot string, m harness.Manifest, store secrets.Store) (Baseline, error) {
	// An incomplete manifest is a DECLARATION fault (the operator's to fix), not infra —
	// error up front so it parks toward the operator immediately, rather than degrading
	// into an infra-class Retry inside Gather (an empty resolve/cache reads as transport).
	if len(m.ResolveCmd) == 0 || len(m.BuildCmd) == 0 || len(m.CacheHomeEnvs) == 0 {
		return Baseline{}, fmt.Errorf("coldproof: manifest for profile %q is incomplete (needs resolve, build, and cache-home fields) — declare the run fields (SB2)", m.Profile)
	}
	img, ev, err := proveCold(ctx, docker, repoRoot, m, store, m.BuildCmd)
	if err != nil {
		return Baseline{}, err
	}
	return baselineFromEvidence(img, ev), nil
}

// proveCold is the shared cold-proof prologue both proofs route through so a fabrication
// reads IDENTICALLY at the provision-time baseline and the final verify — the anti-masking
// property (SB3/SB4) is BY CONSTRUCTION, not a convention two copies must keep in sync. It
// fails closed on a missing required creds-ref BEFORE building anything (SB2c), builds the
// operator-declared image, and Gathers resolve+prove evidence in a fresh throwaway
// container with a fresh cache home; Gather scrubs any echoed secret at the boundary (G7).
// The ONLY thing that differs between the two proofs is proveCmd (the baseline's cold
// BUILD vs the verify's cold TEST) and how the caller maps the evidence.
func proveCold(ctx context.Context, docker, root string, m harness.Manifest, store secrets.Store, proveCmd []string) (cleanroom.BuiltImage, Evidence, error) {
	secretEnv, err := secrets.ResolveAll(store, m.SecretRefs)
	if err != nil {
		return cleanroom.BuiltImage{}, Evidence{}, fmt.Errorf("coldproof: resolve governed secrets: %w", err)
	}
	img, err := cleanroom.BuildImage(ctx, docker, root, m.Image)
	if err != nil {
		return cleanroom.BuiltImage{}, Evidence{}, fmt.Errorf("coldproof: build declared image: %w", err)
	}
	runner := cleanroom.NewContainerRunnerWithSecrets(img.Ref, secretEnv)
	ev := Gather(ctx, runner, root, m.CacheHomeEnvs, m.ResolveCmd, proveCmd, secrets.NewScrubber(secretEnv))
	return img, ev, nil
}

// ProveArtifact builds the operator-declared image (m.Image) from artifactRoot — a fresh
// COLD clone of the COMMITTED artifact — and proves it resolves its base dependencies and
// passes its own TESTS cold in a fresh throwaway container (SB4.3, the clean-room final
// verify). It is ProveBaseline's sibling and shares the identical BuildImage → fresh
// container → Gather core, so a cache-masked fabrication or a harness-only fixup reads the
// same here as at the provision-time baseline — the structural "identical fabrication
// reads" property. The ONLY differences from the baseline: the prove step is m.TestCmd,
// not m.BuildCmd (verify proves resolve then the artifact's own TESTS, which compile it,
// SB4.3), so the terminal check keeps its
// honest "tests" label; and it returns the verify.Verdict directly (no Baseline wrapper —
// there is no image to hand back to a warm loop). It fails closed exactly like the
// baseline: a returned ERROR is an infra/declaration fault the caller parks on before any
// verdict; a returned Verdict with a non-Pass Outcome is a definitive result (Fail =
// genuine non-reproducibility → the dev loop's fix is not self-contained; Retry = transport
// fault). Governed creds-refs (m.SecretRefs, SB2c) are resolved + injected + scrubbed as in
// the baseline; the artifact MUST prove cold with only the ambient image + governed creds,
// zero out-of-band harness fixups (SB3).
func ProveArtifact(ctx context.Context, docker, artifactRoot string, m harness.Manifest, store secrets.Store) (verify.Verdict, error) {
	// An incomplete manifest is a DECLARATION fault — error up front so it parks toward the
	// operator rather than degrading into an infra-class Retry inside Gather.
	if len(m.ResolveCmd) == 0 || len(m.TestCmd) == 0 || len(m.CacheHomeEnvs) == 0 {
		return verify.Verdict{}, fmt.Errorf("coldproof: manifest for profile %q is incomplete (needs resolve, test, and cache-home fields) — declare the run fields (SB2)", m.Profile)
	}
	_, ev, err := proveCold(ctx, docker, artifactRoot, m, store, m.TestCmd)
	if err != nil {
		return verify.Verdict{}, err
	}
	return verify.Decide(ev.ToVerifyInput()), nil
}

// verifyTestsCheckName is verify.Decide's terminal check name (its unexported
// checkTests). The baseline reuses Decide's ordered fail-closed logic but relabels this
// terminal check, because for the baseline the prove step is a cold BUILD, not tests.
const verifyTestsCheckName = "tests"

// baselineProveCheckName is what the baseline calls that terminal check, so a parked
// baseline's evidence reads "build" (SB4.1), not a misleading "tests" (G10).
const baselineProveCheckName = "build"

// baselineFromEvidence maps neutral cold-proof evidence to a Baseline verdict via the
// shared verify.Decide (pure — the classification is offline-testable without docker),
// relabelling the terminal check to "build" so the operator-facing evidence matches
// what the baseline actually proved (resolve + cold build). The evidence details are
// already scrubbed of any injected secret at the Gather boundary (G7), so no scrub is
// needed here.
func baselineFromEvidence(img cleanroom.BuiltImage, ev Evidence) Baseline {
	verdict := verify.Decide(ev.ToVerifyInput())
	verdict.Checks = relabelProveCheck(verdict.Checks)
	return Baseline{Image: img, Outcome: verdict.Outcome, Verdict: verdict}
}

// relabelProveCheck returns a copy of the verify checks with the terminal "tests" check
// renamed to "build" — the baseline's prove step. A pinned test guards this against a
// rename of verify's check (if the match no-ops, the pin fails).
func relabelProveCheck(checks []verify.CheckResult) []verify.CheckResult {
	out := make([]verify.CheckResult, len(checks))
	copy(out, checks)
	for i := range out {
		if out[i].Name == verifyTestsCheckName {
			out[i].Name = baselineProveCheckName
		}
	}
	return out
}
