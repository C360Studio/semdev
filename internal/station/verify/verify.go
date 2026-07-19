// Package verify is semdev's VERIFY station (design simplify-m0-execution-rail R6,
// group 6 — the clean-room-verify gate, G4): the publish-triggered component that
// proves a run's COMMITTED artifact COLD — in a fresh throwaway container over a fresh
// clone with a fresh dependency cache — and stamps the terminal verify.result. It
// replaces the forced single-turn coordinator loop that called the verify_artifact tool
// — the review-approved rule (dev-from-task/07a) now fires a plain `publish` to
// component.verify-station.dispatch on QUINN's review loop, and this component does the
// cold proof with ZERO model turns.
//
// WHERE IT STAMPS: verify.result is a RUN-level fact (the delivery route 08a/08b reads it
// off the run). The review-approved rule fires on Quinn's review loop Q_n, so the run is
// NOT the dispatch's firing entity — it travels as the run_entity_id publish property, and
// this station stamps verify.result on THAT run. (Unlike the floors station there is no
// route mirror: verify does not repeat per-attempt in a loop, so no loop-scoped mirror is
// needed — the delivery route fires on the run's verify.result directly, R1.)
//
// G1: no framework primitive clones+cold-proves an artifact and stamps a run fact off a
// review terminal; the forced-turn tool path burned a paid model call that decides nothing
// (the verdict is harness-derived, G3). The station calls the SAME verifyartifact.RunVerify
// core the tool used — one writer of verify.result (verify-harness), G5. Fires no lifecycle
// transition (G2).
//
// SHARED CHECKOUT (the DI seam): the cold verify clones the run's committed artifact via
// runspace.Checkouts.CloneForVerify — a PROCESS-LOCAL map keyed by run id. This component
// MUST use boot's SAME *runspace.Checkouts instance the dev-loop tools (apply_patch/measure/
// provision) use; a component that built its own would get a different empty map and never
// find the run's checkout. boot threads it in via Register (nil-safe for the census).
//
// RETRY POSTURE: a RETRY verdict (a transient infra fault the proof classifies, never a
// terminal reject of a good artifact, SB5) is stamped as evidence but matches NEITHER
// delivery route — so the Handle returns an error to trigger the base's bounded idempotent
// retry (re-run the cold proof), preserving the forced verify loop's transient resilience.
// A persistent retry (or any persistent fault) exhausts the base's budget → the base stamps
// station.dispatch.failed on the dispatched REVIEW LOOP (07a fires there, per the
// dispatch-entity census) → the loop-fired park rule (run-lifecycle/06) parks the bound run
// toward the human (station-failure-parks; restart half stays R8/group 8). Never a false green.
package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/verifyartifact"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.> dispatch
// namespace the review-approved rule publishes to.
const ComponentName = "verify-station"

// RunEntityProperty is the publish property the review-approved rule threads. The rule
// fires on Quinn's review loop Q_n, so the run (where verify.result lands) travels as a
// property rather than the dispatch's firing entity.
const RunEntityProperty = "run_entity_id"

// handler runs the cold clean-room verify over a run's committed artifact and stamps
// verify.result on the run.
type handler struct {
	clones    verifyartifact.VerifyClones
	manifests verifyartifact.Manifests
	prover    verifyartifact.Prover
	store     secrets.Store // governed creds-refs (SB2c); nil at M0 (no secrets)
	writer    agentictools.OwnedFactWriter
	logger    *slog.Logger
}

