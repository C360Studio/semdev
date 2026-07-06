package openspec

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// ignoreDeltaWarnings excludes the diagnostic-only Delta.Warnings field from
// round-trip comparisons (Warnings is a parse-time artifact, not rendered).
var ignoreDeltaWarnings = cmpopts.IgnoreFields(Delta{}, "Warnings")

// TestParseDelta_AuthFixture pins the parsed structure of a real OpenSpec delta
// spec: ADDED (new requirement), MODIFIED (revised + "(Previously:)"), and
// REMOVED (name + "(Rationale:)") — ADR-057 §D1.
func TestParseDelta_AuthFixture(t *testing.T) {
	d := ParseDelta(readFixture(t, "auth-delta.md"))

	want := &Delta{
		Title: "Delta for Auth",
		Added: []Requirement{{
			Name:      "Multi-Factor Authentication",
			Statement: "The system SHALL require a second factor for accounts with MFA enabled.",
			Scenarios: []Scenario{{
				Name: "TOTP challenge",
				Steps: []Step{
					{Keyword: "GIVEN", Text: "a user with MFA enabled"},
					{Keyword: "WHEN", Text: "the user submits a valid password"},
					{Keyword: "THEN", Text: "the system prompts for a TOTP code"},
					{Keyword: "AND", Text: "grants the session only after a valid code"},
				},
			}},
		}},
		Modified: []ModifiedRequirement{{
			Requirement: Requirement{
				Name:      "Session Expiry",
				Statement: "The system SHALL expire an idle session after 15 minutes of inactivity.",
				Scenarios: []Scenario{{
					Name: "Idle timeout",
					Steps: []Step{
						{Keyword: "GIVEN", Text: "an authenticated session"},
						{Keyword: "WHEN", Text: "15 minutes elapse with no activity"},
						{Keyword: "THEN", Text: "the system invalidates the session"},
					},
				}},
			},
			Previously: "The system SHALL expire an idle session after 30 minutes of inactivity.",
		}},
		Removed: []RemovedRequirement{{
			Name:      "Remember Me",
			Rationale: "Long-lived sessions conflict with the new MFA policy.",
		}},
	}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Errorf("ParseDelta mismatch (-want +got):\n%s", diff)
	}
}

// TestDeltaRoundTrip_SemanticStability is the §D2 gate for deltas.
func TestDeltaRoundTrip_SemanticStability(t *testing.T) {
	first := ParseDelta(readFixture(t, "auth-delta.md"))
	second := ParseDelta(RenderDelta(first))
	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("delta round-trip not semantically stable (-first +second):\n%s", diff)
	}
}

// TestDeltaRoundTrip_Idempotent locks the canonical rendered form.
func TestDeltaRoundTrip_Idempotent(t *testing.T) {
	once := RenderDelta(ParseDelta(readFixture(t, "auth-delta.md")))
	twice := RenderDelta(ParseDelta(once))
	if once != twice {
		t.Errorf("delta render not idempotent:\n once =\n%s\n twice =\n%s", once, twice)
	}
}

func TestParseDelta_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want *Delta
	}{
		{
			name: "added section only",
			md:   "# D\n\n## ADDED Requirements\n\n### Requirement: R\nThe system SHALL x.\n",
			want: &Delta{Title: "D", Added: []Requirement{{Name: "R", Statement: "The system SHALL x."}}},
		},
		{
			name: "modified without a previously line is lenient",
			md:   "# D\n\n## MODIFIED Requirements\n\n### Requirement: R\nThe system SHALL y.\n",
			want: &Delta{Title: "D", Modified: []ModifiedRequirement{{Requirement: Requirement{Name: "R", Statement: "The system SHALL y."}}}},
		},
		{
			name: "removed without a rationale is lenient",
			md:   "# D\n\n## REMOVED Requirements\n\n### Requirement: R\n",
			want: &Delta{Title: "D", Removed: []RemovedRequirement{{Name: "R"}}},
		},
		{
			name: "requirement outside any section is dropped with a warning",
			md:   "# D\n\n### Requirement: Loose\nThe system SHALL z.\n",
			want: &Delta{Title: "D", Warnings: []string{`requirement "Loose" outside any ADDED/MODIFIED/REMOVED section; dropped`}},
		},
		{
			name: "lowercase (previously:) label matched case-insensitively",
			md:   "# D\n\n## MODIFIED Requirements\n\n### Requirement: R\nThe system SHALL x.\n(previously: old behavior)\n",
			want: &Delta{Title: "D", Modified: []ModifiedRequirement{{
				Requirement: Requirement{Name: "R", Statement: "The system SHALL x."},
				Previously:  "old behavior",
			}}},
		},
		{
			name: "prose after (Previously:) folds into statement and round-trips",
			md:   "# D\n\n## MODIFIED Requirements\n\n### Requirement: R\nThe system SHALL x.\n(Previously: old)\nExtra clause.\n",
			want: &Delta{Title: "D", Modified: []ModifiedRequirement{{
				Requirement: Requirement{Name: "R", Statement: "The system SHALL x.\nExtra clause."},
				Previously:  "old",
			}}},
		},
		{
			name: "removed requirement carrying content warns but keeps name+rationale",
			md:   "# D\n\n## REMOVED Requirements\n\n### Requirement: R\nThe system SHALL leftover.\n(Rationale: gone)\n",
			want: &Delta{
				Title:    "D",
				Removed:  []RemovedRequirement{{Name: "R", Rationale: "gone"}},
				Warnings: []string{`removed requirement "R" carries a statement or scenarios; only name + rationale are kept`},
			},
		},
		{
			name: "non-canonical section order normalises (round-trip stays stable)",
			md:   "# D\n\n## REMOVED Requirements\n\n### Requirement: Old\n(Rationale: gone)\n\n## ADDED Requirements\n\n### Requirement: New\nThe system SHALL w.\n",
			want: &Delta{
				Title:   "D",
				Added:   []Requirement{{Name: "New", Statement: "The system SHALL w."}},
				Removed: []RemovedRequirement{{Name: "Old", Rationale: "gone"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseDelta(tt.md)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseDelta mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(got, ParseDelta(RenderDelta(got)), ignoreDeltaWarnings); diff != "" {
				t.Errorf("round-trip unstable (-got +reparsed):\n%s", diff)
			}
		})
	}
}
