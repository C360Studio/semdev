package openspec

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// ignoreHeadings drops the cosmetic Title fields (and diagnostic Warnings) from
// fact round-trip comparisons: ADR-057 §D1 models no title predicate, so a
// "# heading" is a render-synthesised projection, not graph content (§D2 blesses
// formatting drift). Everything else must survive Facts∘FromFacts.
var ignoreHeadings = cmp.Options{
	cmpopts.IgnoreFields(Spec{}, "Title", "Warnings"),
	cmpopts.IgnoreFields(Delta{}, "Title", "Warnings"),
	cmpopts.IgnoreFields(Proposal{}, "Title"),
	cmpopts.IgnoreFields(Design{}, "Title"),
	cmpopts.IgnoreFields(Tasks{}, "Title"),
}

// factMap indexes facts by predicate for assertion. It assumes unique
// predicates (the emit invariant — one rid minter per Facts()), so unlike
// production indexFacts (first-write-wins) its last-write-wins is immaterial.
func factMap(facts []Fact) map[string]string {
	m := make(map[string]string, len(facts))
	for _, f := range facts {
		m[f.Predicate] = f.Object
	}
	return m
}

// TestSpecFacts_RoundTrip_AuthFixture is the living-spec half of slice 5's gate
// (ADR-057 §D2): the parsed model -> facts -> model is stable on modeled fields.
func TestSpecFacts_RoundTrip_AuthFixture(t *testing.T) {
	specs, err := ReadSpecs("testdata/openspec/specs")
	if err != nil {
		t.Fatalf("ReadSpecs: %v", err)
	}
	if len(specs) != 1 || specs[0].Capability != "auth" {
		t.Fatalf("want one auth spec, got %+v", specs)
	}
	orig := specs[0]
	got := SpecFromFacts("auth", orig.Facts())
	if diff := cmp.Diff(&orig, got, ignoreHeadings); diff != "" {
		t.Errorf("Spec fact round-trip mismatch (-orig +got):\n%s", diff)
	}
}

// TestChangeFacts_RoundTrip_AddMFAFixture is the change-folder half of the gate:
// a real proposal+design+tasks+delta change survives Facts∘FromFacts.
func TestChangeFacts_RoundTrip_AddMFAFixture(t *testing.T) {
	c, err := ReadChange("testdata/openspec/changes/add-mfa")
	if err != nil {
		t.Fatalf("ReadChange: %v", err)
	}
	got := ChangeFromFacts(c.Slug, c.Facts())
	if diff := cmp.Diff(c, got, ignoreHeadings); diff != "" {
		t.Errorf("Change fact round-trip mismatch (-orig +got):\n%s", diff)
	}
}

// TestSpecFacts_PredicateShape pins the spec.<cap>.* predicate keys (the §D1
// contract) so an accidental rename is caught, and asserts <rid> = slugified
// requirement name with the original name preserved in .name.
func TestSpecFacts_PredicateShape(t *testing.T) {
	s := &Spec{
		Capability: "auth",
		Purpose:    "Auth and sessions.",
		Requirements: []Requirement{{
			Name:      "Password Login",
			Statement: "The system SHALL allow email+password login.",
			Scenarios: []Scenario{{Name: "Valid", Steps: []Step{{Keyword: "WHEN", Text: "valid creds"}, {Keyword: "THEN", Text: "session granted"}}}},
		}},
	}
	m := factMap(s.Facts())

	want := map[string]string{
		"openspec.spec.auth.purpose":                              "Auth and sessions.",
		"openspec.spec.auth.requirement.password-login.name":      "Password Login",
		"openspec.spec.auth.requirement.password-login.statement": "The system SHALL allow email+password login.",
		"openspec.spec.auth.requirement.password-login.position":  "0",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("fact %q = %q, want %q", k, m[k], v)
		}
	}
	// No lifecycle status predicate is emitted by the pure mapping (writer-only).
	if _, ok := m["openspec.spec.auth.requirement.password-login.status"]; ok {
		t.Error("status predicate must not be emitted by the pure mapping")
	}
	// Scenarios are one JSON-encoded triple object in the §D1 {name,steps:[{kw,text}]} shape.
	gotScenarios := m["openspec.spec.auth.requirement.password-login.scenarios"]
	wantScenarios := `[{"name":"Valid","steps":[{"kw":"WHEN","text":"valid creds"},{"kw":"THEN","text":"session granted"}]}]`
	if gotScenarios != wantScenarios {
		t.Errorf("scenarios JSON = %q, want %q", gotScenarios, wantScenarios)
	}
}

