// Command semdev is the production binary: it registers components and runs the
// arc over semstreams (issue → clean-room-verified PR). The NATS connection
// (docker compose, never embedded), service manager, and run loop land in the
// capability groups; this skeleton proves component registration is wired.
package main

import (
	"fmt"
	"os"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/version"
	"github.com/c360studio/semstreams/component"
)

func main() {
	fmt.Printf("semdev %s\n", version.Version)

	// Registration MUST go through boot.RegisterAll — the same path cmd/e2e-semdev
	// uses — so the two binaries can never drift a component apart.
	reg := component.NewRegistry()
	if err := boot.RegisterAll(reg); err != nil {
		fmt.Fprintf(os.Stderr, "semdev: register components: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("registered %d component factories\n", len(reg.ListFactories()))
	// TODO: NATS client + service manager + StartAll land with the capability groups.
}
