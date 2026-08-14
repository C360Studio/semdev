# Migrate the park-post request contract

## Why

semdev's nine park rules publish the rule engine's flat JSON envelope into
`user.response.>`, while SemStreams owns that family for typed
`agentic.user_response.v1` messages. The conversation-channel decoder accepts
only the flat shape, so two incompatible contracts share one subject family and
the broad consumer can ACK-drop framework messages. SemStreams #952 / ADR-093
reserves the framework family. semdev must move its side-effecting park-post
request to a product-owned exact JetStream subject in the same breaking cut.

## What Changes

- All nine park rules publish exactly `semdev.park-post.request`.
- The rule processor declares an exact required JetStream output and the
  conversation-channel component declares the matching required durable input,
  both carrying raw interface `semdev.park_post_request`/`v1`.
- The flat request decoder validates canonical `entity_id`, exact `subject`,
  RFC3339 `timestamp`, and `source=rule_engine`; `properties` and `related_id`
  remain optional. It is not a registered SemStreams `BaseMessage` payload.
- Both shipped USER streams explicitly capture the new subject. No bridge,
  alias, dual subscription, union decoder, or persisted-state conversion ships.
- A real forge-double journey proves the request remains ACK-pending until
  `Channel.Post` succeeds.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `conversation-channel`: park notices travel over the exact product-owned
  JetStream request contract and ACK only after successful posting.

## Impact

- **Rules:** nine park publishers under `configs/rules/`.
- **Runtime/config:** both shipped configs' USER stream, rule output, and
  conversation-channel input declarations; config versions bump in lockstep.
- **Code/tests:** `internal/conversationchannel`, offline conformance pins, and
  the station-failure park E2E journey.
- **Adoption:** fresh NATS state and the breaking SemStreams tag are required;
  `go.mod` stays on beta.160 until that tag exists.
