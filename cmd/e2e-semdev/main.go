// Command e2e-semdev is the e2e/journey binary. It registers the SAME components
// as cmd/semdev by calling the one shared boot.RegisterAll — a component in one
// binary but not the other is the half-wired-binary silent-flow-break class.
package main

import (
	"fmt"
	"os"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/version"
	"github.com/c360studio/semstreams/component"
)

func main() {
	fmt.Printf("e2e-semdev %s\n", version.Version)

	// Same registration path as cmd/semdev — see boot.RegisterAll.
	reg := component.NewRegistry()
	if err := boot.RegisterAll(reg); err != nil {
		fmt.Fprintf(os.Stderr, "e2e-semdev: register components: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("registered %d component factories\n", len(reg.ListFactories()))
	// TODO: mock-LLM harness + journey wiring land with the e2e journey (group 11).
}
