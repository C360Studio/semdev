# Harness-Measurement Specification

## Purpose

Guarantees that measurement outcomes come only from the harness that executed
the command — never from a model claim: measurement facts have a single
harness writer, tool schemas accept no outcome parameters, and `measure_task`
is the developer's in-loop feedback channel. Per-task adversarial semantic
review is gated on these harness-stamped results, with rejection routing work
back into development within the attempt budget.
## Requirements
### Requirement: Measurement facts are stamped by the executing harness

The harness that executed a task's `test_command` SHALL stamp the measurement
fact (`measurement.result`: at minimum the exit code and derived pass/fail;
test-count evidence is a per-profile addition that arrives with the harness's
output parse, not an M0 guarantee). No measurement fact SHALL originate from a
model claim.

#### Scenario: A failing command records failure regardless of model text
- **WHEN** the executed `test_command` exits non-zero
- **THEN** `measurement.result` records a failing outcome
- **AND** any model text claiming success does not alter the recorded fact

### Requirement: Measurement tool schemas accept no outcome parameters

No registered measurement tool's schema SHALL accept an outcome-shaped field
(`pass`, `passed`, `exit_code`, `success`, `resolved`, `outcome`, …) from the
caller. The schema conformance census SHALL fail the build if one does.

#### Scenario: A schema with an outcome field fails the census
- **WHEN** the schema conformance census runs over all registered tools
- **THEN** any tool whose schema accepts a caller-supplied outcome field fails the build

### Requirement: Adversarial semantic review runs per task, gated on harness facts

The reviewer SHALL review each unit of work — each projected task — on its own,
reading that task's `measurement.result` (not the model's assertion about the
command) and the attempt's cumulative committed diff via read tools, and
recording a per-task verdict (`review.verdict.<task_index>`) with its findings
(`review.findings.<task_index>`). The review SHALL be adversarial: the
reviewer attempts to refute the attempt, and its findings are additive
constraints only — they SHALL NOT weaken an immutable `task.spec`. A task's
verdict SHALL be approving ONLY when that task's `measurement.result` records
a pass AND the reviewer raised no finding against it; the per-task measurement
is the floor under the verdict. A `changes_requested` verdict SHALL route the
task back into a fresh bounded developer attempt within the attempt budget —
never onward to verification; only approved work advances. Delivery
(`open_pr`) SHALL require every projected task to carry an approving
`review.verdict.<i>`.

#### Scenario: A false success claim cannot be approved
- **WHEN** the model claims a task's tests pass but that task's `measurement.result` records failure
- **THEN** the reviewer cannot record an approving `review.verdict` for that task

#### Scenario: Each task is reviewed on its own evidence
- **WHEN** the reviewer reviews task N
- **THEN** `review.verdict.N` is derived from task N's measurement and the findings raised against task N
- **AND** a passing task's verdict is unaffected by a different task's failure

#### Scenario: Findings never relax the spec
- **WHEN** a reviewer raises a finding
- **THEN** the finding adds a constraint and does not remove or weaken any `task.spec` requirement

#### Scenario: Rejection re-enters development within budget
- **WHEN** the reviewer records `changes_requested` and the attempt budget is not exhausted
- **THEN** a fresh bounded developer attempt is dispatched carrying the findings
- **AND** clean-room verification does not run for the rejected attempt

#### Scenario: Rejection beyond budget parks toward the human
- **WHEN** the reviewer records `changes_requested` and the attempt budget is exhausted
- **THEN** the run parks toward the human with the findings

#### Scenario: The reviewer inspects the diff before the verdict
- **WHEN** a review loop runs
- **THEN** the reviewer can read the attempt's cumulative committed diff and source before `submit_review`

### Requirement: Single writer for measurement facts

`measurement.result` SHALL have exactly one writer — the harness that executed
the command — recorded in the checked-in writers table AND realized as that
harness's `pkg/projection` contract owner. The vocabulary table remains the single
source of truth for who owns the predicate; the projection contract is the
runtime realization of that same claim, and the two SHALL NOT diverge.

#### Scenario: Writers census maps measurement.result to one writer
- **WHEN** the writers-census conformance test runs
- **THEN** `measurement.result` maps to exactly one writing harness

#### Scenario: The measurement writer is that harness's projection owner
- **WHEN** the contract-census conformance test runs
- **THEN** `measurement.result`'s vocab writer and its projection-contract owner are the same identity

### Requirement: Measurement is the developer's in-loop feedback channel

