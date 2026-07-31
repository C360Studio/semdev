// Package provisionsandbox is the shared PROVISION CORE (sandbox capability, G3 — the
// provision-and-prove-cold station, design simplify-m0-execution-rail R6). On an approved+
// projected run the provision-station component (internal/station/provision) calls Provision:
// it materializes the run's target checkout, builds the operator-DECLARED image, and proves
// the repo resolves its base dependencies and BUILDS cold in a fresh per-run container — BEFORE
// the dev loop relies on the environment. It stamps a harness-DERIVED readiness/attestation
// package (sandbox.ready + the digest-pinned image + the proven sandbox-scope tier) or, on any
// failure to prove cold, a sandbox.blocked reason a park rule routes to the human (SB5). This is
// the make-or-break both predecessors lacked: neither ever proved a project builds cold in a
// fresh environment.
//
// Provision fires no lifecycle transition (G2): the provision rule fires the station, the
// readiness-gate rule reads sandbox.ready, and a park rule reads sandbox.blocked — this package
// only measures and stamps. Its facts' single writer is sandbox-provisioner (G5). Readiness is
// harness-DERIVED (proven cold), never model-supplied (G3) — this package has no schema and no
// model-facing surface; only the provision station calls it.
package provisionsandbox

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"
)

// Source is stamped on every sandbox.* triple this package owns. It MUST equal the
// writer declared for the sandbox facts in internal/vocab (G5) — a conformance pin
// cross-checks it.
const Source = "sandbox-provisioner"

// The sandbox fact package this harness owns on the run entity.
const (
	// ReadyPredicate is the readiness gate the dev loop proceeds on: "true" only
	// when a sandbox-scope tier proved the claim on a cold-built environment.
	ReadyPredicate = "sandbox.provision.ready"
	// BlockedPredicate carries the (scrubbed) reason provisioning could not prove
	// the sandbox ready — a park rule routes it to the human/operator (SB5).
	BlockedPredicate = "sandbox.provision.blocked"
	// AttestationImagePredicate is the digest-pinned image the baseline proved
	// (provenance: exactly which image built the repo cold).
	AttestationImagePredicate = "sandbox.attestation.image"
	// AttestationTierPredicate is the sandbox-scope tier the readiness rests on.
	AttestationTierPredicate = "sandbox.attestation.tier"
)

// dockerBin is the docker CLI binary the cold proof shells (matches cleanroom's
// default). A field would let a test override it, but tests inject a fake Prover
// (no docker), so a package const keeps the surface small.
const dockerBin = "docker"

// baselineClaim is the claim the M0 baseline readiness rests on — the whole
// artifact under its own unit suite (design D7/D8). The readiness gate is honest
// only if a SANDBOX-scope tier proves THIS claim; an operator-ci/lab tier defers
// it (SB5), never gates it in-sandbox.
const baselineClaim = harness.ClaimUnit

// Sources resolves the on-disk directory holding a run's target SOURCE — the
// artifact the run develops — that Materialize copies into the run's fresh
// checkout. Narrow seam so the tool holds no path logic; nil at M0 schema-only
// registration. The M2 forge-io `--recursive` PR clone slots behind this seam.
type Sources interface {
	Resolve(ctx context.Context, runEntityID string) (string, error)
}

// Checkouts materializes the run's fresh per-run working copy from the source and
// returns its root. It is the SAME instance boot wires to measure_task/verify's
// Workspace seam, so the checkout this station stands up is the one they later run
// against. nil at M0 schema-only registration.
type Checkouts interface {
	Materialize(ctx context.Context, runEntityID, sourceDir string) (string, error)
}

// Manifests resolves the checkout's operator-declared image + run fields (the
// reproducibility contract) from what the repo COMMITTED — no harvest/inference
// (SB2). nil at M0 schema-only registration.
type Manifests interface {
	Resolve(ctx context.Context, checkoutRoot string) (harness.Manifest, error)
}

// Warmers stands up the run's WARM dev sandbox container — the one the bounded
// loop's measure_task (and, later, check_floors) Exec into across
// apply→measure→retry iterations (design SB4). provision_sandbox stands it up ONCE,
// AFTER the cold baseline proved the declared image builds the repo cold, over the
// SAME checkout apply_patch writes (so measure reads the patched bytes). It is the
// SAME instance boot wires to measure_task's Sandboxes seam, so the container this
// station stands up is the one measure later Execs through. A warm-Up fault is a
// provisioning/transport fault the run parks on (SB5). nil at M0 schema-only
// registration. The warm container is torn down by the runtime reaper, never a
// lifecycle transition (G2).
type Warmers interface {
	Provision(ctx context.Context, runEntityID, image, workDir string, cacheEnvs []string) (cleanroom.Sandbox, error)
}

