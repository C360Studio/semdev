// Package conversationchannel is the conversation-channel COMPONENT
// (conversation-channel-seam D8) — the registered runtime surface for semdev's
// human conversation lane, carved out of issue-intake and re-homed behind the
// channel-neutral Channel port. It owns the two lanes that speak to the human on
// their thread:
//
//   - APPROVAL: a durable JetStream consumer on github.event.comment (the same
//     webhook-fed GITHUB stream issue-intake's receiver publishes to — B-1: the
//     receiver still flattens BOTH event types) drives the change-approval gate.
//     Each comment is normalized to a neutral Message by the GitHub Channel impl,
//     so the approval logic never sees a githubwebhook.CommentEvent.
//   - PARK-POST: a durable consumer on user.response.> (the USER stream) posts a
//     parked run's run.awaiting.human message to its thread via Channel.Post.
//
// Both lanes go through the Channel port; issue-intake keeps the code-host issue
// front door (issue events → run mint) + the webhook receiver. The two components
// share one admission core (authorize / SplitRef / RunResolver — a pure reference,
// not a writer). G2: this component fires no lifecycle transition (the approval
// adapter stamps the stand-in fact the resume rule reads; the transition stays
// rule-owned). G3: it stamps only what the human's authorized command derived.
package conversationchannel

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semdev/internal/forge/conversation"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// ComponentName is the registered factory name.
const ComponentName = "conversation-channel"

// GithubStreamName is the durable stream the comment lane rides (issue-intake's
// receiver declares + publishes it).
const GithubStreamName = "GITHUB"

// ComponentConfig is the conversation-channel config surface (bootstrap
// `components` block). It carries the admission knobs the approval authorize needs
// (shared with issue-intake's config — the operator sets both) plus the token env
// for the forge client that posts + checks permissions. Secrets travel by ENV NAME
// only, never by value.
type ComponentConfig struct {
	Ports *component.PortConfig `json:"ports,omitempty" schema:"type:ports,description:The github.event.comment consumer (GITHUB stream) and the user.response consumer (USER stream).,category:basic"`

	// Repo binds the lane to one "owner/name"; comments for any other repo are
	// skipped before the gate (defense in depth over the webhook's own scoping).
	Repo string `json:"repo,omitempty" schema:"type:string,description:The owner/name repository this lane serves; other repos are skipped.,category:basic"`

	// Allowlist, OptInLabel, OptInCommand are the admission knobs (see
	// admission.Config). The approval command is "<OptInCommand> approve".
	Allowlist    []string `json:"allowlist,omitempty" schema:"type:array,description:Actors always authorized (no permission call).,category:basic"`
	OptInLabel   string   `json:"opt_in_label,omitempty" schema:"type:string,description:Label that opts an issue in (default semdev).,category:basic"`
	OptInCommand string   `json:"opt_in_command,omitempty" schema:"type:string,description:Slash command that opts an issue in / prefixes the approve command (default /semdev).,category:basic"`

	// TokenEnv names the env var holding the forge token used both to post
	// messages and to run collaborator permission checks (default GITHUB_TOKEN,
	// the dotenv lane).
	TokenEnv string `json:"token_env,omitempty" schema:"type:string,description:Env var NAME holding the forge token for posting + collaborator permission checks.,category:basic"`

	// APIBase overrides the forge REST endpoint (the e2e forge double); "" = GitHub.
	// Both the Channel's Read (poll transport) and the collaborator checker ride it.
	APIBase string `json:"api_base,omitempty" schema:"type:string,description:Forge REST API base override (e2e double); empty = api.github.com.,category:basic"`

	// Poll configures the PULL-FIRST inbound transport (pull-first-transport). When
	// enabled the component POLLS each awaiting-approval run's thread for the
	// /semdev approve comment instead of consuming webhook comment events — the
	// webhook-unreachable deployment (dev box / behind NAT). Nil/disabled = webhook
	// mode (today). A comment lane is EITHER polled OR webhook-fed, never both (B-1).
	Poll *PollConfig `json:"poll,omitempty" schema:"type:object,description:Pull-first poll transport (enabled + interval); when enabled the poller replaces the webhook comment consumer.,category:basic"`
}

