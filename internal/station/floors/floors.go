// Package floors is semdev's FLOORS station (design simplify-m0-execution-rail R6,
// group 6 — the make-or-break slice): the publish-triggered component that runs the
// deterministic structural floors over a developer attempt and mirrors the routing
// inputs for the rule-native floors route. It replaces the forced single-turn
// coordinator loop that called the check_floors tool — the floors-trigger rule
// (dev-from-task/05) now fires a plain `publish` to component.floors-station.dispatch
// on the DEVELOPER-loop terminal, and this component does the work with ZERO model
// turns.
//
// THE ROUTE-MIRROR COUPLING (the make-or-break): the g4+5 floors route (06a advance /
// 06b+06e not_clean / 06c retry / 06d escalate) fires on the entity carrying the route.*
// mirror. Pre-reshape that was the fresh check_floors coordinator loop (CF_n). With
// check_floors converted to a component there is NO CF_n. So this station stamps
// floor.finding on the RUN (unchanged) AND the route.* mirror on the DEVELOPER loop L_n
// — which is the dispatch's firing entity (req.EntityID), still fresh per attempt (so the
// loop-scoped route.routed self-extinguish is preserved). The route rules 06a-d now fire
// on L_n, unchanged in shape (they key on route.*, not on the loop's role); their
// run_scope=inherit still binds because L_n carries agent.run (it was inherit-spawned by
// the dispatch rule). The run_entity_id travels as a publish property (L_n is the firing
// entity, so the run is not the dispatch entity_id).
//
// G1: no framework primitive runs the floors + mirrors the route off a loop terminal; the
// forced-turn tool path burned a paid model call. The station calls the SAME
// checkfloors.RunFloors core the tool used — one writer of floor.finding (floor-tools) and
// one of the route.* mirror (route-mirror), G5 — preserving the g4+5 measurement
// snapshot-commit staleness binding. Fires no lifecycle transition (G2).
//
// SHARED CHECKOUT (the DI seam): the floors resolve reads the developer's authored files
// from the run's on-disk checkout via runspace.Attempts over runspace.Checkouts — a
// PROCESS-LOCAL map keyed by run id. This component MUST use boot's SAME *runspace.Checkouts
// instance the dev-loop tools (apply_patch/measure) use; a component that built its own
// would get a different empty map and never find the run's attempt. boot threads it in via
// Register (nil-safe for the census).
package floors

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/checkfloors"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/pkg/errs"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ComponentName is the registered factory name and the component.<name>.> dispatch
// namespace the floors-trigger rule publishes to.
const ComponentName = "floors-station"

// The publish properties the floors-trigger rule threads. The rule fires on the
// developer loop (L_n), so the run travels as a property; task_index scopes the
// floors/mirror (M0 single-task = "0", default when absent).
const (
	RunEntityProperty = "run_entity_id"
	TaskIndexProperty = "task_index"
)

// handler runs the floors over a developer attempt and mirrors the route onto L_n.
type handler struct {
	attempts checkfloors.Attempts
	reader   changefacts.Reader
	writer   agentictools.OwnedFactWriter
	logger   *slog.Logger
}

// Handle runs the deterministic floors and stamps floor.finding on the run + the route.*
// mirror on the DEVELOPER loop L_n (req.EntityID, the firing entity). A resolve/stamp
// fault returns an error (the base retries; a persistent fault stamps nothing on L_n, so
// the floors route never fires and the attempt does not advance — the general R6 no-auto-
// park gap at M0, deferred to R8/group 8).
func (h *handler) Handle(ctx context.Context, req station.Request) error {
	runEntityID := req.Prop(RunEntityProperty)
	if runEntityID == "" {
		return fmt.Errorf("floors-station: dispatch carries no %s property — cannot target the run", RunEntityProperty)
	}
	idx := parseTaskIndex(req.Prop(TaskIndexProperty))
	// req.EntityID is the developer loop L_n — the entity the route rules fire on.
	res, err := checkfloors.RunFloors(ctx, h.attempts, h.reader, h.writer, h.logger, runEntityID, req.EntityID, idx)
	if err != nil {
		return fmt.Errorf("floors-station: run floors for task %d on %s: %w", idx, runEntityID, err)
	}
	h.logger.Info("floors station evaluated the attempt",
		slog.String("run_entity_id", runEntityID), slog.String("dev_loop", req.EntityID),
		slog.Int("task_index", idx), slog.Bool("rejected", res.Rejected))
	return nil
}

// parseTaskIndex parses the task_index property, defaulting to 0 (M0 single-task) when
// absent or malformed — a wrong index would floor the wrong task, so a negative parse
// falls back to 0 rather than propagating.
func parseTaskIndex(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// newProcessor builds the floors station from the framework deps plus boot's SHARED run
// checkouts (captured by Register). It fails loud if the checkouts seam is nil — a floors
// component that cannot read the run's authored attempt must not start (never a silent
// no-op), matching the dev-loop tools' fail-closed posture.
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
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "shared run checkouts required (the floors read the authored attempt off the run's checkout)")
	}
	logger := deps.GetLoggerWithComponent(ComponentName)
	factReader := changefacts.NewNATSReader(deps.NATSClient)
	h := &handler{
		attempts: runspace.NewAttempts(factReader, checkouts),
		reader:   factReader,
		writer:   agentictools.NewNATSOwnedFactWriter(deps.NATSClient),
		logger:   logger,
	}
	return station.New(ComponentName, cfg, h, deps.NATSClient, logger)
}

// Register registers the floors station, capturing boot's SHARED *runspace.Checkouts (the
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
		Domain:      "dev-from-task",
		Protocol:    "station",
		Description: "Floors station (R6): runs the structural floors over a developer attempt and mirrors the route onto the developer loop. Replaces the forced check_floors coordinator turn.",
		Version:     "0.1.0",
	})
}
