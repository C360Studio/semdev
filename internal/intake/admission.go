// Package intake is the deterministic, zero-token admission gate at semdev's front
// door: it decides whether a code-host event is allowed to create a run and spend
// budget, BEFORE any run exists and before any paid LLM token is spent. An event
// is admitted only if its actor is AUTHORIZED (a repo collaborator who can push,
// or a member of an explicit allowlist) AND the work is explicitly OPTED IN (a
// `semdev` label or a `/semdev` command). Everything else is rejected
// deterministically and, by default, silently — the non-adversarial-posture door
// the sandbox threat model assumes (design D10).
//
// The gate is host-NEUTRAL: it decides over a normalized Event that the host
// adapter fills from the webhook payload, so no host-specific field reaches this
// logic. It spends ZERO LLM tokens and makes AT MOST ONE GitHub API call (the
// collaborator permission check) — skipped entirely for an allowlisted actor, so
// the common allowlist path is also zero-network. The check FAILS CLOSED: a
// permission-lookup error yields "not admitted" AND surfaces the error, so a
// transient host outage neither admits an unauthorized actor nor permanently
// rejects an authorized one (the caller retries).
//
// This is the pure decision core (like internal/openspec / internal/changefacts):
// it stamps no facts. The intake adapter COMPONENT (which subscribes to the
// webhook events, normalizes them, calls this, and stamps intake.actor /
// intake.admitted) is the registered, host-aware surface built on top.
package intake

import (
	"context"
	"fmt"
	"strings"
)

// Config is the deterministic admission policy. Allowlist and the opt-in signals
// are the only knobs; all are zero-token to evaluate.
type Config struct {
	// Allowlist is the set of code-host actors (logins) always authorized,
	// matched case-insensitively. An allowlisted actor needs NO permission call.
	Allowlist []string
	// OptInLabel is the label that opts a work item in (e.g. "semdev").
	OptInLabel string
	// OptInCommand is the slash-command token that opts a work item in (e.g.
	// "/semdev"), matched as a whole whitespace-delimited token in the event text.
	OptInCommand string
}

// Event is the normalized, host-neutral intake event the gate decides over. The
// host adapter fills it from the webhook payload; the gate never sees a
// host-specific shape.
//
// LOAD-BEARING INVARIANT (the spec's "opted in ... applied BY an authorized
// actor"): AppliedLabels and AuthoredText MUST carry ONLY the opt-in signal that
// THIS event's Actor applied IN THIS EVENT — the label the actor just added (the
// `labeled` action's single label, or the initial labels an opener created the
// issue with) and the text the actor just authored (the issue/PR body they opened
// or the comment they posted). They MUST NOT be populated from the item's
// aggregate CURRENT state (every label now on the issue) or another actor's text.
// Otherwise an authorized actor's unrelated event would inherit a DIFFERENT
// (possibly unauthorized) actor's opt-in signal and be wrongly admitted — a
// privilege-confusion hole. The gate binds authorization and opt-in to the same
// Actor ONLY because the adapter component honors this invariant when it builds
// the Event; a component-increment pin asserts it.
type Event struct {
	// Actor is the code-host actor that produced the event (the sender login).
	// It becomes intake.actor. An empty (or whitespace-only) Actor is never admitted.
	Actor string
	// Owner and Repo scope the collaborator permission check.
	Owner string
	Repo  string
	// AppliedLabels are the labels THIS event's Actor applied IN THIS EVENT (see
	// the type invariant) — never the issue's aggregate current labels.
	AppliedLabels []string
	// AuthoredText is the text THIS event's Actor authored IN THIS EVENT (the
	// issue/PR body they opened or the comment they posted), scanned for the opt-in
	// command — never an aggregate, and never a different author's text.
	AuthoredText string
}

// PermissionChecker resolves an actor's repository permission level
// ("admin" | "maintain" | "write" | "triage" | "read" | "none"). It is the one
// network call the gate may make, and ONLY for a non-allowlisted actor.
// Deterministic and zero-LLM-token.
type PermissionChecker interface {
	Permission(ctx context.Context, owner, repo, actor string) (level string, err error)
}

