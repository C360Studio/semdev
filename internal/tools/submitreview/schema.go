package submitreview

import "github.com/c360studio/semstreams/agentic"

// ListTools returns submit_review's LLM-facing schema. The ONLY input is findings —
// the required changes Quinn raises. There is NO approve/verdict/outcome field (G3):
// the verdict is DERIVED here from the measured facts (measurement.result) and the
// findings, so the reviewer can require more but can never approve past a failing
// measurement. Approval is not Quinn's to assert; it is the harness's to compute.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"findings": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "The required changes you found reviewing the developed work against its task.spec. Each is an additive constraint that must be addressed before delivery; pass an empty list when you require nothing further. You cannot approve — approval is derived from the measured facts plus the absence of findings, and a failing measurement can never be approved however the work describes itself.",
			},
		},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Record your review of the developed work. Provide the required changes you found (findings); the verdict is computed from the harness measurements plus your findings — approved only when every required task has a passing measurement AND you raised no finding, otherwise changes_requested. You read the stamped measurement facts, not the work's claims about itself.",
		Parameters:  params,
	}}
}
