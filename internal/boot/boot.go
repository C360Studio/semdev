// Package boot centralizes component registration for semdev's binaries. Both
// cmd/semdev and cmd/e2e-semdev MUST register components through RegisterAll and
// nothing else: a component present in one binary but not the other is the
// half-wired-binary silent-flow-break class — it compiles, passes review, and
// then drops facts on the floor in the binary that skipped it. One function, two
// callers, identical registration by construction.
package boot

import (
	"fmt"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/componentregistry"
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