// Handle clones the run's committed artifact, proves it COLD, and stamps verify.result on
// the run named by the run_entity_id property. A pre-proof infra fault (clone/resolve/
// prove-could-not-run) stamps nothing and returns an error (the base retries; a persistent
// fault exhausts the base's budget and parks the bound run via station.dispatch.failed on
// the review loop + run-lifecycle/06 — station-failure-parks). A RETRY verdict is stamped
// (evidence) but also returns an error so the base re-runs the cold proof (a transient flake
// must never park a good artifact, SB5; a "retry" matches neither delivery route). A
// terminal pass/fail verdict is stamped and Handle returns nil — the delivery route reads
// verify.result off the run.
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	runEntityID := req.Prop(RunEntityProperty)
	if runEntityID == "" {
		return fmt.Errorf("verify-station: dispatch carries no %s property — cannot target the run", RunEntityProperty)
	}
	res, err := verifyartifact.RunVerify(ctx, h.clones, h.manifests, h.prover, h.store, h.writer, h.logger, runEntityID)
	if err != nil {
		return fmt.Errorf("verify-station: cold clean-room verify for %s: %w", runEntityID, err)
	}
	if res.Verdict.Outcome == verify.OutcomeRetry {
		// A transient infra fault (never a terminal reject of a good artifact). verify.result=
		// retry is stamped as evidence, but it matches NEITHER delivery route (08a pass / 08b
		// fail) — so return an error to trigger the base's bounded idempotent retry (re-run the
		// cold proof), preserving the forced verify loop's transient resilience. A persistent
		// retry exhausts the base budget → station.dispatch.failed on the review loop → the
		// loop-fired park (run-lifecycle/06) routes the run to the human — never a false green.
		return fmt.Errorf("verify-station: cold proof returned retry (transient infra fault) for %s — retrying", runEntityID)
	}
	h.logger.Info("verify station recorded clean-room verdict",
		slog.String("run_entity_id", runEntityID),
		slog.String("outcome", string(res.Verdict.Outcome)),
		slog.String("profile", res.Profile))
	return nil
}

// newProcessor builds the verify station from the framework deps plus boot's SHARED run
// checkouts (captured by Register). It fails loud if the checkouts seam is nil — a verify
// component that cannot clone the run's committed artifact must not start (never a silent
// no-op), matching the dev-loop tools' fail-closed posture. The manifest resolver and cold
// prover are pure adapters (no client); store is nil at M0 (no governed secrets).
func newProcessor(rawConfig json.RawMessage, deps component.Dependencies, checkouts *runspace.Checkouts) (component.Discoverable, error) {
	var cfg station.Config
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return nil, errs.WrapInvalid(err, ComponentName, "NewProcessor", "config unmarshal")
		}
	}
	if cfg.Ports == nil {
		cfg.Ports = station.DefaultPorts(ComponentName)
	}
	if deps.NATSClient == nil {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "NATSClient required")
	}
	if checkouts == nil {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "shared run checkouts required (the cold verify clones the run's committed artifact off the run's checkout)")
	}
	logger := deps.GetLoggerWithComponent(ComponentName)
	writer := agentictools.NewNATSOwnedFactWriter(deps.NATSClient)
	cfg.FactWriter = writer // the harness's own dispatch-outcome stamp (station-failure-parks)
	h := &handler{
		clones:    checkouts, // *runspace.Checkouts implements CloneForVerify (VerifyClones)
		manifests: runspace.Manifests{},
		prover:    verifyartifact.DefaultProver(),
		store:     nil,
		writer:    writer,
		logger:    logger,
	}
	return station.New(ComponentName, cfg, h, deps.NATSClient, logger)
}

// Register registers the verify station, capturing boot's SHARED *runspace.Checkouts (the
// same instance the dev-loop tools use — see the package doc). Called from boot.RegisterAll
// with the live instance; the conformance census passes nil (the factory registers but
// fails loud if ever constructed, which the census never does — it only inspects the
// registry).
func Register(reg *component.Registry, checkouts *runspace.Checkouts) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name: ComponentName,
		Factory: func(raw json.RawMessage, deps component.Dependencies) (component.Discoverable, error) {
			return newProcessor(raw, deps, checkouts)
		},
		Schema:      station.Schema,
		Type:        "processor",
		Domain:      "clean-room-verify",
		Protocol:    "station",
		Description: "Verify station (R6): cold clean-room proof of the committed artifact, stamps verify.result. Replaces the forced verify_artifact coordinator turn.",
		Version:     "0.1.0",
	})
}
