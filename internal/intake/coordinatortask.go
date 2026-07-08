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

	task := &agentic.TaskMessage{
		TaskID:     "intake:" + ref,
		Role:       coordinatorRole,
		Model:      model,
		Prompt:     coordinatorPrompt(ref),
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

// coordinatorPrompt is the front-door wake prompt. It presents the admitted
// issue by its host-neutral ref and instructs the coordinator to record exactly
// one next action with the decide tool, deferring the action choice to the
// persona's decision contract. The issue ref is always present in the text, which
// is what a mock keys its scripted turn on in the journey.
func coordinatorPrompt(issueRef string) string {
	return fmt.Sprintf(`SEMDEV COORDINATOR — an admitted issue needs routing.

Issue %s has been admitted to semdev and does not yet have a run.

Read the facts recorded for this run so far and choose the single next station of the issue→PR arc. Record it with the decide tool: one action from your closed taxonomy, plus a short reason. Do not do the work yourself — your job is to route.`, issueRef)
}
