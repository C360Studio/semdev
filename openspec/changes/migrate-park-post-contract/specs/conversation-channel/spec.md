# conversation-channel — delta for migrate-park-post-contract

## MODIFIED Requirements

### Requirement: Human communication rides the conversation channel

The system SHALL post messages to humans — a parked run's `run.awaiting.human`
notice and any `ask_human` question — to the run's thread through the port, and
the arc SHALL NOT contain a bespoke chat surface. A park rule SHALL request that
external effect through the exact `semdev.park-post.request` subject on a
JetStream output carrying raw interface `semdev.park_post_request`/`v1`; the
conversation-channel component SHALL declare the matching exact durable input.

The raw v1 request SHALL require a canonical non-empty `entity_id`, an embedded
`subject` exactly equal to `semdev.park-post.request`, an RFC3339 `timestamp`,
and `source` exactly equal to `rule_engine`; `properties` and `related_id` MAY be
present. This raw request SHALL NOT be registered or decoded as a SemStreams
BaseMessage payload. `user.response.>` is reserved for the framework-owned typed
`agentic.user_response.v1` family and SHALL NOT be a park-post alias.

The park-post consumer SHALL acknowledge a valid request only after the
configured `ConversationChannel.Post` succeeds or after a declared permanent
graph-only outcome. A transient thread-resolve, graph-read, or post failure
SHALL leave the request eligible for bounded redelivery. The nine park rules
and sole consumer SHALL cut over together with no bridge, wildcard legacy
subscription, union decoder, dual publish, or retained-state conversion.

All existing change-approval command, natural-language classification,
authorization, transparency, and first-writer-wins requirements remain
unchanged.

#### Scenario: A parked run surfaces its message through the exact durable request

- **WHEN** a park rule records `run.awaiting.human`
- **THEN** it publishes one raw `semdev.park_post_request`/`v1` request to exact subject `semdev.park-post.request`
- **AND** the conversation-channel durable consumer posts the park message to the run's thread through the channel port, naming what the human must decide

#### Scenario: Posting must succeed before acknowledgement

- **WHEN** the conversation channel has received a valid park-post request but `Channel.Post` is still blocked or returns a transient error
- **THEN** that JetStream delivery remains unacknowledged and eligible for bounded redelivery
- **AND** it is acknowledged only after the real post succeeds

#### Scenario: A framework typed user response is not a park request

- **WHEN** a typed `agentic.user_response.v1` message is published under `user.response.>`
- **THEN** the semdev park-post consumer does not receive or decode it
- **AND** no compatibility alias or union decoder converts it into a park post
