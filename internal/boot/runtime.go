// This file holds the shared runtime-boot path both semdev binaries drive:
// NewRuntime wires config → NATS → JetStream streams → every registry
// (component, payload, tool, lifecycle) → service dependencies → every
// enabled, constructor-registered service in the config's `services` map —
// but starts nothing. Start/Stop drive the wired Runtime explicitly; Run does
// wire+start+block-on-context+stop in one call and is what cmd/semdev and
// cmd/e2e-semdev actually call.
//
// The framework's own processors (graph-ingest, graph-query, rule,
// agentic-tools, ...) are NOT constructed here, or anywhere in Go: they are
// declared in the config's `components` map and instantiated at runtime by
// the component-manager SERVICE once it starts (see service.ComponentManager
// in the framework). This file's job stops at handing the component-manager
// service everything it needs — the ComponentRegistry (factories),
// ToolRegistry, PayloadRegistry, LifecycleManager, and the raw config it
// reads via config.Manager — via service.Dependencies. Splitting the wiring
// this way (rather than inlining it in main) is what makes it testable
// without a live binary: runtime_integration_test.go drives a Runtime exactly
// as the binaries do.
//
// Mirrors semteams' cmd/semteams/main.go wiring order and helper-extraction
// pattern (setupRegistriesAndManager, createServiceDependencies,
// configureAndCreateServices/createServiceIfEnabled) — semdev's version is
// thinner because M0 has no personas, flow templates, chain-pause, or
// ownership substrate yet; those land with their own capability groups.

package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/standards"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/config"
	"github.com/c360studio/semstreams/metric"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/payloadbuiltins"
	"github.com/c360studio/semstreams/payloadregistry"
	"github.com/c360studio/semstreams/persona"
	"github.com/c360studio/semstreams/pkg/lifecycle"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/processor/agentic-tools/executors"
	"github.com/c360studio/semstreams/service"
	"github.com/c360studio/semstreams/types"

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semdev/internal/forge/clone"
	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/station/provision"
	"github.com/c360studio/semdev/internal/vocab"
)

// defaultNATSURL is the fallback NATS URL when nothing else configures one —
// the bare docker-compose NATS both binaries expect at dev time (never
// embedded; see docker/compose/nats.yml).
const defaultNATSURL = "nats://localhost:24222"

// natsURLsEnvVar overrides both RunOptions.NATSURLs and cfg.NATS.URLs — the
// framework's own env convention for pointing an otherwise-unmodified config
// at a different NATS cluster (matches semteams' createNATSClient).
const natsURLsEnvVar = "SEMSTREAMS_NATS_URLS"

// natsConnectTimeout bounds how long NewRuntime waits for the initial NATS
// handshake to settle before giving up. NATS is a hard boot requirement —
// semdev cannot operate without it — so this is a startup gate, not a
// steady-state timeout.
const natsConnectTimeout = 10 * time.Second

// runtimeShutdownTimeout bounds Run's graceful-shutdown window. Matches
// semteams' -shutdown-timeout default (30s); M0 has no CLI flag surface yet,
// so it is a constant until one lands.
const runtimeShutdownTimeout = 30 * time.Second

// stopServicesGrace is the slack Stop adds on top of the caller's timeout before
// it abandons a wedged svcMgr.StopAll and proceeds to close the remaining
// resources. It exists because of a known semstreams shutdown deadlock: on a cold
// boot the framework's ComponentManager can leak a cm.mu reader (its
// performDetailedHealthCheck spawns a goroutine that RLocks and, on a 50ms
// timeout, is abandoned without the matching RUnlock), after which
// stopAllComponents' own RLock never returns — so StopAll blocks BEFORE it ever
// reaches the stage its internal timeout guards. A healthy StopAll returns within
// `timeout`; this grace only ever elapses when StopAll is genuinely wedged, at
// which point Stop logs and moves on rather than hanging the process forever.
// Filed upstream (see Stop) — remove this bound once the framework fix lands.
const stopServicesGrace = 3 * time.Second

// errStopServicesWedged marks the Stop path where svcMgr.StopAll blew its bounded
// window — the signature of the known upstream ComponentManager deadlock
// (C360Studio/semstreams#508). Stop wraps it with %w so a caller or test can
// errors.Is it and tell the KNOWN wedge apart from a novel shutdown fault (a
// configMgr/NATS error, or a different hang), instead of blanket-tolerating every
// Stop error. Remove alongside the bound once #508 lands.
var errStopServicesWedged = errors.New("stop services: bounded shutdown window exceeded (known semstreams ComponentManager deadlock, C360Studio/semstreams#508)")

