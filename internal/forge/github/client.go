// Package github is semdev's thin GitHub adapter for the forge-io surfaces the
// framework does not provide. The framework ships github_read/github_write tools
// and the github_webhook input, but NOT a collaborator/permission check — which
// the zero-token intake admission gate needs to authorize an actor. This package
// adds exactly that missing call (and, later, the missing list-comments read),
// authenticated the same way the framework client is (Bearer token, api.github.com,
// X-GitHub-Api-Version 2022-11-28), with the base URL injectable for httptest.
//
// It is host-SPECIFIC by design — the one place GitHub's API shape is known. The
// admission gate and the arc consume only the host-neutral result (a permission
// level string), so swapping in another host's client is a local change.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultAPIBase = "https://api.github.com"
	requestTimeout = 10 * time.Second
)

// Client calls the GitHub REST API for the forge-io surfaces semdev adds on top of
// the framework's github tools.
type Client struct {
	token   string
	apiBase string
	http    *http.Client
	logger  *slog.Logger
}

// NewClient builds a GitHub API client authenticated with token. A blank token
// yields a client whose calls fail with an unauthenticated error rather than
// silently succeeding — the caller (the intake component) registers without one
// only in schema/census contexts.
func NewClient(token string) *Client {
	return &Client{token: token, apiBase: defaultAPIBase, http: &http.Client{Timeout: requestTimeout}, logger: slog.Default()}
}

// WithBaseURL overrides the API base (for httptest). Returns the same client for
// chaining.
func (c *Client) WithBaseURL(base string) *Client {
	c.apiBase = base
	return c
}

// WithLogger sets the logger used for the 404→none observability line. Returns the
// same client for chaining.
func (c *Client) WithLogger(l *slog.Logger) *Client {
	if l != nil {
		c.logger = l
	}
	return c
}

// permissionResponse is the shape of GET /repos/{o}/{r}/collaborators/{u}/permission.
// `permission` is the COARSE field (admin|write|read|none); `role_name` is the
// GRANULAR role (admin|maintain|write|triage|read|none) added by API version
// 2022-11-28. We prefer role_name so a maintain/triage distinction survives (the
// admission gate authorizes maintain but not triage), falling back to the coarse
// field.
type permissionResponse struct {
	Permission string `json:"permission"`
	RoleName   string `json:"role_name"`
}

// Permission returns actor's permission level on owner/repo — one of
// "admin" | "maintain" | "write" | "triage" | "read" | "none". It implements
// intake.PermissionChecker. A user who is not a collaborator resolves to "none"
// (a 404 or an explicit none), NOT an error, so the gate rejects them cleanly; a
// transport/5xx/auth failure returns an error so the gate fails closed and retries.
func (c *Client) Permission(ctx context.Context, owner, repo, actor string) (string, error) {
	if c.token == "" {
		return "", fmt.Errorf("github: no token configured; cannot check permission for %q", actor)
	}
	path := fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(actor))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return "", fmt.Errorf("github: build permission request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: permission request for %q: %w", actor, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Not a collaborator / no such user for this repo — a definitive "none",
		// not a failure (the gate treats it as unauthorized, not retryable). A 404
		// also covers a token that cannot see the repo, which would reject EVERYONE;
		// log it so that misconfiguration is distinguishable from correctly
		// rejecting strangers when an operator asks "why is nothing being admitted?"
		c.logger.Debug("github permission 404 → none",
			"owner", owner, "repo", repo, "actor", actor,
			"note", "non-collaborator, or the token cannot see this repo (misscoped token rejects everyone)")
		return "none", nil
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("github: permission for %q returned HTTP %d: %s", actor, resp.StatusCode, snippet(body))
	}

	var pr permissionResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", fmt.Errorf("github: decode permission response for %q: %w", actor, err)
	}
	if pr.RoleName != "" {
		return pr.RoleName, nil
	}
	if pr.Permission != "" {
		return pr.Permission, nil
	}
	return "none", nil
}

// snippet trims an error body for a log-safe message.
func snippet(b []byte) string {
	const maxLen = 200
	if len(b) > maxLen {
		return string(b[:maxLen]) + "…"
	}
	return string(b)
}
