// Package clone is the forge-clone provision source (self-target-provisioning-and-launch-driver,
// design D1): it resolves a run's REAL target by reading the run's run.issue.ref coordinate
// from the graph and git-cloning that repository, returning the per-run source directory the
// provision pipeline materializes the run's checkout from.
//
// G1 framework-alignment: no new framework primitive — it satisfies the EXISTING
// provisionsandbox.Sources seam (the documented M2 flip of runspace.StaticSource's Resolve
// contract, sources.go), reads a fact through the existing changefacts.Reader, and shells git
// through the existing cliexec seam. StaticSource stays the fixture/dev path; the forge-target
// source mode selects this one at boot.
//
// It fails CLOSED like StaticSource (SB5): no coordinate, an unparseable ref, an unknown or
// unreachable repo, or a clone failure errors toward the operator (the run parks), never a
// guessed target. A configured token rides the subprocess ENV via GIT_ASKPASS (cliexec's
// EnvRunner), never a command argument (design D3, no host-process-listing leak).
package clone

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
)

// issueRefPredicate is the run-side coordinate the intake/issue-ref rule stamps (writer
// issue-ref-rule, forge-io). The clone source READS it; it never writes it (G5).
const issueRefPredicate = "run.issue.ref"

// tokenEnvName is the fixed subprocess env var the GIT_ASKPASS helper reads the token from —
// an INTERNAL name, independent of the operator's chosen TokenEnv, so the child env is
// deterministic. The operator's token value is copied into it for the clone subprocess only.
const tokenEnvName = "SEMDEV_FORGE_TOKEN"

// Config is the forge-target source mode's config (the boot `source` block).
type Config struct {
	// BaseURL is the git host base the target lives under: "https://github.com" live, or
	// "file:///…/remotes" (THREE slashes — a two-slash file://host form mis-parses the host)
	// for the offline bare-remote journey. The clone target is <BaseURL>/<owner>/<repo>.git,
	// with owner/repo taken from the RUN's coordinate. Credentials belong in TokenEnv, NEVER
	// embedded in BaseURL (an embedded user:pass would land on git's argv).
	BaseURL string
	// TokenEnv names the env var holding the forge token. EMPTY → UNAUTHENTICATED (correct for
	// file:// and public repos); authentication is opt-in, so a stray ambient GITHUB_TOKEN
	// never silently authenticates a clone. Set it (e.g. "GITHUB_TOKEN") to authenticate.
	TokenEnv string
}

// Source is the forge-clone provisionsandbox.Sources implementation.
type Source struct {
	reader changefacts.Reader
	runner cliexec.Runner
	base   string // per-run source-clone root (each Resolve clones into a fresh subdir)
	cfg    Config
	token  string // resolved token value ("" = unauthenticated)
	logger *slog.Logger
}

// NewSource builds a forge-clone source. base is the directory per-run clones land under; an
// empty base creates a process-scoped temp dir (the NewCheckouts convention). The token is
// resolved ONCE from cfg.TokenEnv at construction — an unset var is a legitimate
// unauthenticated config, not an error (file:// and public repos need none). Fails closed on a
// missing reader/runner, an empty base URL, or an unusable base dir.
func NewSource(reader changefacts.Reader, runner cliexec.Runner, base string, cfg Config, logger *slog.Logger) (*Source, error) {
	if reader == nil {
		return nil, fmt.Errorf("clone: NewSource needs a fact reader")
	}
	if runner == nil {
		return nil, fmt.Errorf("clone: NewSource needs a git runner")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("clone: NewSource needs a forge base URL")
	}
	if base == "" {
		dir, err := os.MkdirTemp("", "semdev-sources-*")
		if err != nil {
			return nil, fmt.Errorf("clone: create source-clone base: %w", err)
		}
		base = dir
	} else if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("clone: create source-clone base %s: %w", base, err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	// Authentication is opt-in: only read a token when the operator NAMED the env var. An
	// empty TokenEnv stays unauthenticated even if GITHUB_TOKEN happens to be exported (M1 —
	// no stray token silently authenticating clones to whatever host BaseURL names).
	var token string
	if cfg.TokenEnv != "" {
		token = os.Getenv(cfg.TokenEnv)
	}
	return &Source{
		reader: reader,
		runner: runner,
		base:   base,
		cfg:    cfg,
		token:  token,
		logger: logger,
	}, nil
}

// Resolve reads the run's run.issue.ref coordinate, clones the repository it names at its
// default branch into a fresh per-run directory, and returns that dir for Materialize. Every
// failure is CLOSED (an error → the run parks): no coordinate, an unparseable ref, or a clone
// fault — never a guessed source. A failed clone reaps its half-materialized dir.
func (s *Source) Resolve(ctx context.Context, runEntityID string) (string, error) {
	if runEntityID == "" {
		return "", fmt.Errorf("clone: resolve needs a run entity id")
	}
	ref, err := s.issueRef(ctx, runEntityID)
	if err != nil {
		return "", err
	}
	owner, repo, err := parseOwnerRepo(ref)
	if err != nil {
		return "", fmt.Errorf("clone: run %s: %w", runEntityID, err)
	}
	cloneURL, err := s.cloneURL(owner, repo)
	if err != nil {
		return "", err
	}

	dest, err := os.MkdirTemp(s.base, "src-*")
	if err != nil {
		return "", fmt.Errorf("clone: create source dir: %w", err)
	}
	if err := s.clone(ctx, cloneURL, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("clone %s/%s: %w", owner, repo, err)
	}
	return dest, nil
}

// issueRef reads the single run.issue.ref string for the run, failing closed when absent.
func (s *Source) issueRef(ctx context.Context, runEntityID string) (string, error) {
	triples, err := s.reader.ReadFacts(ctx, runEntityID, issueRefPredicate)
	if err != nil {
		return "", fmt.Errorf("clone: read run.issue.ref for %s: %w", runEntityID, err)
	}
	for _, tr := range triples {
		if tr.Predicate != issueRefPredicate {
			continue
		}
		if ref, ok := tr.Object.(string); ok && strings.TrimSpace(ref) != "" {
			return strings.TrimSpace(ref), nil
		}
	}
	return "", fmt.Errorf("clone: run %s carries no run.issue.ref — cannot resolve its target (park toward the operator)", runEntityID)
}

// parseOwnerRepo extracts owner and repo from a host-neutral "owner/repo#number" coordinate.
func parseOwnerRepo(ref string) (owner, repo string, err error) {
	hash := strings.LastIndexByte(ref, '#')
	slash := strings.IndexByte(ref, '/')
	if slash <= 0 || hash <= slash+1 {
		return "", "", fmt.Errorf("coordinate %q is not owner/repo#number", ref)
	}
	owner, repo = ref[:slash], ref[slash+1:hash]
	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("coordinate %q is not owner/repo#number", ref)
	}
	return owner, repo, nil
}