// PollConfig is the pull-first poll transport's config block.
type PollConfig struct {
	// Enabled turns on the poller (and, structurally, skips the webhook comment
	// consumer — B-1). A pull-first deployment pairs this with issue-intake
	// http_port 0 (no receiver); a webhook deployment leaves it off.
	Enabled bool `json:"enabled,omitempty" schema:"type:bool,description:Poll the thread for approval comments instead of consuming webhook comment events.,category:basic"`
	// Interval is the poll cadence as a duration (default 15s; a positive value
	// below the 5s floor is clamped up; a non-positive/unparseable value is rejected
	// at Validate — a mis-set 0 must not become a ListComments rate-limit storm, M4).
	Interval string `json:"interval,omitempty" schema:"type:string,description:Poll interval duration (default 15s; floor 5s).,category:basic"`
}

// Poll cadence bounds (pull-first-transport D6).
const (
	defaultPollInterval = 15 * time.Second
	minPollInterval     = 5 * time.Second
)

// pollEnabled reports whether the pull-first poll transport is on.
func (c *ComponentConfig) pollEnabled() bool { return c.Poll != nil && c.Poll.Enabled }

// pollInterval resolves the poll cadence: the parsed interval clamped to the floor,
// or the default when unset/invalid. applyConfigDefaults + Validate have already
// defaulted and floor-clamped a valid config, so on the live path this just parses.
func (c *ComponentConfig) pollInterval() time.Duration {
	if c.Poll == nil || c.Poll.Interval == "" {
		return defaultPollInterval
	}
	d, err := time.ParseDuration(c.Poll.Interval)
	if err != nil || d <= 0 {
		return defaultPollInterval
	}
	if d < minPollInterval {
		return minPollInterval
	}
	return d
}

// Validate requires the jetstream input ports — a consumer-less lane would start
// healthy and silently never process a comment or a park (the flow break the
// constitution bars).
func (c *ComponentConfig) Validate() error {
	if c.Ports == nil || len(c.Ports.Inputs) == 0 {
		return errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "Validate", "ports configuration with the comment + user.response inputs is required")
	}
	// A non-positive or unparseable poll interval must fail LOUD, not silently
	// become a tight ListComments loop (review M4). applyConfigDefaults has already
	// defaulted an empty interval and clamped a sub-floor positive, so only a
	// genuinely bad value ("0s", "-5s", "garbage") reaches here unhealed.
	if c.pollEnabled() {
		if d, err := time.ParseDuration(c.Poll.Interval); err != nil || d <= 0 {
			return errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "Validate",
				"poll.interval must be a positive duration (e.g. 15s)")
		}
	}
	return nil
}

// Schema is the generated config schema for registration.
var Schema = component.GenerateConfigSchema(reflect.TypeOf(ComponentConfig{}))

// DefaultPorts declares the two consumer lanes: the comment events (GITHUB stream,
// filtered to github.event.comment) and the park-lane publishes (USER stream).
func DefaultPorts() *component.PortConfig {
	return &component.PortConfig{
		Inputs: []component.PortDefinition{{
			Name:        "comment_events",
			Type:        "jetstream",
			Subject:     admission.SubjectComment,
			StreamName:  GithubStreamName,
			Required:    true,
			Description: "Flattened comment events (issue-intake's receiver or an e2e journey publishes them).",
		}, {
			Name:        "user_responses",
			Type:        "jetstream",
			Subject:     UserResponseSubject,
			StreamName:  "USER",
			Required:    false,
			Description: "The park rules' user.response publishes — posted to the thread via the Channel port.",
		}, {
			Name:        "user_notes",
			Type:        "jetstream",
			Subject:     UserNoteSubject,
			StreamName:  "USER",
			Required:    false,
			Description: "The classifier fault-note publishes (conversation/05) — posted to the thread via the Channel port. A message only; stamps no fact.",
		}, {
			Name:        "apply_dispatch",
			Type:        "jetstream",
			Subject:     ApplyDispatchSubject,
			StreamName:  ApplyStreamName,
			Required:    false,
			Description: "The NL intent routing rules' dispatch (conversation/03a+03b) — the deterministic apply consumer that re-authorizes, posts transparency, and releases the change gate.",
		}},
		Outputs: []component.PortDefinition{},
	}
}

