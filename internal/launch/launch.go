// Package launch is the operator launch driver (self-target-provisioning-and-launch-driver,
// design D6 / capability operator-launch): the durable production front door BESIDE the webhook
// intake. It mints a run through the SANCTIONED experiment.Launch seam (probe→publish→bind→
// stamp) — reading the issue's authored content from the forge so the wake is never
// content-empty (review H1), publishing the coordinator wake in the SAME shape the webhook
// produces, binding the run it minted by set-difference observation (review M2), and stamping
// the operator-declared A/B condition. It fires NO lifecycle transition from Go (G2): the run is
// minted downstream by the coordinator rule's run_scope=new spawn; the driver binds by a graph
// READ. The e2e journeys' hand-composed mint stays a pinned exemption; this is production.
//
// ADMISSION BYPASS (honest note, G10): unlike the webhook door, this door publishes STRAIGHT to
// the front door — it runs NO zero-token admission gate (authorize + opt-in) and writes NO
// intake.actor.admitted record. That is deliberate: the operator invoking the CLI already holds
// the shell and the forge token, so the actor-authorization the webhook enforces is moot here. A
// launched run therefore carries no admission record and no authorize/opt-in check; the per-ref
// idempotency guard below (not the admission record) is what stops a duplicate mint.
package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semstreams/message"
)

// launchSource labels the wake's BaseMessage origin — the operator front door, distinct from
// the webhook intake's source.
const launchSource = "operator-launch"

// IssueReader reads an issue's authored content — github.Client satisfies it.
type IssueReader interface {
	GetIssue(ctx context.Context, owner, repo string, number int) (github.Issue, error)
}

// Publisher publishes the front-door wake — natsclient.Client satisfies it.
type Publisher interface {
	PublishToStream(ctx context.Context, subject string, data []byte) error
}

// RunSetResolver lists every run entity ID carrying run.issue.ref == ref — intake.NewRunResolver
// satisfies it. The driver diffs this set across its publish to bind the run it minted.
type RunSetResolver interface {
	ResolveRunIDsByRef(ctx context.Context, ref string) ([]string, error)
}

// Deps are the driver's collaborators (all faked in unit pins).
type Deps struct {
	Issues   IssueReader
	Pub      Publisher
	Resolver RunSetResolver
	Writer   experiment.FactWriter
	// Probe is the semsource per-signal readiness check; REQUIRED for the semsource condition,
	// nil for baseline (experiment.Launch enforces this exclusivity).
	Probe  func(context.Context) error
	Logger *slog.Logger
}

// Params configure one launch.
type Params struct {
	// IssueRef is the host-neutral "owner/repo#number" the run develops.
	IssueRef string
	// Model is the coordinator model (config/flag), threaded into the wake.
	Model string
	// Condition is the A/B experiment config; the zero value is baseline (stamps nothing).
	Condition experiment.Config
	// BindTimeout bounds the wait for the coordinator to mint the run (default 60s).
	BindTimeout time.Duration
	// BindPoll is the graph poll interval while binding (default 2s).
	BindPoll time.Duration
	// Force overrides the per-ref idempotency guard — mint even when a run already carries the
	// ref. Off by default: the operator door mirrors the webhook's run-existence guard
	// (component.go:441-457) so a double-launch does not silently spawn a competing run.
	Force bool
}

// Launch mints a run for the issue via experiment.Launch and returns the bound run entity ID.
func Launch(ctx context.Context, d Deps, p Params) (string, error) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	owner, repo, number, err := intake.SplitRef(p.IssueRef)
	if err != nil {
		return "", fmt.Errorf("launch: %w", err)
	}

	// Read the issue's authored content up front — a launch must NEVER proceed against an empty
	// ask (review H1). Body-only, MATCHING the webhook's normalize (AuthoredText = Issue.Body),
	// so the operator and webhook front doors produce byte-identical wake content.
	iss, err := d.Issues.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("launch: read issue %s: %w", p.IssueRef, err)
	}

	// Set-difference bind (review M2): snapshot the runs already carrying this ref BEFORE
	// publishing, so bindRun binds the run THIS launch mints — not a pre-existing one (e.g. from
	// a prior webhook wake). Populated inside publish, which experiment.Launch calls before
	// bindRun, so the snapshot is immediately pre-publish.
	preExisting := map[string]bool{}

	publish := func(ctx context.Context) error {
		ids, err := d.Resolver.ResolveRunIDsByRef(ctx, p.IssueRef)
		if err != nil {
			return fmt.Errorf("launch: snapshot existing runs for %s: %w", p.IssueRef, err)
		}
		for _, id := range ids {
			preExisting[id] = true
		}
		// Per-ref idempotency guard (review HIGH): the webhook door suppresses a duplicate run
		// when the ref already has one; the operator door must not silently mint a competitor.
		// Fail closed unless Force — a second run for one ref means nondeterministic approval
		// and two PRs.
		if len(ids) > 0 && !p.Force {
			return fmt.Errorf("launch: %d run(s) already carry %s (%v) — refusing to mint a duplicate; pass --force to override", len(ids), p.IssueRef, ids)
		}
		in := intake.Intake{Relevant: true, IssueRef: p.IssueRef}
		in.Event.AuthoredText = iss.Body
		task, err := intake.CoordinatorTask(in, p.Model)
		if err != nil {
			return fmt.Errorf("launch: build wake for %s: %w", p.IssueRef, err)
		}
		base := message.NewBaseMessage(task.Schema(), task, launchSource)
		data, err := json.Marshal(base)
		if err != nil {
			return fmt.Errorf("launch: marshal wake: %w", err)
		}
		if err := d.Pub.PublishToStream(ctx, intake.FrontDoorSubject, data); err != nil {
			return fmt.Errorf("launch: publish wake for %s: %w", p.IssueRef, err)
		}
		d.Logger.Info("operator launch published the front-door wake",
			slog.String("ref", p.IssueRef), slog.String("condition", p.Condition.Condition))
		return nil
	}

	bindRun := func(ctx context.Context) (string, error) {
		poll := p.BindPoll
		if poll <= 0 {
			poll = 2 * time.Second
		}
		timeout := p.BindTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		deadline := time.Now().Add(timeout)
		for {
			ids, err := d.Resolver.ResolveRunIDsByRef(ctx, p.IssueRef)
			if err != nil {
				return "", fmt.Errorf("launch: bind run for %s: %w", p.IssueRef, err)
			}
			var fresh []string
			for _, id := range ids {
				if !preExisting[id] {
					fresh = append(fresh, id)
				}
			}
			switch {
			case len(fresh) == 1:
				return fresh[0], nil
			case len(fresh) > 1:
				// A concurrent front door (a webhook) raced this launch between the snapshot
				// and now; binding one nondeterministically would mislabel the condition
				// (review MEDIUM). Fail closed rather than guess.
				return "", fmt.Errorf("launch: ambiguous bind — %d new runs appeared for %s (%v); a concurrent front door raced this launch", len(fresh), p.IssueRef, fresh)
			}
			if time.Now().After(deadline) {
				// FAIL LOUD without stamping — never invent or advance a run (G2, fail-closed).
				return "", fmt.Errorf("launch: no run bound to %s within %s — the mint did not complete", p.IssueRef, timeout)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(poll):
			}
		}
	}

	return experiment.Launch(ctx, p.Condition, d.Probe, publish, bindRun, d.Writer)
}
