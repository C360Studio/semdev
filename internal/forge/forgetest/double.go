// Package forgetest is the protocol-faithful LOCAL FORGE DOUBLE
// (forge-io-real-lanes 4.2): an in-process HTTP server speaking the exact
// GitHub REST shapes semdev's client sends — query-PR-by-head, create-PR,
// create-comment, collaborator-permission — recording every request in order
// so tests can assert the REAL request shapes and their sequence (the
// query-BEFORE-create idempotency ordering). e2e journeys point the client's
// base URL here; no journey depends on a live forge.
//
// The GIT half of delivery (the branch push) does NOT go through this double —
// a push is a git-protocol operation, not a REST call. Tests pair the double
// with a local BARE repository (file:// remote): a REAL git push against a
// real repository, plus real REST shapes against this recorder.
package forgetest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
)

// Request is one recorded call.
type Request struct {
	// Kind is "find_pr" | "create_pr" | "create_comment" | "permission".
	Kind string
	// Method + Path are the raw HTTP surface.
	Method string
	Path   string
	// Query is the raw query string (find_pr carries head=owner:branch&state=all).
	Query string
	// Body is the decoded JSON body for POSTs (nil for GETs).
	Body map[string]any
}

// PR is a pull request the double holds.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Head   string `json:"head"`
	Base   string `json:"base"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
}

// Comment is a recorded issue comment.
type Comment struct {
	IssueNumber int
	Body        string
}

// Double is the recording forge.
type Double struct {
	mu       sync.Mutex
	server   *httptest.Server
	requests []Request
	prs      []PR
	comments []Comment
	nextPR   int

	// Permissions maps actor login → permission level for the permission
	// endpoint ("" → 404/none). Set before use; read under the lock.
	Permissions map[string]string
}

// Start builds and starts the double.
func Start() *Double {
	d := &Double{nextPR: 1, Permissions: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.route)
	d.server = httptest.NewServer(mux)
	return d
}

// URL is the API base to point the client at.
func (d *Double) URL() string { return d.server.URL }

// Close shuts the double down.
func (d *Double) Close() { d.server.Close() }

// Requests returns the recorded calls in arrival order.
func (d *Double) Requests() []Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Request, len(d.requests))
	copy(out, d.requests)
	return out
}

// PRs returns the pull requests the double holds.
func (d *Double) PRs() []PR {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]PR, len(d.prs))
	copy(out, d.prs)
	return out
}

// Comments returns the recorded issue comments.
func (d *Double) Comments() []Comment {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Comment, len(d.comments))
	copy(out, d.comments)
	return out
}

var (
	pullsRe      = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls$`)
	commentsRe   = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$`)
	permissionRe = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/collaborators/([^/]+)/permission$`)
)

func (d *Double) route(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var decoded map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &decoded)
	}

	switch {
	case pullsRe.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		d.record("find_pr", r, decoded)
		d.handleFindPR(w, r)
	case pullsRe.MatchString(r.URL.Path) && r.Method == http.MethodPost:
		d.record("create_pr", r, decoded)
		d.handleCreatePR(w, r, decoded)
	case commentsRe.MatchString(r.URL.Path) && r.Method == http.MethodPost:
		d.record("create_comment", r, decoded)
		d.handleCreateComment(w, r, decoded)
	case permissionRe.MatchString(r.URL.Path) && r.Method == http.MethodGet:
		d.record("permission", r, decoded)
		d.handlePermission(w, r)
	default:
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}
}

func (d *Double) record(kind string, r *http.Request, body map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, Request{
		Kind: kind, Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body,
	})
}

func (d *Double) handleFindPR(w http.ResponseWriter, r *http.Request) {
	head := r.URL.Query().Get("head")
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []PR{}
	for _, pr := range d.prs {
		if pr.Head == headBranch(head) {
			out = append(out, pr)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *Double) handleCreatePR(w http.ResponseWriter, r *http.Request, body map[string]any) {
	m := pullsRe.FindStringSubmatch(r.URL.Path)
	owner, repo := m[1], m[2]
	d.mu.Lock()
	defer d.mu.Unlock()
	pr := PR{
		Number: d.nextPR,
		Title:  str(body["title"]),
		Head:   str(body["head"]),
		Base:   str(body["base"]),
		Body:   str(body["body"]),
		State:  "open",
		URL:    fmt.Sprintf("%s/%s/%s/pull/%d", d.server.URL, owner, repo, d.nextPR),
	}
	d.nextPR++
	d.prs = append(d.prs, pr)
	writeJSON(w, http.StatusCreated, pr)
}

func (d *Double) handleCreateComment(w http.ResponseWriter, r *http.Request, body map[string]any) {
	m := commentsRe.FindStringSubmatch(r.URL.Path)
	var number int
	_, _ = fmt.Sscanf(m[3], "%d", &number)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.comments = append(d.comments, Comment{IssueNumber: number, Body: str(body["body"])})
	writeJSON(w, http.StatusCreated, map[string]any{"id": len(d.comments)})
}

func (d *Double) handlePermission(w http.ResponseWriter, r *http.Request) {
	m := permissionRe.FindStringSubmatch(r.URL.Path)
	actor := m[3]
	d.mu.Lock()
	level := d.Permissions[actor]
	d.mu.Unlock()
	if level == "" {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permission": level, "role_name": level})
}

// headBranch strips the "owner:" prefix of a head query value.
func headBranch(head string) string {
	if i := strings.IndexByte(head, ':'); i >= 0 {
		return head[i+1:]
	}
	return head
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
