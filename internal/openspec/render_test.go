package openspec

import (
	"strings"
	"testing"
)

// TestRenderChangeFolder_ComposesArtifactsWithMarkers verifies the single-doc
// projection: each present artifact appears under its openspec/changes/<slug>/
// file marker, and a Change reconstructed via ChangeFromFacts (which does not
// model the cosmetic "# Title" headings) renders with synthesized titles rather
// than an empty "# " line.
func TestRenderChangeFolder_ComposesArtifactsWithMarkers(t *testing.T) {
	// Round-trip through facts so the artifacts come back Title-less, exercising
	// the title-synthesis path (the real render_openspec input shape).
	src := &Change{
		Slug: "add-mfa",
		Proposal: &Proposal{
			Intent:   "Protect accounts.",
			ScopeIn:  []string{"TOTP"},
			ScopeOut: []string{},
			Approach: "Add a second factor.",
		},
		Deltas: []Delta{{
			Capability: "auth",
			Added: []Requirement{{
				Name:      "TOTP verification",
				Statement: "The system SHALL require a valid TOTP code.",
				Scenarios: []Scenario{{Name: "bad code", Steps: []Step{
					{Keyword: "WHEN", Text: "an incorrect code is submitted"},
					{Keyword: "THEN", Text: "the login is rejected"},
				}}},
			}},
		}},
		Tasks: &Tasks{Sections: []TaskSection{{Name: "1. Core", Tasks: []Task{
			{Number: "1.1", Text: "add totp"},
		}}}},
	}
	got := RenderChangeFolder(ChangeFromFacts("add-mfa", src.Facts()))

	for _, want := range []string{
		"# OpenSpec change: add-mfa",
		"<!-- openspec/changes/add-mfa/proposal.md -->",
		"<!-- openspec/changes/add-mfa/specs/auth/spec.md -->",
		"<!-- openspec/changes/add-mfa/tasks.md -->",
		"## Intent",
		"The system SHALL require a valid TOTP code.",
		"#### Scenario: bad code",
		"- [ ] 1.1 add totp",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderChangeFolder output missing %q\n--- got ---\n%s", want, got)
		}
	}

	// Title synthesis: no empty heading line should survive (the round-tripped
	// artifacts have Title="" and must be filled).
	if strings.Contains(got, "# \n") {
		t.Errorf("RenderChangeFolder left an empty '# ' heading (title not synthesized)\n%s", got)
	}
	// The delta's capability is used as its synthesized spec.md title.
	if !strings.Contains(got, "# auth") {
		t.Errorf("delta spec.md title not synthesized from capability; got:\n%s", got)
	}
}

// TestRenderChangeFolder_OmitsAbsentArtifacts confirms a sparse change (no
// design, no tasks) renders only the present artifacts' markers.
func TestRenderChangeFolder_OmitsAbsentArtifacts(t *testing.T) {
	c := &Change{
		Slug:     "minimal",
		Proposal: &Proposal{Intent: "why", Approach: "how"},
		Deltas: []Delta{{Capability: "cap", Added: []Requirement{{
			Name: "R", Statement: "The system SHALL X.",
			Scenarios: []Scenario{{Name: "s", Steps: []Step{{Keyword: "WHEN", Text: "a"}, {Keyword: "THEN", Text: "b"}}}},
		}}}},
	}
	got := RenderChangeFolder(c)
	if strings.Contains(got, "design.md") {
		t.Error("rendered a design.md marker for a change with no Design")
	}
	if strings.Contains(got, "tasks.md") {
		t.Error("rendered a tasks.md marker for a change with no Tasks")
	}
}
