package github

// The PR-delivery + comment-posting surfaces (forge-io-real-lanes groups 3+4):
// query-PR-by-head-branch (the forge-level idempotency guard), create-PR (the
// evidence-bearing delivery), and create-comment (the park-message lane). Same
// auth + base-URL-injection discipline as the permission/comments reads — the
// e2e journeys point the base at the local forge double and exercise these
// exact request shapes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// PR is the host-neutral view of a pull request the delivery lane needs.
type PR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

// prResponse is GitHub's PR shape (the fields we map).
type prResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

// FindPRByHead returns the pull request whose head is {headOwner}:{branch} in
// owner/repo, or nil when none exists — the FORGE-LEVEL idempotency guard
// (query-existing-by-head-branch FIRST; create only on absence). state=all so
// an already-merged or closed delivery still resolves to its PR rather than
// double-opening a duplicate.
func (c *Client) FindPRByHead(ctx context.Context, owner, repo, headOwner, branch string) (*PR, error) {
	if c.token == "" {
		return nil, fmt.Errorf("github: no token configured; cannot query PRs for %s/%s", owner, repo)
	}
	q := url.Values{}
	q.Set("head", headOwner+":"+branch)
	q.Set("state", "all")
	q.Set("per_page", "1")
	endpoint := c.apiBase + fmt.Sprintf("/repos/%s/%s/pulls?%s",
		url.PathEscape(owner), url.PathEscape(repo), q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build find-PR request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: find PR by head %s:%s: %w", headOwner, branch, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: find PR by head %s:%s returned HTTP %d: %s", headOwner, branch, resp.StatusCode, snippet(body))
	}
	var raw []prResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("github: decode PR list: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return &PR{Number: raw[0].Number, HTMLURL: raw[0].HTMLURL, State: raw[0].State}, nil
}

// PRRequest is the create-PR input.
type PRRequest struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
}

// CreatePR opens a pull request. The caller (openpr's forge delivery) has
// already pushed the head branch and queried for an existing PR — this is the
// create-on-absence leg of the doubly-idempotent delivery.
func (c *Client) CreatePR(ctx context.Context, owner, repo string, pr PRRequest) (*PR, error) {
	if c.token == "" {
		return nil, fmt.Errorf("github: no token configured; cannot create a PR on %s/%s", owner, repo)
	}
	payload, err := json.Marshal(pr)
	if err != nil {
		return nil, fmt.Errorf("github: marshal PR request: %w", err)
	}
	endpoint := c.apiBase + fmt.Sprintf("/repos/%s/%s/pulls", url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("github: build create-PR request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: create PR %s: %w", pr.Head, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("github: create PR %s returned HTTP %d: %s", pr.Head, resp.StatusCode, snippet(body))
	}
	var raw prResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("github: decode created PR: %w", err)
	}
	return &PR{Number: raw.Number, HTMLURL: raw.HTMLURL, State: raw.State}, nil
}

// CreateComment posts body as a comment on issue/PR number — the park-message
// lane (a parked run's run.awaiting.human reaching the human where they live).
func (c *Client) CreateComment(ctx context.Context, owner, repo string, number int, body string) error {
	if c.token == "" {
		return fmt.Errorf("github: no token configured; cannot comment on %s/%s#%d", owner, repo, number)
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("github: marshal comment: %w", err)
	}
	endpoint := c.apiBase + fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("github: build comment request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: comment on %s/%s#%d: %w", owner, repo, number, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("github: comment on %s/%s#%d returned HTTP %d: %s", owner, repo, number, resp.StatusCode, snippet(respBody))
	}
	return nil
}

// setHeaders applies the standard auth + version headers.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
}
