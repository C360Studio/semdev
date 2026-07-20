// Command semdev is the production binary: it brings up the shared runtime
// (NATS, docker compose, never embedded; every registry; every configured
// service) and runs the arc over semstreams (issue → clean-room-verified PR).
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
// unless overridden by SEMDEV_CONFIG. Repo-relative — matches how the config
// itself references repo-relative rule-pack paths (see configs/semdev-bootstrap.json).
const defaultConfigPath = "configs/semdev-bootstrap.json"

func main() {
	fmt.Printf("semdev %s\n", version.Version)

	// Runtime boot MUST go through boot.Run — the same path cmd/e2e-semdev uses —
	// so the two binaries can never drift a component, tool, or service apart
	// (the half-wired-binary silent-flow-break class; see internal/boot/boot.go
	// and internal/boot/runtime.go). Neither binary wires NATS, the component/
	// tool/service registries, or the ServiceManager independently.
	// The forge-target source (self-target provisioning) is declared in the config file's
	// `source.forge` block; nil (no block) keeps the fixture default. Loaded here at the
	// composition edge, like GitHubToken, so boot stays hermetic to the file layout.
	forgeSource, err := boot.LoadForgeSourceConfig(configPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "semdev: %v\n", err)
		os.Exit(1)
	}

	opts := boot.RunOptions{
		ConfigPath:  configPath(),
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		ForgeSource: forgeSource,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := boot.Run(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "semdev: %v\n", err)
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