// Component is the conversation-channel processor.
type Component struct {
	config   ComponentConfig
	nats     *natsclient.Client
	approv   *approvalAdapter
	apply    *applyConsumer
	parkpost *parkPoster
	pollLoop *poller // nil in webhook mode; the pull-first inbound transport in poll mode
	logger   *slog.Logger

	mu         sync.RWMutex
	started    bool
	startTime  time.Time
	pollCancel context.CancelFunc // cancels the poll goroutine on Stop (M5); guarded by mu

	eventsConsumed int64
	errors         int64
	lastActivity   atomic.Value // time.Time
}

var (
	_ component.Discoverable       = (*Component)(nil)
	_ component.LifecycleComponent = (*Component)(nil)
)

// NewProcessor is the component factory.
func NewProcessor(rawConfig json.RawMessage, deps component.Dependencies) (component.Discoverable, error) {
	var cfg ComponentConfig
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &cfg); err != nil {
			return nil, errs.WrapInvalid(err, ComponentName, "NewProcessor", "config unmarshal")
		}
	}
	if cfg.Ports == nil {
		cfg.Ports = DefaultPorts()
	}
	applyConfigDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.NATSClient == nil {
		return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor", "NATSClient required")
	}
	logger := deps.GetLoggerWithComponent(ComponentName)

	// One forge client serves BOTH roles: the admission permission checker AND the
	// Channel's posting commenter. Without a token, admission runs allowlist-only
	// (a non-allowlisted actor is a DEFINITIVE reject) and the park stays graph-only
	// (a nil channel — the poster's documented degrade).
	var checker admission.PermissionChecker
	var channel conversation.Channel
	if token := strings.TrimSpace(os.Getenv(cfg.TokenEnv)); token != "" {
		client := github.NewClient(token).WithLogger(logger)
		if cfg.APIBase != "" {
			client = client.WithBaseURL(cfg.APIBase)
		}
		checker = client
		channel = conversation.NewGitHubChannel(client)
	} else {
		checker = admission.AllowlistOnlyChecker{}
		logger.Warn("no forge token in env; approval runs allowlist-only (collaborator checks unavailable) and park messages stay graph-only (no channel)",
			slog.String("token_env", cfg.TokenEnv))
	}

	c := &Component{
		config: cfg,
		nats:   deps.NATSClient,
		logger: logger,
	}
	c.approv = newApprovalAdapter(deps.NATSClient, cfg, checker, deps.Platform, logger)
	// The deterministic apply consumer (nl-conversation-intent D6). It shares this
	// component's forge client, checker, and — structurally, via the injected stamp
	// func — the ONE sanctioned gate writer the exact-command fast-path uses (G5/D11).
	// A nil channel (no forge token) makes the NL apply lane REFUSE rather than
	// degrade (grp5-review H2): unlike the park lane, whose post is a courtesy on
	// top of a durable fact, here the post is the entire visibility guard for a
	// decision with no harness floor. The exact-command lane still works.
	c.apply = &applyConsumer{
		cfg:     cfg,
		channel: channel,
		checker: checker,
		fetcher: admission.NewNATSEntityFetcher(deps.NATSClient),
		stamp:   c.approv.stampDecision,
		logger:  logger,
	}
	c.parkpost = &parkPoster{
		channel: channel,
		fetcher: admission.NewNATSEntityFetcher(deps.NATSClient),
		logger:  logger,
	}
	// Pull-first poll transport (pull-first-transport D2): the poller Reads each
	// awaiting-approval thread through the SAME channel + feeds the SAME approval
	// core (handleMessage) the webhook consumer uses. It needs a channel to Read, so
	// poll mode with no forge token is a fail-closed wiring error (a dead lane, not a
	// silent degrade — the boot guard catches the http_port-0 + poll-off combo, this
	// catches poll-on + no-token).
	if cfg.pollEnabled() {
		if channel == nil {
			return nil, errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "NewProcessor",
				"poll mode requires a forge token (to read the thread for approval comments) but none is set in "+cfg.TokenEnv)
		}
		lister := admission.NewRunResolver(deps.NATSClient, deps.Platform.Org, deps.Platform.Platform)
		c.pollLoop = newPoller(channel, lister, c.approv.handleMessage, cfg.Repo, cfg.pollInterval(), logger)
	}
	return c, nil
}