// Prover builds the declared image and proves the repo builds COLD, returning the
// baseline verdict. It is coldproof.ProveBaseline behind an interface so the fact-
// stamping is unit-testable without docker (a fake returns a canned Baseline); the
// real proof runs docker-gated. Always supplied (a pure adapter needing no client).
type Prover interface {
	ProveBaseline(ctx context.Context, docker, repoRoot string, m harness.Manifest, store secrets.Store) (coldproof.Baseline, error)
}

// coldProver is the production Prover: the real cold proof (design SB4.1).
type coldProver struct{}

func (coldProver) ProveBaseline(ctx context.Context, docker, repoRoot string, m harness.Manifest, store secrets.Store) (coldproof.Baseline, error) {
	return coldproof.ProveBaseline(ctx, docker, repoRoot, m, store)
}

// DefaultProver returns the production cold-proof Prover for boot to wire.
func DefaultProver() Prover { return coldProver{} }

// ProvisionResult reports the provisioning outcome for the caller to shape (the tool's
// JSON summary / the station's log). A blocked run is a STAMPED result (sandbox.blocked),
// not an error: Ready is false and Reason carries the (scrubbed) block reason a park rule
// routes. NoOp marks the already-provisioned short-circuit (nothing stamped).
type ProvisionResult struct {
	Ready  bool
	Image  string // attested image digest when Ready
	Tier   string // proven sandbox-scope tier when Ready
	Reason string // block reason when !Ready
	NoOp   bool   // already-provisioned no-op (nothing re-materialized or stamped)
}

// ProvisionDeps bundles the harness seams the shared Provision core needs — grouped into a
// struct (rather than a long parameter list) because there are many. The provision station
// (internal/station/provision, R6) constructs one and passes it to Provision.
type ProvisionDeps struct {
	Sources   Sources
	Checkouts Checkouts
	Manifests Manifests
	Warmers   Warmers
	Prover    Prover
	Store     secrets.Store
	Reader    changefacts.Reader
	Writer    *graphown.Writer
	// DockerCheck probes the docker daemon before provisioning so an absent daemon parks the
	// HUMAN (SB5), distinct from a declared image that cannot build (parks the operator).
	// Defaulted to cleanroom.DockerAvailable when nil.
	DockerCheck func(ctx context.Context, docker string) error
	Logger      *slog.Logger
}

