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

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/componentregistry"
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
	// semdev's own tool executors register here as later groups add them (task
	// 7.1 measurement, floor tools) — each G1-gated and G3-scanned.
	return nil
}
