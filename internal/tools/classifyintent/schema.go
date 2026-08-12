package classifyintent

import (
	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semstreams/agentic"
)

// ListTools returns classify_intent's LLM-facing schema. The model supplies ONLY
// its JUDGMENT — the classified intent (from the closed taxonomy) and a short
// reason. It takes NO author and NO message_id: the message the classifier was
// spawned to read is already on the run's conversation.pending.* triples, so the
// HARNESS binds identity (D2 / architect H3 / semstreams HIGH-2). An LLM-supplied
// author would be a misattribution / approval-injection hole. There is likewise
// no outcome/verdict field (G3): the intent is a ROUTING signal the harness
// records, not a measurement — the consequential gate fact is stamped
// deterministically downstream (approval-adapter, G5), never here.
func (e *Executor) ListTools() []agentic.ToolDefinition {
	params := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"intent": map[string]any{
				"type":        "string",
				"enum":        conversationintent.Names(),
				"description": "Your reading of the human's single message: `approve` (an explicit directive to proceed with the proposed change), `reject` (an explicit directive to abandon it), or `none` (anything short of an explicit directive — ordinary chatter, a reaction, ambiguous positivity, or a question). Default to `none`: NEVER read approval from silence, a thumbs-up, or vague enthusiasm. You classify ONE message; you do not decide who may approve and you never name an author.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "One sentence quoting or paraphrasing the message text that drove your classification. It is recorded for the human's visibility; it is inert to the authorization path (the harness re-checks who may approve).",
			},
		},
		"required": []string{"intent", "reason"},
	}
	return []agentic.ToolDefinition{{
		Name:        ToolName,
		Effect:      agentic.ToolEffectMutating,
		Description: "Record your classification of the human's message at the change-approval gate as exactly one intent (approve / reject / none) plus a short reason. You supply only the reading; the harness binds the message's author and id from the run and re-checks authorization before anything happens. Default to `none` unless the message is an explicit directive to proceed or to abandon the change.",
		Parameters:  params,
	}}
}
