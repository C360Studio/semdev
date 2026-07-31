package openpr

// The REAL forge delivery (forge-io-real-lanes D5, discharging the reshape's
// group 8): push the run's committed attempt branch to the configured remote,
// then create-or-adopt the pull request — doubly idempotent (the graph-side
// read-before-create guard in Deliver + the forge-level query-by-head-branch
// here), with the evidence summary in the PR body. The M0 `local-delivery:`
// stub is DELETED: an unconfigured forge is a LOUD error, the station's
// bounded retries exhaust, and the run PARKS (station-failure-parks) — never a
// silent stub masquerading as a delivery.

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/graphown"
)

// ForgeConfig is the delivery target (the delivery-station's `forge` config
// block; the token travels by ENV NAME via the dotenv lane, resolved by the
// station factory).
type ForgeConfig struct {
	// Owner/Repo scope the API calls.
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// RemoteURL is the git push target (https://github.com/o/r.git live;
	// file:///…/bare.git in journeys — a REAL push either way).
	RemoteURL string `json:"remote_url"`
	// BaseBranch is the PR base (default main).
	BaseBranch string `json:"base_branch"`
	// APIBase overrides the API endpoint (the e2e forge double); "" = GitHub.
	APIBase string `json:"api_base"`
	// TokenEnv names the env var holding the forge token (default GITHUB_TOKEN).
	TokenEnv string `json:"token_env"`
}

// Configured reports whether a real delivery target exists. An unconfigured
// forge FAILS delivery closed — the stub path no longer exists.
func (f ForgeConfig) Configured() bool {
	return f.Owner != "" && f.Repo != "" && f.RemoteURL != ""
}

// ForgeAPI is the narrow REST surface delivery needs — github.Client satisfies
// it; unit pins fake it.
type ForgeAPI interface {
	FindPRByHead(ctx context.Context, owner, repo, headOwner, branch string) (*github.PR, error)
	CreatePR(ctx context.Context, owner, repo string, pr github.PRRequest) (*github.PR, error)
}

// CheckoutRoots resolves a run's warm checkout directory —
// runspace.Checkouts satisfies it.
type CheckoutRoots interface {
	Root(ctx context.Context, runEntityID string) (string, error)
}

// Delivery is the real forge delivery core the delivery station drives.
type Delivery struct {
	Reader changefacts.Reader
	Writer *graphown.Writer
	API    ForgeAPI
	Roots  CheckoutRoots
	Runner cliexec.Runner
	Forge  ForgeConfig
	Token  string
	Logger *slog.Logger
}

// BranchPrefix is the delivery head-branch namespace.
const BranchPrefix = "semdev/"

// Deliver pushes the run's committed branch, creates-or-adopts the PR, and
// stamps delivery.pr.ref = the REAL PR URL. Idempotent at BOTH layers:
//  1. graph-side — an existing pr.ref returns verbatim, the create leg never
//     runs (the reshape-7.4 replay guard, unchanged);
//  2. forge-side — query-by-head-branch FIRST, create only on absence, so a
//     concurrent double-fire past the graph guard resolves to ONE pull request.
func (d *Delivery) Deliver(ctx context.Context, runEntityID string) (string, error) {
	// Layer 1 — the graph-side replay guard (presence-based, fail-closed on a
	// corrupt object; identical contract to the reshape-7.4 pin).
	existing, err := d.Reader.ReadFacts(ctx, runEntityID, RefPredicate)
	if err != nil {
		return "", fmt.Errorf("open-pr: read existing delivery on %s: %w", runEntityID, err)
	}
	for _, tr := range existing {
		if tr.Predicate != RefPredicate {
			continue
		}
		ref, ok := tr.Object.(string)
		if !ok {
			return "", fmt.Errorf("open-pr: existing %s on %s has a non-string object %T — failing closed rather than re-opening", RefPredicate, runEntityID, tr.Object)
		}
		return ref, nil
	}

	// Fail CLOSED without a forge: the M0 local stub is DELETED. The station's
	// retries exhaust and the run parks (station-failure-parks) — loud, never
	// a fake delivery reference.
	if !d.Forge.Configured() {
		return "", fmt.Errorf("open-pr: no forge configured (owner/repo/remote_url) — delivery fails closed; the local-delivery stub no longer exists")
	}

	branch := BranchPrefix + runSuffix(runEntityID)

	// The REAL push — of the RECORDED verified commit, never bare HEAD: the
	// delivered bytes must be exactly the bytes the measure + clean-room
	// verify ran over (G4/G7). attempt.commit.sha is apply_patch's committed
	// snapshot pointer; absent means nothing verified exists to deliver —
	// fail closed (review finding: pushing HEAD made the invariant implicit).
	sha := d.factString(ctx, runEntityID, "attempt.commit.sha")
	if sha == "" {
		return "", fmt.Errorf("open-pr: run %s carries no attempt.commit.sha — no verified commit to deliver (fail closed)", runEntityID)
	}
	root, err := d.Roots.Root(ctx, runEntityID)
	if err != nil {
		return "", fmt.Errorf("open-pr: resolve checkout for %s: %w", runEntityID, err)
	}
	pushURL, err := d.pushURL()
	if err != nil {
		return "", err
	}
	res, err := d.Runner.Run(ctx, root, "git", "push", pushURL, sha+":refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("open-pr: git push %s: %w", branch, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("open-pr: git push %s exited %d: %s", branch, res.ExitCode, d.sanitize(res.Stderr))
	}

	// Layer 2 — the forge-level guard: adopt an existing PR by head branch
	// BEFORE creating (state=all: a merged/closed delivery still resolves).
	pr, err := d.API.FindPRByHead(ctx, d.Forge.Owner, d.Forge.Repo, d.Forge.Owner, branch)
	if err != nil {
		return "", fmt.Errorf("open-pr: query existing PR for %s: %w", branch, err)
	}
	if pr == nil {
		req := github.PRRequest{
			Title: d.prTitle(ctx, runEntityID),
			Head:  branch,
			Base:  d.baseBranch(),
			Body:  d.evidenceSummary(ctx, runEntityID),
		}
		pr, err = d.API.CreatePR(ctx, d.Forge.Owner, d.Forge.Repo, req)
		if err != nil {
			return "", fmt.Errorf("open-pr: create PR for %s: %w", branch, err)
		}
	} else if d.Logger != nil {
		d.Logger.Info("open-pr: adopted existing PR by head branch (forge-level idempotency)",
			slog.String("branch", branch), slog.String("url", pr.HTMLURL))
	}

	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  RefPredicate,
		Object:     pr.HTMLURL,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := d.Writer.Replace(ctx, runEntityID, []message.Triple{triple}); err != nil {
		return "", fmt.Errorf("open-pr: stamp %s on %s: %w", RefPredicate, runEntityID, err)
	}
	return pr.HTMLURL, nil
}

