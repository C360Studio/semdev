// Package intake is semdev's code-host issue front door — the issue-intake
// COMPONENT (forge-io-real-lanes D1; NARROWED by conversation-channel-seam D8 to
// the issue lane). It owns BOTH halves of the webhook ISSUE lane, because the
// framework retired its github-webhook input in the beta.147 boundary wave and the
// cutover checklist transferred the receiver to semdev:
//
//   - RECEIVER (optional, http_port > 0): a minimal HTTP listener that validates
//     the GitHub HMAC (X-Hub-Signature-256, secret via env — never in config),
//     filters to the issues/issue_comment events the lanes consume, FLATTENS the
//     raw payload (internal/forge/githubwebhook — semdev owns the shapes AND the
//     mapping now), and publishes the flattened JSON onto the durable GITHUB
//     stream keyed by the delivery GUID (JetStream msg-id dedup absorbs GitHub
//     redeliveries at the stream layer). The receiver flattens BOTH event types —
//     GitHub delivers all events to one URL — so a comment event still reaches the
//     conversation-channel component's consumer (conversation-channel-seam B-1).
//
//   - CONSUMER: a durable JetStream consumer on github.event.issue that runs
//     Normalize → the admission gate (Decide — authorized, opted-in, zero tokens
//     for rejects) → births the admission record (the spec's intake.actor.admitted,
//     content-derived ID = the idempotency backstop) → publishes the coordinator
//     wake via CoordinatorTask + PublishToStream (the journey-proven byte shape).
//     The COMMENT + park-post lanes moved to the conversation-channel component.
//
// G1 (framework-alignment note): a rule cannot consume a raw webhook stream, decode
// a host payload, run a network permission check, or build a prompt — this is
// component-shaped work exactly like the six deterministic stations. G2: publishing
// the wake is NOT a lifecycle transition (the mint rule fires it off the
// coordinator's decide); the component fires no transition. G3: the admission
// record stamps only what the gate itself derived. The admission decision core is
// the shared internal/intake/admission package — Decide is the one gate.
package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/errs"
)

// ComponentName is the registered factory name.
const ComponentName = "issue-intake"

// GithubStreamName is the durable stream the webhook lane rides. semdev's
// bootstrap DECLARES it (the framework no longer brings a GITHUB stream).
const GithubStreamName = "GITHUB"

// maxWebhookBody bounds a webhook request body (GitHub's own payload cap is
// 25MB; anything near that is not an issue event we consume).
const maxWebhookBody = 1 << 20 // 1 MiB

// ComponentConfig is the issue-intake config surface (bootstrap `components`
// block). Secrets travel by ENV NAME only (the dotenv lane), never by value.
type ComponentConfig struct {
	Ports *component.PortConfig `json:"ports,omitempty" schema:"type:ports,description:One jetstream input consuming github.event.issue from the GITHUB stream.,category:basic"`

	// HTTPPort is the receiver's listen port; 0 DISABLES the receiver (the
	// consumer still runs — e2e journeys publish flattened events straight
	// onto the stream).
	HTTPPort int `json:"http_port,omitempty" schema:"type:int,description:Webhook receiver port; 0 disables the HTTP receiver.,category:basic"`
	// Path is the receiver's webhook path (default /github/webhook).
	Path string `json:"path,omitempty" schema:"type:string,description:Webhook receiver path.,category:basic"`
	// WebhookSecretEnv names the env var holding the HMAC secret; empty skips
	// signature validation (dev only — log-visible).
	WebhookSecretEnv string `json:"webhook_secret_env,omitempty" schema:"type:string,description:Env var NAME holding the GitHub webhook HMAC secret; empty disables validation.,category:basic"`

	// Repo binds the lane to one "owner/name"; events for any other repo are
	// skipped before the gate (defense in depth over the webhook's own scoping).
	Repo string `json:"repo,omitempty" schema:"type:string,description:The owner/name repository this intake lane serves; other repos are skipped.,category:basic"`

	// Allowlist, OptInLabel, OptInCommand are the admission knobs (see
	// admission.Config — the spec'd gate).
	Allowlist    []string `json:"allowlist,omitempty" schema:"type:array,description:Actors always authorized (no permission call).,category:basic"`
	OptInLabel   string   `json:"opt_in_label,omitempty" schema:"type:string,description:Label that opts an issue in (default semdev).,category:basic"`
	OptInCommand string   `json:"opt_in_command,omitempty" schema:"type:string,description:Slash command that opts an issue in (default /semdev).,category:basic"`

	// Model is the model_registry capability the coordinator wake names
	// (default coordinator).
	Model string `json:"model,omitempty" schema:"type:string,description:Model capability for the coordinator wake.,category:basic"`
	// TokenEnv names the env var holding the forge token for the permission
	// check (default GITHUB_TOKEN, the dotenv lane).
	TokenEnv string `json:"token_env,omitempty" schema:"type:string,description:Env var NAME holding the forge token for collaborator permission checks.,category:basic"`
}

