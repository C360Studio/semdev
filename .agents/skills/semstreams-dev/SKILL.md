---
name: semstreams-dev
description: The house-way front door for building in semstreams — but in semdev, primitive-first (G1). Use when you think you need a new component/tool/port/payload, and want to first prove a rule + persona + fact can't do it, then build it correctly if it's justified.
argument-hint: [what you think you're building, e.g. "a verify tool" or "an issue-intake input"]
---

# Building in semstreams — the semdev way

## What do you think you're building?

$ARGUMENTS

## STOP — clear the G1 gate first

In semdev, **the default answer is not a new component.** Before this skill's mechanics apply,
prove the primitive-first case (constitution **G1**):

1. **Can a rule + persona + fact + an existing component express this?** Most coordination,
   lifecycle, and decision logic is rules (see `/orchestration-check`). Most "communicate state"
   is a KV write (see `/kv-or-stream`). If yes → do that, and you're done here.
2. **If a new Go component/tool is genuinely required**, it needs, in its change:
   - a **framework-alignment note** — what primitive you considered and why it can't do this;
   - a **registry entry** — the conformance pin inventories `cmd/semdev/tools/*` and processor
     registrations against a checked-in registry; an unregistered addition fails the build.
   - No note + no entry = the change does not land. A component-per-concern reflex is **B6**.
3. **Measurement tools take no outcome parameters (G3).** If the thing you're building records a
   pass/fail/exit-code/resolved fact, that fact is stamped by the harness that ran the command —
   never accepted from the model. Design the schema accordingly.

Framework source of truth: `~/Code/c360/semstreams` (read it to confirm the API, don't assume).

## §1 Component anatomy (once G1 is cleared)

A component is a self-describing unit discovered at runtime. Five registered types
(`RegistrationConfig.Type`): `input` (source), `processor` (transform), `output` (sink),
`storage` (persist), `gateway` (query surface). Typical layout:

```
processor/your-thing/
  config.go            # Config struct with schema tags (validation + discovery + schema gen)
  your_thing.go        # implements Discoverable (+ LifecycleComponent if it runs)
  register.go          # exports Register(*component.Registry) error
  payload_registry.go  # ONLY if it emits new payload/fact types — see /new-payload
  *_test.go
```

**`Discoverable` (required):** `Meta()`, `InputPorts()`, `OutputPorts()`, `ConfigSchema()`,
`Health()`, `DataFlow()`. **`LifecycleComponent` (optional, type-asserted):** `Start(ctx)` /
`Stop(timeout)` — implement only if the component *runs* (watches/listens/ticks). Thread the
`Start` ctx into every goroutine — never `context.Background()`.

**Explicit registration (the #1 footgun).** semstreams uses explicit `Register`, NOT `init()`
self-registration. Your package exports a `Register(r *component.Registry) error` calling
`r.RegisterWithConfig(...)` with `Name`, `Factory`, `Schema`, `Type`, `Protocol`, `Domain`,
`Version`, `Dependencies`.

> **Wire it into EVERY semdev binary, not just one.** The critical pair is the production binary
> and the e2e binary. A component in one but not the other is the half-migrated-binary silent-flow
> break. After adding registration: `grep -rn "your-thing" cmd/` and confirm it's in each binary.
> (semdev's concrete binary names land with the M0 spine — confirm against the real `cmd/` tree.)

**Config via schema tags.** Fields carry `schema:` tags (`type:` · `description:` · `default:` ·
`category:basic|advanced` · bare `required`) that drive validation, the operator surface, AND the
generated JSON schema. Every operator-reachable field needs a JSON-round-trip test (no shadow
structs). Any config change → regenerate the schema and commit the diff, or CI fails.

## §2 Port picker

Ports are typed I/O dependencies. Pick by what the data IS:

| Need | Port | Note |
|---|---|---|
| Observe entity/index **state** (a fact; re-delivers on restart) | **KVWatchPort** | Default for "react to state." Confirm via `/kv-or-stream`. |
| Durable **request/work** (at-least-once; resumes from last ack) | **JetStreamPort** | Tasks / LLM calls / tool execution. |
| Fire-and-forget **pub/sub** | **NATSPort** | Rare — most "events" are a KV write. |
| Raw **TCP/UDP** socket | **NetworkPort** | Ingress inputs. |
| **Outbound HTTP** client / polling | **HTTPClientPort** | Descriptor-not-runtime; secrets-as-refs. |
| **Filesystem** read/write | **FilePort** | |
| Stream-read **bulky content** from ObjectStore | **StoreReadPort** | Pairs with `ContentStorable` + ref-triple. |
| **Periodic** tick | **TimerPort** | |

Rules of thumb: **facts → KV, requests → JetStream**; **bulky payloads never ride rules or
messages** (store + pass a ref); set `ResourceID()` so two components contending for the same
socket/bucket/stream are caught at wiring time.

## Finish checklist (before you call it done)

1. **G1 satisfied?** Alignment note + registry entry present in the change.
2. **Registered in every binary?** `grep -rn "your-thing" cmd/` — production AND e2e binary.
3. **Schema regenerated & committed?** No drift in the generated schema/spec diff.
4. **New payload/fact types registered?** `init()` + `MarshalJSON` wrapping `BaseMessage` (type
   alias) + blank import + production-decoder round-trip test. See `/new-payload`.
5. **NATS request callers checked?** Classified handler → use `RequestClassified`, not raw
   `Request` + `Unmarshal` (ADR-060 silent-success trap).
6. **G3 clean?** No outcome parameter in any measurement tool's schema.
7. **G5/G9 clean?** Every predicate you write has a single declared writer and maps to this
   change's slug in the vocabulary table.
8. **Gates green:** build · lint · `go test -race ./...` · integration · contract. Breaking
   change → a relevant e2e tier green before it lands.
9. **Run the `semstreams-reviewer` agent.**

Sibling skills: `/orchestration-check` · `/kv-or-stream` · `/new-payload`.
