package intake

import (
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/taxonomy"
	"github.com/c360studio/semstreams/agentic"
)

// TestCoordinatorTaskFrontDoorShape pins the load-bearing shape of the
// front-door wake: the three fields that decide whether the coordinator can
// route at all. A regression on any of them is the "reaches the model but never
// decides" failure the first front-door slice hit.
func TestCoordinatorTaskFrontDoorShape(t *testing.T) {
	in := Intake{Relevant: true, IssueRef: "octo/widget#42"}

	task, err := CoordinatorTask(in, "mock")
	if err != nil {
		t.Fatalf("CoordinatorTask: %v", err)
	}

	if task.Role != "coordinator" {
		t.Errorf("Role = %q, want coordinator", task.Role)
	}
	if task.Model != "mock" {
		t.Errorf("Model = %q, want mock", task.Model)
	}

	// nil, NOT empty: nil is global discovery (advertises decide); []{} advertises
	// zero tools and the coordinator can never route. This distinction is the whole
	// bug the field guards against, so assert it precisely.
	if task.Tools != nil {
		t.Errorf("Tools = %#v, want nil (global discovery); an empty slice advertises zero tools", task.Tools)
	}

	if task.ToolChoice == nil || task.ToolChoice.Mode != "required" {
		t.Errorf("ToolChoice = %#v, want mode=required", task.ToolChoice)
	}

	raw, ok := task.Metadata[agentic.MetadataKeyDecideActionAllowlist]
	if !ok {
		t.Fatalf("Metadata missing %q; decide would run unconstrained", agentic.MetadataKeyDecideActionAllowlist)
	}
	allowlist, ok := raw.([]string)
	if !ok {
		t.Fatalf("action allowlist is %T, want []string", raw)
	}
	if !slices.Equal(allowlist, taxonomy.Names()) {
		t.Errorf("action allowlist = %v, want the closed taxonomy %v", allowlist, taxonomy.Names())
	}

	// The mock keys its scripted decide turn on a substring of the prompt, so the
	// issue ref must appear verbatim; and the prompt must actually invoke decide.
	if !strings.Contains(task.Prompt, in.IssueRef) {
		t.Errorf("prompt does not mention the issue ref %q", in.IssueRef)
	}
	if !strings.Contains(task.Prompt, "decide") {
		t.Errorf("prompt does not instruct the coordinator to use the decide tool")
	}
}

// TestCoordinatorTaskAllowlistCoversEveryAction guards the invariant that the
// front door constrains decide to EXACTLY the closed taxonomy — every action a
// downstream rule can route on is admissible, and nothing outside it is.
func TestCoordinatorTaskAllowlistCoversEveryAction(t *testing.T) {
	task, err := CoordinatorTask(Intake{IssueRef: "o/r#1"}, "mock")
	if err != nil {
		t.Fatalf("CoordinatorTask: %v", err)
	}
	allowlist, _ := task.Metadata[agentic.MetadataKeyDecideActionAllowlist].([]string)

	for _, a := range taxonomy.Actions {
		if !slices.Contains(allowlist, string(a)) {
			t.Errorf("taxonomy action %q missing from the front-door allowlist", a)
		}
	}
	if len(allowlist) != len(taxonomy.Actions) {
		t.Errorf("allowlist has %d entries, want exactly the %d-action taxonomy (no extras)", len(allowlist), len(taxonomy.Actions))
	}
}

// TestCoordinatorTaskRejectsEmptyInputs fails closed: no issue ref or no model
// means the caller has nothing routable, so building a wake is an error rather
// than a task that spawns a loop with a blank prompt or an unresolvable model.
func TestCoordinatorTaskRejectsEmptyInputs(t *testing.T) {
	if _, err := CoordinatorTask(Intake{IssueRef: ""}, "mock"); err == nil {
		t.Error("empty issue ref: want error, got nil")
	}
	if _, err := CoordinatorTask(Intake{IssueRef: "  "}, "mock"); err == nil {
		t.Error("whitespace issue ref: want error, got nil")
	}
	if _, err := CoordinatorTask(Intake{IssueRef: "o/r#1"}, ""); err == nil {
		t.Error("empty model: want error, got nil")
	}
}