// Validate requires the jetstream input port — a consumer-less intake would
// start healthy and silently never admit anything (the flow break the
// constitution bars). Start SKIPS non-JetStream inputs, so the check demands a
// subject-bearing JetStream lane specifically, not just any input.
func (c *ComponentConfig) Validate() error {
	if c.Ports == nil || len(c.Ports.Inputs) == 0 {
		return errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "Validate", "ports configuration with the github events input is required")
	}
	for _, port := range c.Ports.Inputs {
		if _, _, ok := jetstreamLane(port); ok {
			return nil
		}
	}
	return errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "Validate", "no input port is a subject-bearing JetStream lane — the consumer would start and never admit anything")
}

// Schema is the generated config schema for registration.
var Schema = component.GenerateConfigSchema(reflect.TypeOf(ComponentConfig{}))

// DefaultPorts declares the issue consumer lane (github.event.issue on the GITHUB
// stream). The comment + park-post lanes moved to the conversation-channel
// component; the receiver still flattens BOTH event types onto the stream. The
// output is the admission-record birth write — the canonical typed mutation
// requester (every component that mutates the graph declares one).
func DefaultPorts() *component.PortConfig {
	return &component.PortConfig{
		Inputs: []component.PortDefinition{{
			Name:        "github_events",
			Required:    true,
			Description: "Flattened issue events (semdev's receiver or an e2e journey publishes them).",
			Config: component.JetStreamPort{
				StreamName: GithubStreamName,
				Subjects:   []string{admission.SubjectIssue},
			},
		}},
		Outputs: []component.PortDefinition{graphown.RequesterPortDefinition("The admission-record birth write (strict create through the projection client).")},
	}
}

// jetstreamLane extracts the consumer coordinates from a declared input port,
// reporting false for a port that is not a subject-bearing JetStream lane.
func jetstreamLane(port component.PortDefinition) (subject, stream string, ok bool) {
	js, isJS := port.Config.(component.JetStreamPort)
	if !isJS || len(js.Subjects) == 0 || js.Subjects[0] == "" {
		return "", "", false
	}
	return js.Subjects[0], js.StreamName, true
}

// StreamPublisher is the narrow publish surface the lanes use (the wake and the
// receiver's event publishes) — natsclient.Client satisfies it.
type StreamPublisher interface {
	PublishToStream(ctx context.Context, subject string, data []byte) error
	PublishToStreamWithMsgID(ctx context.Context, subject string, data []byte, msgID string) error
}

// Component is the issue-intake processor.
type Component struct {
	config   ComponentConfig
	nats     *natsclient.Client
	pub      StreamPublisher
	creator  EntityCreator
	checker  admission.PermissionChecker
	resolver admission.RunResolver
	platform component.PlatformMeta
	logger   *slog.Logger

	webhookSecret string

	mu         sync.RWMutex
	started    bool
	startTime  time.Time
	httpServer *http.Server

	eventsConsumed int64
	admitted       int64
	rejected       int64
	errors         int64
	lastActivity   atomic.Value // time.Time
}

