package intake

import (
	"fmt"
	"strings"

	"github.com/c360studio/semdev/internal/taxonomy"
	"github.com/c360studio/semstreams/agentic"
)

// FrontDoorSubject is the AGENT-stream subject a coordinator wake is published
// to. agentic-loop subscribes agent.task.* with a SINGLE-token wildcard, so the
// subject MUST be exactly two segments — a longer subject silently drops into the
// void (the loop never sees it and no run is ever spawned). The intake adapter
// and the group-11 journey publish here.
const FrontDoorSubject = "agent.task.coordinator"

// coordinatorRole is the persona role the front-door loop runs as. It matches the
// persona fragment directory (configs/personas/fragments/coordinator) seeded into
// the PERSONAS bucket, so the loop assembles Sarah's decision contract.
const coordinatorRole = "coordinator"

// CoordinatorTask builds the front-door coordinator wake for an admitted intake:
// the TaskMessage that spawns a run's first coordinator loop. It is the host-way
// front door — publishing this to FrontDoorSubject is NOT a G2 lifecycle
// transition (the framework mints the run when a rule fires on the coordinator's
// resulting decide); product Go never fires the transition itself.
//
// Three fields are load-bearing and easy to get wrong:
//
//   - Tools is left nil, NOT an empty slice. nil means "global discovery" — the
//     agentic-loop advertises every registered executor, the framework `decide`
//     tool among them, so the coordinator can route. An explicit []{} means "no
//     tools", and the coordinator could never call decide (the exact reason the
//     first front-door slice reached the model but never routed).
//   - ToolChoice is required, so a weak model cannot terminate text-only without
//     ever calling decide (semstreams#132/#158 class); the coordinator stays on
//     the tool path.
//   - Metadata carries the closed decide-action allowlist (the whole taxonomy).
//     The loop propagates task Metadata onto every ToolCall, and the decide tool
//     rejects any action outside it — so an off-taxonomy action re-picks rather
//     than landing an unroutable decision fact.
//
// The prompt states the situation and names the issue by its host-neutral ref;
// it does NOT prescribe the action — the seeded persona's decision contract maps
// "a newly admitted issue with no run yet" to its terminal. model is the
// model_registry capability or endpoint the coordinator runs on.
func CoordinatorTask(in Intake, model string) (*agentic.TaskMessage, error) {
	ref := strings.TrimSpace(in.IssueRef)
	if ref == "" {
		return nil, fmt.Errorf("intake: coordinator task needs a non-empty issue ref")
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("intake: coordinator task needs a non-empty model")
	}

	// TaskID is the BARE host-neutral ref (forge-io-real-lanes D2 as-built): the
	// framework stamps it verbatim on the coordinator loop as agent.loop.task,
	// and the issue-ref rule substitutes that triple onto the minted run as
	// run.issue.ref — a prefix here would be stamped into the graph (rule
	// substitution cannot strip it). Nothing binds on a prefix (verified at the
	// D2 settlement); the front-door loop is discriminated by its
	// coordinator.decision.next-action=issue_intake decision, not the TaskID.
	task := &agentic.TaskMessage{
		TaskID:     ref,
		Role:       coordinatorRole,
		Model:      model,
		Prompt:     coordinatorPrompt(ref, in.Event.AuthoredText),
		Tools:      nil, // global discovery — see the doc comment (NOT []{})
		ToolChoice: &agentic.ToolChoice{Mode: "required"},
		Metadata: map[string]any{
			agentic.MetadataKeyDecideActionAllowlist: taxonomy.Names(),
		},
	}
	if err := task.Validate(); err != nil {
		return nil, fmt.Errorf("intake: coordinator task invalid: %w", err)
	}
	return task, nil
}

// wakeContentRuneBudget bounds the issue content embedded in the wake prompt.
// AuthoredText is webhook-supplied and unbounded; a pathological issue body must
// not blow the coordinator's context, so the wake truncates at this many runes
// and says so loudly rather than silently carrying a mid-document cut as if
// complete.
const wakeContentRuneBudget = 4096

// coordinatorPrompt is the front-door wake prompt. It presents the admitted
// issue by its host-neutral ref and instructs the coordinator to record exactly
// one next action with the decide tool, deferring the action choice to the
// persona's decision contract. The issue ref is always present in the text, which
// is what a mock keys its scripted turn on in the journey.
//
// authoredText is the issue's actor-attributed content (Intake.Event.AuthoredText,
// bound to the actor by the admission invariant). The wake is the ONLY place that
// content enters the arc — the routing coordinator preserves the ask in its
// decision reason, which is the sole content channel into the authoring loop —
// so a non-empty body is embedded here, bounded by wakeContentRuneBudget. An
// empty body reproduces the pre-content prompt byte-for-byte (the mock
// journeys' contract).
func coordinatorPrompt(issueRef, authoredText string) string {
	content := ""
	if txt := strings.TrimSpace(authoredText); txt != "" {
		if r := []rune(txt); len(r) > wakeContentRuneBudget {
			txt = string(r[:wakeContentRuneBudget]) + "\n[content truncated]"
		}
		content = fmt.Sprintf("\nThe issue's author wrote:\n\n---\n%s\n---\n", txt)
	}
	return fmt.Sprintf(`SEMDEV COORDINATOR — an admitted issue needs routing.

Issue %s has been admitted to semdev and does not yet have a run.
%s
Read the facts recorded for this run so far and choose the single next station of the issue→PR arc. Record it with the decide tool: one action from your closed taxonomy, plus a short reason. Do not do the work yourself — your job is to route.`, issueRef, content)
}
