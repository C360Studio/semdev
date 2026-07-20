## Context

The `conversation-channel-seam` carve landed the `Channel` port with two verbs
(`Post`, `ResolveThread`) and kept the inbound transport webhook-fed: the
issue-intake receiver flattens `issue_comment` → `github.event.comment` on the
GITHUB stream, and the conversation-channel component's durable consumer drives the
`/semdev approve` gate. The `Read` verb was deliberately deferred as latent code —
it now has a caller. semdev is PULL-FIRST / webhook-optional: the receiver is
already config-optional (`http_port 0` disables it, the runtime starts fine), and
the outbound lanes (Post, PR delivery, `ListComments`) work from anywhere. The one
remaining inbound assumption is the `/semdev approve` gate — reachable only when a
webhook can reach the process. This change closes that with a poller.

Three facts from the carve's review shape the scope:
- **B-1**: the receiver serves issues AND comments (one URL, flattens both), so
  "webhook receiver" and "comment transport" are one toggle at the deployment level.
- **B-2**: a read cursor must NOT be a domain-graph fact (a B1/B9 bespoke-state
  smell) — the design's own idempotency argument shows it isn't needed for
  correctness; keep it in-memory.
- **H-1**: a poll read has ONE identity (the comment author), no separate webhook
  sender — the neutral `Message.Author` is the authorization principal. The carve
  already made `Message.Author` the principal, so the approval core is unchanged.

## Goals / Non-Goals

**Goals:**
- Add `Read(thread, cursor) → ([]Message, cursor)` to the `Channel` port (GitHub v1
  over `ListComments`), and a poller that reads each awaiting-approval run's thread
  so the approval gate is operable with NO inbound webhook.
- One approval core fed by BOTH the webhook consumer and the poller (H-1 unified).
- The poll transport is fail-safe: idempotent (re-read safe), bounded, read-only
  (G2), no new writer (G5), no new fact (B-2).

**Non-Goals:**
- NL intent (Phase 2) — the exact `/semdev approve` command match is unchanged.
- The draft-PR review surface / what the human sees to approve (Phase 3).
- Non-GitHub `Read` impls; issue DISCOVERY by poll (issues enter via `semdev launch`).
- The `ask_human`-reply / `human.opt.signal` resume lane (the carve's RESERVED
  forward contract — the poller reads `/semdev approve`, not free-text replies).

## Decisions

### D1 — the `Read` verb + an opaque `Cursor`
```
type Cursor string // opaque, impl-defined; "" = read from the top
Read(ctx, thread ThreadRef, cursor Cursor) (msgs []Message, next Cursor, err error)
```
`Read` returns the messages on `thread` AFTER `cursor`, plus the cursor to pass next
time. `Cursor` is opaque to the poller — only the impl encodes/parses it. For GitHub
v1 it is the last-seen comment ID (comment IDs are monotonic), so `Read` filters
`ListComments` to comments with `id > cursor` and returns the max id as `next`. An
empty cursor reads the whole thread (the restart / first-poll case). `Read` fails
closed (returns err); the caller retries next interval and never blocks.

### D2 — the poller: enumerate awaiting-approval runs, read their threads
A goroutine in the conversation-channel component (started only in poll mode). Each
interval it:
1. Enumerates runs at `agent.run.phase == awaiting_approval` (a read-only graph
   prefix query over the chain-execution namespace — G2, never a lifecycle write) and
   reads each run's `run.issue.ref` (the thread coordinate). The enumeration MUST use
   `RequestClassified` (mirroring `ResolveRunIDsByRef`) so a classified error body is
   NOT decoded as a zero-valued response (empty `Entities` → "no runs awaiting" → the
   poller silently idles — the ADR-060 silent-success class at the seam the whole
   transport depends on). When `cfg.Repo` is bound, the enumeration filters to that
   repo's refs so the poller never reads a foreign thread it cannot access (review L1).
2. For each such thread, `channel.Read(thread, cursor[thread])` → fresh Messages; it
   stores the returned cursor and feeds each Message to the shared approval core.

**Error vs empty (review H2)**: the loop MUST distinguish an enumeration/`Read`
TRANSPORT ERROR (log + retry next tick) from an EMPTY SUCCESSFUL result (idle — there
is genuinely nothing to poll). Conflating them means a persistent graph fault presents
as "nothing awaiting" indefinitely — a stalled poller = an approval that never lands =
a run parked forever, silently. A tick never crashes the loop.

