# Design — migrate-park-post-contract

## Context and surface inventory

- Nine rule files publish `user.response.$entity.instance` with the framework
  rule engine's flat `executePublish` envelope.
- The USER stream captures `user.>` in both shipped configs, so the publish is
  durable only incidentally; the rule component declares no matching JetStream
  output port.
- `conversation-channel` consumes broad `user.response.>` and decodes only
  `entity_id` through its local `publishEnvelope`. It is a delivery adapter for
  park notices, not a typed `agentic.UserResponse` reader.
- Posting is already downstream of the durable `run.awaiting.human` fact and
  returns an error on resolve/post transport failure, allowing bounded
  redelivery. Nil channel and permanently unusable issue refs remain explicit
  graph-only terminal outcomes.
- SemStreams #952 / ADR-093 reserves `user.response.>` for the typed framework
  contract and prevents generic rules from publishing into that family.

## Goals / non-goals

Goals: separate contract ownership, make durability explicit at both ports,
fail closed on malformed wire identity, preserve rule-owned lifecycle behavior,
and prove ACK ordering across the real adapter.

Non-goals: a compatibility bridge; reading typed `agentic.UserResponse` in
semdev; registering a new polymorphic payload; changing park fact ownership;
adding buffering or an operator knob; converting retained USER messages.

## Decisions

### D1 — one exact product-owned subject and one raw v1 contract

The subject is `semdev.park-post.request`. The port contract is
`semdev.park_post_request`/`v1`, represented by the rule engine's existing raw
publish envelope:

- required: canonical non-empty `entity_id`, exact `subject`, RFC3339
  `timestamp`, `source=rule_engine`;
- optional: `properties`, `related_id`.

It does not implement `message.Payload`, has no type discriminator, and is not
registered in the SemStreams payload registry. Strict decoding rejects unknown
fields and trailing JSON so a typed BaseMessage cannot become an accidental
union member.

### D2 — JetStream is the correct primitive

The shared `kv-or-stream` four-test result is unanimous: a restart must not
re-post an ACKed notification; one delivery worker should post it; posting is a
slow external side effect; and the message is a request to act, not current
world state. Therefore the existing USER JetStream carries the exact subject.
The rule output port makes `PublishToStream` intentional rather than relying on
a broad stream capture of a core-NATS publish.

### D3 — ACK only after the external effect succeeds

The handler returns nil only after `Channel.Post` succeeds or after a declared
permanent graph-only outcome. A transient resolve/read/post fault returns an
error for bounded redelivery. The E2E forge double blocks the real comment POST,
then reads the named durable consumer's `NumAckPending`: it must be nonzero while
blocked and zero only after release and a recorded comment. No arbitrary sleep
is used; request arrival and consumer state are explicit synchronization.

### D4 — fresh-state breaking cut, no migration surface

All nine publishers and the sole delivery consumer change together. There is no
old-subject subscription, dual publish, alias, union decoder, or retained-message
converter. The two shipped stream catalogs add the exact subject. Adoption uses
fresh NATS state, consistent with beta.160 migration discipline.

### D5 — SemStreams tag gates dependency adoption

The semdev implementation and proofs can be prepared on beta.160 because the
existing typed port surface already selects JetStream by exact output subject.
`go.mod` does not move until the breaking SemStreams release containing ADR-093
exists. At that point semdev bumps once, runs clean-room/OpenSpec/conformance and
the relevant real E2E journey, and lands lockstep with the upstream reservation.

## Adopter seam inventory

The specific adopter is a semdev rule author adding a new park path. They must
know only that a park writes the durable `run.awaiting.human` fact first and then
uses the one declared exact request subject; the rule engine owns every wire
identity value. If they do nothing during the upstream upgrade, the reserved-
family validation rejects the legacy rule at load rather than silently mixing
payloads. The contract is discoverable in both shipped ports, this change, and
`docs/port-manifest.md`. They should not predict stream placement, construct a
typed payload, tune a buffer, or manage ACKs; the framework observes the exact
output port and the component owns delivery outcomes.

## Primitive-first / constitutional alignment

No new tool, component, subscriber, predicate, or fact writer is introduced.
Rules continue to own the park transition (G2); the existing conversation-
channel component performs the HTTP side effect rules cannot (G1); no outcome
is model supplied (G3); the real ACK/post evidence is harness observed (G7).

## Risks

- A missing stream subject or mismatched port silently makes the request
  unroutable. Mitigation: two-config conformance census pins the stream plus
  producer/consumer port subject and interface byte-for-byte.
- A future park rule could retain the legacy family. Mitigation: the census
  evaluates `on_enter`, `on_exit`, `while_true`, and `on_recovery` independently.
  Every phase that authors `run.awaiting.human` through `add_triple`,
  `update_triple`, or a non-empty `reconcile_predicates` must publish exactly
  once on the owned subject in that same phase. SemStreams' reserved-subject
  validation separately rejects any future generic rule action targeting
  `user.response.>`.
- A handler refactor could ACK at receipt. Mitigation: unit transport-fault pins
  plus the real consumer `NumAckPending` barrier proof.
