package graphown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/pkg/ownership"
	"github.com/c360studio/semstreams/pkg/projection"
)

// Clients is the composition root's bound-owner registry: the ONE place that maps a
// vocab Source to its contract-bound mutation client.
//
// It exists because ADR-056 binds a client to exactly ONE owner
// (MutationClientConfig.Owner is singular), while semdev's old model injected a
// SINGLE shared OwnedFactWriter into every tool that stamped under its own Source.
// Three call sites write as TWO owners each — check_floors (floor-tools +
// route-mirror), submit_review (reviewer-quinn + route-mirror), and the approval
// adapter (approval-adapter + conversation-adapter) — so they take two writers, not
// one client that guesses.
//
// A nil *Clients is VALID and yields nil writers: that is the schema-scanning census
// path (no NATS client), where a tool registers schema-only and fails loudly if a
// write is ever attempted. This is why Writer must return a nil *Writer rather than
// a Writer wrapping a nil client — tools guard on `writer == nil`, and the wrapper
// would pass that guard and fail later, at the write.
type Clients struct {
	byOwner map[string]*projection.MutationClient
}

// Writer returns owner's bound write surface, or nil when nothing is bound (the
// census path, or an owner the composition root did not bind).
func (c *Clients) Writer(owner string) *Writer {
	client := c.client(owner)
	if client == nil {
		return nil
	}
	return NewWriter(owner, client)
}

// ReadWriter is Writer plus the authoritative read-back, for the two
// shrinking-package sites (check_floors' findings clear, project_tasks'
// immutability gate). One MutationClient serves both roles.
func (c *Clients) ReadWriter(owner string) *Writer {
	client := c.client(owner)
	if client == nil {
		return nil
	}
	return NewReadWriter(owner, client, client)
}

func (c *Clients) client(owner string) *projection.MutationClient {
	if c == nil {
		return nil
	}
	return c.byOwner[owner]
}

// Bound reports the owners with a live client, sorted.
func (c *Clients) Bound() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.byOwner))
	for o := range c.byOwner {
		out = append(out, o)
	}
	slices.Sort(out)
	return out
}