// TestChangeFacts_PredicateShape pins the change.<slug>.* delta + proposal keys,
// including the delta op markers and the previously/rationale fields.
func TestChangeFacts_PredicateShape(t *testing.T) {
	c := &Change{
		Slug:     "add-mfa",
		Proposal: &Proposal{Intent: "why", ScopeIn: []string{"a", "b"}, Approach: "how"},
		Deltas: []Delta{{
			Capability: "auth",
			Added:      []Requirement{{Name: "MFA", Statement: "The system SHALL require MFA."}},
			Modified:   []ModifiedRequirement{{Requirement: Requirement{Name: "Session Expiry", Statement: "SHALL expire after 15m."}, Previously: "SHALL expire after 30m."}},
			Removed:    []RemovedRequirement{{Name: "Remember Me", Rationale: "conflicts with MFA"}},
		}},
	}
	m := factMap(c.Facts())

	want := map[string]string{
		"openspec.change.add-mfa.proposal.intent":                      "why",
		"openspec.change.add-mfa.proposal.scope_in":                    `["a","b"]`,
		"openspec.change.add-mfa.proposal.approach":                    "how",
		"openspec.change.add-mfa.delta.auth.mfa.op":                    "added",
		"openspec.change.add-mfa.delta.auth.mfa.statement":             "The system SHALL require MFA.",
		"openspec.change.add-mfa.delta.auth.session-expiry.op":         "modified",
		"openspec.change.add-mfa.delta.auth.session-expiry.previously": "SHALL expire after 30m.",
		"openspec.change.add-mfa.delta.auth.remember-me.op":            "removed",
		"openspec.change.add-mfa.delta.auth.remember-me.rationale":     "conflicts with MFA",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("fact %q = %q, want %q", k, m[k], v)
		}
	}
	// acceptance_command / status are writer-only — not in the pure mapping.
	for _, forbidden := range []string{"openspec.change.add-mfa.acceptance_command", "openspec.change.add-mfa.status"} {
		if _, ok := m[forbidden]; ok {
			t.Errorf("%q must not be emitted by the pure mapping", forbidden)
		}
	}
}

