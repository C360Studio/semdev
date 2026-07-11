package provisionsandbox

import "github.com/c360studio/semstreams/agentic"

// ListTools returns provision_sandbox's schema. It takes NO input: the run's
// source, its declared image + run fields, and the run entity are all resolved
// from the run and the operator's committed declaration — the model may only
// TRIGGER provisioning, never supply what environment to build or whether it is
// ready (G3). Readiness is PROVEN cold (build the declared image, resolve+build
// the repo in a fresh cache), not asserted.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Description: "Provision the run's sandbox and prove it cold: materialize the target checkout, build the operator-declared image, and prove the repo resolves its base dependencies and builds in a fresh cache. Records the measured readiness/attestation (sandbox.ready + the digest-pinned image and proven tier) or, if the declared image cannot build the repo cold, a block toward the operator. Takes no arguments — you cannot supply the environment or the outcome; it is proven, not claimed. The dev loop cannot begin until a sandbox-scope tier is proven ready.",
		Parameters:  params,
	}}
}