// RequireBound asserts every owner in want has a live client, returning a named
// error listing the misses. It is the boot census D5 task 6.1(a) calls for: an owner
// that failed to bind, or a typo'd Source at a call site, otherwise surfaces only as
// a nil writer at the FIRST WRITE — deep inside a station handler, where several
// paths can do nothing but log. Fail at boot instead.
func (c *Clients) RequireBound(want ...string) error {
	var missing []string
	for _, owner := range want {
		if c.client(owner) == nil {
			missing = append(missing, owner)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return fmt.Errorf(
		"graphown: %d owner(s) have no bound mutation client: %v — a write under one would fail at its first call site, not here",
		len(missing), missing,
	)
}

// ErrOwnersAlreadyBoundInProcess is returned by a SECOND bind of an owner this
// process already bound. See boundInProcess for why that is fatal rather than
// idempotent.
var ErrOwnersAlreadyBoundInProcess = errors.New("graphown: owners already bound in this process")

// boundInProcess tracks every owner this process has bound, because the framework's
// own duplicate guard cannot see across calls and a second bind is DESTRUCTIVE.
//
// ownership.NewRegistry mints a fresh crypto/rand incarnation per Registry
// (registry.go:118-140), and RegisterOwner unconditionally deletes the owner's epoch
// entry and re-inserts it stamped with that NEW incarnation (registry.go:379-390) —
// there is no liveness check on the prior holder. Meanwhile a MutationClient
// captures its token once at bind and never refreshes it
// (mutation_client.go:91-102). So a second registration of an owner someone already
// holds permanently invalidates the first holder's token: every subsequent owned
// write mismatches, which under observe-only is a warn + meter tick per predicate
// per write, and under enforce_owner_lease is a hard reject of the whole write path.
//
// ErrOwnerAlreadyBound does NOT protect against this — it is per-Registry in-memory
// state (registry.go:313-320), and each bind call builds a new Registry. This guard
// is the in-process half. The CROSS-process half cannot be enforced from here and is
// a deployment discipline: see BindOwners' doc.
var boundInProcess = struct {
	mu     sync.Mutex
	owners map[string]bool
}{owners: map[string]bool{}}

// BindAll binds EVERY derived owner. Use it from the long-running runtime, which
// hosts every writer. A short-lived command must use BindOwners with just the owners
// it writes — see that doc for why binding extra owners is actively harmful.
func BindAll(ctx context.Context, nc *natsclient.Client, logger *slog.Logger) (*Clients, error) {
	owners, err := Owners()
	if err != nil {
		return nil, err
	}
	return BindOwners(ctx, nc, logger, owners...)
}

// BindOwners is the composition root's ownership setup, run ONCE per process before
// any owner writes: ensure the ownership buckets, start one process-lifetime
// heartbeater, and bind each named owner to its COMPLETE contract set.
//
// # Bind only what you write
//
// Registering an owner is not free and not idempotent — it SUPERSEDES whoever held
// it (see boundInProcess). So a process must bind exactly the owners it stamps facts
// as, and no more. The long-running runtime binds all of them; `semdev launch`
// writes exactly one predicate (experiment.run.condition) and binds only
// experiment-intake. Binding all 17 from the CLI — as an earlier draft did — leaves a
// live runtime permanently fenced out of its own write path, with no way to notice:
// the runtime binds once at boot and never re-registers.
//
// The residual cross-process hazard is deliberate and bounded: `semdev launch` still
// supersedes the runtime's experiment-intake token. That is harmless because the
// runtime never writes experiment.run.condition (StampCondition has one caller, the
// launch path), so no runtime write ever presents the stale token. Any FUTURE
// short-lived command must make the same argument explicitly before it binds.
//
// ctx owns the heartbeater's lifetime — started here, stops on cancellation. The
// heartbeat is not optional for owning owners: RegisterOwner creates the
// OWNER_PRESENCE key, but without ongoing beats it ages out after
// ownership.PresenceTTL and the next registrant compacts the owning entry out of the
// epoch.
func BindOwners(ctx context.Context, nc *natsclient.Client, logger *slog.Logger, owners ...string) (*Clients, error) {
	if nc == nil {
		return nil, fmt.Errorf("graphown: NATS client is required to bind projection owners")
	}
	if len(owners) == 0 {
		return nil, fmt.Errorf("graphown: no owners to bind")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := claimInProcess(owners); err != nil {
		return nil, err
	}

	registry, err := ownership.EnsureBuckets(ctx, nc, logger, nil)
	if err != nil {
		releaseInProcess(owners)
		return nil, fmt.Errorf("graphown: ensure ownership buckets: %w", err)
	}
	heartbeater := registry.NewHeartbeater(ownership.HeartbeatInterval)
	go heartbeater.Run(ctx)

	out := &Clients{byOwner: make(map[string]*projection.MutationClient, len(owners))}
	for _, owner := range owners {
		contracts, cerr := ContractsFor(owner)
		if cerr != nil {
			releaseInProcess(owners)
			return nil, cerr
		}
		client, berr := projection.BindMutationClient(ctx, projection.MutationClientConfig{
			NATS:        nc,
			Registry:    registry,
			Heartbeater: heartbeater,
			Owner:       owner,
			Contracts:   contracts,
			// The DELETED OwnedFactWriter drove every write through
			// RequestWithRetryClassified(DefaultRetryConfig) precisely to ride out
			// transient no-responders while graph-ingest restarts or its subscription
			// propagates. MutationClientConfig's zero Retry means MaxRetries 0 —
			// normalizeRetryConfig only clamps negatives, it does not default — so
			// leaving it unset would silently drop that retry and turn a graph-ingest
			// blip into a lost station.dispatch.failed stamp (a run that stalls instead
			// of parking, since that path can only log).
			Retry: natsclient.DefaultRetryConfig(),
		})
		if berr != nil {
			releaseInProcess(owners)
			return nil, fmt.Errorf("graphown: bind projection owner %q (%d contracts): %w", owner, len(contracts), berr)
		}
		out.byOwner[owner] = client
	}
	logger.Info("bound semdev projection owners",
		slog.Int("owners", len(owners)),
		slog.Any("bound", out.Bound()),
		slog.Duration("heartbeat_interval", ownership.HeartbeatInterval))
	return out, nil
}

func claimInProcess(owners []string) error {
	boundInProcess.mu.Lock()
	defer boundInProcess.mu.Unlock()
	var dupes []string
	for _, o := range owners {
		if boundInProcess.owners[o] {
			dupes = append(dupes, o)
		}
	}
	if len(dupes) > 0 {
		slices.Sort(dupes)
		return fmt.Errorf(
			"%w: %v — a second registration mints a NEW incarnation and invalidates the first bind's token, "+
				"so every write from the earlier client would mismatch its lease (share the existing *Clients instead)",
			ErrOwnersAlreadyBoundInProcess, dupes,
		)
	}
	for _, o := range owners {
		boundInProcess.owners[o] = true
	}
	return nil
}

func releaseInProcess(owners []string) {
	boundInProcess.mu.Lock()
	defer boundInProcess.mu.Unlock()
	for _, o := range owners {
		delete(boundInProcess.owners, o)
	}
}

// ResetInProcessBindingsForTest clears the in-process bind ledger. Test-only: a test
// binary that boots several runtimes in sequence legitimately re-binds, whereas
// production binds once. Never call it from product code.
func ResetInProcessBindingsForTest() {
	boundInProcess.mu.Lock()
	defer boundInProcess.mu.Unlock()
	boundInProcess.owners = map[string]bool{}
}
