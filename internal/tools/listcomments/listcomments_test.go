package listcomments

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semstreams/agentic"
)

type fakeLister struct {
	comments []github.Comment
	err      error
	gotOwner string
	gotNum   int
}

func (f *fakeLister) ListComments(_ context.Context, owner, _ string, number int) ([]github.Comment, error) {
	f.gotOwner, f.gotNum = owner, number
	return f.comments, f.err
}

func call(args map[string]any) agentic.ToolCall {
	return agentic.ToolCall{ID: "c1", Name: ToolName, Arguments: args}
}

func TestListCommentsReturnsThread(t *testing.T) {
	f := &fakeLister{comments: []github.Comment{{Author: "alice", Body: "hi"}, {Author: "bob", Body: "yo"}}}
	res, err := New(f, nil).Execute(context.Background(), call(map[string]any{"owner": "octo", "repo": "r", "number": 7}))
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if f.gotOwner != "octo" || f.gotNum != 7 {
		t.Errorf("lister called with owner=%q num=%d", f.gotOwner, f.gotNum)
	}
	if !strings.Contains(res.Content, "alice") || !strings.Contains(res.Content, `"count":2`) {
		t.Errorf("result missing comments/count: %s", res.Content)
	}
}

func TestListCommentsRequiresCoordinates(t *testing.T) {
	for _, args := range []map[string]any{
		{"repo": "r", "number": 1},
		{"owner": "o", "number": 1},
		{"owner": "o", "repo": "r"}, // number 0
	} {
		res, _ := New(&fakeLister{}, nil).Execute(context.Background(), call(args))
		if res.Error == "" {
			t.Errorf("args %v were accepted; want a required-field rejection", args)
		}
	}
}

func TestListCommentsFailsWithoutClient(t *testing.T) {
	res, _ := New(nil, nil).Execute(context.Background(), call(map[string]any{"owner": "o", "repo": "r", "number": 1}))
	if res.Error == "" {
		t.Error("expected an error when no GitHub client is wired")
	}
}

// The schema is content/coordinate-only — no outcome field (G3).
func TestSchemaHasNoOutcomeField(t *testing.T) {
	defs := New(nil, nil).ListTools()
	if len(defs) != 1 || defs[0].Name != ToolName {
		t.Fatalf("want one tool %q, got %+v", ToolName, defs)
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	for _, f := range []string{"owner", "repo", "number"} {
		if _, ok := props[f]; !ok {
			t.Errorf("schema missing %q", f)
		}
	}
	for _, forbidden := range []string{"outcome", "pass", "success", "verdict", "result", "body"} {
		if _, ok := props[forbidden]; ok {
			t.Errorf("schema exposes forbidden field %q (G3 / read-only)", forbidden)
		}
	}
}
