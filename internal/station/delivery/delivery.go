// Package delivery is semdev's DELIVERY station (design simplify-m0-execution-rail
// R6, group 6): the publish-triggered component that records pr.ref when a run's
// three delivery signals cohere (verify.result=pass, review.verdict.0=approved,
// openspec.validated). It replaces the forced single-turn coordinator loop that
// used to call the open_pr tool — the delivery rule (dev-from-task/08a) now fires a
// plain `publish` to component.delivery-station.dispatch and this component stamps
// pr.ref with ZERO model turns.
//
// G1: no framework primitive records a delivery reference off a fact — a rule can
// route the coherence signals but cannot invoke the delivery Go, and a forced
// model turn to call a deterministic tool is a paid call that decides nothing (the
// ref is harness-formed, G3). The station pattern (internal/station) is the
// framework-aligned answer. This station is the SOLE caller of the delivery core
// (openpr.Deliver, the open_pr tool having been deleted at 6E); the core is the
// single writer of pr.ref (Source open-pr, G5) and is idempotent — it reads the
// run's existing pr.ref before creating, so a re-fired dispatch returns the same
// ref and never re-opens (R8). Fires no lifecycle transition (G2).
package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.>
// dispatch namespace the delivery rule publishes to.
const ComponentName = "delivery-station"

// handler stamps pr.ref for the coherent run named by the dispatch's firing
// entity (the delivery rule fires on the RUN, so req.EntityID is the run). It
// carries the reader the delivery core uses to short-circuit a replay (R8
// idempotency — read the run's existing pr.ref before creating one).
type handler struct {
	reader changefacts.Reader
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// Handle records the delivery reference on the run. On a write fault it returns
// the error and stamps NOTHING (fail-closed: never a false delivery) — the generic
// base retries it (pr.ref is idempotent, latest-wins), and a persistent fault is
// metered. At M0 the run does not auto-park on that persistent fault (the delivery
// rule's marker is already set and rules are edge-triggered); the
// routed-without-result reconciliation is R8/group 8. pr.ref is a run-level fact,
// so req.EntityID (the firing run) is the whole target.
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	ref, err := openpr.Deliver(ctx, h.reader, h.writer, req.EntityID)
	if err != nil {
		return fmt.Errorf("delivery-station: stamp %s on %s: %w", openpr.RefPredicate, req.EntityID, err)
	}
	h.logger.Info("delivery station recorded the delivery reference",
		slog.String("run_entity_id", req.EntityID), slog.String("pr_ref", ref))
	return nil
}

// NewProcessor is the component factory registered with the component registry.
// It builds the owned-fact writer and the station Component from the injected
// framework dependencies (the delivery station is self-sufficient — it needs only
// the NATS client, no shared runspace state).
func NewProcessor(rawConfig json.RawMessage, deps component.Dependencies) (component.Discoverable, error) {
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
	logger := deps.GetLoggerWithComponent(ComponentName)
	h := &handler{
		reader: changefacts.NewNATSReader(deps.NATSClient),
		writer: agentictools.NewNATSOwnedFactWriter(deps.NATSClient),
		logger: logger,
	}
	return station.New(ComponentName, cfg, h, deps.NATSClient, logger)
}

// Register registers the delivery station with the component registry, called
// from boot.RegisterAll so both semdev binaries pick it up together.
func Register(reg *component.Registry) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name:        ComponentName,
		Factory:     NewProcessor,
		Schema:      station.Schema,
		Type:        "processor",
		Protocol:    "station",
		Domain:      "forge-io",
		Description: "Delivery station (R6): records pr.ref when a run's delivery signals cohere. Replaces the forced open_pr coordinator turn.",
		Version:     "0.1.0",
	})
}