// RunOptions configures the shared runtime-boot path (NewRuntime and Run). It
// is the seam through which cmd/semdev and cmd/e2e-semdev pass their only
// permitted point of divergence — which config file to boot from and which
// GitHub token to inject — while otherwise wiring an identical runtime.
type RunOptions struct {
	// ConfigPath is the bootstrap config JSON file. Required.
	ConfigPath string
	// NATSURLs overrides the NATS connection URL(s) (comma-separated for a
	// cluster). Empty falls through to the SEMSTREAMS_NATS_URLS env var, then
	// cfg.NATS.URLs, then defaultNATSURL — see resolveNATSURLs.
	NATSURLs string
	// GitHubToken is injected into RegisterTools so the forge-io tools
	// (github_list_comments, ...) register live instead of schema-only. Read
	// from the environment at the composition edge (cmd/*'s main), never
	// here — keeps this package hermetic to its caller's choice.
	GitHubToken string
	// SandboxSourceDir is the run's target SOURCE in FIXTURE mode — the operator-configured
	// directory provision_sandbox materializes each run's checkout from and cold-proves (the
	// in-repo Go fixture the journey drives). Empty makes the provision source resolve fail
	// closed, so a run parks toward the operator rather than provisioning a guessed target
	// (SB5). Mutually exclusive with ForgeSource (design D5).
	SandboxSourceDir string
	// ForgeSource selects the FORGE-TARGET source mode: the run's source is CLONED from the
	// real repository its run.issue.ref coordinate names (self-target provisioning). Set it
	// (nil disables) to develop a real target instead of the fixture; exactly one of
	// SandboxSourceDir / ForgeSource may be configured — both is a loud boot error (design D5).
	ForgeSource *ForgeSourceConfig
	// PersonasDir is the root of the role-fragment tree (<root>/<role>/*.md)
	// seeded into the PERSONAS KV bucket at boot. Empty derives it from the
	// config file's own directory (<configDir>/personas/fragments), which is
	// correct for both binaries (config + personas ship together in configs/).
	// The group-11 journey copies only the config JSON to a temp dir, so it sets
	// this explicitly to the repo's real fragment tree — otherwise the coordinator
	// would run on the framework's default persona instead of Sarah's contract.
	PersonasDir string
	// Logger receives every log line the runtime boot emits. Nil defaults to
	// slog.Default().
	Logger *slog.Logger
}

// ForgeSourceConfig is the forge-target source mode (design D5): the git host base the run's
// target lives under, and the env var its token is read from. Mirrors clone.Config; carried on
// RunOptions and (for the operator binary) loaded from the config file's `source.forge` block.
type ForgeSourceConfig struct {
	// BaseURL is the git host base: "https://github.com" live, or "file:///…/remotes" for the
	// offline bare-remote journey. The clone target is <BaseURL>/<owner>/<repo>.git.
	BaseURL string `json:"base_url"`
	// TokenEnv names the env var holding the forge token (default GITHUB_TOKEN); unused for
	// file:// / public repos.
	TokenEnv string `json:"token_env"`
}

// sourceSpec maps RunOptions to the provision station's source selection, FAILING CLOSED on an
// ambiguous config (design D5): a forge source and a fixture dir set together is a loud boot
// error, and a forge source without a base URL is rejected. Neither set → fixture mode with an
// empty dir, which resolves fail-closed at runtime (SB5), the pre-change default.
func sourceSpec(opts RunOptions) (provision.SourceSpec, error) {
	if opts.ForgeSource != nil {
		if opts.SandboxSourceDir != "" {
			return provision.SourceSpec{}, fmt.Errorf("boot: both a fixture source dir and a forge source are configured — set exactly one (design D5)")
		}
		if strings.TrimSpace(opts.ForgeSource.BaseURL) == "" {
			return provision.SourceSpec{}, fmt.Errorf("boot: forge source is configured without a base URL")
		}
		return provision.SourceSpec{Forge: &clone.Config{
			BaseURL:  opts.ForgeSource.BaseURL,
			TokenEnv: opts.ForgeSource.TokenEnv,
		}}, nil
	}
	return provision.SourceSpec{FixtureDir: opts.SandboxSourceDir}, nil
}

