// Command e2e-semdev is the e2e/journey binary. It brings up the SAME runtime
// as cmd/semdev by calling the one shared boot.Run — a component, tool, or
// service wired in one binary but not the other is the half-wired-binary
// silent-flow-break class.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/version"
)

// defaultConfigPath is the bootstrap config both semdev binaries boot from
// unless overridden by SEMDEV_CONFIG. Matches cmd/semdev's default; the mock-
// LLM harness + journey-specific wiring land with the e2e journey (group 11).
const defaultConfigPath = "configs/semdev-bootstrap.json"

func main() {
	fmt.Printf("e2e-semdev %s\n", version.Version)

	// Same runtime-boot path as cmd/semdev — see boot.Run.
	opts := boot.RunOptions{
		ConfigPath:  configPath(),
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := boot.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "e2e-semdev: %v\n", err)
		os.Exit(1)
	}
}

// configPath returns SEMDEV_CONFIG when set, else defaultConfigPath.
func configPath() string {
	if p := os.Getenv("SEMDEV_CONFIG"); p != "" {
		return p
	}
	return defaultConfigPath
}