`measure_task` SHALL be callable by the developer inside its own loop. The
harness executes the task's immutable `test_command` in the run's sandbox,
stamps `measurement.result` (latest-wins per task), and returns the command's
real output and harness-derived pass/fail as the tool result — the
developer's feedback for the next iteration. The call SHALL NOT terminate
the loop. The schema SHALL continue to accept no outcome or command
parameters (G3): the model chooses *when* to measure, never *what the
outcome is*.

#### Scenario: In-loop measurement feeds the next patch
- **WHEN** the developer invokes `measure_task` after applying a patch
- **THEN** the real command output and harness-derived pass/fail return into the same loop
- **AND** the loop continues for a follow-up patch

#### Scenario: Skipping measurement cannot advance the task
- **WHEN** a developer loop terminates without any harness-stamped measurement
- **THEN** the attempt routes as failed
- **AND** no route treats the model's own claim as a measurement

### Requirement: Go fact-writes go through a contract-validated projection client

Every Go component that writes graph facts SHALL do so through a
`pkg/projection` mutation client validating against a declared local
`projection.Contract`, never through an ad-hoc graph mutation. The write
still travels over NATS to graph-ingest; what the contract adds is local
intent validation — entity-pattern match, declared predicate groups, and a
write verb per group — so a misdirected write fails loudly at the writer
instead of landing silently.

Each contract's predicate set SHALL be derived from the checked-in
single-writer vocabulary table, so the projection contracts and the G5 writer
census cannot drift: a predicate owned by a Source in the table appears in
that Source's contract, and no other.

The write verb SHALL match the fact's semantics: a replace-by-predicate fact
is a reconcile group, an append-only ledger is an append group, and a
primary-subject creation fact is a birth predicate issued as a strict create.
A verb mismatch is silent at compile time, so a conformance census SHALL
assert every contract's verb against the fact's declared pattern.

The client's classified outcomes SHALL be honored, never reinterpreted:
`entity_not_found`, `revision_mismatch`, and `commit_unknown` are distinct
results, and ambiguity is never treated as success. A revision conflict on a
single-writer group MAY be retried a bounded number of times within the
writing seam; exhaustion SHALL surface the classified error to the caller
unchanged.

#### Scenario: A misclassed predicate fails at the writer, not silently

- **WHEN** a Go write presents a predicate on an entity class outside its
  contract's declared pattern
- **THEN** the write is rejected with a named error before any wire request,
  never silently applied

#### Scenario: Contracts match the single-writer census

- **WHEN** the contract-census conformance test runs
- **THEN** every projection contract's predicates match the vocab writer
  table exactly

#### Scenario: A creation fact is a strict create and a duplicate is idempotent

- **WHEN** a birth write finds its content-derived entity already exists
- **THEN** the writer treats the conflict as the already-admitted duplicate
  path — no error escalation, and no repeated downstream wake

#### Scenario: Revision-conflict retry is bounded and loud on exhaustion

- **WHEN** a reconcile write loses the revision fence more times than the
  bounded retry allows
- **THEN** the classified revision-conflict error reaches the caller
  unchanged, where existing retry/park routing owns it

### Requirement: Write coverage is proven positively at boot

Write coverage SHALL be proven positively and offline: the projection client
carries every declared contract before any component or tool that writes is
registered, and boot fails naming the gap otherwise. No Go call site SHALL
name a `graph.mutation.*` subject outside the owning seam — with zero
sanctioned exceptions — and an offline census SHALL enforce both properties.

#### Scenario: A missing contract fails at boot, not at first write

- **WHEN** the runtime boots and a declared writer has no contract in the
  constructed client
- **THEN** boot fails naming the writer, before anything that writes is
  registered

#### Scenario: A hand-rolled mutation subject fails the census

- **WHEN** a Go call site outside the owning seam names a `graph.mutation.*`
  subject
- **THEN** the offline census fails naming the file and subject

### Requirement: Every registered tool declares a worst-effect classification

Every tool semdev registers into the executor registry SHALL declare a
worst-effect classification (`read_only`, `mutating`, or `external_effect`)
per the framework's effect contract: the value is a worst-effect claim, an
outbound read is `read_only`, and `external_effect` dominates `mutating`. A
source-level census SHALL fail on any semdev-registered tool whose effect is
absent or unrecognized, so a new tool cannot ship unclassified.

Effect metadata is descriptive: it SHALL NOT alter any configured gate
(`approval_required`, `allowed_tools`, per-loop advertised-tool admission)
in either direction.

#### Scenario: An unclassified tool fails the census

- **WHEN** a tool is registered without a valid effect classification
- **THEN** the offline census fails naming the tool

#### Scenario: Discovery serves the declared effect

- **WHEN** the tool catalog is served over the discovery port
- **THEN** each semdev tool carries its declared effect value

