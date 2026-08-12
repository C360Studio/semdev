package openspec

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseDesign_DarkModeFixture(t *testing.T) {
	d := ParseDesign(readFixture(t, "dark-mode-design.md"))
	want := &Design{
		Title:             "Design: Add Dark Mode",
		TechnicalApproach: "A ThemeContext drives a data-theme attribute on the document root; colors resolve through CSS custom properties so a switch needs no re-render.",
		Decisions: []DesignDecision{
			{
				Name: "CSS custom properties over CSS-in-JS",
				Body: "Custom properties let the theme switch without re-rendering the tree and keep the palette in one place.",
			},
			{
				Name: "Persist in localStorage, not a cookie",
				Body: "The preference is client-only and never needs to reach the server.",
			},
		},
		DataFlow: "Settings toggle -> ThemeContext -> data-theme attribute -> CSS variable cascade.",
		FileChanges: []FileChange{
			{Path: "src/theme/ThemeContext.tsx", Kind: "new"},
			{Path: "src/styles/tokens.css", Kind: "modified"},
			{Path: "src/legacy/themeShim.js", Kind: "deleted"},
		},
	}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Errorf("ParseDesign mismatch (-want +got):\n%s", diff)
	}
}

func TestDesignRoundTrip(t *testing.T) {
	first := ParseDesign(readFixture(t, "dark-mode-design.md"))
	if diff := cmp.Diff(first, ParseDesign(RenderDesign(first))); diff != "" {
		t.Errorf("design round-trip not stable (-first +second):\n%s", diff)
	}
	once := RenderDesign(first)
	if twice := RenderDesign(ParseDesign(once)); once != twice {
		t.Errorf("design render not idempotent:\n once =\n%s\n twice =\n%s", once, twice)
	}
}

func TestParseDesign_PreservesExtraSections(t *testing.T) {
	md := "# Design: X\n\n## Technical Approach\nUse graph facts.\n\n## Future Roadmap\n### Skill Import Adapter\nMarkdown becomes a reviewed OpenSpec change.\n"
	want := []MarkdownSection{{
		Heading: "Future Roadmap",
		Body:    "### Skill Import Adapter\nMarkdown becomes a reviewed OpenSpec change.",
	}}

	got := ParseDesign(md)
	if diff := cmp.Diff(want, got.ExtraSections); diff != "" {
		t.Errorf("extra sections mismatch (-want +got):\n%s", diff)
	}
	rendered := RenderDesign(got)
	for _, want := range []string{
		"## Future Roadmap",
		"### Skill Import Adapter",
		"Markdown becomes a reviewed OpenSpec change.",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered design missing %q\n%s", want, rendered)
		}
	}
	if diff := cmp.Diff(got, ParseDesign(rendered)); diff != "" {
		t.Errorf("design extra-section round-trip not stable (-got +reparsed):\n%s", diff)
	}
}

func TestParseFileChange(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want FileChange
		ok   bool
	}{
		{"path and kind", "- `a/b.go` (new)", FileChange{Path: "a/b.go", Kind: "new"}, true},
		{"path only", "- `a/b.go`", FileChange{Path: "a/b.go"}, true},
		{"asterisk bullet", "* `a/b.go` (deleted)", FileChange{Path: "a/b.go", Kind: "deleted"}, true},
		{"no backtick path", "- a/b.go (new)", FileChange{}, false},
		{"not a bullet", "`a/b.go` (new)", FileChange{}, false},
		{"unterminated backtick", "- `a/b.go (new)", FileChange{}, false},
		{"trailing text after kind drops kind", "- `x.go` (new) note", FileChange{Path: "x.go"}, true},
		{"multi-word kind kept", "- `x.go` (new, urgent)", FileChange{Path: "x.go", Kind: "new, urgent"}, true},
		{"inner backtick takes first pair", "- `a`b`.go` (new)", FileChange{Path: "a"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseFileChange(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseFileChange(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
