package openspec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// ignoreWarnings excludes the diagnostic-only Spec.Warnings field from
// round-trip comparisons — it is a parse-time artifact, not rendered content.
var ignoreWarnings = cmpopts.IgnoreFields(Spec{}, "Warnings")

// TestParseSpec_AuthFixture pins the parsed structure of a real OpenSpec
// capability spec — the RFC-2119 statement + Given/When/Then scenarios that
// ADR-057 §D3 corrected away from EARS.
func TestParseSpec_AuthFixture(t *testing.T) {
	spec := ParseSpec(readFixture(t, "auth-spec.md"))

	want := &Spec{
		Title:   "Auth Specification",
		Purpose: "User authentication and session management for the application.",
		Requirements: []Requirement{
			{
				Name:      "Password Login",
				Statement: "The system SHALL allow a registered user to authenticate with email and password.",
				Scenarios: []Scenario{
					{
						Name: "Valid credentials",
						Steps: []Step{
							{Keyword: "GIVEN", Text: "a registered user with a valid password"},
							{Keyword: "WHEN", Text: "the user submits the correct email and password"},
							{Keyword: "THEN", Text: "the system grants an authenticated session"},
							{Keyword: "AND", Text: "the session persists until logout or expiry"},
						},
					},
					{
						Name: "Invalid password",
						Steps: []Step{
							{Keyword: "GIVEN", Text: "a registered user"},
							{Keyword: "WHEN", Text: "the user submits an incorrect password"},
							{Keyword: "THEN", Text: "the system rejects the login"},
							{Keyword: "AND", Text: "the system does not reveal whether the email exists"},
						},
					},
				},
			},
			{
				Name:      "Session Expiry",
				Statement: "The system SHALL expire an idle session after 30 minutes of inactivity.",
				Scenarios: []Scenario{
					{
						Name: "Idle timeout",
						Steps: []Step{
							{Keyword: "GIVEN", Text: "an authenticated session"},
							{Keyword: "WHEN", Text: "30 minutes elapse with no activity"},
							{Keyword: "THEN", Text: "the system invalidates the session"},
						},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(want, spec); diff != "" {
		t.Errorf("ParseSpec mismatch (-want +got):\n%s", diff)
	}
}

// TestRoundTrip_SemanticStability is P1's gate (ADR-057 §D2): parse → render →
// parse must be stable on the modeled fields. We assert on the *parsed* model,
// not the byte output, because markdown formatting is a lossy projection.
func TestRoundTrip_SemanticStability(t *testing.T) {
	first := ParseSpec(readFixture(t, "auth-spec.md"))
	second := ParseSpec(RenderSpec(first))
	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("round-trip not semantically stable (-first +second):\n%s", diff)
	}
}

// TestRoundTrip_Idempotent checks render is a fixed point after the first pass:
// render(parse(render(parse(x)))) == render(parse(x)). This locks the canonical
// rendered form so future churn is visible.
func TestRoundTrip_Idempotent(t *testing.T) {
	once := RenderSpec(ParseSpec(readFixture(t, "auth-spec.md")))
	twice := RenderSpec(ParseSpec(once))
	if once != twice {
		t.Errorf("render not idempotent:\n once =\n%s\n twice =\n%s", once, twice)
	}
}

// TestParseSpec_EdgeCases is the table-driven robustness suite. Each case pins
// the *intended* parse (encoding the lenient-ingest decisions) AND asserts the
// result round-trips stably (ignoring diagnostic Warnings).
func TestParseSpec_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want *Spec
	}{
		{
			name: "asterisk and plus bullets recognised as steps",
			md:   "# T\n\n## Requirements\n\n### Requirement: R\nThe system SHALL x.\n\n#### Scenario: S\n* GIVEN a\n+ THEN b\n",
			want: &Spec{Title: "T", Requirements: []Requirement{{
				Name: "R", Statement: "The system SHALL x.",
				Scenarios: []Scenario{{Name: "S", Steps: []Step{
					{Keyword: "GIVEN", Text: "a"}, {Keyword: "THEN", Text: "b"}}}},
			}}},
		},
		{
			name: "orphan scenario before any requirement is dropped with a warning",
			md:   "# T\n\n#### Scenario: Loose\n- GIVEN a\n",
			want: &Spec{Title: "T", Warnings: []string{`scenario "Loose" before any requirement; dropped`}},
		},
		{
			name: "crlf line endings normalised",
			md:   "# T\r\n\r\n## Purpose\r\nHello there.\r\n",
			want: &Spec{Title: "T", Purpose: "Hello there."},
		},
		{
			name: "multi-line statement preserves authored lines",
			md:   "# T\n\n## Requirements\n\n### Requirement: R\nThe system SHALL support:\n- email login\n- sso login\n",
			want: &Spec{Title: "T", Requirements: []Requirement{{
				Name: "R", Statement: "The system SHALL support:\n- email login\n- sso login"}}},
		},
		{
			name: "requirement with no scenarios",
			md:   "# T\n\n## Requirements\n\n### Requirement: R\nThe system SHALL x.\n",
			want: &Spec{Title: "T", Requirements: []Requirement{{Name: "R", Statement: "The system SHALL x."}}},
		},
		{
			name: "unknown section is skipped",
			md:   "# T\n\n## Notes\nignore me\n\n## Requirements\n\n### Requirement: R\nThe system SHALL x.\n",
			want: &Spec{Title: "T", Requirements: []Requirement{{Name: "R", Statement: "The system SHALL x."}}},
		},
		{
			name: "empty input yields an empty spec",
			md:   "",
			want: &Spec{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSpec(tt.md)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseSpec mismatch (-want +got):\n%s", diff)
			}
			// Every edge case must also round-trip stably on modeled content
			// (Warnings are diagnostic and intentionally not preserved).
			if diff := cmp.Diff(got, ParseSpec(RenderSpec(got)), ignoreWarnings); diff != "" {
				t.Errorf("round-trip unstable (-got +reparsed):\n%s", diff)
			}
		})
	}
}

// TestParseStep covers the keyword/verbatim split, including the keyword-only
// and case-insensitive paths.
func TestParseStep(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Step
	}{
		{"keyword and text", "GIVEN a user", Step{Keyword: "GIVEN", Text: "a user"}},
		{"keyword only", "GIVEN", Step{Keyword: "GIVEN"}},
		{"lowercase keyword normalised", "then it works", Step{Keyword: "THEN", Text: "it works"}},
		{"non-keyword bullet preserved verbatim", "a bare note with no keyword", Step{Text: "a bare note with no keyword"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, parseStep(tt.in)); diff != "" {
				t.Errorf("parseStep(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}
