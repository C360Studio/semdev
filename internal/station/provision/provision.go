// Package provision is semdev's PROVISION station (design simplify-m0-execution-rail R6,
// group 6 — the provision-and-prove-cold station, the make-or-break both predecessors
// lacked): the publish-triggered component that, on an approved+projected run, materializes
// the run's checkout, builds the operator-DECLARED image, proves the repo builds COLD in a
// fresh container, stands up the WARM dev container the loop measures in, and stamps
// sandbox.ready (+attestation) or sandbox.blocked. It replaces the forced single-turn
// coordinator loop that called the provision_sandbox tool — the provision rule (sandbox/01)
// now fires a plain `publish` to component.provision-station.dispatch ON THE RUN, and this
// component does the cold proof with ZERO model turns.
//
// WHERE IT STAMPS: the sandbox.* facts are RUN-level. The provision rule fires on the RUN
// entity (its conditions are all run facts: approved + projected + not-parked), so the run IS
// the dispatch's firing entity — req.EntityID is the run, and there is NO publish property
// (unlike floors/verify, which fire on a loop and thread the run as a property). The station
// stamps sandbox.* on req.EntityID.
//
// G1: no framework primitive materializes+cold-proves a sandbox and stamps a run fact off an
// approval; the forced-turn tool path burned a paid model call that decides nothing (readiness
// is proven cold, not asserted, G3). The station calls the SAME provisionsandbox.Provision
// core the tool used — one writer of sandbox.* (sandbox-provisioner), G5. Fires no lifecycle
// transition (G2): the readiness gate (dev-from-task/02 on sandbox.ready) and the park rule
// (sandbox/02 on sandbox.blocked) are separate rules that READ these facts.
//
// THE DI SEAM (both instances): provisioning reads/writes the run's on-disk checkout AND
// stands up the run's WARM container. runspace.Checkouts (run→dir) and runspace.Sandboxes
// (run→warm container) are PROCESS-LOCAL maps; the warm container this station Up's must be
// the SAME one the measure_task TOOL later Execs into (one run, one warm container). So this
// component MUST capture boot's SAME *runspace.Checkouts AND *runspace.Sandboxes the dev-loop
// tools use — a component that built its own would materialize into an instance measure_task
// can't see. boot threads BOTH in via Register (nil-safe for the census). It also needs the
// run's SOURCE dir (what to materialize the checkout from) — an operator/runtime choice
// (RunOptions.SandboxSourceDir), threaded through Register as a value (an empty dir makes the
// source resolve fail closed → block → park, SB5, never a guessed target).
//
// FAILURE POSTURE: a BLOCK (docker absent, unresolvable source, unbuildable image, non-cold
// baseline, warm-Up fault) is a STAMPED sandbox.blocked — the station's Handle returns nil
// (the park rule routes it), exactly the forced tool's posture (a block was a success result,
// not a loop error). Only a graph-WRITE fault (the block/ready fact could not be stamped)
// returns an error, which the generic base retries (bounded, idempotent). A persistent
// write fault exhausts the base's retries → station.dispatch.failed on the run → the
// run-fired park rule (run-lifecycle/05) parks it toward the human (station-failure-parks;
// the restart half stays R8/group 8). Never a false green.
package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/provisionsandbox"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.> dispatch
// namespace the provision rule publishes to.
const ComponentName = "provision-station"

// handler provisions the run's sandbox, proves it cold, and stamps the readiness package.
type handler struct {
	deps provisionsandbox.ProvisionDeps
}