var (
	_ component.Discoverable       = (*Component)(nil)
	_ component.LifecycleComponent = (*Component)(nil)
)

// NewProcessor is the component factory.
func NewProcessor(rawConfig json.RawMessage, deps component.Dependencies, clients *graphown.Clients) (component.Discoverable, error) {
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

	// The permission checker: a token via the dotenv lane enables the
	// collaborator check; without one, ONLY allowlisted actors can be
	// authorized (Decide fails closed for everyone else) — a legitimate
	// allowlist-only deployment shape, stated loud at boot.
	var checker admission.PermissionChecker
	if token := strings.TrimSpace(os.Getenv(cfg.TokenEnv)); token != "" {
		checker = github.NewClient(token).WithLogger(logger)
	} else {
		// Allowlist-only mode, chosen DELIBERATELY at boot (no token). A
		// non-allowlisted actor is then a DEFINITIVE reject — not the
		// fail-closed-retry an unavailable checker would produce (retrying can
		// never change the answer when there is no checker to recover).
		checker = admission.AllowlistOnlyChecker{}
		logger.Warn("no forge token in env; admission runs allowlist-only (collaborator checks unavailable — non-allowlisted actors are definitively rejected)",
			slog.String("token_env", cfg.TokenEnv))
	}

	// The admission birth surface: strict create under the admission-check
	// contract. The DIRECT assignment is deliberate: on the census path this is
	// an interface wrapping a typed-nil *Creator, whose nil-receiver guard
	// fails the first write with a named error — assigning conditionally would
	// leave a nil INTERFACE, and the write would panic instead of erroring.
	var creator EntityCreator = clients.Creator(RecordSource)
	c := &Component{
		config:   cfg,
		nats:     deps.NATSClient,
		pub:      deps.NATSClient,
		creator:  creator,
		checker:  checker,
		resolver: admission.NewRunResolver(deps.NATSClient, deps.Platform.Org, deps.Platform.Platform),
		platform: deps.Platform,
		logger:   logger,
	}
	return c, nil
}

func applyConfigDefaults(cfg *ComponentConfig) {
	if cfg.Path == "" {
		cfg.Path = "/github/webhook"
	}
	if cfg.OptInLabel == "" {
		cfg.OptInLabel = "semdev"
	}
	if cfg.OptInCommand == "" {
		cfg.OptInCommand = "/semdev"
	}
	if cfg.Model == "" {
		cfg.Model = "coordinator"
	}
	if cfg.TokenEnv == "" {
		cfg.TokenEnv = "GITHUB_TOKEN"
	}
}

// Register registers the issue-intake component with the component registry
// (called from boot.RegisterAll so both binaries pick it up together). clients
// supplies the admission birth surface; nil is the schema-scanning census path
// (a nil creator fails loudly if a write is ever attempted).
func Register(reg *component.Registry, clients *graphown.Clients) error {
	return reg.RegisterWithConfig(component.RegistrationConfig{
		Name: ComponentName,
		Factory: func(raw json.RawMessage, deps component.Dependencies) (component.Discoverable, error) {
			return NewProcessor(raw, deps, clients)
		},
		Schema:      Schema,
		Type:        "processor",
		Domain:      "forge-io",
		Protocol:    "webhook",
		Description: "Issue-intake front door: webhook receiver + durable issue-admission consumer; admitted issues wake the coordinator (forge-io-real-lanes).",
		Version:     "0.1.0",
	})
}