// LoadForgeSourceConfig reads the `source.forge` block from the bootstrap config file (a second
// read of the same file, the experiment.LoadConfig pattern — the framework loader ignores
// unknown top-level keys). Returns nil when no forge source is declared (fixture default). A
// malformed file is rejected loudly. The operator binary calls this to populate
// RunOptions.ForgeSource; the e2e journeys set the field directly.
func LoadForgeSourceConfig(path string) (*ForgeSourceConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("boot: read config %s: %w", path, err)
	}
	var wrapper struct {
		Source struct {
			Forge *ForgeSourceConfig `json:"forge"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("boot: parse source config %s: %w", path, err)
	}
	return wrapper.Source.Forge, nil
}

// Runtime is the live, wired semstreams runtime: config loaded, NATS
// connected, every registry built and populated, every configured service
// constructed — everything short of actually starting. Both semdev binaries
// build one through NewRuntime and drive it identically via Start/Stop (or
// Run, which does both around a context).
type Runtime struct {
	cfg          *config.Config
	nats         *natsclient.Client
	configMgr    *config.Manager
	svcMgr       *service.Manager
	toolRegistry *agentictools.ExecutorRegistry
	// sandboxes is the run-scoped warm dev-container registry; Stop reaps its
	// containers so a shutdown leaks no docker resource. nil is a valid zero (no
	// live tools wired), so Stop nil-guards it.
	sandboxes *runspace.Sandboxes
	// graphWriters is the process's contract-validated graph write surface (one
	// shared mutation client; ADR-091 — contracts validate local intent, nothing
	// registers). Exposed via GraphWriters so an in-process caller (the e2e
	// stand-ins) resolves writers through the SAME contract table production uses.
	graphWriters *graphown.Clients
	logger       *slog.Logger
}

// GraphWriters returns the runtime's contract-validated write surface. Callers
// that need to stamp a fact in-process draw their writer from here so the
// contract resolution (D3b) governs their write exactly as it does production's.
func (r *Runtime) GraphWriters() *graphown.Clients { return r.graphWriters }

// closeBootNATS closes the boot-owned NATS connection on a FRESH bounded
// context, so a SIGINT-canceled boot ctx does not skip the final drain (the
// launch path's idiom, adopted on every NewRuntime error tail).
func closeBootNATS(nc *natsclient.Client) {
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = nc.Close(closeCtx)
}

// resolveNATSURLs implements the documented precedence: an explicit
// RunOptions override beats the environment variable, which beats the config
// file's nats.urls, which beats the hardcoded localhost default.
func resolveNATSURLs(optsURLs string, cfg *config.Config) string {
	if optsURLs != "" {
		return optsURLs
	}
	if envURLs := os.Getenv(natsURLsEnvVar); envURLs != "" {
		return envURLs
	}
	if len(cfg.NATS.URLs) > 0 {
		return strings.Join(cfg.NATS.URLs, ",")
	}
	return defaultNATSURL
}

// platformMeta extracts platform identity from config, preferring the
// federation InstanceID (multi-instance deployments) over the bare platform
// ID — mirrors semteams' extractPlatformMeta. component.PlatformMeta is a
// type alias for types.PlatformMeta (see semstreams/component/dependencies.go),
// so this single value is handed to both service.Dependencies.Platform and
// executors.ToolDependencies.Platform without conversion.
func platformMeta(cfg *config.Config) types.PlatformMeta {
	id := cfg.Platform.InstanceID
	if id == "" {
		id = cfg.Platform.ID
	}
	return types.PlatformMeta{Org: cfg.Platform.Org, Platform: id}
}

// ruleProcessorFactory is the framework factory name of the rule-processor
// component (the component `name`, not its instance key) whose rules_files this
// loader resolves to absolute paths.
const ruleProcessorFactory = "rule-processor"

// loadRuntimeConfig loads and validates the bootstrap config, then rewrites the
// rule pack's file paths to absolute (resolveRulePackPaths) so the runtime is
// CWD-independent. Validation happens before any NATS connection so a malformed
// config fails fast instead of surfacing as a confusing downstream error.
func loadRuntimeConfig(path string, logger *slog.Logger) (*config.Config, experiment.Config, error) {
	cfg, err := config.NewLoader().LoadFile(path)
	if err != nil {
		return nil, experiment.Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, experiment.Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	// semdev's own `experiment` section rides the SAME config file (the
	// framework loader ignores unknown top-level keys); an invalid section
	// fails the boot loudly here, never at first tool call.
	expCfg, err := experiment.LoadConfig(path)
	if err != nil {
		return nil, experiment.Config{}, err
	}
	// The loaded condition is logged UNCONDITIONALLY so a typo'd section that
	// silently decoded to the baseline default is diagnosable from boot output
	// (an unknown condition VALUE already fails loudly above).
	logger.Info("experiment condition loaded", "condition", expCfg.Condition, "semsource_endpoint", expCfg.SemsourceEndpoint)
	// The semsource condition selects the variant dispatch pack (D2) by
	// FILE substitution — before path resolution, so the swap operates on the
	// authored relative names and the loaded rules are exactly the files on
	// disk (never a load-time transformation, D2b).
	if err := applyExperimentVariantPack(cfg, expCfg, filepath.Dir(path), logger); err != nil {
		return nil, experiment.Config{}, err
	}
	if err := resolveRulePackPaths(cfg, filepath.Dir(path)); err != nil {
		return nil, experiment.Config{}, fmt.Errorf("resolve rule pack paths in %s: %w", path, err)
	}
	return cfg, expCfg, nil
}

// variantSuffix is the semsource-condition variant pack's filename convention:
// a baseline rule `X.json` with a sibling `X-semsource.json` on disk is
// SUBSTITUTED (never appended — appending would load both siblings and
// double-fire the developer spawn; the mutual-exclusion pin guards the same
// invariant offline) when boot declares the semsource condition. The variant
// files carry `_semsource`-suffixed rule ids and are byte-identical to their
// baselines except the appended semsource tools (the parity pin).
const variantSuffix = "-semsource.json"

// applyExperimentVariantPack substitutes variant rule files into the rule
// component's rules_files when the semsource condition is declared. Fails
// LOUDLY if the condition is declared but no variant file exists (a missing
// pack must not silently run the baseline under a semsource label — the
// half-labeled-evidence class D4 exists to kill).
func applyExperimentVariantPack(cfg *config.Config, exp experiment.Config, configDir string, logger *slog.Logger) error {
	swapped := 0
	for key, comp := range cfg.Components {
		if comp.Name != ruleProcessorFactory || len(comp.Config) == 0 {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(comp.Config, &raw); err != nil {
			return fmt.Errorf("decode %q config for the variant pack: %w", key, err)
		}
		files, ok := raw["rules_files"].([]any)
		if !ok || len(files) == 0 {
			continue
		}
		// A HAND-LISTED variant entry is a misconfiguration in EVERY condition:
		// substitution is the only sanctioned load path. Listed alongside its
		// baseline it double-loads two distinct rule ids that both spawn the
		// developer on the same decide event (the framework loader collapses
		// exact-id duplicates only) — the double-dispatch class the offline
		// mutual-exclusion pin guards on the SHIPPED config; this guards the
		// config actually handed to boot.
		for _, f := range files {
			if s, _ := f.(string); strings.HasSuffix(s, variantSuffix) {
				return fmt.Errorf("rules_files lists variant rule %q directly — variants load ONLY by boot substitution under the semsource condition; list the baseline instead", s)
			}
		}
		if !exp.Semsource() {
			continue
		}
		for i, f := range files {
			s, _ := f.(string)
			if s == "" {
				continue
			}
			candidate := strings.TrimSuffix(s, ".json") + variantSuffix
			// Entries may be authored relative to the config dir (the shipped
			// bootstrap) or already absolute (a test harness that pre-resolved
			// them); stat the candidate the same way the loader will read it.
			probe := candidate
			if !filepath.IsAbs(probe) {
				probe = filepath.Join(configDir, candidate)
			}
			if _, err := os.Stat(probe); err != nil {
				continue
			}
			files[i] = candidate
			swapped++
			// Each swap is logged so a PARTIAL variant pack (a variant file
			// missing from a deployed tree — the offline pins only see the
			// repo) is diagnosable from boot output: a mixed arm would show
			// fewer swaps than the pack ships.
			logger.Info("experiment variant rule substituted", "rule", candidate)
		}
		raw["rules_files"] = files
		data, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("re-encode %q config after the variant swap: %w", key, err)
		}
		comp.Config = data
		cfg.Components[key] = comp
	}
	if !exp.Semsource() {
		return nil
	}
	logger.Info("experiment variant pack applied", "condition", exp.Condition, "swapped", swapped)
	if swapped == 0 {
		return fmt.Errorf("experiment: condition %q declared but no %s variant rule file was found next to any bootstrapped rule — the variant pack is missing (a silent baseline run under a semsource label is the exact half-labeled-evidence class the condition gate exists to kill)", experiment.ConditionSemsource, variantSuffix)
	}
	return nil
}

// resolveRulePackPaths rewrites the rule component's rules_files to ABSOLUTE
// paths, resolved relative to the config file's own directory. It exists because
// the framework's rule loader reads each rules_files entry with a bare os.ReadFile
// — i.e. relative to the process CWD — and a read miss is SWALLOWED at component
// construction (the run-lifecycle pack silently vanishes and, since rules own every
// transition, every run then stalls with no failure surfaced). A repo-relative path
// only resolves when the binary happens to run from the repo root; resolving here
// makes the rule engine load the same pack no matter where the process (or a test)
// is launched from. rules_files are authored relative to the CONFIG file, so a
// config that moves carries its pack with it.
func resolveRulePackPaths(cfg *config.Config, configDir string) error {
	for key, comp := range cfg.Components {
		if comp.Name != ruleProcessorFactory || len(comp.Config) == 0 {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(comp.Config, &raw); err != nil {
			return fmt.Errorf("decode %q config: %w", key, err)
		}
		files, ok := raw["rules_files"].([]any)
		if !ok || len(files) == 0 {
			continue
		}
		resolved := make([]any, len(files))
		for i, f := range files {
			s, _ := f.(string)
			if s != "" && !filepath.IsAbs(s) {
				abs, err := filepath.Abs(filepath.Join(configDir, s))
				if err != nil {
					return fmt.Errorf("resolve rules_files[%d] %q: %w", i, s, err)
				}
				s = abs
			}
			resolved[i] = s
		}
		raw["rules_files"] = resolved
		rewritten, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("re-encode %q config: %w", key, err)
		}
		comp.Config = rewritten
		cfg.Components[key] = comp
	}
	return nil
}

// personasDir resolves the persona fragment root: an explicit RunOptions
// override, else the convention path beside the config file
// (<configDir>/personas/fragments). Deriving from the config dir keeps the
// two binaries CWD-independent for the same reason resolveRulePackPaths does
// for rules — config and personas ship together under configs/.
func personasDir(opts RunOptions) string {
	if opts.PersonasDir != "" {
		return opts.PersonasDir
	}
	return filepath.Join(filepath.Dir(opts.ConfigPath), "personas", "fragments")
}

// RequiredCoordinatorFragments are the coordinator persona fragment IDs (role
// dir + filename stem, per persona.LoadFromDirectory's ID scheme) the front door
// cannot route without: 00-identity establishes Sarah, 10-decision-contract
// carries the closed decide taxonomy the whole arc routes on. seedPersonas
// requires them present after loading, and the integration pin asserts the same
// set — one invariant, two consumers, so they cannot drift.
var RequiredCoordinatorFragments = []string{
	"coordinator/00-identity",
	"coordinator/10-decision-contract",
}

// seedPersonas upserts every role fragment under dir into the PERSONAS KV
// bucket so the agentic-loop assembles semdev's own coordinator/developer/
// reviewer prompts (Sarah/Amelia/Quinn) instead of the framework defaults.
//
// It FAILS CLOSED — but on the CONTENT that matters, not merely a dir stat.
// semstreams' LoadFromDirectory is tolerant by design: a missing dir, a wrong
// dir with no <role>/*.md, or an unreadable fragment all warn-and-return-nil,
// loading zero fragments while boot proceeds green. That is exactly the silent
// arc-misroute the constitution bars (the coordinator persona carries the closed
// decision-taxonomy contract).
//
// So after loading we bind the check to THIS boot's load: for each load-bearing
// coordinator fragment we read the file this call intended to seed
// (<dir>/<id>.md — a mis-set path or removed file fails HERE) and require the
// PERSONAS bucket to hold exactly that content. Verifying against the on-disk
// source, not just "some value is present," is what makes a later boot with a
// bad path fail rather than silently keep routing on a prior boot's stale
// fragments (the bucket persists across boots; a bare re-Get would pass on them).
func seedPersonas(ctx context.Context, client *natsclient.Client, dir string, logger *slog.Logger) error {
	mgr, err := persona.NewManager(client)
	if err != nil {
		return fmt.Errorf("open persona manager: %w", err)
	}
	if err := persona.LoadFromDirectory(ctx, dir, mgr, logger); err != nil {
		return fmt.Errorf("seed personas from %s: %w", dir, err)
	}
	for _, id := range RequiredCoordinatorFragments {
		// The fragment file this boot intends to seed. LoadFromDirectory's ID
		// scheme is <role>/<stem>, mapping to <dir>/<role>/<stem>.md.
		want, err := os.ReadFile(filepath.Join(dir, id+".md"))
		if err != nil {
			return fmt.Errorf("read required coordinator persona fragment %q under %s: %w", id, dir, err)
		}
		p, err := mgr.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("required coordinator persona fragment %q not seeded from %s: %w", id, dir, err)
		}
		if p == nil || p.Content != string(want) {
			return fmt.Errorf("required coordinator persona fragment %q in the PERSONAS bucket does not match %s (this boot's seed did not take)", id, dir)
		}
	}
	return nil
}

// connectRuntimeNATS resolves the NATS URL(s), connects, and blocks (up to
// natsConnectTimeout) until the connection is confirmed live. NATS is never
// embedded (project rule) and is a hard boot requirement — there is no
// degraded-mode path that runs without it.
func connectRuntimeNATS(ctx context.Context, optsURLs string, cfg *config.Config) (*natsclient.Client, error) {
	urls := resolveNATSURLs(optsURLs, cfg)

	natsClient, err := natsclient.NewClient(urls)
	if err != nil {
		return nil, fmt.Errorf("create NATS client: %w", err)
	}
	if err := natsClient.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, natsConnectTimeout)
	defer cancel()
	if err := natsClient.WaitForConnection(waitCtx); err != nil {
		return nil, fmt.Errorf("wait for NATS connection: %w", err)
	}
	return natsClient, nil
}

// runtimeRegistries bundles every registry NewRuntime builds before service
// construction — component, payload, and tool — plus the lifecycle Manager.
// A single struct return keeps buildRuntimeRegistries' signature small and
// gives wireServices one value to thread into service.Dependencies instead
// of four.
type runtimeRegistries struct {
	componentReg *component.Registry
	payloadReg   *payloadregistry.Registry
	toolReg      *agentictools.ExecutorRegistry
	lifecycleMgr *lifecycle.Manager
	// sandboxes is the run-scoped WARM dev-container registry provision_sandbox writes
	// to and measure_task reads from (the same instance, one warm container per run).
	// NewRuntime holds it so Stop can reap the containers (a SIGINT must not leak a
	// docker container per run). Created only on the live path (a real NATS client);
	// nil for the schema-scanning censuses.
	sandboxes *runspace.Sandboxes
	// graphOwners is the process's contract-validated write surface (ADR-091),
	// threaded to every component and tool that stamps a fact.
	graphOwners *graphown.Clients
}

// productShellSkippedBuiltins names the framework builtins semdev deliberately does
// not register — see the SkipBuiltins comment in buildRuntimeRegistries for why
// write_todos cannot be wired from a product shell. The boot gate that makes the
// skip load-bearing lives in RegisterBuiltins' live-NATS branch, unreachable from
// `go test ./...`; TestWriteTodosStaysSkipped is the offline pin on both the entry
// and its precondition (nothing under configs/ references the tool).
var productShellSkippedBuiltins = []string{"write_todos"}

// buildRuntimeRegistries wires every registry a component or tool can be
// constructed against:
//
//   - componentReg: the framework + semdev component factories, through this
//     package's own RegisterAll — the same seam the G1 census builds against,
//     so production and census cannot drift.
//   - payloadReg: the framework's first-party payload types. semdev owns no
//     product payloads yet (M0); a future one registers here alongside
//     payloadbuiltins.Register, mirroring semteams' buildPayloadRegistry.
//   - toolReg: the framework builtins + semdev's own G1-gated tools, through
//     this package's RegisterTools — gated on a live NATS client so
//     fact-writing tools get real writers instead of the schema-only nil path
//     the conformance censuses take.
//   - lifecycleMgr: the run-entity workflow DECLARATION (RegisterLifecycle →
//     agentrun.Register). No transition fires from here — rules own every
//     transition (G2); this only registers what the workflow's edges are.
func buildRuntimeRegistries(ctx context.Context, natsClient *natsclient.Client, platform types.PlatformMeta, expCfg experiment.Config, opts RunOptions, logger *slog.Logger) (*runtimeRegistries, error) {
	// The SHARED, run-scoped, PROCESS-LOCAL runspace instances are created HERE (the live
	// path always has a real client) — BEFORE RegisterAll — and handed to BOTH the R6
	// station components (RegisterAll) and the dev-loop tools (RegisterTools). They must be
	// the same instances: a station component that built its own runspace.Checkouts /
	// Sandboxes would get a different empty map and never find the run's checkout/container.
	// NewRuntime keeps sandboxes via the returned struct so Stop can reap the containers it
	// stood up (checkouts are temp dirs cleaned on process exit).
	checkouts, err := runspace.NewCheckouts("", cliexec.OSRunner{})
	if err != nil {
		return nil, fmt.Errorf("create run checkouts: %w", err)
	}
	sandboxes := runspace.NewSandboxes()
	// The provision-time standards capture, shared by the provision station (which fills
	// it) and the floors station (which gates on it). One instance, like checkouts and
	// sandboxes: the checks lane must read what provisioning validated, not anything
	// reachable from the container the attempt runs in.
	standardsSnapshots := standards.NewSnapshots()

	spec, err := sourceSpec(opts)
	if err != nil {
		return nil, err
	}
	// The graph write surface, BEFORE any component or tool that writes facts is
	// registered: one contract-validating mutation client carrying every derived
	// contract (ADR-091 — contracts validate local intent, nothing registers or
	// leases). Construction is purely local, so a failure here is a contract bug.
	graphClients, err := declaredGraphClients(natsClient)
	if err != nil {
		return nil, err
	}

	componentReg := component.NewRegistry()
	// Boot census: every writer the vocab table declares must resolve a writer
	// surface BEFORE anything that writes is registered. Without this, a typo'd
	// Source at a call site surfaces only as a nil writer at its FIRST WRITE, deep
	// inside a station handler where several paths can do nothing but log. Fail at
	// boot instead.
	declaredOwners, err := graphown.Owners()
	if err != nil {
		return nil, err
	}
	if err := graphClients.RequireWriters(declaredOwners...); err != nil {
		return nil, err
	}

	if err := RegisterAll(componentReg, checkouts, sandboxes, spec, standardsSnapshots, graphClients); err != nil {
		return nil, fmt.Errorf("register components: %w", err)
	}

	payloadReg := payloadregistry.New()
	if err := payloadbuiltins.Register(payloadReg); err != nil {
		return nil, fmt.Errorf("register builtin payloads: %w", err)
	}

	toolReg := agentictools.NewExecutorRegistry()
	toolDeps := executors.ToolDependencies{
		NATSClient: natsClient,
		Platform:   platform,
		Logger:     logger,
		// beta.159: RegisterBuiltins now HARD-FAILS on write_todos unless it is given
		// a projection.MutationClient. semdev cannot supply one: the tool writes under
		// the framework's own `agentic.loop-execution` contract, whose definition lives
		// in semstreams' internal/builtinprojection — not importable from here — and
		// binding a hand-copied duplicate would claim the framework's cell under a
		// semdev owner (a two-writer hazard, G5) and rot silently the moment the
		// framework's shape moves.
		//
		// Skipping is correct rather than merely expedient: semdev references
		// write_todos NOWHERE (no persona, no allowlist, no rule), so no spawn ever
		// advertises it — and under beta.149 executor enforcement a call to an
		// unadvertised tool is rejected anyway. The framework's own binaries wire it
		// via service.WireOwnership(builtinprojection.Contracts()...), which is exactly
		// the path a product shell has no access to.
		//
		// If semdev ever WANTS agent-private todos, this becomes an upstream ask for an
		// exported accessor to the builtin contracts — not a local re-declaration.
		SkipBuiltins: productShellSkippedBuiltins,
	}
	if err := RegisterTools(ctx, toolReg, toolDeps, opts.GitHubToken, expCfg, checkouts, sandboxes, graphClients); err != nil {
		return nil, fmt.Errorf("register tools: %w", err)
	}

	lifecycleMgr := lifecycle.NewManager(natsClient, logger)
	if err := RegisterLifecycle(lifecycleMgr); err != nil {
		return nil, fmt.Errorf("register lifecycle workflow: %w", err)
	}

	return &runtimeRegistries{
		componentReg: componentReg,
		payloadReg:   payloadReg,
		toolReg:      toolReg,
		lifecycleMgr: lifecycleMgr,
		sandboxes:    sandboxes,
		graphOwners:  graphClients,
	}, nil
}

// wireServices builds the metrics registry + platform identity, every
// registry (buildRuntimeRegistries), the service registry/manager, the
// service.Dependencies every constructed service and component shares, and
// finally constructs (but does not start) every enabled, constructor-
// registered service in cfg.Services. Returns the service manager and the
// built registries bundle — the latter so NewRuntime can expose the tool
// registry (the integration smoke test asserts on the advertised tool set) and
// hold the warm-sandbox registry for Stop to reap.
func wireServices(ctx context.Context, cfg *config.Config, expCfg experiment.Config, natsClient *natsclient.Client, configMgr *config.Manager, opts RunOptions, logger *slog.Logger) (*service.Manager, *runtimeRegistries, error) {
	metricsRegistry := metric.NewMetricsRegistry()
	platform := platformMeta(cfg)

	regs, err := buildRuntimeRegistries(ctx, natsClient, platform, expCfg, opts, logger)
	if err != nil {
		return nil, nil, err
	}

	serviceReg := service.NewServiceRegistry()
	if err := service.RegisterAll(serviceReg); err != nil {
		return nil, nil, fmt.Errorf("register services: %w", err)
	}
	svcMgr := service.NewServiceManager(serviceReg)

	// ServiceManager is deliberately LEFT NIL (matching semteams' createService
	// dependencies). CreateService holds the manager's write lock across each
	// service constructor; the framework's default heartbeat service calls
	// deps.ServiceManager.GetService(...) inside its constructor, which takes the
	// manager's READ lock — a non-reentrant RWMutex self-deadlock if ServiceManager
	// is set. Leaving it nil makes heartbeat skip that health-lookup branch (it just
	// reports no component health), which is the framework's intended M0 posture.
	svcDeps := &service.Dependencies{
		NATSClient:        natsClient,
		MetricsRegistry:   metricsRegistry,
		Logger:            logger,
		Platform:          platform,
		Manager:           configMgr,
		ComponentRegistry: regs.componentReg,
		ToolRegistry:      regs.toolReg,
		PayloadRegistry:   regs.payloadReg,
		LifecycleManager:  regs.lifecycleMgr,
	}

	// ConfigureFromServices is the WHOLE composition in beta.160: it constructs
	// every enabled configured service itself and seals the running set
	// (services are restart-only composition). A follow-up construction pass
	// would double-construct and be rejected.
	if err := svcMgr.ConfigureFromServices(cfg.Services, svcDeps); err != nil {
		return nil, nil, fmt.Errorf("configure service manager: %w", err)
	}

	return svcMgr, regs, nil
}

// NewRuntime wires the full shared runtime: config → NATS → JetStream streams
// → every registry → service dependencies → every enabled service in
// cfg.Services. It does NOT start anything — callers drive that explicitly
// via Start (or Run, which wires+starts+blocks+stops in one call).
//
// Failure cleanup is explicit and linear rather than defer-based: each error
// path below closes exactly the resources opened by the steps before it (the
// NATS connection, then also the config manager once it exists), so a reader
// can audit "what's alive at this point" by reading top-to-bottom without
// tracing deferred closures.
func NewRuntime(ctx context.Context, opts RunOptions) (*Runtime, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Declare semdev's canonical predicate vocabulary with the framework registry BEFORE
	// any component (the rule processor, at Start) validates rules against it. beta.147's
	// rule-load predicate-declaration check is UNCONDITIONAL and hard-fails an undeclared
	// predicate; a non-canonical name PANICS in Register, so this is also the boot-time
	// canonical-shape guard over the whole vocabulary. Idempotent across NewRuntime calls
	// (vocabulary.Register amends), so repeated e2e boots in one process are safe.
	vocab.Register()

	cfg, expCfg, err := loadRuntimeConfig(opts.ConfigPath, logger)
	if err != nil {
		return nil, err
	}

	// Boot coherence guard (pull-first-transport H1): warn LOUDLY if the assembled
	// bootstrap leaves the /semdev approve lane unreachable (no webhook receiver AND
	// poll off) — the silent dead-approval-lane combo the two component blocks can't
	// see individually. Loud-warn, not fail-closed: a harness/external publisher can
	// still feed the stream directly (the shipped default + journeys do).
	if reachable, detail := inboundApprovalReachable(cfg); !reachable {
		logger.Warn("conversation approval lane may be unreachable (pull-first-transport H1): "+detail,
			slog.String("config", opts.ConfigPath))
	}

	natsClient, err := connectRuntimeNATS(ctx, opts.NATSURLs, cfg)
	if err != nil {
		return nil, err
	}

	if err := config.NewStreamsManager(natsClient, logger).EnsureStreams(ctx, cfg); err != nil {
		closeBootNATS(natsClient)
		return nil, fmt.Errorf("ensure JetStream streams: %w", err)
	}

	configMgr, err := config.NewConfigManager(cfg, natsClient, logger)
	if err != nil {
		closeBootNATS(natsClient)
		return nil, fmt.Errorf("create config manager: %w", err)
	}
	if err := configMgr.Start(ctx); err != nil {
		closeBootNATS(natsClient)
		return nil, fmt.Errorf("start config manager: %w", err)
	}

	svcMgr, regs, err := wireServices(ctx, cfg, expCfg, natsClient, configMgr, opts, logger)
	if err != nil {
		_ = configMgr.Stop(5 * time.Second)
		closeBootNATS(natsClient)
		return nil, err
	}

	// Seed the PERSONAS KV bucket from the fragment tree BEFORE Start, so the
	// agentic-loop's per-task persona assembly reads Sarah/Amelia/Quinn rather
	// than the framework defaults. A wiring fault (wrong path) fails boot here
	// rather than silently degrading the coordinator's routing prompt.
	if err := seedPersonas(ctx, natsClient, personasDir(opts), logger); err != nil {
		_ = configMgr.Stop(5 * time.Second)
		closeBootNATS(natsClient)
		return nil, err
	}

	return &Runtime{
		cfg:          cfg,
		nats:         natsClient,
		configMgr:    configMgr,
		svcMgr:       svcMgr,
		toolRegistry: regs.toolReg,
		sandboxes:    regs.sandboxes,
		graphWriters: regs.graphOwners,
		logger:       logger,
	}, nil
}

// Start begins running every constructed service (svcMgr.StartAll) —
// component-manager among them, which is what actually instantiates
// cfg.Components (graph-ingest, graph-query, rule, agentic-tools, ...) per
// the framework's config-driven component model (see this file's package
// doc). NewRuntime wires; Start runs.
func (r *Runtime) Start(ctx context.Context) error {
	if err := r.svcMgr.StartAll(ctx); err != nil {
		return fmt.Errorf("start services: %w", err)
	}
	return nil
}

// Stop shuts the runtime down in reverse-dependency order: services first
// (svcMgr.StopAll, which itself stops components in reverse start order),
// then the config manager, then the NATS connection. Every step runs even if
// an earlier one fails — best-effort, matching semteams' deferred-cleanup
// posture — and every failure is joined into the returned error so a caller
// sees the whole picture instead of just the first fault.
//
// svcMgr.StopAll is bounded by an OUTER deadline (timeout + stopServicesGrace),
// not just the timeout StopAll takes internally, because of a known semstreams
// ComponentManager shutdown deadlock (filed upstream: C360Studio/semstreams —
// see design.md D13). On a cold boot with the agentic-execution plane the
// framework leaks a cm.mu reader in its health check, after which
// stopAllComponents' RLock never returns and StopAll wedges BEFORE reaching the
// stage its own timeout guards. Without this bound, Stop — and therefore a
// SIGINT on the live binary — would hang forever. When the bound elapses we log
// the wedge, abandon the StopAll goroutine (it leaks, but the process is on its
// way out), and still close the config manager and NATS so those resources are
// released. Remove the bound once the upstream fix lands.
func (r *Runtime) Stop(timeout time.Duration) error {
	var errs []error

	stopDone := make(chan error, 1)
	go func() { stopDone <- r.svcMgr.StopAll(timeout) }()
	stopTimer := time.NewTimer(timeout + stopServicesGrace)
	defer stopTimer.Stop()
	select {
	case err := <-stopDone:
		if err != nil {
			errs = append(errs, fmt.Errorf("stop services: %w", err))
		}
	case <-stopTimer.C:
		r.logger.Warn("svcMgr.StopAll did not return within the shutdown budget; abandoning it and continuing shutdown (known semstreams ComponentManager deadlock, C360Studio/semstreams#508 — see runtime.Stop docs)",
			slog.Duration("budget", timeout+stopServicesGrace))
		errs = append(errs, fmt.Errorf("%w (after %s)", errStopServicesWedged, timeout+stopServicesGrace))
	}

	// Reap any warm dev sandboxes (docker containers + fresh cache volumes) the run
	// loop stood up. This is a lifecycle-INDEPENDENT best-effort teardown (G2: not a
	// transition — runspace owns the infra), so a Stop/SIGINT does not leak a container
	// per run. It runs regardless of a wedged StopAll (Down detaches from ctx
	// internally), on its own bounded window.
	if r.sandboxes != nil {
		reapCtx, cancel := context.WithTimeout(context.Background(), timeout)
		if err := r.sandboxes.CloseAll(reapCtx); err != nil {
			errs = append(errs, fmt.Errorf("reap warm sandboxes: %w", err))
		}
		cancel()
	}

	if err := r.configMgr.Stop(timeout); err != nil {
		errs = append(errs, fmt.Errorf("stop config manager: %w", err))
	}
	// Bound nats.Close too: the wedge path has already abandoned StopAll to keep
	// the "Stop always returns" property, so a hung Close on context.Background()
	// would re-introduce the very unbounded hang this method exists to prevent.
	closeCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := r.nats.Close(closeCtx); err != nil {
		errs = append(errs, fmt.Errorf("close NATS client: %w", err))
	}
	return errors.Join(errs...)
}

// ServiceManager exposes the wired *service.Manager for tests/assertions that
// need to reach into the running services (e.g. GetService).
func (r *Runtime) ServiceManager() *service.Manager {
	return r.svcMgr
}

// ToolRegistry exposes the wired *agentictools.ExecutorRegistry so callers —
// the integration smoke test, primarily — can assert on the advertised tool
// set without standing up a second registration path.
func (r *Runtime) ToolRegistry() *agentictools.ExecutorRegistry {
	return r.toolRegistry
}

// Run wires a Runtime (NewRuntime), starts it (Start), blocks until ctx is
// canceled (the binaries pass a signal.NotifyContext ctx — SIGINT/SIGTERM),
// then stops it (Stop) with runtimeShutdownTimeout. This is the single
// entrypoint both cmd/semdev and cmd/e2e-semdev call to bring up the runtime —
// the binary-parity contract for runtime boot, mirroring the one
// boot.RegisterAll already holds for component registration: neither binary
// wires NATS, the service manager, or any registry independently.
func Run(ctx context.Context, opts RunOptions) error {
	rt, err := NewRuntime(ctx, opts)
	if err != nil {
		return fmt.Errorf("wire runtime: %w", err)
	}

	if startErr := rt.Start(ctx); startErr != nil {
		_ = rt.Stop(runtimeShutdownTimeout)
		return fmt.Errorf("start runtime: %w", startErr)
	}

	<-ctx.Done()

	if stopErr := rt.Stop(runtimeShutdownTimeout); stopErr != nil {
		return fmt.Errorf("stop runtime: %w", stopErr)
	}
	return nil
}