func applyConfigDefaults(cfg *ComponentConfig) {
	if cfg.OptInLabel == "" {
		cfg.OptInLabel = "semdev"
	}
	if cfg.OptInCommand == "" {
		cfg.OptInCommand = "/semdev"
	}
	if cfg.TokenEnv == "" {
		cfg.TokenEnv = "GITHUB_TOKEN"
	}
	// Poll cadence defaults + floor clamp (M4): default an unset interval; clamp a
	// positive-but-sub-floor value up to the floor. A non-positive/unparseable value
	// is left as-is so Validate rejects it loudly (never a silent tight loop).
	if cfg.pollEnabled() {
		if cfg.Poll.Interval == "" {
			cfg.Poll.Interval = defaultPollInterval.String()
		} else if d, err := time.ParseDuration(cfg.Poll.Interval); err == nil && d > 0 && d < minPollInterval {
			cfg.Poll.Interval = minPollInterval.String()
		}
	}
}

// Register registers the conversation-channel component with the component registry
// (called from boot.RegisterAll so both binaries pick it up together).
func Register(reg *component.Registry) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name:        ComponentName,
		Factory:     NewProcessor,
		Schema:      Schema,
		Type:        "processor",
		Domain:      "conversation-channel",
		Protocol:    "webhook",
		Description: "Conversation-channel: the human approval + park-post lanes behind the channel-neutral Channel port (conversation-channel-seam).",
		Version:     "0.1.0",
	})
}

// Initialize is a no-op (no secret to load — the token env is read at construction).
func (c *Component) Initialize() error { return nil }

// Start wires the two durable consumers.
func (c *Component) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errs.WrapFatal(errs.ErrAlreadyStarted, ComponentName, "Start", "already started")
	}
	c.started = true
	c.startTime = time.Now()
	c.mu.Unlock()
	fail := func(err error) error {
		c.mu.Lock()
		c.started = false
		c.mu.Unlock()
		return err
	}

	for _, port := range c.activeConsumerPorts() {
		if err := c.setupConsumer(ctx, port); err != nil {
			return fail(err)
		}
	}

	// The pull-first inbound transport (poll mode): launch the poller on a context
	// this component CANCELS on Stop (M5). Log the ACTIVE inbound mode LOUDLY (H1a):
	// the dangerous combo — no webhook receiver AND poll off — is a silent
	// dead-approval lane the component cannot see alone (the receiver is a different
	// component's knob); boot's coherence guard (inboundApprovalReachable) catches it,
	// and this log makes the per-component mode diagnosable.
	if c.config.pollEnabled() {
		pollCtx, cancel := context.WithCancel(ctx)
		c.mu.Lock()
		c.pollCancel = cancel
		c.mu.Unlock()
		go c.pollLoop.run(pollCtx)
		c.logger.Info("conversation-channel: poll ENABLED — pull-first inbound transport (webhook comment consumer SKIPPED)",
			slog.Duration("interval", c.config.pollInterval()), slog.String("repo", c.config.Repo))
	} else {
		c.logger.Info("conversation-channel: poll DISABLED — approval requires an inbound webhook receiver (webhook mode)",
			slog.String("repo", c.config.Repo))
	}
	return nil
}

