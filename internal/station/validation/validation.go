// Package validation is semdev's VALIDATION station (design simplify-m0-execution-rail
// R6, group 6): the publish-triggered component that runs the OpenSpec CLI oracle over a
// run's authored change and stamps openspec.validated from the real exit code. It replaces
// the forced single-turn coordinator loop that called the validate_change tool — the
// validate rule (coordinator/03) now fires a plain `publish` to
// component.validation-station.dispatch and this component validates with ZERO model turns.
//
// G1: no framework primitive shells the OpenSpec validator off a fact and stamps the
// harness-measured verdict — a rule can chain off the authored marker but cannot invoke the
// CLI, and a forced model turn to call a deterministic oracle is a paid call that decides
// nothing (the verdict is the CLI's exit code, never model-supplied — G3). The station
// pattern (internal/station) is the framework-aligned answer. It calls the SAME
// validatechange.Validate core the tool used, so openspec.validated keeps its single writer
// (G5, Source openspec-validate-harness) and the D15#0 content-revision binding is enforced
// in one place. Fires no lifecycle transition (G2) — a gate rule reads openspec.validated.
//
// WHY TWO PROPERTIES: the validate rule fires on the AUTHORING LOOP entity (that is where
// create_change stamps openspec.change.authored), not the run — so the publish carries both
// the run_entity_id (the loop's inherited agent.run) and the slug as properties, since the
// firing entity is the loop, not the run.
package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.>
// dispatch namespace the validate rule publishes to.
const ComponentName = "validation-station"

// The publish properties the validate rule threads (the rule fires on the authoring
// loop, so both the run and the slug travel as properties, not the firing entity).
const (
	RunEntityProperty = "run_entity_id"
	SlugProperty      = "slug"
)

// handler runs the OpenSpec oracle over the run's change and stamps/clears
// openspec.validated.
type handler struct {
	reader changefacts.Reader
	runner cliexec.Runner
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// Handle validates the run's authored change. A wiring/authoring/transport fault
// returns an error (the base retries — the oracle-unrunnable case is transient); a CLI
// REJECTION is not an error (the marker is cleared, the change needs re-authoring — the
// gate simply does not advance). run_entity_id + slug arrive as publish properties.
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	runEntityID := req.Prop(RunEntityProperty)
	slug := req.Prop(SlugProperty)
	if runEntityID == "" {
		return fmt.Errorf("validation-station: dispatch carries no %s property — cannot target the run", RunEntityProperty)
	}
	out, err := validatechange.Validate(ctx, h.reader, h.runner, h.writer, runEntityID, slug)
	if err != nil {
		return fmt.Errorf("validation-station: validate %q on %s: %w", slug, runEntityID, err)
	}
	h.logger.Info("validation station ran the OpenSpec oracle",
		slog.String("run_entity_id", runEntityID), slog.String("slug", slug), slog.Bool("validated", out.Validated))
	return nil
}

// NewProcessor is the component factory. The validation station is self-sufficient — the
// change-fact reader and owned-fact writer are stateless wrappers over the NATS client, and
// the OpenSpec CLI runs via a plain os/exec runner (no shared runspace state).
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
	writer := agentictools.NewNATSOwnedFactWriter(deps.NATSClient)
	cfg.FactWriter = writer // the harness's own dispatch-outcome stamp (station-failure-parks)
	h := &handler{
		reader: changefacts.NewNATSReader(deps.NATSClient),
		runner: cliexec.OSRunner{},
		writer: writer,
		logger: logger,
	}
	return station.New(ComponentName, cfg, h, deps.NATSClient, logger)
}

// Register registers the validation station with the component registry, called from
// boot.RegisterAll so both semdev binaries pick it up together.
func Register(reg *component.Registry) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name:        ComponentName,
		Factory:     NewProcessor,
		Schema:      station.Schema,
		Type:        "processor",
		Domain:      "openspec-io",
		Protocol:    "station",
		Description: "Validation station (R6): runs the OpenSpec CLI oracle over a run's authored change and stamps openspec.validated. Replaces the forced validate_change coordinator turn.",
		Version:     "0.1.0",
	})
}
