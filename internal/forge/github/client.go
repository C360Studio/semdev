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
	"strings"
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

// Issue is an issue's authored content — the fields the operator launch driver needs to build
// a content-bearing coordinator wake. Host-neutral to the caller.
type Issue struct {
	Number int
	Title  string
	Body   string
}

// issueResponse is the GitHub shape of GET /repos/{o}/{r}/issues/{n} (the subset we read).
type issueResponse struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// GetIssue reads issue `number` in owner/repo — its title and body — the authored content the
// operator launch driver threads into the coordinator wake (the M1 content lane; the webhook
// front door draws the same content from its payload). It is the direct client read the CLI
// needs (the framework exposes github_get_issue only as an agentic tool). A blank token is a
// loud error; any non-200 (including a 404 for a non-existent issue) is an error, so a launch
// never proceeds against an empty ask (fail closed).
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, error) {
	if c.token == "" {
		return Issue{}, fmt.Errorf("github: no token configured; cannot read %s/%s#%d", owner, repo, number)
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(owner), url.PathEscape(repo), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return Issue{}, fmt.Errorf("github: build get-issue request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return Issue{}, fmt.Errorf("github: get-issue %s/%s#%d: %w", owner, repo, number, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return Issue{}, fmt.Errorf("github: get-issue %s/%s#%d returned HTTP %d: %s", owner, repo, number, resp.StatusCode, snippet(body))
	}
	var ir issueResponse
	if err := json.Unmarshal(body, &ir); err != nil {
		return Issue{}, fmt.Errorf("github: decode issue %s/%s#%d: %w", owner, repo, number, err)
	}
	return Issue{Number: ir.Number, Title: ir.Title, Body: ir.Body}, nil
}

// Comment is one issue/PR comment — the fields semdev needs to read a
// conversation. Host-neutral to the caller (no GitHub-specific shape leaks out).
type Comment struct {
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	URL       string `json:"url"`
}

// commentResponse is the GitHub shape of an item in
// GET /repos/{o}/{r}/issues/{n}/comments.
type commentResponse struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// maxCommentPages bounds pagination so a misbehaving API cannot loop forever.
// 100 pages × 100/page = 10k comments, well beyond any real thread.
const maxCommentPages = 100

// ListComments returns ALL comments on issue/PR number in owner/repo, in GitHub's
// default oldest-first order, following pagination to exhaustion — the newest
// human instruction or correction is on the LAST page, so a single-page read would
// ground the agent in stale context. It is the thin read the framework's github
// tools lack (github_add_comment writes, github_get_issue reads the issue but not
// its comments). A blank token is a loud error, never a silent empty list.
func (c *Client) ListComments(ctx context.Context, owner, repo string, number int) ([]Comment, error) {
	if c.token == "" {
		return nil, fmt.Errorf("github: no token configured; cannot list comments for %s/%s#%d", owner, repo, number)
	}
	next := c.apiBase + fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), number)

	var out []Comment
	for page := 0; next != "" && page < maxCommentPages; page++ {
		raw, nextURL, err := c.fetchCommentPage(ctx, next)
		if err != nil {
			return nil, fmt.Errorf("github: list comments for %s/%s#%d: %w", owner, repo, number, err)
		}
		for _, r := range raw {
			out = append(out, Comment{ID: r.ID, Author: r.User.Login, Body: r.Body, CreatedAt: r.CreatedAt, URL: r.HTMLURL})
		}
		next = nextURL
	}
	return out, nil
}

// fetchCommentPage GETs one comments page (a full URL — the first built by
// ListComments, subsequent ones taken verbatim from the Link header), returning
// the decoded page and the rel="next" URL ("" when this is the last page).
func (c *Client) fetchCommentPage(ctx context.Context, pageURL string) ([]commentResponse, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	var raw []commentResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", fmt.Errorf("decode: %w", err)
	}
	return raw, nextPageLink(resp.Header.Get("Link")), nil
}

// nextPageLink extracts the rel="next" URL from a GitHub Link header, or "" if
// there is no next page. Header shape:
// `<https://api.github.com/...?page=2>; rel="next", <...?page=9>; rel="last"`.
func nextPageLink(header string) string {
	if header == "" {
		return "" // common single-page case
	}
	for _, part := range strings.Split(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		var link, rel string
		for i, s := range segs {
			s = strings.TrimSpace(s)
			if i == 0 {
				link = strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">")
				continue
			}
			if r, ok := strings.CutPrefix(s, "rel="); ok {
				rel = strings.Trim(r, `"`)
			}
		}
		if rel == "next" {
			return link
		}
	}
	return ""
}

// snippet trims an error body for a log-safe message.
func snippet(b []byte) string {
	const maxLen = 200
	if len(b) > maxLen {
		return string(b[:maxLen]) + "…"
	}
	return string(b)
}
