package boot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/graphown"

	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/forge/semsource"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/launch"
)

// RunLaunch is the operator launch entry point — the durable front door beside the webhook
// (self-target-provisioning-and-launch-driver, capability operator-launch). It is a THIN CLIENT
// of an ALREADY-RUNNING semdev runtime: it connects NATS, reads the platform + experiment
// condition from the same config, and mints one run via launch.Launch (the sanctioned
// experiment.Launch seam). It does NOT bring up components/services (that is boot.Run's job and
// must already be running to consume the wake and mint the run), so it deliberately does not go
// through boot.Run. It ensures no streams — a missing front-door stream means no runtime is up,
// and PublishToStream fails closed.
//
// The caller supplies the run-specific params (issue ref, model, bind timeouts); RunLaunch fills
// Condition from the config's experiment block and builds the semsource readiness probe when
// that condition is declared. Returns the bound run entity id.
func RunLaunch(ctx context.Context, opts RunOptions, params launch.Params) (string, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	cfg, expCfg, err := loadRuntimeConfig(opts.ConfigPath, logger)
	if err != nil {
		return "", err
	}
	natsClient, err := connectRuntimeNATS(ctx, opts.NATSURLs, cfg)
	if err != nil {
		return "", err
	}
	// Close on a FRESH short context so a SIGINT-canceled ctx does not skip the final drain
	// (the last write is a synchronous request, but drain is the sturdier idiom).
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = natsClient.Close(closeCtx)
	}()

	platform := platformMeta(cfg)

	// The launch driver stamps exactly ONE owned fact (experiment.run.condition,
	// owner experiment-intake), so it binds ONLY that owner — never BindAll.
	// Registering an owner SUPERSEDES whoever holds it: a fresh incarnation replaces
	// the epoch entry with no liveness check, and a MutationClient never refreshes
	// its captured token. Binding all 17 here would leave a running `task serve`
	// permanently fenced out of its own write path (warn + meter per predicate per
	// write today; a hard reject of every owned write once enforcement flips), with
	// nothing to notice it — the runtime binds once at boot and never re-registers.
	//
	// Superseding experiment-intake specifically is harmless: the runtime never
	// writes experiment.run.condition (StampCondition's only caller is this path), so
	// no runtime write ever presents the stale token.
	graphClients, err := graphown.BindOwners(ctx, natsClient, logger, experiment.Source)
	if err != nil {
		return "", fmt.Errorf("bind projection owners: %w", err)
	}

	// The experiment condition is operator-declared in the config (G3/G5 — the same evidence
	// label the mint path stamps). The semsource condition REQUIRES a readiness probe;
	// experiment.Launch fails closed if it is declared without one.
	params.Condition = expCfg
	var probe func(context.Context) error
	if expCfg.Semsource() {
		client := semsource.NewClient(expCfg.SemsourceEndpoint)
		probe = func(ctx context.Context) error { return experiment.CheckReadiness(ctx, client) }
	}

	d := launch.Deps{
		Issues:   github.NewClient(opts.GitHubToken),
		Pub:      natsClient,
		Resolver: admission.NewRunResolver(natsClient, platform.Org, platform.Platform),
		Writer:   graphClients.Writer(experiment.Source),
		Probe:    probe,
		Logger:   logger,
	}

	// Surface the run id even on the stamp-failure tail: experiment.Launch returns a non-empty
	// id when the run was bound but the condition label write failed, so the operator can see
	// which run is unlabeled (and the ledger treats it as degraded) rather than losing it.
	runID, err := launch.Launch(ctx, d, params)
	if err != nil {
		return runID, err
	}
	logger.Info("operator launch bound the minted run",
		slog.String("ref", params.IssueRef), slog.String("run", runID), slog.String("condition", expCfg.Condition))
	return runID, nil
}

// LaunchModel resolves the coordinator model for an operator launch. The CLI flag is REQUIRED
// (the wake carries the model, which must match the running runtime's model_registry key); an
// empty flag is a loud error rather than a guessed default that could name a provider the
// runtime does not serve.
func LaunchModel(flagModel string) (string, error) {
	if flagModel != "" {
		return flagModel, nil
	}
	return "", fmt.Errorf("launch: no model specified (pass --model; the model_registry key the running runtime serves)")
}
