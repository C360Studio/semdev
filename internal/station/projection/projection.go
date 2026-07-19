// Package projection is semdev's PROJECTION station (design simplify-m0-execution-rail
// R6, group 6): the publish-triggered component that freezes an approved change's tasks
// into the immutable task.spec. It replaces the forced single-turn coordinator loop that
// called the project_tasks tool — the projection rule (dev-from-task/03) now fires a plain
// `publish` to component.projection-station.dispatch and this component projects with ZERO
// model turns.
//
// G1: no framework primitive projects a change's tasks off a fact — a rule can route the
// approval signal but cannot invoke the projection Go, and a forced model turn to call a
// deterministic tool is a paid call that decides nothing (task.spec is the task definition,
// not a model outcome — G3). The station pattern (internal/station) is the framework-aligned
// answer. It calls the SAME projecttasks.Project core the tool used, so task.spec keeps its
// single writer (G5, Source task-projector) and the D15#0 validated-content binding +
// target_files-includes-tests contract are enforced in one place. Fires no transition (G2).
package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.>
// dispatch namespace the projection rule publishes to.
const ComponentName = "projection-station"

// SlugProperty is the publish property carrying the approved change slug (the run's
// change facts are slug-scoped, so the rule threads the slug that a rule condition
// cannot wildcard out of the predicate key).
const SlugProperty = "slug"

// handler projects the run's approved change tasks. The projection rule fires on the
// RUN, so req.EntityID is the run; the slug arrives as a publish property.
type handler struct {
	reader changefacts.Reader
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// Handle freezes the change's tasks into task.spec on the run. A refusal (schema gap,
// re-projection, unvalidated content, target_files contract) or a graph fault returns
// an error: the base retries, and a persistent refusal stamps no task.spec
// (fail-closed) — the base then stamps station.dispatch.failed on the run and the
// run-fired park rule (run-lifecycle/05) parks it toward the human, naming this
// station and the refusal (station-failure-parks; real-LLM run 1's exact shape,
// now journey-pinned). The restart half stays R8/group 8.
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	slug := req.Prop(SlugProperty)
	count, err := projecttasks.Project(ctx, h.reader, h.writer, h.logger, req.EntityID, slug)
	if err != nil {
		return fmt.Errorf("projection-station: project %q on %s: %w", slug, req.EntityID, err)
	}
	h.logger.Info("projection station froze task.spec",
		slog.String("run_entity_id", req.EntityID), slog.String("slug", slug), slog.Int("tasks", count))
	return nil
}

// NewProcessor is the component factory. The projection station is self-sufficient —
// it needs only the NATS client (the change-fact reader and owned-fact writer are
// stateless wrappers over it), no shared runspace state.
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
		writer: writer,
		logger: logger,
	}
	return station.New(ComponentName, cfg, h, deps.NATSClient, logger)
}

// Register registers the projection station with the component registry, called from
// boot.RegisterAll so both semdev binaries pick it up together.
func Register(reg *component.Registry) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name:        ComponentName,
		Factory:     NewProcessor,
		Schema:      station.Schema,
		Type:        "processor",
		Domain:      "dev-from-task",
		Protocol:    "station",
		Description: "Projection station (R6): freezes an approved change's tasks into immutable task.spec. Replaces the forced project_tasks coordinator turn.",
		Version:     "0.1.0",
	})
}
