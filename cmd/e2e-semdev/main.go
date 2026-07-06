// Command e2e-semdev is the e2e/journey binary. It MUST register the same
// components as cmd/semdev — a component in one binary but not the other is the
// half-wired-binary silent-flow-break class. Shared registration lands in task
// 1.5; this is the buildable skeleton.
package main

import (
	"fmt"

	"github.com/c360studio/semdev/internal/version"
)

func main() {
	fmt.Printf("e2e-semdev %s\n", version.Version)
	// TODO(1.5): componentregistry.RegisterAll(registry) — MUST match cmd/semdev.
}