// Initialize loads the webhook secret (env indirection — the config carries
// only the NAME).
func (c *Component) Initialize() error {
	if c.config.WebhookSecretEnv != "" {
		c.webhookSecret = os.Getenv(c.config.WebhookSecretEnv)
		if c.webhookSecret == "" {
			// Fail LOUD: an operator who configured a secret env expects HMAC
			// validation; silently accepting unsigned posts would be an open door.
			return errs.WrapInvalid(errs.ErrInvalidConfig, ComponentName, "Initialize",
				fmt.Sprintf("webhook_secret_env %q is configured but empty in the environment", c.config.WebhookSecretEnv))
		}
	}
	return nil
}

// Start wires the durable consumer and (when configured) the HTTP receiver.
func (c *Component) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errs.WrapFatal(errs.ErrAlreadyStarted, ComponentName, "Start", "already started")
	}
	// Claim started inside ONE critical section (no check-then-act window); a
	// consumer-setup failure below unwinds the claim.
	c.started = true
	c.startTime = time.Now()
	c.mu.Unlock()
	fail := func(err error) error {
		c.mu.Lock()
		c.started = false
		c.mu.Unlock()
		return err
	}

	for _, port := range c.config.Ports.Inputs {
		if _, _, ok := jetstreamLane(port); !ok {
			continue
		}
		if err := c.setupConsumer(ctx, port); err != nil {
			return fail(err)
		}
	}

	if c.config.HTTPPort > 0 {
		c.startReceiver()
	}

	c.logger.Info("issue-intake started",
		slog.Bool("receiver", c.config.HTTPPort > 0),
		slog.String("repo", c.config.Repo))
	return nil
}

// setupConsumer creates the durable GITHUB-stream consumer (the agentic-tools
// consumer shape: bounded redelivery, heartbeat-acked work).
func (c *Component) setupConsumer(ctx context.Context, port component.PortDefinition) error {
	subject, streamName, _ := jetstreamLane(port)
	if streamName == "" {
		streamName = GithubStreamName
	}
	resolved, err := port.Resolve(component.DirectionInput)
	if err != nil {
		return errs.WrapInvalid(err, ComponentName, "Start", "resolve input port "+port.Name)
	}
	consumerCfg, err := component.GetConsumerConfig(resolved)
	if err != nil {
		return errs.WrapInvalid(err, ComponentName, "Start", "consumer config for "+port.Name)
	}
	// max_deliver is set DELIBERATELY (review finding): the framework default
	// of 3 gives the intake retry case only ~2 retries. ConsumeWithHeartbeat
	// naks with a fixed 30s delay (its own contract — a BackOff list here would
	// be dead config), so 10 deliveries ≈ 4.5 minutes of retry budget.
	// MessageTimeout bounds one handler run ABOVE the worst case (the run
	// resolver's bounded pagination); the 20s heartbeat keeps a slow handler
	// acked-in-progress.
	maxDeliver := consumerCfg.MaxDeliver
	if maxDeliver == 0 || maxDeliver == 3 {
		maxDeliver = 10
	}
	cfg := natsclient.StreamConsumerConfig{
		StreamName:     streamName,
		ConsumerName:   "issue-intake-" + port.Name,
		FilterSubject:  subject,
		DeliverPolicy:  consumerCfg.DeliverPolicy,
		AckPolicy:      consumerCfg.AckPolicy,
		MaxDeliver:     maxDeliver,
		AckWait:        time.Minute,
		MaxAckPending:  8,
		AutoCreate:     false,
		MessageTimeout: 3 * time.Minute,
	}
	err = c.nats.ConsumeStreamWithConfig(ctx, cfg, func(msgCtx context.Context, msg jetstream.Msg) {
		if hbErr := natsclient.ConsumeWithHeartbeat(msgCtx, msg, 20*time.Second, func(workCtx context.Context) error {
			return c.handleEvent(workCtx, msg.Subject(), msg.Data())
		}); hbErr != nil {
			c.logger.Error("intake event handler error", slog.Any("error", hbErr))
		}
	})
	if err != nil {
		return errs.WrapTransient(err, ComponentName, "Start", fmt.Sprintf("consumer setup for %s on %s", subject, streamName))
	}
	return nil
}