// activeConsumerPorts returns the input ports Start wires as durable consumers. In
// POLL mode it EXCLUDES the webhook comment lane (SubjectComment) — the poller owns
// inbound comments, so a comment is never double-processed (the XOR, B-1). The
// park-post lane (user.response) runs in BOTH modes (it is the outbound park lane,
// orthogonal to the inbound comment transport).
func (c *Component) activeConsumerPorts() []component.PortDefinition {
	pollMode := c.config.pollEnabled()
	var out []component.PortDefinition
	for _, port := range c.config.Ports.Inputs {
		if port.Type != "jetstream" || port.Subject == "" {
			continue
		}
		if pollMode && port.Subject == admission.SubjectComment {
			continue
		}
		out = append(out, port)
	}
	return out
}

// setupConsumer creates a durable consumer for one input port (the agentic-tools
// consumer shape: bounded redelivery, heartbeat-acked work).
func (c *Component) setupConsumer(ctx context.Context, port component.PortDefinition) error {
	streamName := port.StreamName
	if streamName == "" {
		streamName = GithubStreamName
	}
	consumerCfg := component.GetConsumerConfigFromDefinition(port)
	// max_deliver is set DELIBERATELY (the framework default of 3 gives the
	// approval-races-mint case only ~2 retries before a human's /semdev approve is
	// silently dropped). ConsumeWithHeartbeat naks with a fixed 30s delay, so 10
	// deliveries ≈ 4.5 minutes of retry budget.
	maxDeliver := consumerCfg.MaxDeliver
	if maxDeliver == 0 || maxDeliver == 3 {
		maxDeliver = 10
	}
	maxAckPending := 8
	// The APPLY lane is deliberately SERIAL (max-ack-pending 1, task 5.1): two
	// concurrent opposite dispatches for one run would otherwise interleave their
	// gate-still-open reads. NOTE this serializes the consumer against ITSELF only
	// — globally, since there is one durable consumer on one exact subject — and is
	// NOT a substitute for the D7 rule partition: the exact-command fast-path runs
	// on a different consumer (or the poller goroutine) and can stamp a gate fact
	// concurrently with an in-flight apply. A run holding BOTH gate facts fires
	// NEITHER lifecycle rule, which is an unsurfaced stall, not a park.
	//
	// The redelivery budget is a CRASH-LOOP backstop, NOT the retry budget:
	// handleApplyDispatch retries in-process and then NOTIFIES THE HUMAN, leaving
	// the gate open. It deliberately NEVER parks — a park at the change-approval
	// gate is unrecoverable (run-lifecycle/02 and /07 both require
	// run.awaiting.human absent, and nothing in the repo ever removes it), so a
	// parked run could never be approved or cancelled and even /semdev approve
	// would die. See apply.go's FAILURE POSTURE block; the opt-out is enforced by
	// TestApplyLaneIsTheDeliberateNonParkingStation.
	if port.Subject == ApplyDispatchSubject {
		maxAckPending = 1
		maxDeliver = applyMaxDeliverCap
	}
	cfg := natsclient.StreamConsumerConfig{
		StreamName:     streamName,
		ConsumerName:   ComponentName + "-" + port.Name,
		FilterSubject:  port.Subject,
		DeliverPolicy:  consumerCfg.DeliverPolicy,
		AckPolicy:      consumerCfg.AckPolicy,
		MaxDeliver:     maxDeliver,
		AckWait:        time.Minute,
		MaxAckPending:  maxAckPending,
		AutoCreate:     false,
		MessageTimeout: 3 * time.Minute,
	}
	err := c.nats.ConsumeStreamWithConfig(ctx, cfg, func(msgCtx context.Context, msg jetstream.Msg) {
		if hbErr := natsclient.ConsumeWithHeartbeat(msgCtx, msg, 20*time.Second, func(workCtx context.Context) error {
			return c.handleEvent(workCtx, msg.Subject(), msg.Data())
		}); hbErr != nil {
			// Count the transient handler failure so Health/DataFlow reflect a
			// redelivering lane rather than reporting flat-zero errors (review L3).
			atomic.AddInt64(&c.errors, 1)
			c.logger.Error("conversation-channel event handler error", slog.Any("error", hbErr))
		}
	})
	if err != nil {
		return errs.WrapTransient(err, ComponentName, "Start", "consumer setup for "+port.Subject+" on "+streamName)
	}
	return nil
}

