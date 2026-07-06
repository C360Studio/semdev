---
name: kv-or-stream
description: Decide between KV Watch and JetStream Stream for a new communication path. Use when designing inter-component communication, adding a message/fact flow, or choosing storage primitives. Following this heuristic is how semdev stays clear of B1 (bespoke state).
argument-hint: [description of the communication being designed]
---

# KV Watch vs JetStream Stream (semdev)

## What are you designing?

$ARGUMENTS

This is a framework decision heuristic (semstreams). Getting it right is also how semdev avoids
**B1**: the danger isn't using KV — it's inventing a bespoke state layer *on top of* KV (manager
+ cache + write-through, `PLAN_STATES`-class domain buckets). Use the framework primitives the
house way and B1 doesn't happen. Framework docs:
`~/Code/c360/semstreams/docs/concepts/03-streams-vs-kv-watches.md` and `02-kv-twofer.md`.

## The 4-test heuristic

Apply in order; the first clear answer is usually sufficient.

### Test 1 — Restart test (sharpest)
If this processor restarted, should it re-process messages it already handled?
- **Yes** (re-process is correct recovery) → **KV Watch**
- **No** (re-process would be wrong) → **JetStream Stream**

### Test 2 — Fan-out vs queue
Should multiple processors all react, or should exactly one handle it?
- **All react** (fan-out) → **KV Watch**
- **Only one** (queue) → **JetStream Stream**

### Test 3 — Processing time
- **Fast and idempotent** → **KV Watch**
- **Slow or real side effects** → **JetStream Stream**

### Test 4 — Nature test
Is this a fact about the world, or a request to do something?
- **Fact** (entity state, index entry, current status) → **KV Watch**
- **Request** (execute task, call LLM, run a command) → **JetStream Stream**

### Conflict check
If the tests disagree, the concept is probably two things conflated — split it.

## Common cases

| Communication | Primitive | Reason |
|---|---|---|
| Entity state changed | KV Watch | Fact; fan-out; fast; idempotent |
| New task to execute | JetStream Stream | Request; queue; expensive; side effects |
| Index update | KV Write (others watch) | Fact; fan-out; fast |
| LLM call | JetStream Stream | Request; queue; slow; costly |
| Loop / run current state | KV | Fact; queryable; recoverable |
| Tool execution request | JetStream Stream | Request; side effects |
| Tool result returned | JetStream Stream | Response; once; push delivery |
| Measurement fact (harness-stamped) | KV Write | Fact; latest-value; rule-observable (G3) |

## The KV twofer (and the B1 line)

Every NATS KV bucket is backed by a JetStream stream, so one KV write gives you three interfaces:
**State** (`kv.Get`), **Events** (`kv.Watch` fan-out), **History** (replay from any revision).

> **This framework twofer is fine and encouraged.** What semdev bans (**B1**) is the *bespoke*
> twofer semspec built on top of it: a manager wrapping a cache + a KV bucket + a triple
> write-through, or app-owned domain state buckets the rule engine wasn't watching. Use the raw KV
> primitive and let rules watch it; do not build a state-manager layer around it. If you're
> writing a "Manager" that keeps a cache in sync with a bucket, stop — that's the banned pattern.

**Bootstrap phase**: a starting KV watcher delivers ALL current values matching the pattern, then
a `nil` entry signals live updates. Distinguish bootstrap from live or you treat existing entities
as "new" on restart. **JetStream durable consumers** with `DeliverPolicy: new` resume from last
ack — no replay. Using KV for state AND JetStream for work in the same component is the standard
pattern.

Sibling skills: `/orchestration-check` (rule vs component) · `/semstreams-dev` (declaring the
port). When a change lands, run the `semstreams-reviewer` agent.
