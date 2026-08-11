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

### Requirement: Go fact-writes go through a contract-bound projection owner

Every Go component that writes owned facts SHALL do so through a `pkg/projection`
mutation client bound to a declared `projection.Contract`, never through an ad-hoc
graph mutation. The write still travels over NATS to graph-ingest; what the
contract adds is a declared owner identity, a per-predicate write mode, and an
owner-token fence — so an owned write cannot originate from an unbound writer.

Each contract's predicate set SHALL be derived from the checked-in single-writer
vocabulary table, so the projection owners and the G5 writer census cannot drift:
a predicate owned by a Source in the table is owned by that Source's projection
contract, and no other.

The write mode SHALL match the fact's semantics: a replace-by-predicate fact is a
`replace-owned` group, an append-only ledger is an `append-evidence` group, and a
primary-subject creation fact is a birth predicate. A mode mismatch is silent at
compile time — an append ledger bound as replace-owned drops prior entries — so a
conformance census SHALL assert every contract's mode against the fact's declared
pattern.

#### Scenario: An owned write is refused from an unbound writer
- **WHEN** a Go path attempts an owned write without a bound projection contract
- **THEN** the write does not compile or is rejected, never silently applied

#### Scenario: Contracts match the single-writer census
- **WHEN** the contract-census conformance test runs
- **THEN** every projection contract's owner and predicates match the vocab writer table exactly

#### Scenario: An append ledger is bound append-evidence, not replace-owned
- **WHEN** a predicate is a declared append-set ledger
- **THEN** its contract group mode is `append-evidence` and the census fails on any other mode

### Requirement: Owned-write coverage is proven positively; the lease stays observe-only

graph-ingest SHALL run the owner lease observe-only for the life of the beta.159
pin: `enforce_owner_lease` explicitly false in every shipped config, never
absent — an absent key silently inherits whatever the framework default becomes.
The originally planned enforcement flip is VOID as-built (design D5, 2026-08-11):
semstreams' announced final refactor phase removes the ownership/lease mechanism,
so the posture question transfers to the next-tag migration change, re-asked
against ownership's replacement.

Because the lease meter cannot see an un-tokened write (it counts stale tokens,
not missing ones), owned-write coverage SHALL be proven positively and offline
instead: every declared owner binds at boot BEFORE any component or tool that
writes is registered, and no Go call site names a graph-mutation subject outside
the owning seam, with sanctioned exceptions named individually and capped.

#### Scenario: The observe-only posture is explicit and pinned
- **WHEN** a shipped config declares the graph-ingest component
- **THEN** `enforce_owner_lease` is present and false
- **AND** an absent key or a true value fails the offline conformance pin

#### Scenario: An unbound owner fails at boot, not at first write
- **WHEN** the runtime boots and a declared owner has no bound mutation client
- **THEN** boot fails naming the owner, before anything that writes is registered

#### Scenario: A hand-rolled mutation subject fails the census
- **WHEN** a Go call site outside the owning seam names a `graph.mutation.*` subject beyond the named sanctioned exceptions
- **THEN** the offline census fails, naming the file and line
