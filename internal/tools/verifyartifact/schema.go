package verifyartifact

import "github.com/c360studio/semstreams/agentic"

// ListTools returns verify_artifact's schema. It takes NO input: the artifact, its
// reproducibility manifest, and the run entity are all resolved from the run — the
// model may only TRIGGER the clean-room proof, never supply what it proves or the
// outcome it records (G3). The verdict is measured in fresh isolation, not asserted.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Verify the delivered artifact in a fresh clean room: provision isolation with a cold build-cache home, resolve and build from the artifact's own declarations, run its own tests, and record the measured verify.result (pass/fail/retry). Takes no arguments — you cannot supply the outcome; it is proven, not claimed. A run cannot open a PR until this records a pass.",
		Parameters:  params,
	}}
}
