package validatechange

import "github.com/c360studio/semstreams/agentic"

// ListTools returns the validate_change tool's LLM-facing schema. The only input
// is the change slug — which change on the run to validate. It takes NO outcome /
// valid / pass field (G3): the verdict is the OpenSpec CLI's real exit code, which
// this tool's Go (the harness) reads and stamps; the model triggers the check but
// never supplies its result. The run entity is resolved from call metadata.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"slug": map[string]any{"type": "string", "description": "The change slug (changes/<slug>/) to validate with the OpenSpec CLI."},
		},
		"required": []string{"slug"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Validate a run's OpenSpec change with the real OpenSpec CLI (the deterministic compatibility oracle). Records openspec.validated only if the CLI passes; on failure returns the validator's issues. You supply only the slug — never a pass/fail (the harness reads the CLI's real exit code).",
		Parameters:  params,
	}}
}
