package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The client implements the admission gate's PermissionChecker so it drops
// straight into intake.Decide — the compile-time assertion lives IN intake
// (component.go), which imports this package; asserting it here would be an
// import cycle now that the intake component constructs the client.

// A 200 returns the granular role_name in preference to the coarse permission,
// and sends the expected auth headers to the collaborators/permission endpoint.
func TestPermissionPrefersRoleNameAndAuthenticates(t *testing.T) {
	var gotPath, gotAuth, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotVersion = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-GitHub-Api-Version")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"permission":"write","role_name":"maintain"}`))
	}))
	defer srv.Close()

	c := NewClient("tok-123").WithBaseURL(srv.URL)
	level, err := c.Permission(context.Background(), "octo", "repo", "alice")
	if err != nil {
		t.Fatalf("permission: %v", err)
	}
	if level != "maintain" {
		t.Errorf("level = %q, want maintain (granular role_name preferred over coarse permission)", level)
	}
	if gotPath != "/repos/octo/repo/collaborators/alice/permission" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("auth header = %q, want Bearer tok-123", gotAuth)
	}
	if gotVersion != "2022-11-28" {
		t.Errorf("api-version header = %q", gotVersion)
	}
}

// Falls back to the coarse `permission` field when role_name is absent.
func TestPermissionFallsBackToCoarse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"permission":"admin"}`))
	}))
	defer srv.Close()
	level, err := NewClient("t").WithBaseURL(srv.URL).Permission(context.Background(), "o", "r", "u")
	if err != nil || level != "admin" {
		t.Fatalf("level=%q err=%v, want admin/nil", level, err)
	}
}

// A non-collaborator (404) resolves to "none" WITHOUT an error, so the gate
// rejects them cleanly rather than failing closed-with-retry.
func TestPermissionNotFoundIsNoneNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	level, err := NewClient("t").WithBaseURL(srv.URL).Permission(context.Background(), "o", "r", "stranger")
	if err != nil {
		t.Fatalf("404 must not error: %v", err)
	}
	if level != "none" {
		t.Errorf("level = %q, want none for a non-collaborator", level)
	}
}

// A 5xx returns an error so the gate fails closed and retries (a transient GitHub
// outage must not be read as a definitive rejection).
func TestPermissionServerErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := NewClient("t").WithBaseURL(srv.URL).Permission(context.Background(), "o", "r", "u"); err == nil {
		t.Error("expected an error on HTTP 500 (fail closed + retry)")
	}
}

// No token → a loud error, never a silent success.
func TestPermissionRequiresToken(t *testing.T) {
	if _, err := NewClient("").Permission(context.Background(), "o", "r", "u"); err == nil {
		t.Error("expected an error with no token")
	}
}

// ListComments maps GitHub's comment shape to the host-neutral Comment (author
// from user.login), preserving order, and hits the issues/{n}/comments endpoint.
func TestListCommentsMapsAndOrders(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[
			{"id":1,"body":"first","created_at":"2026-01-01","html_url":"u1","user":{"login":"alice"}},
			{"id":2,"body":"second","created_at":"2026-01-02","html_url":"u2","user":{"login":"bob"}}
		]`))
	}))
	defer srv.Close()

	got, err := NewClient("t").WithBaseURL(srv.URL).ListComments(context.Background(), "octo", "repo", 7)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if gotPath != "/repos/octo/repo/issues/7/comments" {
		t.Errorf("path = %q", gotPath)
	}
	if len(got) != 2 || got[0].Author != "alice" || got[0].Body != "first" || got[1].Author != "bob" {
		t.Errorf("comments mapped wrong (order/author/body): %+v", got)
	}
}

// ListComments follows pagination to exhaustion: a busy thread's LATEST comment is
// on the last page, so a single-page read would return stale context. Two pages
// via the Link header; the newest comment (page 2) must be present and last.
func TestListCommentsPaginatesToLastPage(t *testing.T) {
	var pagesServed int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "2":
			pagesServed++
			// Last page — no Link: rel="next".
			_, _ = w.Write([]byte(`[{"id":3,"body":"the latest instruction","user":{"login":"maintainer"}}]`))
		default: // page 1 (or unset)
			pagesServed++
			// Point rel="next" at page 2 on THIS test server.
			w.Header().Set("Link", `<`+srvURL(r)+`/x?page=2>; rel="next", <`+srvURL(r)+`/x?page=2>; rel="last"`)
			_, _ = w.Write([]byte(`[{"id":1,"body":"first","user":{"login":"alice"}},{"id":2,"body":"second","user":{"login":"bob"}}]`))
		}
	}))
	defer srv.Close()

	got, err := NewClient("t").WithBaseURL(srv.URL).ListComments(context.Background(), "octo", "repo", 7)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if pagesServed < 2 {
		t.Fatalf("only %d page(s) fetched; pagination did not follow rel=next", pagesServed)
	}
	if len(got) != 3 {
		t.Fatalf("got %d comments across pages, want 3: %+v", len(got), got)
	}
	if got[2].Body != "the latest instruction" || got[2].Author != "maintainer" {
		t.Errorf("the latest comment (page 2) is missing or out of order: %+v", got)
	}
}

// srvURL reconstructs the test server's base URL from a request (scheme is http
// for httptest), so a handler can build a rel="next" Link that points back at
// itself.
func srvURL(r *http.Request) string {
	return "http://" + r.Host
}

// A non-200 is a loud error, not an empty list; no token is a loud error.
func TestListCommentsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := NewClient("t").WithBaseURL(srv.URL).ListComments(context.Background(), "o", "r", 1); err == nil {
		t.Error("expected an error on HTTP 403")
	}
	if _, err := NewClient("").ListComments(context.Background(), "o", "r", 1); err == nil {
		t.Error("expected an error with no token")
	}
}

// The actor is path-escaped so a crafted login cannot alter the request path.
func TestPermissionEscapesActor(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"permission":"none"}`))
	}))
	defer srv.Close()
	_, _ = NewClient("t").WithBaseURL(srv.URL).Permission(context.Background(), "o", "r", "a/../../admin")
	if strings.Contains(gotPath, "/../") {
		t.Errorf("actor was not escaped; path traversal reached the request path: %q", gotPath)
	}
}