// handleEvent dispatches one flattened event to the issue lane. A nil return ACKS
// (definitive outcome — admitted, rejected, skipped, malformed); an error return
// NAKS for bounded redelivery (transient faults only: a permission lookup that
// failed, a graph/publish blip — the fail-closed retry the admission spec demands).
func (c *Component) handleEvent(ctx context.Context, subject string, payload []byte) error {
	atomic.AddInt64(&c.eventsConsumed, 1)
	c.lastActivity.Store(time.Now())
	if subject == admission.SubjectIssue {
		return c.handleIssueEvent(ctx, payload)
	}
	c.logger.Debug("intake: irrelevant event subject; skipping", slog.String("subject", subject))
	return nil
}

// handleIssueEvent runs the intake lane: Normalize → gate → record → wake.
func (c *Component) handleIssueEvent(ctx context.Context, payload []byte) error {
	in, err := Normalize(admission.SubjectIssue, payload)
	if err != nil {
		// Malformed payloads are logged and ACKED — redelivering garbage
		// forever is a poison loop, and the receiver only publishes shapes it
		// flattened itself.
		atomic.AddInt64(&c.errors, 1)
		c.logger.Error("intake: malformed issue event; skipping", slog.Any("error", err))
		return nil
	}
	if !in.Relevant {
		return nil
	}
	if c.config.Repo != "" && !strings.EqualFold(strings.TrimSpace(c.config.Repo), refRepo(in.IssueRef)) {
		c.logger.Debug("intake: event for unbound repo; skipping", slog.String("ref", in.IssueRef), slog.String("bound", c.config.Repo))
		return nil
	}

	decision, err := admission.Decide(ctx, c.admissionConfig(), in.Event, c.checker)
	if err != nil {
		// Fail closed AND retry: a transient permission-lookup fault must not
		// admit, and must not permanently reject an authorized actor.
		atomic.AddInt64(&c.errors, 1)
		c.logger.Warn("intake: authorization undetermined; redelivering", slog.String("ref", in.IssueRef), slog.Any("error", err))
		return err
	}
	if !decision.Admitted {
		atomic.AddInt64(&c.rejected, 1)
		c.logger.Info("intake: event rejected (zero tokens)",
			slog.String("ref", in.IssueRef), slog.String("actor", decision.Actor), slog.String("reason", decision.Reason))
		return nil
	}

	// Record FIRST (the content-derived ID is the idempotency backstop), wake
	// second. ErrAlreadyRecorded ⇒ this delivery was processed before — but
	// "before" includes the crash-between-record-and-wake window AND a wake
	// publish that failed and was redelivered, so DISCRIMINATE via the run:
	// if a run already carries this ref, this is a genuine duplicate (skip);
	// if NO run exists, the wake never took — publish it now (msg-id = the
	// record ID, so JetStream dedup absorbs an in-window double). This is what
	// makes the wake-publish NAK below an actual retry rather than a dead end
	// (review finding).
	deliveryID := eventDeliveryID(payload)
	recordID := AdmissionRecordEntityID(c.platform.Org, c.platform.Platform, in.IssueRef, deliveryID)
	if err := RecordAdmission(ctx, c.creator, recordID, decision.Actor, in.IssueRef); err != nil {
		if errors.Is(err, ErrAlreadyRecorded) {
			run, rerr := c.resolver.ResolveRunByRef(ctx, in.IssueRef)
			if rerr != nil {
				atomic.AddInt64(&c.errors, 1)
				return fmt.Errorf("intake: recorded admission but could not check for the run: %w", rerr) // redeliver
			}
			if run.EntityID != "" {
				c.logger.Info("intake: admission already recorded and the run exists; duplicate delivery skipped",
					slog.String("ref", in.IssueRef), slog.String("run", run.EntityID))
				return nil
			}
			c.logger.Warn("intake: admission recorded but NO run carries the ref — the wake never took (crash window or failed publish); re-publishing the wake",
				slog.String("ref", in.IssueRef), slog.String("record", recordID))
			// fall through to the wake publish below
		} else {
			atomic.AddInt64(&c.errors, 1)
			return err // transient graph fault — redeliver
		}
	}

	task, err := CoordinatorTask(*in, c.config.Model)
	if err != nil {
		atomic.AddInt64(&c.errors, 1)
		c.logger.Error("intake: could not build coordinator wake", slog.String("ref", in.IssueRef), slog.Any("error", err))
		return nil // a build failure is content-shaped, not transient
	}
	base := message.NewBaseMessage(task.Schema(), task, ComponentName)
	data, err := json.Marshal(base)
	if err != nil {
		atomic.AddInt64(&c.errors, 1)
		c.logger.Error("intake: could not marshal wake envelope", slog.Any("error", err))
		return nil
	}
	// Msg-ID = the admission record ID: JetStream dedup absorbs a re-publish
	// of the same wake inside the dedup window (a second layer under the
	// record + run-existence discrimination above).
	if err := c.pub.PublishToStreamWithMsgID(ctx, FrontDoorSubject, data, recordID); err != nil {
		atomic.AddInt64(&c.errors, 1)
		// Redeliver: the next attempt hits ErrAlreadyRecorded, finds NO run,
		// and re-publishes — the retry genuinely retries (review finding).
		return fmt.Errorf("intake: publish coordinator wake: %w", err)
	}
	atomic.AddInt64(&c.admitted, 1)
	c.logger.Info("intake: issue admitted; coordinator woken",
		slog.String("ref", in.IssueRef), slog.String("actor", decision.Actor), slog.String("record", recordID))
	return nil
}

