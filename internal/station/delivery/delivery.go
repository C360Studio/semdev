// Package delivery is semdev's DELIVERY station (design simplify-m0-execution-rail
// R6, group 6; made REAL by forge-io-real-lanes group 4): the publish-triggered
// component that pushes the run's committed attempt branch and opens the
// evidence-bearing pull request when a run's three delivery signals cohere
// (verify.result=pass, review.verdict=approved, openspec.validated). The
// delivery rule (dev-from-task/08a) fires a plain `publish` to
// component.delivery-station.dispatch and this component delivers with ZERO
// model turns.
//
// G1: no framework primitive delivers a PR off a fact — a rule can route the
// coherence signals but cannot push a branch or call a forge API. This station
// is the SOLE driver of the delivery core (openpr.Delivery); the core is the
// single writer of pr.ref (Source open-pr, G5) and is DOUBLY idempotent (graph
// read-guard + forge query-by-head — forge-io-real-lanes D5). Fires no
// lifecycle transition (G2). An UNCONFIGURED forge fails delivery closed (the
// M0 local stub is deleted): the base's retries exhaust, station.dispatch.failed
// lands on the run, and the run-fired park rule routes it to a human.
package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.>
// dispatch namespace the delivery rule publishes to.
const ComponentName = "delivery-station"

// stationConfig is the delivery station's config shape: the generic station
// ports plus the forge target (forge-io-real-lanes D6 — token by env NAME only).
type stationConfig struct {
	station.Config
	Forge openpr.ForgeConfig `json:"forge"`
}

// deliverySchema exposes the FULL config shape (ports + forge) to config
// discovery — registering the generic station.Schema would hide the forge
// block (review finding).
var deliverySchema = component.GenerateConfigSchema(reflect.TypeOf(stationConfig{}))

// handler drives the real delivery for the coherent run named by the
// dispatch's firing entity (the delivery rule fires on the RUN).
type handler struct {
	delivery *openpr.Delivery
	logger   *slog.Logger
}

// Handle delivers the run: push the committed branch, create-or-adopt the PR,
// stamp delivery.pr.ref = the PR URL. Any fault returns the error and stamps
// NOTHING (fail-closed: never a false delivery) — the generic base retries
// (delivery is doubly idempotent), and a persistent fault exhausts the
// retries, stamps station.dispatch.failed on the run, and the run-fired park
// rule (run-lifecycle/05) parks it toward the human.
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	ref, err := h.delivery.Deliver(ctx, req.EntityID)
	if err != nil {
		return fmt.Errorf("delivery-station: deliver %s: %w", req.EntityID, err)
	}
	h.logger.Info("delivery station delivered the pull request",
		slog.String("run_entity_id", req.EntityID), slog.String("pr_ref", ref))
	return nil
}

// newProcessor builds the delivery station from the framework deps plus boot's
// SHARED run checkouts (the push reads the run's committed git objects — the
// same instance the dev-loop tools committed into; a private instance would
// never find the checkout).
func newProcessor(rawConfig json.RawMessage, deps component.Dependencies, checkouts *runspace.Checkouts) (component.Discoverable, error) {
	var cfg stationConfig
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
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "shared run checkouts required (delivery pushes the run's committed attempt branch)")
	}
	logger := deps.GetLoggerWithComponent(ComponentName)
	writer := agentictools.NewNATSOwnedFactWriter(deps.NATSClient)
	cfg.FactWriter = writer // the harness's own dispatch-outcome stamp (station-failure-parks)

	tokenEnv := cfg.Forge.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "GITHUB_TOKEN"
	}
	token := strings.TrimSpace(os.Getenv(tokenEnv))
	client := github.NewClient(token).WithLogger(logger)
	if cfg.Forge.APIBase != "" {
		client = client.WithBaseURL(cfg.Forge.APIBase)
	}
	if !cfg.Forge.Configured() {
		// Registered and started, but every delivery will FAIL CLOSED and park
		// its run — stated loud at boot so an operator sees why.
		logger.Warn("delivery station has NO forge configured (owner/repo/remote_url) — deliveries will fail closed and park (the local stub no longer exists)")
	} else if token == "" && strings.HasPrefix(cfg.Forge.RemoteURL, "https://") {
		// The symmetric misconfiguration (review finding): a configured https
		// forge with no token fails only AT DELIVERY (retries → park) — say so
		// at boot, not at the first parked run.
		logger.Warn("delivery station forge is configured but the token env is EMPTY for an https remote — every delivery will fail closed and park",
			slog.String("token_env", tokenEnv))
	}

	h := &handler{
		delivery: &openpr.Delivery{
			Reader: changefacts.NewNATSReader(deps.NATSClient),
			Writer: writer,
			API:    client,
			Roots:  checkouts,
			Runner: cliexec.OSRunner{},
			Forge:  cfg.Forge,
			Token:  token,
			Logger: logger,
		},
		logger: logger,
	}
	return station.New(ComponentName, cfg.Config, h, deps.NATSClient, logger)
}

// Register registers the delivery station, capturing boot's SHARED
// *runspace.Checkouts (the branch push reads the run's committed objects).
// Called from boot.RegisterAll with the live instance; the conformance census
// passes nil (the factory registers but fails loud if ever constructed, which
// the census never does — it only inspects the registry).
func Register(reg *component.Registry, checkouts *runspace.Checkouts) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name: ComponentName,
		Factory: func(raw json.RawMessage, deps component.Dependencies) (component.Discoverable, error) {
			return newProcessor(raw, deps, checkouts)
		},
		Schema:      deliverySchema,
		Type:        "processor",
		Protocol:    "station",
		Domain:      "forge-io",
		Description: "Delivery station (R6, real forge at M2): pushes the run's committed branch and opens the evidence-bearing PR when the delivery signals cohere.",
		Version:     "0.1.0",
	})
}