// Decision is the gate's verdict. Admitted gates run creation (a rejected event
// creates no run and spends no token); Actor becomes intake.actor; Reason is
// recorded for observability.
type Decision struct {
	Admitted   bool
	Actor      string
	Authorized bool
	OptedIn    bool
	Reason     string
}

// authorizedLevels are the repository permission levels that count as an
// authorized collaborator — one who can push. A read-only or triage viewer is
// deliberately NOT authorized to spend budget (the conservative, zero-token,
// non-adversarial default). An allowlist entry is the escape hatch for any actor
// the operator explicitly trusts regardless of repo permission.
var authorizedLevels = map[string]bool{"admin": true, "maintain": true, "write": true}

// Decide runs the deterministic, zero-token admission gate. It returns a
// non-nil error ONLY when it could not determine authorization (a permission
// lookup failed); in that case Admitted is false (fail closed) and the caller
// retries rather than treating it as a definitive rejection. A definitive
// "not admitted" (unauthorized or not opted in) returns a nil error.
func Decide(ctx context.Context, cfg Config, ev Event, checker PermissionChecker) (Decision, error) {
	ev.Actor = strings.TrimSpace(ev.Actor)
	d := Decision{Actor: ev.Actor}
	if ev.Actor == "" {
		d.Reason = "no actor on the event; rejected"
		return d, nil
	}

	authorized, err := authorize(ctx, cfg, ev, checker)
	if err != nil {
		// Fail closed but surface the error: a transient permission-lookup
		// failure must not admit an unauthorized actor, nor permanently reject an
		// authorized one — the caller retries.
		d.Reason = fmt.Sprintf("could not determine authorization for %q: %v", ev.Actor, err)
		return d, err
	}
	d.Authorized = authorized
	d.OptedIn = optedIn(cfg, ev)

	switch {
	case !authorized:
		d.Reason = fmt.Sprintf("actor %q is neither allowlisted nor a push-capable collaborator; rejected", ev.Actor)
	case !d.OptedIn:
		d.Reason = fmt.Sprintf("actor %q is authorized but the work is not opted in (no %q label or %q command); no run", ev.Actor, cfg.OptInLabel, cfg.OptInCommand)
	default:
		d.Admitted = true
		d.Reason = fmt.Sprintf("actor %q is authorized and opted in; admitted", ev.Actor)
	}
	return d, nil
}

// authorize reports whether the actor may drive semdev. An allowlisted actor is
// authorized WITHOUT a permission call (zero network); otherwise the actor must
// hold a push-capable repository permission. A checker error propagates (fail
// closed at the call site).
func authorize(ctx context.Context, cfg Config, ev Event, checker PermissionChecker) (bool, error) {
	if allowlisted(cfg.Allowlist, ev.Actor) {
		return true, nil
	}
	if checker == nil {
		return false, fmt.Errorf("no permission checker wired and %q is not allowlisted", ev.Actor)
	}
	level, err := checker.Permission(ctx, ev.Owner, ev.Repo, ev.Actor)
	if err != nil {
		return false, err
	}
	return authorizedLevels[strings.ToLower(strings.TrimSpace(level))], nil
}

// allowlisted reports whether actor is in the allowlist (case-insensitive login
// match — GitHub logins are case-insensitive).
func allowlisted(allowlist []string, actor string) bool {
	for _, a := range allowlist {
		if strings.EqualFold(strings.TrimSpace(a), actor) {
			return true
		}
	}
	return false
}

// optedIn reports whether the work is explicitly opted in: the opt-in label is
// present, or the opt-in command appears as a whole whitespace-delimited token in
// the event text (so "/semdev" opts in but "/semdevil" does not).
func optedIn(cfg Config, ev Event) bool {
	if cfg.OptInLabel != "" {
		for _, l := range ev.AppliedLabels {
			if strings.EqualFold(strings.TrimSpace(l), cfg.OptInLabel) {
				return true
			}
		}
	}
	// Match the command case-insensitively (like the label) and only as a whole
	// whitespace-delimited token, so "/semdev" opts in but "/semdevil" does not.
	if cfg.OptInCommand != "" {
		for _, tok := range strings.Fields(ev.AuthoredText) {
			if strings.EqualFold(tok, cfg.OptInCommand) {
				return true
			}
		}
	}
	return false
}