func (c *Component) admissionConfig() admission.Config {
	return admission.Config{
		Allowlist:    c.config.Allowlist,
		OptInLabel:   c.config.OptInLabel,
		OptInCommand: c.config.OptInCommand,
	}
}

// refRepo returns the "owner/repo" half of an "owner/repo#number" ref.
func refRepo(ref string) string {
	if i := strings.LastIndex(ref, "#"); i > 0 {
		return ref[:i]
	}
	return ref
}

// eventDeliveryID pulls the receiver-stamped delivery GUID off a flattened
// event without caring which event type it is.
func eventDeliveryID(payload []byte) string {
	var probe struct {
		DeliveryID string `json:"delivery_id"`
	}
	_ = json.Unmarshal(payload, &probe)
	return probe.DeliveryID
}

// The github client satisfies the admission gate's checker contract — asserted
// here (not in the github package: that direction would be an import cycle).
var _ admission.PermissionChecker = (*github.Client)(nil)

// --- the HTTP receiver ---

// startReceiver starts the webhook listener. Failures binding the port fail
// loud via the error log + errors counter; the consumer half keeps running.
func (c *Component) startReceiver() {
	mux := http.NewServeMux()
	mux.HandleFunc(c.config.Path, c.handleWebhook)
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", c.config.HTTPPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// A slow-body client must not hold a handler goroutine open (review
		// finding) — GitHub delivers promptly; 30s is generous.
		ReadTimeout: 30 * time.Second,
	}
	c.mu.Lock()
	c.httpServer = srv
	c.mu.Unlock()
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			atomic.AddInt64(&c.errors, 1)
			c.logger.Error("issue-intake webhook receiver failed", slog.Any("error", err))
		}
	}()
	c.logger.Info("issue-intake webhook receiver listening",
		slog.Int("port", c.config.HTTPPort), slog.String("path", c.config.Path),
		slog.Bool("hmac", c.webhookSecret != ""))
}

