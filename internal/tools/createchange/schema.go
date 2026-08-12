package createchange

import "github.com/c360studio/semstreams/agentic"

// ListTools returns the create_change tool's LLM-facing schema. The input is the
// change CONTENT only — proposal, spec deltas, tasks — mirroring the OpenSpec
// model. It accepts NO outcome/validated/pass field (G3): validation is a
// separate harness step, and the run entity is resolved for the tool from call
// metadata, not supplied by the model.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	stringArray := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
		if len(required) > 0 {
			s["required"] = append([]string(nil), required...)
		}
		return s
	}
	arrayOf := func(item map[string]any, desc string) map[string]any {
		return map[string]any{"type": "array", "items": item, "description": desc}
	}

	step := obj(map[string]any{
		"kw":   map[string]any{"type": "string", "description": "Step keyword: GIVEN, WHEN, THEN, or AND."},
		"text": map[string]any{"type": "string", "description": "The step text after the keyword."},
	}, "kw", "text")
	scenario := obj(map[string]any{
		"name":  map[string]any{"type": "string", "description": "Short scenario name."},
		"steps": arrayOf(step, "Given/When/Then steps; include at least a WHEN and a THEN."),
	}, "name", "steps")
	requirement := obj(map[string]any{
		"name":      map[string]any{"type": "string", "description": "Requirement name (after '### Requirement:')."},
		"statement": map[string]any{"type": "string", "description": "RFC-2119 statement, e.g. 'The system SHALL ...'."},
		"scenarios": arrayOf(scenario, "Acceptance scenarios for the requirement."),
	}, "name", "statement", "scenarios")
	modified := obj(map[string]any{
		"name":       map[string]any{"type": "string"},
		"statement":  map[string]any{"type": "string"},
		"scenarios":  arrayOf(scenario, "Revised acceptance scenarios."),
		"previously": map[string]any{"type": "string", "description": "The prior statement being revised."},
	}, "name", "statement", "scenarios")
	removed := obj(map[string]any{
		"name":      map[string]any{"type": "string"},
		"rationale": map[string]any{"type": "string", "description": "Why the requirement is removed."},
	}, "name", "rationale")
	delta := obj(map[string]any{
		"capability": map[string]any{"type": "string", "description": "The capability this spec delta belongs to (specs/<capability>/)."},
		"added":      arrayOf(requirement, "Brand-new requirements."),
		"modified":   arrayOf(modified, "Revised requirements."),
		"removed":    arrayOf(removed, "Removed requirements."),
	}, "capability")

	// No `done` / completion field: a freshly authored change's tasks are never
	// pre-completed. Task status is DERIVED from execution markers and gate facts
	// (dev-from-task spec), not authored — the author tool coerces every task to
	// not-done regardless of what the model supplies.
	// The execution-rich fields are dev-from-task INPUTS (all authoring intent, no
	// outcomes — G3): target_files is where the work lands, test_command is how it is
	// checked (a command string, never a result), budget is the requested iteration
	// bound (never an attempt count). They are graph-only (not rendered into
	// tasks.md). The projector enforces the Karpathy schema over them; this tool only
	// records them, so none is `required` here — a task authored without one is
	// stamped partial and the projector fails it toward the human.
	intBudget := map[string]any{"type": "integer", "description": "Requested iteration budget for this task; the projector clamps it to [1,5]."}
	task := obj(map[string]any{
		"number":       map[string]any{"type": "string", "description": "OpenSpec dotted number, e.g. '1.1'."},
		"text":         map[string]any{"type": "string", "description": "The task description (the projector's goal)."},
		"target_files": stringArray("Files this task will create or change (at least one). MUST include the *_test.go file its test_command measures — the test that proves the work is part of the work, so list it even when it already exists and only the source file changes. A task whose target_files carry no test file is REJECTED at projection and the run stalls toward the human."),
		"test_command": map[string]any{"type": "string", "description": "The command that verifies this task, e.g. 'go test ./...'."},
		"assumptions":  stringArray("Assumptions the task relies on (state them even if empty)."),
		"non_goals":    stringArray("What this task explicitly does NOT do (state them even if empty)."),
		"budget":       intBudget,
	}, "text")
	taskSection := obj(map[string]any{
		"section": map[string]any{"type": "string", "description": "The task group heading, e.g. '1. Foundation'."},
		"items":   arrayOf(task, "Tasks in this section."),
	}, "section", "items")

	proposal := obj(map[string]any{
		"intent":    map[string]any{"type": "string", "description": "Why this change exists (the '## Intent' prose)."},
		"scope_in":  stringArray("What is in scope."),
		"scope_out": stringArray("What is explicitly out of scope."),
		"approach":  map[string]any{"type": "string", "description": "How the change will be carried out."},
	}, "intent")

	params := obj(map[string]any{
		"slug":     map[string]any{"type": "string", "description": "The change slug (changes/<slug>/ folder name)."},
		"proposal": proposal,
		"deltas":   arrayOf(delta, "One spec delta per affected capability."),
		"tasks":    arrayOf(taskSection, "Implementation tasks, grouped by section."),
	}, "slug", "proposal", "deltas", "tasks")

	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectMutating,
		Description: "Author an OpenSpec change from an intaken issue. Stamps openspec.change.* facts on the run entity (resolved for you); the graph is authoritative and the artifacts are hydrated from it. Emit the change CONTENT only — never a validation outcome.",
		Parameters:  params,
	}}
}