Once a run is approved it leaves `awaiting_approval` (the resume rule advances it), so
the poller stops polling that thread. A run that never gets approved keeps being
polled — the human can approve at any time.

### D3 — one approval core, two transports (H-1)
Refactor the approval adapter: extract `handleMessage(ctx, msg Message, thread
ThreadRef) error` — repo-bind check, `/semdev approve` command match on `msg.Body`,
build `admission.Event{Actor: msg.Author, Owner/Repo: SplitRef(thread), AuthoredText:
msg.Body}`, `admission.Authorize`, resolve the run by thread, stamp
`run.change.approved`. The WEBHOOK consumer feeds it via
`NormalizeInboundComment(payload) → (msg, thread, ok)` (the sender==author guard
contained in the impl); the POLLER feeds it via `Read`. Both authorize
`Message.Author` and run the identical command-match + authorize + resolve + stamp.

**Accepted transport divergence on edited comments (review M3)**: the two transports
are NOT byte-identical on an EDITED comment. `ListComments` returns each comment's
CURRENT body (no created/edited flag), so the poll path honors an approve EDITED INTO
a comment's current body; the webhook path fires only on `action == "created"` and
never sees an edit. This is not a security hole — GitHub attributes an edited comment
to its original author, and `Authorize` gates on that author — and it is arguably more
correct (the thread's current state is the truth). We ACCEPT and DOCUMENT it rather
than claim byte-identity: the shared core is byte-identical for a CREATED approve
comment; the poll path additionally honors an edited-in approve.

### D4 — poll-vs-webhook ownership: XOR at the component (B-1)
A `poll` config block (`enabled`, `interval`). When `poll.enabled` the component runs
the POLLER and SKIPS the webhook `comment_events` consumer — so a comment is never
double-processed. When disabled (default) the webhook consumer runs (today's
behavior). The park-post consumer (`user.response.>`) runs in BOTH modes (it is the
outbound park lane, orthogonal to inbound). A pull-first deployment sets issue-intake
`http_port 0` (no receiver) + conversation-channel `poll.enabled true`; a hosted
deployment sets `http_port > 0` + `poll.enabled false`. The XOR is structural (the
component wires one inbound comment lane), not merely documented.

**The "neither" dead-lane guard (review H1)**: the dangerous combination is
issue-intake `http_port 0` (no receiver) AND conversation-channel `poll.enabled
false` — the component boots healthy, wires the webhook `comment_events` consumer, and
nothing ever publishes to it, so every run parks at `awaiting_approval` forever,
silently. The component cannot see it alone (the two knobs live in different config
blocks). So (a) the component's `Start` logs the ACTIVE inbound mode LOUDLY ("poll
ENABLED (interval …)" vs "poll DISABLED — approval requires an inbound webhook
receiver"), and (b) `internal/boot` — which assembles the full bootstrap and sees BOTH
component blocks — adds a coherence check that fails closed (or loud-warns) when
`issue-intake.http_port == 0 && conversation-channel.poll.enabled == false`. This is
the whole point of the change (make approval reachable pull-first), caught at boot not
in a silently-parked run.

### D5 — the cursor is IN-MEMORY, rebuilt by idempotent re-read (B-2)
`map[ThreadRef]Cursor` on the poller, never a graph fact. On restart it is empty, so
the first poll of each active thread reads from the top and re-feeds every comment;
re-processing an already-applied `/semdev approve` is a no-op (the adapter's
`alreadyApproved` guard returns before any write). Correctness comes from the
approval's idempotency, not from cursor durability. Bounded: `ListComments` paginates
to `maxCommentPages`; a restart re-reads each active thread once. The map is owned by
the single poll goroutine (no mutex needed) and is pruned each tick to the current
awaiting-approval set, so it does not grow unbounded across a run's lifetime (review L2).

### D6 — cadence + fail-safety
`interval` default 15s; `Validate`/`applyConfigDefaults` REJECTS a non-positive
interval and clamps to a floor (≥ 5s) so a mis-set `0` cannot become a tight loop that
hammers `ListComments` into rate-limit exhaustion + a per-iteration graph storm
(review M4). One `Read` per awaiting-approval thread per tick (one paginated
`ListComments`). A `Read` error is logged + retried next tick (never blocks, never
crashes the loop). The loop is bound to a context the component actually CANCELS on
`Stop`: `Start` captures a `context.CancelFunc` and `Stop` calls it (today's `Stop`
only flips `started` — it must gain the cancel, or the goroutine leaks and keeps
polling + re-authorizing after shutdown, review M5). On restart the cursor is empty so
the first tick re-runs `Authorize` (a collaborator-permission API call) on every
comment of every active thread — bounded and acceptable, and D7/M2 keeps it from
recurring every tick. No paid token — `ListComments` is a forge REST read,
deterministic and zero-LLM.

### D7 — GitHub `Read` impl over `ListComments`
`Read` calls `ListComments(owner, repo, number)` (from `SplitRef(thread)`), maps each
`github.Comment` → `Message{ID: itoa(c.ID), Author: c.Author, Body: c.Body, At:
parse(c.CreatedAt)}` — the poll `Message.ID` is the REAL comment id (unlike the
webhook path's delivery-GUID fallback), which is exactly the dedup key. The cursor
comparison is NUMERIC, not lexical (review M1): parse the cursor to `int64` (empty =
0 = read all) and keep comments with `c.ID > cursorInt` — a string compare would break
at a digit-width boundary (`"9" > "10"` lexically) and ship green on small-id unit
tests. When the filtered set is EMPTY, `Read` returns the INPUT cursor unchanged, NOT
max-of-empty (review M2): max-of-empty resets the cursor to the top and a still-gated
thread (which never leaves the enumeration) would re-read + re-`Authorize` its whole
history every tick — a forge + permission-API storm. Otherwise the next cursor is the
max id in the returned set. No attribution guard: a polled comment has one author, so
every `Message` is attributable to `Message.Author`.

### D8 — no new vocabulary, no new writer
`run.change.approved` stays written by `approval-adapter` (G5). No predicate is
registered (the cursor is in-memory, B-2; the phase read is a framework predicate the
poller READS, `agent.run.phase`). The poller fires no lifecycle transition (G2) — it
observes the phase and feeds an approve Message; the resume rule owns the transition.

## Risks / Trade-offs

- [The poll transport is net-new behavior with its own failure modes] → it is
  fail-safe by construction: idempotent (re-read replays a no-op), bounded (interval
  + pagination), read-only (G2 phase enumeration + `ListComments`), no new
  writer/fact. The `TestBridgeProofApprovalByPollNoWebhook` journey proves the whole
  path with NO webhook; the existing webhook/park journeys prove webhook mode is
  unchanged.
- [Double-processing if both webhook + poll ran] → the component wires the inbound
  comment lane as an XOR (D4), so both cannot run; even if they did, the approval is
  idempotent.
- [Polling latency vs the webhook] → the webhook stays as an optional latency
  accelerator; poll `interval` trades latency for portability (the pull-first default).
- [Enumerating awaiting-approval runs each tick is a graph query] → a bounded prefix
  query (the existing lesson-reader pattern), once per interval, read-only.

## Migration Plan

1. Add `Read` + `Cursor` to the `Channel` port and the GitHub impl (over
   `ListComments`), with unit pins (filter-by-cursor, comment→Message map, empty
   cursor reads all).
2. Refactor the approval adapter to the shared `handleMessage` core; the webhook
   consumer keeps its normalize-then-handle path (regression: the webhook journey
   stays green byte-for-byte).
3. Add the poller (interval loop, in-memory cursor, awaiting-approval enumeration) +
   the `poll` config block + the XOR wiring; a resolver method
   `ListRunsAwaitingApproval` (read-only).
4. The `TestBridgeProofApprovalByPollNoWebhook` journey (http_port 0 + poll enabled),
   red-first.
5. Spec: MODIFY `conversation-channel` (add the `Read` verb + poll transport + the
   poll-vs-webhook ownership requirement). Docs (the pull-first deployment shape).
Rollback: additive — reverting drops the poller + `Read`; webhook mode is untouched.

## Open Questions (RESOLVED)

- **OQ1 — which runs does the poller poll?** RESOLVED: runs at `agent.run.phase ==
  awaiting_approval` (the change-approval gate). Not parked/`awaiting_human` runs —
  the `ask_human`-reply lane is the carve's RESERVED forward contract, out of scope.
- **OQ2 — cursor as a fact?** RESOLVED NO (B-2): in-memory, rebuilt by idempotent
  re-read. No `conversation.thread.cursor` predicate.
- **OQ3 — can webhook + poll coexist?** RESOLVED: XOR at the component (D4) — poll
  mode skips the webhook comment consumer. The webhook is the optional accelerator.
