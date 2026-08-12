package conversationchannel

import (
	"context"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/forge/conversation"
)

// The POLLER (pull-first-transport D2) — the pull-first inbound transport. A
// webhook-unreachable deployment (dev box / behind NAT) cannot receive pushed
// comment events, so it drives the /semdev approve gate by POLLING: each interval it
// enumerates the runs AWAITING APPROVAL, reads each run's thread, and feeds fresh
// Messages to the SAME approval core the webhook consumer uses (D3). It fires no
// lifecycle transition and stamps no new fact (the core owns the stand-in write, G2);
// it is fail-safe by construction — idempotent (re-read replays a no-op against the
// core's alreadyApproved guard), bounded (interval + ListComments pagination),
// read-only (the enumeration + ListComments), and cursor-in-memory (B-2, not a fact).

// awaitingRunLister enumerates the run.issue.ref of every run at the change-approval
// gate (agent.run.phase == awaiting_approval), repo-scoped. admission.NATSRunResolver
// satisfies it; a fake drives the poller in unit pins.
type awaitingRunLister interface {
	ListRunsAwaitingApproval(ctx context.Context, repo string) ([]string, error)
}

// messageHandler is the shared approval core the poller feeds — the approval
// adapter's handleMessage (D3). It authorizes msg.Author and stamps the approval; a
// non-nil error is transient (the poller re-reads the message next tick).
type messageHandler func(ctx context.Context, msg conversation.Message, thread conversation.ThreadRef) error

// poller drives the pull-first inbound transport. Its cursor map is owned by the
// single goroutine run() spawns (no mutex needed) and is pruned each tick to the
// current awaiting set (L2), so it does not grow unbounded across run lifetimes.
type poller struct {
	channel  conversation.Channel
	lister   awaitingRunLister
	handle   messageHandler
	repo     string
	interval time.Duration
	logger   *slog.Logger

	cursors map[conversation.ThreadRef]conversation.Cursor
}

func newPoller(channel conversation.Channel, lister awaitingRunLister, handle messageHandler, repo string, interval time.Duration, logger *slog.Logger) *poller {
	return &poller{
		channel:  channel,
		lister:   lister,
		handle:   handle,
		repo:     repo,
		interval: interval,
		logger:   logger,
		cursors:  map[conversation.ThreadRef]conversation.Cursor{},
	}
}

// run polls until ctx is cancelled (the component cancels it on Stop — M5). It ticks
// immediately (low first-approval latency), then on the interval. The loop never
// crashes: a tick's transport faults are logged and retried next interval.
func (p *poller) run(ctx context.Context) {
	p.tick(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// tick runs one poll pass: enumerate awaiting-approval runs, read each thread, feed
// fresh Messages to the core. A tick never blocks or crashes the loop.
func (p *poller) tick(ctx context.Context) {
	refs, err := p.lister.ListRunsAwaitingApproval(ctx, p.repo)
	if err != nil {
		// A TRANSPORT ERROR is retried next tick — NEVER conflated with an empty
		// result (H2). Conflating them means a persistent graph fault presents as
		// "nothing awaiting" and the poller idles forever, stranding the gate. We do
		// NOT prune cursors here: the awaiting set is unknown, so keep every cursor.
		p.logger.Warn("poll: enumerate awaiting-approval runs failed; retrying next tick", slog.Any("error", err))
		return
	}

	active := make(map[conversation.ThreadRef]bool, len(refs))
	for _, ref := range refs {
		thread, rerr := p.channel.ResolveThread(ctx, ref)
		if rerr != nil {
			// This continue is BEFORE active[thread]=true, so a resolve fault lets the
			// thread's cursor be pruned this tick — unlike the Read-error path below,
			// which keeps it. That asymmetry is unreachable for GitHub v1 (ResolveThread
			// is pure identity, never errors) and fail-safe if a networked resolve
			// (Slack) ever errors: the next tick re-reads from cursor 0 and re-applies
			// as a no-op at the core (alreadyApproved). Revisit if that cost matters.
			p.logger.Warn("poll: resolve thread failed; skipping this tick", slog.String("ref", ref), slog.Any("error", rerr))
			continue
		}
		active[thread] = true

		msgs, next, rerr := p.channel.Read(ctx, thread, p.cursors[thread])
		if rerr != nil {
			// Read error — keep the cursor and retry next tick (not empty; H2). Read
			// returns the input cursor on error, so the place is preserved either way.
			p.logger.Warn("poll: read thread failed; retrying next tick", slog.String("ref", ref), slog.Any("error", rerr))
			continue
		}

		// Advance the cursor ONLY after every message handles cleanly, so a transient
		// handle fault (e.g. the approval racing the mint) re-reads the same messages
		// next tick rather than skipping past them — the re-apply is a no-op at the
		// core (alreadyApproved).
		handledAll := true
		for _, msg := range msgs {
			if herr := p.handle(ctx, msg, thread); herr != nil {
				p.logger.Warn("poll: approval handler errored; re-reading next tick",
					slog.String("ref", ref), slog.String("msg_id", msg.ID), slog.Any("error", herr))
				handledAll = false
				break
			}
		}
		if handledAll {
			p.cursors[thread] = next
		}
	}

	// Prune cursors to the current awaiting set (L2): a run that left the gate
	// (approved → executing) drops out of the enumeration, so its cursor is removed.
	for thread := range p.cursors {
		if !active[thread] {
			delete(p.cursors, thread)
		}
	}
}
