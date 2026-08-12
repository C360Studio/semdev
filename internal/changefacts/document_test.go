package changefacts

import (
	"testing"

	"github.com/c360studio/semdev/internal/openspec"
	"github.com/google/go-cmp/cmp"
)

// The all-fields schema pin: every modeled field of the change document —
// including the Modified/Removed delta shapes and the Design artifact that
// live fixtures rarely exercise — survives Marshal∘Unmarshal verbatim. This is
// the guard that bites if a field ever gains a `json:"-"` tag, goes unexported,
// or otherwise silently stops round-tripping through the canonical blob (the
// conformance pin over the active change only covers the fields that fixture
// populates). Nil-vs-empty distinctions are asserted where they are
// load-bearing (RichTask lists — see the RichTask doc).
func TestDocumentRoundTripsAllFields(t *testing.T) {
	budget := 2
	doc := ChangeDocument{
		Change: &openspec.Change{
			Slug: "add-mfa",
			Proposal: &openspec.Proposal{
				Title:         "Proposal: Add MFA",
				Intent:        "Protect accounts.",
				ScopeIn:       []string{"TOTP"},
				ScopeOut:      []string{"SMS"},
				Approach:      "Add a second factor.",
				ExtraSections: []openspec.MarkdownSection{{Heading: "Future", Body: "WebAuthn later."}},
			},
			Design: &openspec.Design{
				Title:             "Design: Add MFA",
				TechnicalApproach: "Server-side TOTP validation.",
				Decisions:         []openspec.DesignDecision{{Name: "D1", Body: "RFC 6238, 30s step."}},
				DataFlow:          "login -> challenge -> verify",
				FileChanges:       []openspec.FileChange{{Path: "auth/totp.go", Kind: "added"}},
				ExtraSections:     []openspec.MarkdownSection{{Heading: "Risks", Body: "Clock skew."}},
			},
			Deltas: []openspec.Delta{{
				Capability: "auth",
				Title:      "auth",
				Added: []openspec.Requirement{{
					Name:      "TOTP verification",
					Statement: "The system SHALL require a valid TOTP code.",
					Scenarios: []openspec.Scenario{{Name: "bad code", Steps: []openspec.Step{
						{Keyword: "WHEN", Text: "an incorrect code is submitted"},
						{Keyword: "THEN", Text: "the login is rejected"},
					}}},
				}},
				Modified: []openspec.ModifiedRequirement{{
					Requirement: openspec.Requirement{
						Name:      "Password Login",
						Statement: "The system SHALL require a second factor after password auth.",
						Scenarios: []openspec.Scenario{{Name: "otp prompt", Steps: []openspec.Step{
							{Keyword: "WHEN", Text: "a valid password is submitted"},
							{Keyword: "THEN", Text: "a TOTP challenge is issued"},
						}}},
					},
					Previously: "The system SHALL allow password-only login.",
				}},
				Removed:  []openspec.RemovedRequirement{{Name: "Remember me", Rationale: "Weakens MFA."}},
				Warnings: []string{"bullet variance in scenario 2"},
			}},
			Tasks: &openspec.Tasks{
				Title: "Tasks",
				Sections: []openspec.TaskSection{{
					Name: "1. Core",
					Tasks: []openspec.Task{
						{Number: "1.1", Text: "add totp", Done: true},
						{Number: "1.2", Text: "wire challenge", Done: false},
					},
				}},
			},
		},
		RichTasks: []RichTask{
			{
				Index:       0,
				TargetFiles: []string{"auth/totp.go", "auth/totp_test.go"},
				TestCommand: "go test ./auth/",
				Assumptions: []string{"server clock is NTP-synced"},
				NonGoals:    []string{"SMS fallback"},
				Budget:      &budget,
			},
			{
				// Authored-empty lists (non-nil) vs absent budget (nil): the
				// gap-vs-empty distinction the RichTask doc declares load-bearing.
				Index:       1,
				TargetFiles: []string{},
				Assumptions: []string{},
				NonGoals:    []string{},
			},
		},
	}

	blob, err := MarshalDocument(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalDocument(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if diff := cmp.Diff(doc, got); diff != "" {
		t.Errorf("document did not round-trip (-authored +decoded):\n%s", diff)
	}

	// The load-bearing nil-vs-empty assertions, explicit (cmp already
	// distinguishes them; these make the contract legible on failure).
	if got.RichTasks[1].TargetFiles == nil {
		t.Error("authored-empty target_files decoded as nil — the gap-vs-empty distinction collapsed")
	}
	if got.RichTasks[1].Budget != nil {
		t.Error("absent budget decoded as non-nil")
	}
}
