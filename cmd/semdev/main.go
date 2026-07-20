// Command semdev is the production binary. With no subcommand it brings up the shared runtime
// (NATS, docker compose, never embedded; every registry; every configured service) and runs the
// arc over semstreams (issue → clean-room-verified PR). With `launch`, it is a thin operator
// front door that mints ONE run against a named issue through the sanctioned experiment.Launch
// seam (self-target-provisioning-and-launch-driver) — a client of an already-running runtime.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/launch"
	"github.com/c360studio/semdev/internal/version"
)

// defaultConfigPath is the bootstrap config both semdev binaries boot from
// unless overridden by SEMDEV_CONFIG. Repo-relative — matches how the config
// itself references repo-relative rule-pack paths (see configs/semdev-bootstrap.json).
const defaultConfigPath = "configs/semdev-bootstrap.json"

func main() {
	fmt.Printf("semdev %s\n", version.Version)

	// `semdev launch <owner/repo#N>` is the operator front door — a thin client that publishes
	// a coordinator wake to an ALREADY-RUNNING runtime (it does not boot one). All runtime
	// bring-up still goes through the single shared boot.Run below (the half-wired-binary guard).
	if len(os.Args) > 1 && os.Args[1] == "launch" {
		if err := runLaunch(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "semdev launch: %v\n", err)
			os.Exit(1)
		}
		return
	}

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

// runLaunch parses the launch subcommand's flags and mints one run against the named issue via
// boot.RunLaunch (the operator front door). The condition rides the config's experiment block;
// GITHUB_TOKEN authenticates the issue read.
func runLaunch(args []string) error {
	// Extract the positional issue ref BEFORE flag parsing: Go's flag package stops at the first
	// non-flag token, so `launch <ref> --model X` would otherwise drop every flag after the ref.
	// Pull the first non-dash token out and parse the rest, so ref position does not matter.
	var issueRef string
	var flagArgs []string
	for _, a := range args {
		if issueRef == "" && a != "" && a[0] != '-' {
			issueRef = a
			continue
		}
		flagArgs = append(flagArgs, a)
	}

	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	ref := fs.String("ref", "", "issue coordinate to launch — owner/repo#number (also accepted as a positional arg)")
	model := fs.String("model", "", "coordinator model — the running runtime's model_registry key")
	bindTimeout := fs.Duration("bind-timeout", 60*time.Second, "how long to wait for the coordinator to mint the run")
	force := fs.Bool("force", false, "mint even when a run already carries this issue ref (default: refuse the duplicate)")
	if err := fs.Parse(flagArgs); err != nil {
		if err == flag.ErrHelp {
			return nil // -h/--help printed usage; a clean exit
		}
		return err
	}
	if issueRef == "" {
		issueRef = *ref
	}
	if issueRef == "" {
		return fmt.Errorf("usage: semdev launch <owner/repo#number> --model KEY [--bind-timeout D] [--force]")
	}
	m, err := boot.LaunchModel(*model)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	opts := boot.RunOptions{ConfigPath: configPath(), GitHubToken: os.Getenv("GITHUB_TOKEN")}
	runID, err := boot.RunLaunch(ctx, opts, launch.Params{
		IssueRef:    issueRef,
		Model:       m,
		BindTimeout: *bindTimeout,
		Force:       *force,
	})
	if err != nil {
		return err
	}
	fmt.Printf("launched run %s for %s\n", runID, issueRef)
	return nil
}

// configPath returns SEMDEV_CONFIG when set, else defaultConfigPath.
func configPath() string {
	if p := os.Getenv("SEMDEV_CONFIG"); p != "" {
		return p
	}
	return defaultConfigPath
}