// Handle provisions the run named by the dispatch's firing entity (the provision rule fires
// on the RUN, so req.EntityID IS the run — no property). A BLOCK is stamped and returns nil
// (the park rule routes sandbox.blocked); only a graph-WRITE fault returns an error so the
// base retries. A persistent fault exhausts the base's retries and parks the run via
// station.dispatch.failed + run-lifecycle/05 (station-failure-parks). Never a false green.
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	res, err := provisionsandbox.Provision(ctx, h.deps, req.EntityID)
	if err != nil {
		return fmt.Errorf("provision-station: provision run %s: %w", req.EntityID, err)
	}
	if res.NoOp {
		h.deps.Logger.Info("provision station: run already provisioned — no-op", slog.String("run_entity_id", req.EntityID))
		return nil
	}
	if !res.Ready {
		// A stamped block (sandbox.blocked) — the park rule routes it. Not a handler error.
		h.deps.Logger.Warn("provision station blocked the run (sandbox not ready)",
			slog.String("run_entity_id", req.EntityID), slog.String("reason", res.Reason))
		return nil
	}
	h.deps.Logger.Info("provision station provisioned + proved cold — sandbox ready",
		slog.String("run_entity_id", req.EntityID), slog.String("image", res.Image), slog.String("tier", res.Tier))
	return nil
}

// newProcessor builds the provision station from the framework deps plus boot's SHARED run
// checkouts + warm-sandbox registry (captured by Register) and the operator-configured source
// dir. It fails loud if either shared seam is nil — a provision component that cannot
// materialize into the shared checkout, or stand up into the shared warm-container registry
// measure_task reads, must not start (never a silent no-op). An EMPTY sourceDir is NOT a
// fail-loud: it makes the source resolve fail closed → block → park (SB5), matching the tool.
func newProcessor(rawConfig json.RawMessage, deps component.Dependencies, checkouts *runspace.Checkouts, sandboxes *runspace.Sandboxes, sourceDir string) (component.Discoverable, error) {
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
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "shared run checkouts required (provisioning materializes into the run's checkout the dev-loop tools read)")
	}
	if sandboxes == nil {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "shared warm-sandbox registry required (provisioning stands up the warm container measure_task Execs into)")
	}
	logger := deps.GetLoggerWithComponent(ComponentName)
	factReader := changefacts.NewNATSReader(deps.NATSClient)
	writer := agentictools.NewNATSOwnedFactWriter(deps.NATSClient)
	cfg.FactWriter = writer // the harness's own dispatch-outcome stamp (station-failure-parks)
	h := &handler{
		deps: provisionsandbox.ProvisionDeps{
			Sources:     runspace.StaticSource{Dir: sourceDir},
			Checkouts:   checkouts, // *runspace.Checkouts implements Materialize (provision Checkouts)
			Manifests:   runspace.Manifests{},
			Warmers:     sandboxes, // *runspace.Sandboxes implements Provision (provision Warmers)
			Prover:      provisionsandbox.DefaultProver(),
			Store:       nil, // M0: no governed secrets (SB2c)
			Reader:      factReader,
			Writer:      writer,
			DockerCheck: cleanroom.DockerAvailable,
			Logger:      logger,
		},
	}
	return station.New(ComponentName, cfg, h, deps.NATSClient, logger)
}

// Register registers the provision station, capturing boot's SHARED *runspace.Checkouts and
// *runspace.Sandboxes (the same instances the dev-loop tools use — see the package doc) plus
// the operator-configured run source dir. Called from boot.RegisterAll with the live
// instances; the conformance census passes nil/"" (the factory registers but fails loud if
// ever constructed, which the census never does — it only inspects the registry).
func Register(reg *component.Registry, checkouts *runspace.Checkouts, sandboxes *runspace.Sandboxes, sourceDir string) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name: ComponentName,
		Factory: func(raw json.RawMessage, deps component.Dependencies) (component.Discoverable, error) {
			return newProcessor(raw, deps, checkouts, sandboxes, sourceDir)
		},
		Schema:      station.Schema,
		Type:        "processor",
		Domain:      "sandbox",
		Protocol:    "station",
		Description: "Provision station (R6): materializes the checkout, cold-proves the declared image, stands up the warm container, stamps sandbox.ready/blocked. Replaces the forced provision_sandbox coordinator turn.",
		Version:     "0.1.0",
	})
}