// handleEvent dispatches one event to its lane. A nil return ACKS (definitive); an
// error return NAKS for bounded redelivery (transient faults only).
func (c *Component) handleEvent(ctx context.Context, subject string, payload []byte) error {
	atomic.AddInt64(&c.eventsConsumed, 1)
	c.lastActivity.Store(time.Now())
	switch {
	case subject == admission.SubjectComment:
		return c.approv.handleCommentEvent(ctx, payload)
	case subject == ApplyDispatchSubject:
		return c.apply.handleApplyDispatch(ctx, payload)
	case strings.HasPrefix(subject, "user.response."):
		return c.parkpost.handleUserResponse(ctx, payload)
	case strings.HasPrefix(subject, "user.note."):
		return c.parkpost.handleUserNote(ctx, payload)
	default:
		c.logger.Debug("conversation-channel: irrelevant event subject; skipping", slog.String("subject", subject))
		return nil
	}
}

// The github client satisfies the admission checker contract — asserted here.
var _ admission.PermissionChecker = (*github.Client)(nil)

// --- lifecycle + discovery boilerplate ---

// Stop unwinds the consumers with the client and CANCELS the poll goroutine (M5):
// today's Stop only flipped `started`, which would leak the poller — it would keep
// polling + re-authorizing after shutdown. Capturing and calling the cancel here is
// what actually stops it.
func (c *Component) Stop(_ time.Duration) error {
	c.mu.Lock()
	c.started = false
	cancel := c.pollCancel
	c.pollCancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Meta implements Discoverable.
func (c *Component) Meta() component.Metadata {
	return component.Metadata{Name: ComponentName, Type: "processor", Description: "semdev conversation-channel (approval + park-post behind the Channel port)", Version: "0.1.0"}
}

// InputPorts implements Discoverable.
func (c *Component) InputPorts() []component.Port {
	ports := make([]component.Port, 0, len(c.config.Ports.Inputs))
	for _, p := range c.config.Ports.Inputs {
		ports = append(ports, component.Port{Name: p.Name, Direction: component.DirectionInput, Required: p.Required, Config: component.NATSPort{Subject: p.Subject}})
	}
	return ports
}

// OutputPorts implements Discoverable — the lanes post out-of-band (the Channel)
// and stamp facts (the approval writer), not through a declared output port.
func (c *Component) OutputPorts() []component.Port { return []component.Port{} }

// ConfigSchema implements Discoverable.
func (c *Component) ConfigSchema() component.ConfigSchema { return Schema }

// Health implements Discoverable.
func (c *Component) Health() component.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var uptime time.Duration
	if c.started {
		uptime = time.Since(c.startTime)
	}
	return component.HealthStatus{
		Healthy:    c.started,
		LastCheck:  time.Now(),
		ErrorCount: int(atomic.LoadInt64(&c.errors)),
		Uptime:     uptime,
	}
}

// DataFlow implements Discoverable.
func (c *Component) DataFlow() component.FlowMetrics {
	processed := atomic.LoadInt64(&c.eventsConsumed)
	errCount := atomic.LoadInt64(&c.errors)
	var rate float64
	if processed > 0 {
		rate = float64(errCount) / float64(processed)
	}
	last, _ := c.lastActivity.Load().(time.Time)
	return component.FlowMetrics{ErrorRate: rate, LastActivity: last}
}
