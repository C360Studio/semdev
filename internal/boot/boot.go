// Package boot centralizes component registration for semdev's binaries. Both
// cmd/semdev and cmd/e2e-semdev MUST register components through RegisterAll and
// nothing else: a component present in one binary but not the other is the
// half-wired-binary silent-flow-break class — it compiles, passes review, and
// then drops facts on the floor in the binary that skipped it. One function, two
// callers, identical registration by construction.
package boot

import (
	"context"
	"fmt"

	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/componentregistry"
	"github.com/c360studio/semstreams/pkg/lifecycle"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/processor/agentic-tools/executors"
)

// RegisterAll registers every component semdev's binaries run into reg. It wraps
// semstreams' framework registration (componentregistry.Register); semdev's own
// components — the G1-gated tools and processors — register here as the
// capability groups land, so both binaries pick them up together.
func RegisterAll(reg *component.Registry) error {
	if err := componentregistry.Register(reg); err != nil {
		return fmt.Errorf("register framework components: %w", err)
	}
	// semdev's own components register here (task 2.1 onward). Keep every
	// addition inside this function so both binaries stay in lockstep.
	return nil
}

// RegisterTools registers every agentic tool executor semdev exposes into reg:
// the framework builtins plus semdev's own G1-gated tools (none at M0). The G3
// schema census builds the tool registry through this same seam, so a semdev
// tool cannot land uncovered by the outcome-field pin — the census and
// production registration cannot drift.
func RegisterTools(ctx context.Context, reg *agentictools.ExecutorRegistry, deps executors.ToolDependencies) error {
	if err := executors.RegisterBuiltins(ctx, reg, deps); err != nil {
		return fmt.Errorf("register builtin tools: %w", err)
	}

	// A fact-writing tool holds a TriplePublisher built from the NATS client. When
	// there is no client (the schema-scanning censuses), the publisher is nil and
	// the tool registers schema-only; its Execute fails loudly if ever called
	// without one, so a fact is never silently dropped.
	var publisher agentictools.TriplePublisher
	if deps.NATSClient != nil {
		publisher = agentictools.NewNATSTriplePublisher(deps.NATSClient)
	}
	if err := reg.RegisterTool(createchange.ToolName, createchange.New(publisher, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", createchange.ToolName, err)
	}
	// More semdev tools register here as later groups add them (measurement,
	// floors, verify) — each G1-gated (registry.Entries) and G3-scanned.
	return nil
}

// RegisterLifecycle registers semdev's run-entity workflow into the lifecycle
// Manager: the framework's agent-run workflow is the run entity (design D2).
// Rules own every transition on it (G2) — product Go registers the workflow
// declaration here but fires no transition. The binaries call this when they
// wire the runtime (with a live Manager); the run-lifecycle rule pack drives the
// phase transitions.
func RegisterLifecycle(mgr *lifecycle.Manager) error {
	if err := agentrun.Register(mgr); err != nil {
		return fmt.Errorf("register agent-run workflow: %w", err)
	}
	return nil
}