// Provision materializes the run's checkout, builds the operator-DECLARED image and proves
// the repo resolves+builds COLD in a fresh container, stands up the WARM dev container the
// bounded loop measures in, and stamps sandbox.ready (+attestation) or sandbox.blocked.
// The shared provision core the provision station (design simplify-m0-execution-rail R6)
// calls — the sole writer of sandbox.* (sandbox-provisioner, G5).
//
// It returns:
//   - ProvisionResult{Ready:true,...}, nil — proven cold + warm container up (readiness stamped)
//   - ProvisionResult{Ready:false, Reason}, nil — a BLOCK is stamped (a park rule routes it), NOT an error
//   - ProvisionResult{NoOp:true, Ready:true}, nil — already provisioned (no re-materialize)
//   - (_, error) — ONLY a graph-WRITE fault (the block/ready fact could not be stamped) — retryable
//
// A blocked run (docker absent, unresolvable source, unbuildable image, non-cold baseline,
// warm-Up fault) is a stamped sandbox.blocked, never an error — the readiness is
// harness-DERIVED from the cold proof, never model-supplied (G3). Fires no lifecycle
// transition (G2).
//
// CONTRACT: Sources/Checkouts/Manifests/Warmers/Reader/Writer must be non-nil — the caller
// (the provision station factory) constructs them and fails loud on a nil seam. Nil
// Prover/DockerCheck/Logger are defaulted.
func Provision(ctx context.Context, deps ProvisionDeps, runEntityID string) (ProvisionResult, error) {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Prover == nil {
		deps.Prover = DefaultProver()
	}
	dockerCheck := deps.DockerCheck
	if dockerCheck == nil {
		dockerCheck = cleanroom.DockerAvailable
	}

	// Idempotency guard (belt-and-suspenders to the provision rule's fired-once marker): a
	// run already proven ready is NOT re-materialized — a re-materialize is destructive (it
	// replaces the checkout) and would wipe an in-progress apply_patch (group-4 carry-forward).
	// The rule's marker prevents re-spawn in the normal case; this covers a replay/redelivery
	// that re-triggers with the readiness fact still present.
	if alreadyReady(ctx, deps.Reader, runEntityID) {
		deps.Logger.Info("provision: run already provisioned ready — no-op", slog.String("run_entity_id", runEntityID))
		return ProvisionResult{Ready: true, NoOp: true}, nil
	}

	// Absent docker is an infra fault the run parks the HUMAN on (SB5) — distinct from a
	// declared image that cannot build cold (which parks the OPERATOR). Probe before touching
	// the source so the reason is honest.
	if err := dockerCheck(ctx, dockerBin); err != nil {
		return block(ctx, deps, runEntityID, fmt.Sprintf("docker unavailable — the sandbox cannot be provisioned; start/install docker (park toward the human): %v", err))
	}
	sourceDir, err := deps.Sources.Resolve(ctx, runEntityID)
	if err != nil {
		return block(ctx, deps, runEntityID, fmt.Sprintf("resolve run source: %v", err))
	}
	checkoutRoot, err := deps.Checkouts.Materialize(ctx, runEntityID, sourceDir)
	if err != nil {
		return block(ctx, deps, runEntityID, fmt.Sprintf("materialize checkout: %v", err))
	}
	manifest, err := deps.Manifests.Resolve(ctx, checkoutRoot)
	if err != nil {
		return block(ctx, deps, runEntityID, fmt.Sprintf("resolve declared image/manifest: %v", err))
	}

	// The cold proof: build the declared image, prove the repo builds cold in a fresh cache. A
	// returned error is an infra/declaration fault (undeclared/unbuildable image, missing
	// required secret); a non-Ready Baseline built but did not prove cold. Both block the run —
	// the dev loop never proceeds on an unproven environment (SB5). The Baseline evidence is
	// already scrubbed of any injected secret at coldproof's boundary (G7); at M0 store is nil.
	baseline, err := deps.Prover.ProveBaseline(ctx, dockerBin, checkoutRoot, manifest, deps.Store)
	if err != nil {
		return block(ctx, deps, runEntityID, fmt.Sprintf("prove baseline cold: %v", err))
	}

	// Readiness requires BOTH: the environment built the repo cold (baseline), AND a
	// sandbox-scope tier proves the claim in-sandbox (SB5 — an operator-ci/lab-only claim is
	// deferred toward the operator, never gated in-sandbox).
	tier := sandboxTierName(manifest.Tiers, baselineClaim)
	if !baseline.Ready() || tier == "" {
		return block(ctx, deps, runEntityID, notReadyReason(baseline))
	}

	// The cold baseline passed. Stand up the WARM dev container the bounded loop's measure_task
	// Execs into (SB4 warm dev iterations): the SAME declared image, over the SAME checkout
	// apply_patch writes (bind-mounted at /work), with fresh per-run caches. Up'd ONCE here and
	// reused across apply→measure→retry (the cold baseline and cold final verify are separate
	// throwaway containers — three instances, one warm), torn down by the runtime reaper. A
	// warm-Up fault is a provisioning/transport fault (docker) → park toward the human, exactly
	// like the docker-absent path — never a silent proceed onto an unstood-up sandbox (SB5).
	if _, err := deps.Warmers.Provision(ctx, runEntityID, baseline.Image.Ref, checkoutRoot, manifest.CacheHomeEnvs); err != nil {
		return block(ctx, deps, runEntityID, fmt.Sprintf("proved the repo builds cold but could not stand up the warm dev container: %v — retryable infra fault (park toward the human)", err))
	}
	return ready(ctx, deps, runEntityID, baseline.Image.Digest, tier)
}

// alreadyReady reports whether the run already carries sandbox.ready == "true". A read error
// is treated as "not ready" (re-prove rather than skip): fail-closed biases toward proving,
// never toward assuming a green that isn't stamped.
func alreadyReady(ctx context.Context, reader changefacts.Reader, runEntityID string) bool {
	triples, err := reader.ReadFacts(ctx, runEntityID, ReadyPredicate)
	if err != nil {
		return false
	}
	for _, t := range triples {
		if t.Predicate == ReadyPredicate {
			if s, ok := t.Object.(string); ok && s == "true" {
				return true
			}
		}
	}
	return false
}

