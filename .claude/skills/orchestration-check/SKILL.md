---
name: orchestration-check
description: Decide whether logic belongs in a rule, a component/tool, or an upstream engine ask — and confirm it doesn't re-grow semspec's app-side state machine. Use when adding orchestration or lifecycle logic, designing a multi-step flow, or reviewing a boundary. In semdev this is G1 + G2 operationalized.
argument-hint: [pattern or logic being evaluated]
---

# Orchestration Layer Check (semdev)

## What pattern are you evaluating?

$ARGUMENTS

semdev's whole thesis is that semspec drowned in a bespoke lifecycle state machine (5/6 edges in
Go, a 10k-LOC hybrid core). This skill is the gate that keeps that from happening again. It
operationalizes **G1** (primitive-first) and **G2** (rules own the lifecycle) and points at the
**B1/B3/B7** banned patterns. Framework reference: `~/Code/c360/semstreams/docs/concepts/14-orchestration-layers.md`.

## The two layers (there is no third)

semstreams has **rules** and **components**. There is no separate workflow engine. Multi-step
patterns are coordinated rule sets firing components, with per-action `MaxIterations` as the cap.

| Layer | Owns | Does NOT own |
|-------|------|--------------|
| **Rule engine** | Trigger conditions, action sequencing, iteration caps (`MaxIterations`), condition evaluation, **all lifecycle transitions** | Work execution, business logic, payload semantics |
| **Component / tool** | Execution mechanics, its own internal state | Caller identity, multi-step coordination, **lifecycle transitions** |

## Quick decision

| Pattern | Use |
|---------|-----|
| A completes → B starts (no retry, no loop) | Single rule, one action |
| A → B → C → D (no loop) | Rule chain (one rule per transition) |
| A → if X then B else C | One rule, action-level `when` clauses |
| A → B → A → B… (max N times) | Rule chain with per-action `MaxIterations` cap + explicit cap-exhaust action |
| Fan-out + fan-in | Fan-out rule + synchronizer-key rule |
| Execute an LLM call, run a command, git/forge I/O | Component / tool (and clear the **G1** gate first) |
| The rule engine genuinely cannot express the condition | **Upstream semstreams ask + documented interim that parks toward the human** — NOT app-side Go |

## The rules

1. **Rules trigger; they don't orchestrate inline.** A rule fires one set of actions, not a
   stateful sequence. Anti-pattern: rule A sets `step=1`, rule B watches `step=1` sets `step=2`…
   (marker choreography — **B3**). Fix: each rule fires a component; state lives on the entity,
   not in step counters.
2. **Components execute; they don't coordinate.** A component does one thing and emits a result;
   it does not call another component inline. The next component is fired by a rule reacting to
   the result.
3. **Components/tools are caller-agnostic.** Same behavior regardless of who triggered them.
   Anti-pattern: `if msg.loop_id != "" {…}` inside a tool. Behavior differences come from config,
   not caller introspection.
4. **State ownership is exclusive (G5).** One writer per piece of state. Domain entities →
   graph-ingest. Operational results → the producing component's own bucket. Trigger conditions
   and iteration counters → the rule engine. Two owners of the same state is **B4**.
5. **New KV bucket? Ask twice — in semdev, three times.** Can it live on an existing entity as
   triples? In an existing bucket under a distinct key prefix? As an ObjectStore ref? If all "no"
   and it's genuinely component-owned operational state, register it where the rule engine watches
   and document it. **Never** create app-side state buckets the rule engine isn't configured to
   watch — that is **B1** (the `PLAN_STATES`-class trap).
6. **Engine gaps file as engine work, never as app-side plumbing (G2).** If the rule engine can't
   express what you need, the answer is: file the upstream semstreams ask, record it in the
   change, and park toward the human in the interim. A silent Go reconciler or backstop ticker is
   **B3/B7** — the exact 7k-LOC `workflow/reactive/` mistake that condemned semspec.

## The semdev lifecycle question (always ask it)

Before writing ANY Go that changes a run's status/phase/terminal state, stop:

- **Is this a lifecycle transition?** If yes → it belongs in a rule matching facts, not in Go
  (**G2**). Product Go firing a transition is blocking.
- **Where does the deciding fact come from?** A harness stamps measurement facts (**G3**); a rule
  reads them and transitions. Go does not infer the outcome and set the status itself.
- **If a rule can't do it**, write the upstream ask and the human-park interim — don't reach for
  a Go workaround.

## State-storage boundaries

| Category | Storage | Rule-observable? | In graph? |
|----------|---------|------------------|-----------|
| Domain entities | `ENTITY_STATES` KV (graph-ingest only) | Yes | Yes (`Graphable`) |
| Operational results | Component-specific KV | Yes (via watch registration) | No |
| Events / work items | JetStream streams | No (rules watch KV) | No |
| Bulky payloads | ObjectStore ref; ref-triple on the entity | Indirectly | Refs only |

Rules carry **references, never content**. Freeform/bulky payloads go to ObjectStore and pass a
ref; do not write operational results into `ENTITY_STATES` (it pollutes the graph).

## Before you call it settled
- [ ] No step-counter choreography across rule firings (**B3**)
- [ ] No component dispatching another component inline
- [ ] No branching on caller identity
- [ ] No app-side state machine / reconciler around an engine limitation (**B1/B3/B7**) — engine
      ask filed instead, human-park documented
- [ ] Any new bucket cleared the ask-three-times gate and is watched + documented
- [ ] No Go firing a lifecycle transition (**G2**); one writer per predicate (**G5**)

Sibling skills: `/kv-or-stream` (which primitive) · `/semstreams-dev` (if a component IS
justified). When a change lands, run the `semstreams-reviewer` agent.
