package conformance

import (
	"fmt"
	"testing"

	"github.com/c360studio/semstreams/agentic"
)

// Effect metadata census (migrate-semstreams-beta160 D6, ADR-089 adopter note).
//
// The framework's own source-level effect check covers ONLY its in-repo tool
// packages — an adopter's executors resolve to `unknown` silently, never to
// read_only. This census closes that gap for semdev: every tool the registry
// serves (built through boot.RegisterTools, the same seam as production and
// the G3 census) must carry an effect value that is present AND recognized, so
// a new semdev tool cannot ship unclassified or misspelled.
//
// Effect is DESCRIPTIVE (adopter note Rule 5): it alters no configured gate in
// either direction. The gate pins that prove that are the existing
// advertised-tools and approval-gate tests, which run unchanged in the same
// suite — classification landing green across them IS the no-gate-change
// evidence (task 6.3).
func TestEveryRegisteredToolDeclaresAWorstEffect(t *testing.T) {
	tools := semdevToolRegistry(t).ListTools()
	if len(tools) == 0 {
		t.Fatal("tool registry is empty; the effect census would pass vacuously")
	}
	for _, def := range tools {
		for _, msg := range effectViolations(def.Name, def.Effect) {
			t.Error(msg)
		}
	}
}

// effectViolations reports why one tool's declared effect fails the census.
// Split out so the red-first test below can drive it directly.
func effectViolations(name string, effect agentic.ToolEffect) []string {
	if effect == "" {
		return []string{fmt.Sprintf(
			"tool %q declares no effect — undeclared resolves to unknown, which a consumer must treat as at least as restrictive as external_effect; classify it (read_only / mutating / external_effect)",
			name,
		)}
	}
	switch effect {
	case agentic.ToolEffectReadOnly, agentic.ToolEffectMutating, agentic.ToolEffectExternal:
		return nil
	default:
		return []string{fmt.Sprintf(
			"tool %q declares unrecognized effect %q — a misspelled value survives decode as-received and canonicalizes to unknown; use the agentic.ToolEffect* constants",
			name, effect,
		)}
	}
}

// Red-first: the census must flag an absent and a misspelled effect, and must
// not false-flag any recognized class.
func TestEffectCensusCatchesUnclassifiedTools(t *testing.T) {
	if len(effectViolations("fake_tool", "")) == 0 {
		t.Error("census missed an ABSENT effect; an unclassified tool would ship silently")
	}
	if len(effectViolations("fake_tool", agentic.ToolEffect("read-only"))) == 0 {
		t.Error("census missed a MISSPELLED effect; it would canonicalize to unknown silently")
	}
	for _, ok := range []agentic.ToolEffect{agentic.ToolEffectReadOnly, agentic.ToolEffectMutating, agentic.ToolEffectExternal} {
		if len(effectViolations("fake_tool", ok)) != 0 {
			t.Errorf("census false-flagged recognized effect %q", ok)
		}
	}
}