// ready stamps the readiness package (sandbox.ready + attestation), clearing any prior block.
// The image digest and tier are HARNESS-derived (from the proven baseline / the committed
// manifest), never model-supplied (G3). A graph-write failure returns an error (retryable).
func ready(ctx context.Context, deps ProvisionDeps, runEntityID, imageDigest, tier string) (ProvisionResult, error) {
	add := []message.Triple{
		provisionTriple(runEntityID, ReadyPredicate, "true"),
		provisionTriple(runEntityID, AttestationImagePredicate, imageDigest),
		provisionTriple(runEntityID, AttestationTierPredicate, tier),
	}
	if err := deps.Writer.Replace(ctx, runEntityID, add); err != nil {
		return ProvisionResult{}, fmt.Errorf("provision_sandbox: stamp readiness on %s: %w", runEntityID, err)
	}
	deps.Logger.Info("provision recorded sandbox readiness",
		slog.String("run_entity_id", runEntityID),
		slog.String("image", imageDigest),
		slog.String("tier", tier))
	return ProvisionResult{Ready: true, Image: imageDigest, Tier: tier}, nil
}

// block stamps sandbox.blocked with the reason, clearing any stale readiness so a re-prove
// that now fails cannot leave a green behind. It returns a stamped ProvisionResult (the
// harness did its job — it measured and recorded the block); the park rule routes the blocked
// run to the human/operator. Only a graph-write failure returns an error.
//
// G7 (forward): at M0 store is nil, so no secret VALUE can reach a reason — ProveBaseline
// injects secrets only at run time inside Gather and scrubs its evidence there, and its error
// returns name missing REFS, not values. When a governed store is wired, route reasons through
// a secrets.NewScrubber(secretEnv) (or pin ProveBaseline's error contract scrub-safe) before
// they land in a fact or the surfaced result.
func block(ctx context.Context, deps ProvisionDeps, runEntityID, reason string) (ProvisionResult, error) {
	add := []message.Triple{provisionTriple(runEntityID, BlockedPredicate, reason)}
	if err := deps.Writer.Replace(ctx, runEntityID, add); err != nil {
		return ProvisionResult{}, fmt.Errorf("provision_sandbox: stamp block on %s: %w", runEntityID, err)
	}
	deps.Logger.Warn("provision blocked the run (sandbox not ready)",
		slog.String("run_entity_id", runEntityID),
		slog.String("reason", reason))
	return ProvisionResult{Ready: false, Reason: reason}, nil
}

// provisionTriple builds one sandbox fact stamped with this harness's Source (G5).
func provisionTriple(runEntityID, predicate, object string) message.Triple {
	return message.Triple{
		Subject:    runEntityID,
		Predicate:  predicate,
		Object:     object,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
}

// sandboxTierName returns the name of the first SANDBOX-scope tier that proves the
// claim, or "" if none — the honest "can a sandbox tier prove this?" check the
// readiness gate rests on (mirrors harness.AssessReadiness's sandbox arm). An
// unrelated sandbox tier (one that does not prove the claim) does not count.
func sandboxTierName(tiers []harness.Tier, claim string) string {
	for _, t := range tiers {
		if t.Scope == harness.TierSandbox && slices.Contains(t.Proves, claim) {
			return t.Name
		}
	}
	return ""
}

// notReadyReason phrases why a built-but-not-ready baseline blocks, routing the
// reason to the RIGHT owner (the whole human/operator distinction the tool draws):
//   - Retry — an infra/transport fault interrupted the proof (daemon, provisioning,
//     network); retryable, and a persistent fault parks toward the HUMAN (like the
//     docker-absent path), NOT the operator — it is not a declared-image defect.
//   - Fail — the declared image genuinely could not resolve/build the repo cold →
//     park toward the OPERATOR to fix the declared image.
//   - Ready but no sandbox-scope tier — the claim is deferred toward the operator
//     (only an operator-ci/lab tier can prove it; never gated in-sandbox, SB5).
func notReadyReason(b coldproof.Baseline) string {
	switch {
	case b.Outcome == verify.OutcomeRetry:
		return fmt.Sprintf("provisioning hit an infra/transport fault proving the repo cold (outcome=retry, failed=%v) — retryable; a persistent fault parks toward the human, not the operator", b.Verdict.FailedChecks())
	case !b.Ready():
		return fmt.Sprintf("the declared image did not prove the repo builds cold (outcome=%s, failed=%v) — fix the declared image (park toward the operator)", b.Outcome, b.Verdict.FailedChecks())
	default:
		return fmt.Sprintf("no sandbox-scope tier proves the %q claim — only an operator-ci/lab tier can, so the claim is deferred toward the operator (never gated in-sandbox, SB5)", baselineClaim)
	}
}
