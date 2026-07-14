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

	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/runspace"
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
)

// defaultNATSURL is the fallback NATS URL when nothing else configures one —
// the bare docker-compose NATS both binaries expect at dev time (never
// embedded; see docker/compose/nats.yml).
const defaultNATSURL = "nats://localhost:4222"

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
	// SandboxSourceDir is the run's target SOURCE at M0 — the operator-configured
	// directory provision_sandbox materializes each run's checkout from and
	// cold-proves (the in-repo Go fixture the journey drives). Empty makes the
	// provision tool's source resolve fail closed, so a run parks toward the operator
	// rather than provisioning a guessed target (SB5). forge-io resolves this per-run
	// from the run's issue_ref at M2, behind the same seam.
	SandboxSourceDir string
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
	logger    *slog.Logger
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
func loadRuntimeConfig(path string) (*config.Config, error) {
	cfg, err := config.NewLoader().LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	if err := resolveRulePackPaths(cfg, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("resolve rule pack paths in %s: %w", path, err)
	}
	return cfg, nil
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
}

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
func buildRuntimeRegistries(ctx context.Context, natsClient *natsclient.Client, platform types.PlatformMeta, opts RunOptions, logger *slog.Logger) (*runtimeRegistries, error) {
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

	componentReg := component.NewRegistry()
	if err := RegisterAll(componentReg, checkouts, sandboxes, opts.SandboxSourceDir); err != nil {
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
	}
	if err := RegisterTools(ctx, toolReg, toolDeps, opts.GitHubToken, checkouts, sandboxes); err != nil {
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
	}, nil
}

// createConfiguredServices constructs every enabled, constructor-registered
// service in services except service-manager (ConfigureFromServices already
// configured that one directly, from the same map — see semteams'
// configureAndCreateServices). A configured-but-unregistered name (no
// HasConstructor match) is skipped rather than treated as fatal: a config
// typo or a not-yet-landed service should not crash boot, it should be quietly
// absent — the same posture semteams' createServiceIfEnabled takes.
func createConfiguredServices(svcMgr *service.Manager, services types.ServiceConfigs, svcDeps *service.Dependencies) error {
	for name, svcCfg := range services {
		if name == "service-manager" {
			continue
		}
		if !svcCfg.Enabled {
			continue
		}
		if !svcMgr.HasConstructor(name) {
			continue
		}
		if _, err := svcMgr.CreateService(name, svcCfg.Config, svcDeps); err != nil {
			return fmt.Errorf("create service %s: %w", name, err)
		}
	}
	return nil
}

// wireServices builds the metrics registry + platform identity, every
// registry (buildRuntimeRegistries), the service registry/manager, the
// service.Dependencies every constructed service and component shares, and
// finally constructs (but does not start) every enabled, constructor-
// registered service in cfg.Services. Returns the service manager and the
// built registries bundle — the latter so NewRuntime can expose the tool
// registry (the integration smoke test asserts on the advertised tool set) and
// hold the warm-sandbox registry for Stop to reap.
func wireServices(ctx context.Context, cfg *config.Config, natsClient *natsclient.Client, configMgr *config.Manager, opts RunOptions, logger *slog.Logger) (*service.Manager, *runtimeRegistries, error) {
	metricsRegistry := metric.NewMetricsRegistry()
	platform := platformMeta(cfg)

	regs, err := buildRuntimeRegistries(ctx, natsClient, platform, opts, logger)
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

	if err := svcMgr.ConfigureFromServices(cfg.Services, svcDeps); err != nil {
		return nil, nil, fmt.Errorf("configure service manager: %w", err)
	}
	if err := createConfiguredServices(svcMgr, cfg.Services, svcDeps); err != nil {
		return nil, nil, err
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
// tracing deferred closures. Nothing past the config manager holds a resource
// that needs releasing before Start — the registries and constructed-but-not-
// started services are in-memory only.
func NewRuntime(ctx context.Context, opts RunOptions) (*Runtime, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	cfg, err := loadRuntimeConfig(opts.ConfigPath)
	if err != nil {
		return nil, err
	}

	natsClient, err := connectRuntimeNATS(ctx, opts.NATSURLs, cfg)
	if err != nil {
		return nil, err
	}

	if err := config.NewStreamsManager(natsClient, logger).EnsureStreams(ctx, cfg); err != nil {
		_ = natsClient.Close(ctx)
		return nil, fmt.Errorf("ensure JetStream streams: %w", err)
	}

	configMgr, err := config.NewConfigManager(cfg, natsClient, logger)
	if err != nil {
		_ = natsClient.Close(ctx)
		return nil, fmt.Errorf("create config manager: %w", err)
	}
	if err := configMgr.Start(ctx); err != nil {
		_ = natsClient.Close(ctx)
		return nil, fmt.Errorf("start config manager: %w", err)
	}

	svcMgr, regs, err := wireServices(ctx, cfg, natsClient, configMgr, opts, logger)
	if err != nil {
		_ = configMgr.Stop(5 * time.Second)
		_ = natsClient.Close(ctx)
		return nil, err
	}

	// Seed the PERSONAS KV bucket from the fragment tree BEFORE Start, so the
	// agentic-loop's per-task persona assembly reads Sarah/Amelia/Quinn rather
	// than the framework defaults. A wiring fault (wrong path) fails boot here
	// rather than silently degrading the coordinator's routing prompt.
	if err := seedPersonas(ctx, natsClient, personasDir(opts), logger); err != nil {
		_ = configMgr.Stop(5 * time.Second)
		_ = natsClient.Close(ctx)
		return nil, err
	}

	return &Runtime{
		cfg:          cfg,
		nats:         natsClient,
		configMgr:    configMgr,
		svcMgr:       svcMgr,
		toolRegistry: regs.toolReg,
		sandboxes:    regs.sandboxes,
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
