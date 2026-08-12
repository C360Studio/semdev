package openspec

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseProposal_DarkModeFixture(t *testing.T) {
	p := ParseProposal(readFixture(t, "dark-mode-proposal.md"))
	want := &Proposal{
		Title:  "Proposal: Add Dark Mode",
		Intent: "Users have requested a dark mode option to reduce eye strain during nighttime usage.",
		ScopeIn: []string{
			"Add a theme toggle in settings",
			"Support system preference detection",
		},
		ScopeOut: []string{"Per-component theme overrides"},
		Approach: "Introduce a ThemeContext backed by a CSS custom-property palette and persist the choice in localStorage.",
	}
	if diff := cmp.Diff(want, p); diff != "" {
		t.Errorf("ParseProposal mismatch (-want +got):\n%s", diff)
	}
}

func TestProposalRoundTrip(t *testing.T) {
	first := ParseProposal(readFixture(t, "dark-mode-proposal.md"))
	if diff := cmp.Diff(first, ParseProposal(RenderProposal(first))); diff != "" {
		t.Errorf("proposal round-trip not stable (-first +second):\n%s", diff)
	}
	once := RenderProposal(first)
	if twice := RenderProposal(ParseProposal(once)); once != twice {
		t.Errorf("proposal render not idempotent:\n once =\n%s\n twice =\n%s", once, twice)
	}
}

func TestParseProposal_PreservesExtraSections(t *testing.T) {
	md := "# Proposal: X\n\n## Intent\nship it\n\n## Future Roadmap\n### Skill Import Adapter\nMarkdown seeds a reviewed pack.\n"
	want := []MarkdownSection{{
		Heading: "Future Roadmap",
		Body:    "### Skill Import Adapter\nMarkdown seeds a reviewed pack.",
	}}

	got := ParseProposal(md)
	if diff := cmp.Diff(want, got.ExtraSections); diff != "" {
		t.Errorf("extra sections mismatch (-want +got):\n%s", diff)
	}
	rendered := RenderProposal(got)
	for _, want := range []string{
		"## Future Roadmap",
		"### Skill Import Adapter",
		"Markdown seeds a reviewed pack.",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered proposal missing %q\n%s", want, rendered)
		}
	}
	if diff := cmp.Diff(got, ParseProposal(rendered)); diff != "" {
		t.Errorf("proposal extra-section round-trip not stable (-got +reparsed):\n%s", diff)
	}
}

// TestParseProposal_FlatScopeDefaultsToInScope: a "## Scope" with a flat bullet
// list (no "In scope:" / "Out of scope:" labels) is taken as in-scope, and
// normalises to the labelled form on render.
func TestParseProposal_FlatScopeDefaultsToInScope(t *testing.T) {
	md := "# Proposal: X\n\n## Scope\n- a\n- b\n"
	want := &Proposal{Title: "Proposal: X", ScopeIn: []string{"a", "b"}}

	got := ParseProposal(md)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("flat-scope mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(got, ParseProposal(RenderProposal(got))); diff != "" {
		t.Errorf("round-trip unstable (-got +reparsed):\n%s", diff)
	}
}

func TestParseProposal_ScopeVariants(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want *Proposal
	}{
		{
			name: "only out of scope",
			md:   "# P\n\n## Scope\nOut of scope:\n- x\n",
			want: &Proposal{Title: "P", ScopeOut: []string{"x"}},
		},
		{
			name: "out-then-in normalises to in-before-out on render",
			md:   "# P\n\n## Scope\nOut of scope:\n- x\n\nIn scope:\n- y\n",
			want: &Proposal{Title: "P", ScopeIn: []string{"y"}, ScopeOut: []string{"x"}},
		},
		{
			name: "labels with no bullets yield no scope",
			md:   "# P\n\n## Scope\nIn scope:\nOut of scope:\n",
			want: &Proposal{Title: "P"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseProposal(tt.md)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseProposal mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(got, ParseProposal(RenderProposal(got))); diff != "" {
				t.Errorf("round-trip unstable (-got +reparsed):\n%s", diff)
			}
		})
	}
}
