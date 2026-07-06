// Command semdev is the production binary: it registers components and runs the
// arc over semstreams (issue → clean-room-verified PR). Component registration,
// the NATS connection (docker compose, never embedded), and the run loop land in
// task 1.5 and the capability groups; this is the buildable skeleton.
package main

import (
	"fmt"

	"github.com/c360studio/semdev/internal/version"
)

func main() {
	fmt.Printf("semdev %s\n", version.Version)
	// TODO(1.5): componentregistry.RegisterAll(registry) — MUST match
	// cmd/e2e-semdev exactly (the half-wired-binary silent-flow-break class).
}