// TestFacts_RIDCollision proves two requirements whose names slugify equal get
// distinct <rid> keys, and that the round-trip recovers both names (read from
// .name, never un-slugified from the disambiguated rid). ADR-057 OQ2.
func TestFacts_RIDCollision(t *testing.T) {
	s := &Spec{
		Capability: "auth",
		Requirements: []Requirement{
			{Name: "Log In", Statement: "SHALL a."},
			{Name: "Log-In", Statement: "SHALL b."}, // slugifies to the same "log-in"
		},
	}
	facts := s.Facts()
	m := factMap(facts)
	if m["openspec.spec.auth.requirement.log-in.name"] != "Log In" {
		t.Errorf("first rid name = %q, want %q", m["openspec.spec.auth.requirement.log-in.name"], "Log In")
	}
	if m["openspec.spec.auth.requirement.log-in-2.name"] != "Log-In" {
		t.Errorf("disambiguated rid name = %q, want %q", m["openspec.spec.auth.requirement.log-in-2.name"], "Log-In")
	}
	got := SpecFromFacts("auth", facts)
	if diff := cmp.Diff(s, got, ignoreHeadings); diff != "" {
		t.Errorf("collision round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestChangeFacts_RoundTrip_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		in   *Change
	}{
		{
			name: "multi-capability delta sorts by capability",
			in: &Change{Slug: "c", Deltas: []Delta{
				{Capability: "auth", Added: []Requirement{{Name: "A", Statement: "SHALL a."}}},
				{Capability: "billing", Added: []Requirement{{Name: "B", Statement: "SHALL b."}}},
			}},
		},
		{
			name: "capability name containing a dot still right-parses",
			in: &Change{Slug: "c", Deltas: []Delta{
				{Capability: "auth.v2", Removed: []RemovedRequirement{{Name: "Old", Rationale: "gone"}}},
			}},
		},
		{
			name: "more than nine tasks keep numeric order across sections",
			in: func() *Change {
				secA := TaskSection{Name: "A"}
				secB := TaskSection{Name: "B"}
				for i := range 7 {
					secA.Tasks = append(secA.Tasks, Task{Text: "a", Done: i%2 == 0})
				}
				for i := range 5 {
					secB.Tasks = append(secB.Tasks, Task{Number: "2." + string(rune('1'+i)), Text: "b"})
				}
				return &Change{Slug: "c", Tasks: &Tasks{Sections: []TaskSection{secA, secB}}}
			}(),
		},
		{
			name: "implicit (empty-name) leading section",
			in: &Change{Slug: "c", Tasks: &Tasks{Sections: []TaskSection{
				{Name: "", Tasks: []Task{{Number: "1", Text: "loose"}}},
				{Name: "Group", Tasks: []Task{{Number: "2", Text: "grouped"}}},
			}}},
		},
		{
			name: "design with decisions and file changes",
			in: &Change{Slug: "c", Design: &Design{
				TechnicalApproach: "layer it",
				Decisions:         []DesignDecision{{Name: "X over Y", Body: "because"}},
				DataFlow:          "a -> b",
				FileChanges:       []FileChange{{Path: "a.go", Kind: "new"}, {Path: "b.go"}},
			}},
		},
		{
			// A fully-zero task (blank number/text, done=false) emits only its
			// always-stamped "<i>.done=false" fact; intChildIndices must still
			// discover it (pins why addRaw, not add, stamps done).
			name: "fully-zero task survives on its done fact alone",
			in: &Change{Slug: "c", Tasks: &Tasks{Sections: []TaskSection{
				{Name: "", Tasks: []Task{{}, {Number: "2", Text: "real"}}},
			}}},
		},
		{
			name: "wholly empty change round-trips to all-nil",
			in:   &Change{Slug: "c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChangeFromFacts(tt.in.Slug, tt.in.Facts())
			if diff := cmp.Diff(tt.in, got, ignoreHeadings); diff != "" {
				t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestScenarioJSON_Shape locks the encoded scenario wire shape independently of
// the Go field names — the ADR-057 §D1 {name, steps:[{kw,text}]} contract that a
// later consumer (ADR-054/055 claim analysis) parses.
func TestScenarioJSON_Shape(t *testing.T) {
	encoded := encodeScenarios([]Scenario{{
		Name:  "S",
		Steps: []Step{{Keyword: "GIVEN", Text: "x"}, {Text: "bare"}},
	}})
	var generic []map[string]any
	if err := json.Unmarshal([]byte(encoded), &generic); err != nil {
		t.Fatalf("scenario JSON does not parse: %v", err)
	}
	if generic[0]["name"] != "S" {
		t.Errorf("scenario name key wrong: %v", generic[0])
	}
	steps, _ := generic[0]["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %v", steps)
	}
	first, _ := steps[0].(map[string]any)
	if first["kw"] != "GIVEN" || first["text"] != "x" {
		t.Errorf("step uses wrong keys: %v", first)
	}
	// A keyword-less bare step omits kw (omitempty) but keeps text.
	second, _ := steps[1].(map[string]any)
	if _, hasKW := second["kw"]; hasKW || second["text"] != "bare" {
		t.Errorf("bare step shape wrong: %v", second)
	}
}