// cloneURL builds <BaseURL>/<owner>/<repo>.git. For an http(s) base with a token configured,
// it sets ONLY the x-access-token username (a fixed, non-secret GitHub convention) so the
// token itself never enters the URL/argv — it rides GIT_ASKPASS's env instead (design D3).
func (s *Source) cloneURL(owner, repo string) (string, error) {
	u, err := url.Parse(s.cfg.BaseURL)
	if err != nil {
		return "", fmt.Errorf("clone: parse base url %q: %w", s.cfg.BaseURL, err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + owner + "/" + repo + ".git"
	if s.token != "" && (u.Scheme == "https" || u.Scheme == "http") {
		u.User = url.User("x-access-token")
	}
	return u.String(), nil
}

// clone runs `git clone` into dest. It always sets GIT_TERMINAL_PROMPT=0 so an
// auth-requiring target fails fast instead of blocking on a prompt (M2, SB5). WITH a token it
// also injects it through GIT_ASKPASS via cliexec's EnvRunner — subprocess env, never argv —
// and FAILS CLOSED if the runner cannot inject env, refusing a token-on-argv fallback (design
// D3). On a non-zero exit it logs the full git stderr for the operator but returns a SCRUBBED
// summary: the error becomes the run's block reason, which is posted as a PUBLIC issue comment,
// so raw git stderr (internal hostnames, private-repo existence) must never ride it (M4).
func (s *Source) clone(ctx context.Context, cloneURL, dest string) error {
	args := []string{"clone", "-q", cloneURL, dest}
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if s.token != "" {
		askpass, cleanup, err := writeAskpass()
		if err != nil {
			return err
		}
		defer cleanup()
		env = append(env, "GIT_ASKPASS="+askpass, tokenEnvName+"="+s.token)
	}

	var res cliexec.Result
	var err error
	if er, ok := s.runner.(cliexec.EnvRunner); ok {
		res, err = er.RunWithEnv(ctx, "", env, "git", args...)
	} else if s.token != "" {
		// A token needs env injection; a runner without it would force the token onto argv.
		return fmt.Errorf("a token is configured but the git runner cannot inject env — refusing to put the token on argv (design D3)")
	} else {
		res, err = s.runner.Run(ctx, "", "git", args...)
	}
	if err != nil {
		return err // transport failure (git missing / context cancelled)
	}
	if res.ExitCode != 0 {
		s.logger.Warn("forge clone failed",
			slog.String("url", cloneURL), // carries only the non-secret x-access-token username
			slog.Int("exit", res.ExitCode),
			slog.String("stderr", strings.TrimSpace(res.Stderr)))
		return fmt.Errorf("git clone failed (exit %d) — verify the repository exists and the token has access", res.ExitCode)
	}
	return nil
}

// writeAskpass writes a throwaway GIT_ASKPASS helper that echoes the token from the subprocess
// env — never a file containing the token, never argv. Returns its path and a cleanup func.
func writeAskpass() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "semdev-askpass-*.sh")
	if err != nil {
		return "", func() {}, fmt.Errorf("clone: create askpass helper: %w", err)
	}
	name := f.Name()
	remove := func() { _ = os.Remove(name) }
	// Echo the token ENV VAR (not the value) — git calls this for the x-access-token user's
	// password prompt. The token is never written into this script.
	if _, err := f.WriteString("#!/bin/sh\nprintf '%s' \"$" + tokenEnvName + "\"\n"); err != nil {
		_ = f.Close()
		remove()
		return "", func() {}, fmt.Errorf("clone: write askpass helper: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", func() {}, err
	}
	if err := os.Chmod(name, 0o700); err != nil {
		remove()
		return "", func() {}, err
	}
	return name, remove, nil
}
