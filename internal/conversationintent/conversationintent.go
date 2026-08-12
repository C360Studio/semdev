// Package conversationintent is semdev's closed CONVERSATION-INTENT taxonomy
// (nl-conversation-intent D1) — the canonical set of intents a human's
// natural-language message on a run's thread can carry at the change-approval gate.
// It is the exact analog of internal/taxonomy (the coordinator's action taxonomy):
// this Go list is the source of truth; the `conversation` persona's decision-contract
// fragment and the intent-routing rules are pinned to it, and a conformance census
// fails on drift. The classifier persona READS a message and returns one of these
// (a ROUTING signal, never a measurement — G3); the deterministic apply consumer,
// not the LLM, stamps the consequential gate fact.
package conversationintent

import "slices"

// Intent is one member of the closed taxonomy.
type Intent string

// The three intents. `approve`/`reject` are the two directives that drive the gate;
// `none` is the conservative default for anything short of an explicit directive
// (ordinary chatter, ambiguous positivity) — it leaves the run gated.
const (
	Approve Intent = "approve"
	Reject  Intent = "reject"
	None    Intent = "none"
)

// Intents is the complete closed taxonomy. Order is presentational; the census
// treats it as a set.
var Intents = []Intent{Approve, Reject, None}

// Valid reports whether s is a member of the closed taxonomy — the routability check
// the "off-taxonomy intent is not routable" pin rests on (a hallucinated
// classification cannot drive the gate).
func Valid(s string) bool {
	return slices.Contains(Intents, Intent(s))
}

// Names returns the taxonomy as plain strings.
func Names() []string {
	out := make([]string, len(Intents))
	for i, v := range Intents {
		out[i] = string(v)
	}
	return out
}