// pushURL injects the token into an https remote (x-access-token form); a
// file:// or ssh remote passes through untouched. The token NEVER reaches an
// error message (see sanitize).
func (d *Delivery) pushURL() (string, error) {
	remote := d.Forge.RemoteURL
	if d.Token == "" || !strings.HasPrefix(remote, "https://") {
		return remote, nil
	}
	u, err := url.Parse(remote)
	if err != nil {
		return "", fmt.Errorf("open-pr: parse remote_url: %w", err)
	}
	u.User = url.UserPassword("x-access-token", d.Token)
	return u.String(), nil
}

// sanitize strips the token from process output before it can reach a log or
// an error (a failed push echoes the remote URL). BOTH forms: raw, and the
// percent-encoded form url.UserPassword produces for reserved characters
// (review finding — a future token with specials must not leak through git's
// stderr echo).
func (d *Delivery) sanitize(s string) string {
	if d.Token == "" {
		return s
	}
	s = strings.ReplaceAll(s, d.Token, "***")
	if enc := url.QueryEscape(d.Token); enc != d.Token {
		s = strings.ReplaceAll(s, enc, "***")
	}
	return s
}

func (d *Delivery) baseBranch() string {
	if d.Forge.BaseBranch != "" {
		return d.Forge.BaseBranch
	}
	return "main"
}

// prTitle names the PR from the run's validated change slug (falling back to
// the run suffix — the title is display, the body is the evidence).
func (d *Delivery) prTitle(ctx context.Context, runEntityID string) string {
	slug := d.factString(ctx, runEntityID, "openspec.change.slug")
	if slug == "" {
		return "semdev: run " + runSuffix(runEntityID)
	}
	return "semdev: " + slug
}

// evidenceSummary renders the PR body from the run's HARNESS-stamped facts —
// what was verified, how, and where the full trajectory lives (G7: the body
// cites recorded evidence, it never invents any).
func (d *Delivery) evidenceSummary(ctx context.Context, runEntityID string) string {
	var b strings.Builder
	b.WriteString("## semdev delivery evidence\n\n")
	fmt.Fprintf(&b, "Run: `%s` (the full trajectory lives on this graph entity)\n\n", runEntityID)
	rows := []struct{ label, predicate string }{
		{"Change", "openspec.change.slug"},
		{"Change validated (content revision)", "openspec.change.validated"},
		{"Measured command", "measurement.result.command"},
		{"Measured passed", "measurement.result.passed"},
		{"Measured commit", "measurement.result.commit"},
		{"Review verdict", "review.verdict.value"},
		{"Clean-room verify", "verify.cleanroom.result"},
		{"Delivered commit", "attempt.commit.sha"},
	}
	b.WriteString("| Evidence | Value |\n|---|---|\n")
	for _, row := range rows {
		v := d.factString(ctx, runEntityID, row.predicate)
		if v == "" {
			v = "—"
		}
		fmt.Fprintf(&b, "| %s | `%s` |\n", row.label, strings.ReplaceAll(v, "`", "'"))
	}
	b.WriteString("\nOpened by semdev's delivery station (rule-routed, zero model turns at delivery).\n")
	return b.String()
}

// factString reads the first string object of the exact predicate on the run.
func (d *Delivery) factString(ctx context.Context, runEntityID, predicate string) string {
	facts, err := d.Reader.ReadFacts(ctx, runEntityID, predicate)
	if err != nil {
		if d.Logger != nil {
			d.Logger.Warn("open-pr: evidence read failed; the PR body row degrades to absent", slog.String("predicate", predicate), slog.Any("error", err))
		}
		return ""
	}
	for _, tr := range facts {
		if tr.Predicate != predicate {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			return s
		}
	}
	return ""
}

// runSuffix is the run entity's final ID segment (the chain UUID).
func runSuffix(runEntityID string) string {
	if i := strings.LastIndexByte(runEntityID, '.'); i >= 0 {
		return runEntityID[i+1:]
	}
	return runEntityID
}