// handleWebhook is the receiver: validate → filter → flatten → publish. It flattens
// BOTH issues and issue_comment events (GitHub delivers all event types to one URL,
// conversation-channel-seam B-1) — a comment event still reaches the
// conversation-channel component's own consumer on the GITHUB stream.
func (c *Component) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// MaxBytesReader (not a silent LimitReader truncation): an oversize body
	// surfaces as 413, never as a mystifying HMAC 401 over truncated bytes.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if c.webhookSecret != "" && !validSignature(c.webhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		atomic.AddInt64(&c.errors, 1)
		c.logger.Warn("issue-intake: webhook signature invalid; rejected")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	now := time.Now().UTC()

	var subject string
	var flattened any
	switch eventType {
	case "issues":
		ev, ferr := githubwebhook.FlattenIssueEvent(body, deliveryID, now)
		if ferr != nil {
			c.logger.Error("issue-intake: could not flatten issues payload", slog.Any("error", ferr))
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		subject, flattened = admission.SubjectIssue, ev
	case "issue_comment":
		ev, ferr := githubwebhook.FlattenCommentEvent(body, deliveryID, now)
		if ferr != nil {
			c.logger.Error("issue-intake: could not flatten issue_comment payload", slog.Any("error", ferr))
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		subject, flattened = admission.SubjectComment, ev
	default:
		// Not a lane we consume (ping, stars, …) — accepted and dropped.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	data, err := json.Marshal(flattened)
	if err != nil {
		c.logger.Error("issue-intake: could not marshal flattened event", slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Msg-ID = the delivery GUID: JetStream dedup absorbs GitHub redeliveries
	// at the stream layer before the consumer ever sees a duplicate.
	if err := c.pub.PublishToStreamWithMsgID(r.Context(), subject, data, deliveryID); err != nil {
		atomic.AddInt64(&c.errors, 1)
		c.logger.Error("issue-intake: could not publish event to the GITHUB stream", slog.Any("error", err))
		// 500 marks the delivery FAILED in GitHub's webhook UI; recovery is a
		// MANUAL redeliver there (GitHub does not auto-retry) — honest and
		// visible, chosen over local buffering (review finding: state this
		// plainly, it is the load-bearing justification).
		http.Error(w, "publish failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// validSignature checks the GitHub HMAC (X-Hub-Signature-256: "sha256=<hex>").
func validSignature(secret, header string, body []byte) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(strings.ToLower(strings.TrimSpace(sig))))
}

// --- lifecycle + discovery boilerplate ---

// Stop shuts the receiver down; the JetStream consumer unwinds with the client.
func (c *Component) Stop(timeout time.Duration) error {
	c.mu.Lock()
	srv := c.httpServer
	c.httpServer = nil
	c.started = false
	c.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return nil
}

// Meta implements Discoverable.
func (c *Component) Meta() component.Metadata {
	return component.Metadata{Name: ComponentName, Type: "processor", Description: "semdev issue-intake front door (webhook receiver + issue-admission consumer)", Version: "0.1.0"}
}

// InputPorts implements Discoverable.
func (c *Component) InputPorts() []component.Port {
	return resolvePorts(c.config.Ports.Inputs, component.DirectionInput)
}

// OutputPorts implements Discoverable: the declared graph-mutation requester
// (the admission birth write). The wake publish stays a stream write, not a
// declared output port (it mirrors the journey's front-door publish).
func (c *Component) OutputPorts() []component.Port {
	return resolvePorts(c.config.Ports.Outputs, component.DirectionOutput)
}

// resolvePorts resolves declared definitions for discovery. A definition that
// fails strict resolution is dropped here because Discoverable has no error
// channel. Inputs are re-resolved LOUDLY by Start's consumer setup; the output
// requester's guard is narrower — it is a compile-time constant
// (graphown.RequesterPortDefinition) proven by the real-NATS boot, so a drop
// here can only follow a framework port-rule change.
func resolvePorts(defs []component.PortDefinition, dir component.Direction) []component.Port {
	ports := make([]component.Port, 0, len(defs))
	for _, d := range defs {
		p, err := d.Resolve(dir)
		if err != nil {
			continue
		}
		ports = append(ports, p)
	}
	return ports
}

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
