//go:build e2e

// The mock-LLM BRIDGE PROOF. It boots the REAL shared runtime against the
// in-process mock LLM (zero paid tokens) and drives the whole issue→PR arc
// through it — front door → issue_intake → create_change → validate → human
// approval → project task.spec → provision + prove-cold sandbox → dispatch
// Amelia's BOUNDED MULTI-TURN dev loop (apply_patch → measure in-container →
// stop) → structural floors + route mirror → FLOORS ROUTE advance → review
// (Quinn) → REVIEW ROUTE approved → cold clean-room verify → DELIVERY ROUTE
// coherent → open_pr → delivery.pr.ref. Routing is RULE-NATIVE (the reshape deleted the
// check_gate/check_coherence route-token tools — routes compose from harness facts).
//
// This is a bridge proof, NOT a completeness claim (G7/G10): it proves the rail
// CONNECTS end-to-end under the mock — that every station's rules, tools, and
// facts wire together and the whole chain advances — not that the rail is
// correct, production-shaped, or that M0 is complete. A completion claim needs
// real-LLM evidence and a real forge delivery (a mock run is bridge proof and
// never counts as real-LLM evidence — see internal/ledger).
//
// The route-a-decision path is the spine every station layers onto: the
// front-door wake is built exactly as the intake adapter builds it (via
// intake.CoordinatorTask — nil tools = global discovery, tool_choice=required,
// the closed decide-action allowlist), the seeded coordinator persona is live,
// and the mock's scripted decide turn lands a coordinator.decision.next_action
// triple on the loop entity. If the tool config, the persona seeding, the front
// door subject, or the decide registration is wrong, the loop reaches the model
// but never routes and no decision fact appears.
//
// Run via `task e2e`, which resets NATS first so the runtime loads the
// bridge proof's patched config from file rather than a stale versioned-KV copy.

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/service"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/forge/forgetest"
	"github.com/c360studio/semdev/internal/forge/semsource"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/mockllm"

	"github.com/c360studio/semdev/internal/graphown"
)

// journeyIssueRef is the host-neutral issue ref this journey admits. It is
// distinctive so the mock can key its scripted decide turn on it as a substring
// of the coordinator prompt (intake.CoordinatorTask always embeds the ref).
const (
	journeyIssueRef    = "c360studio/semdev-journey#1"
	journeyIssueNumber = 1
)

// journeyDecideAction is the action the mock's scripted decide returns — the
// terminal a coordinator picks for a newly admitted issue with no run yet.
const journeyDecideAction = "issue_intake"

// journeyChangeSlug is the slug the mock's scripted create_change authors. The
// change facts land on the run entity as ONE scalar document (beta.147 D3:
// openspec.change.document) plus the flat openspec.change.slug/.revision pointers
// — not a openspec.change.<slug>.* triple tree.
const journeyChangeSlug = "journey-spine-change"

// journeyChangeIntent is the proposal intent the mock's scripted create_change
// authors (journeyChangeArgs). requireChangeAuthored decodes the run's authored
// openspec.change.document and asserts it round-trips, so the fixture and the
// assertion share this constant rather than risking independent literals drifting.
const journeyChangeIntent = "make the spine journey author a real change"

// journeyDevAction is the action the re-woken coordinator decides after the human
// approves the change — the entry to the dev-loop rail.
const journeyDevAction = "dev_from_task"

// (provision is a publish-triggered COMPONENT now, R6 group 6 — the provision rule
// (sandbox/01) publishes the provision-station rather than forcing a provision_sandbox
// model turn, so there is no provision prompt for the mock to guard on.)

// journeyDevRewakeMarker is a distinctive substring of the dev re-wake prompt
// (dev-from-task/02-rewake-coordinator-dev.json) that the mock's dev-decide turn
// guards on — the re-wake prompt threads $entity.id (the run entity), not the issue
// ref or slug, so the turn is keyed on this stable phrase instead.
const journeyDevRewakeMarker = "Begin developing"

// journeyDeveloperMarker is a distinctive substring of Amelia's dispatch prompt
// (dev-from-task/04-dispatch-developer.json) the mock's apply_patch turn guards on.
const journeyDeveloperMarker = "SEMDEV DEVELOPER"

// journeyReviewMarker is a distinctive substring of Quinn's review prompt
// (dev-from-task/06a-route-advance.json) the mock's submit_review turn guards on.
const journeyReviewMarker = "SEMDEV REVIEWER"

// (verify is a publish-triggered COMPONENT now, R6 group 6 — the review-approved route
// publishes the verify-station rather than forcing a verify_artifact model turn, so there
// is no verify prompt for the mock to guard on.)

// journeyFixtureFixDiff is the developer's authored fix for the go-health-class
// fixture's real boundary bug (`>` → `>=` at the warning threshold) — the exact
// unified diff apply_patch lands on the run's checkout. It mirrors the fixture
// health.go so `git apply` matches; the subsequent measure proves it was real.
const journeyFixtureFixDiff = "--- a/health.go\n" +
	"+++ b/health.go\n" +
	"@@ -28,7 +28,7 @@ func Classify(cpu, mem float64) Status {\n" +
	" \tswitch {\n" +
	" \tcase pressure >= criticalThreshold:\n" +
	" \t\treturn Unhealthy\n" +
	"-\tcase pressure > warningThreshold:\n" +
	"+\tcase pressure >= warningThreshold:\n" +
	" \t\treturn Degraded\n" +
	" \tdefault:\n" +
	" \t\treturn Healthy\n"

// journeyRetryMarker is the distinctive substring of the RETRY re-dispatch prompt
// (dev-from-task/06c-route-retry.json — "your previous attempt … did NOT pass the
// sandbox gate"). The exact phrase "sandbox gate" is ABSENT from the initial dispatch
// prompt (04, whose lowercase "did not pass" is a deliberate near-miss), Quinn's prompt,
// and the dev re-wake prompt — the wording, not merely the casing, is disjoint, so a
// future prompt edit is less likely to quietly re-couple them. This disjointness is
// LOAD-BEARING: the fail-then-pass mock keys attempt 2's fixtures on it so the positional
// tool cursor stays parked on those fixtures (a marker miss does not burn the cursor)
// until the retry loop's prompt actually arrives — attempt 1's loop then ends on a
// completion instead of consuming attempt 2's apply_patch. If it ever re-couples, the
// RequestCount==11 assertion fails loud (it can never silently false-green).
const journeyRetryMarker = "did NOT pass the sandbox gate"

// journeyFixtureWipDiff is Amelia's FIRST (failing) attempt in the retry journey: a WIP
// edit that COMPILES but leaves the boundary bug (keeps `>`, only appends a FIXME), so
// the in-container measure is RED exactly like the pristine fixture. It drives the floors
// route not_clean→retry. The checkout chains (apply_patch commits cumulatively), so the
// corrective attempt below diffs against THIS committed line, not pristine.
const journeyFixtureWipDiff = "--- a/health.go\n" +
	"+++ b/health.go\n" +
	"@@ -28,7 +28,7 @@ func Classify(cpu, mem float64) Status {\n" +
	" \tswitch {\n" +
	" \tcase pressure >= criticalThreshold:\n" +
	" \t\treturn Unhealthy\n" +
	"-\tcase pressure > warningThreshold:\n" +
	"+\tcase pressure > warningThreshold: // FIXME(retry): boundary still excludes the threshold\n" +
	" \t\treturn Degraded\n" +
	" \tdefault:\n" +
	" \t\treturn Healthy\n"

// journeyFixtureRetryFixDiff is Amelia's SECOND (corrective) attempt: it transforms the
// WIP line the first attempt committed into the real `>=` fix, so the second measure is
// GREEN and the floors route advances. Its `-` context is byte-identical to
// journeyFixtureWipDiff's `+` line so `git apply` matches the chained checkout.
const journeyFixtureRetryFixDiff = "--- a/health.go\n" +
	"+++ b/health.go\n" +
	"@@ -28,7 +28,7 @@ func Classify(cpu, mem float64) Status {\n" +
	" \tswitch {\n" +
	" \tcase pressure >= criticalThreshold:\n" +
	" \t\treturn Unhealthy\n" +
	"-\tcase pressure > warningThreshold: // FIXME(retry): boundary still excludes the threshold\n" +
	"+\tcase pressure >= warningThreshold:\n" +
	" \t\treturn Degraded\n" +
	" \tdefault:\n" +
	" \t\treturn Healthy\n"

// journeyReviewRetryMarker is the distinctive substring of the REVIEW-RETRY re-dispatch prompt
// (dev-from-task/07b-review-retry.json — "the reviewer (Quinn) requested changes"). "requested
// changes" reaches the model only via 07b's developer prompt (07c is a budget-exhausted PARK
// with no model turn) and is absent from the dispatch prompt (04) and Quinn's review prompt
// (06a) — so the rejection journey keys attempt 2's fixtures on it, and the positional cursor
// parks them until Quinn's rejection re-enters development.
const journeyReviewRetryMarker = "requested changes"

// journeyDispatchTaskMarker is a substring UNIQUE to the INITIAL dispatch prompt
// (dev-from-task/04-dispatch-developer.json — "develop this run's current task") — it is
// ABSENT from the transient-retry prompt (06f, "interrupted by a TRANSIENT infrastructure
// error") and every other spawn. The transient-grace journey sets it as the mockllm error
// marker so ONLY the initial dispatch's model call 500s (a transient model_error terminal);
// the 06f re-dispatch prompt lacks it, so the retried loop proceeds normally.
const journeyDispatchTaskMarker = "develop this run's current task"

// journeyTransientMarker is the distinctive substring of the TRANSIENT-retry re-dispatch prompt
// (dev-from-task/06f-route-transient-retry.json — "TRANSIENT infrastructure error"), absent from
// the initial dispatch (04) and every other spawn. The transient-grace journey keys the retry
// loop's fixtures on it so the positional cursor parks them until the transient re-dispatch arrives.
const journeyTransientMarker = "TRANSIENT infrastructure error"

// journeyReviewFinding is the required change Quinn raises on her FIRST review of the rejection
// journey. A single finding forces verdict=changes_requested even on a PASSING measurement (G3:
// approved ⟺ measured-pass ∧ no findings) — the rejection lever. It is realistic (G8): a review
// asking the boundary be documented so nobody "fixes" the inclusive `>=` back to `>`.
const journeyReviewFinding = "The inclusive warning boundary (`>=`) is correct but undocumented — " +
	"add a short comment at that case so a future reader does not regress it back to `>`."

// journeyFixtureAddressDiff is Amelia's SECOND attempt in the rejection journey: a small delta
// (relative to attempt 1's committed `>=` tree) that ADDRESSES Quinn's finding by adding the
// requested comment. It keeps the measure GREEN (a comment changes no behavior), so Quinn's
// second review — with no new finding — approves. Its `-` context matches attempt 1's fix line.
const journeyFixtureAddressDiff = "--- a/health.go\n" +
	"+++ b/health.go\n" +
	"@@ -28,7 +28,7 @@ func Classify(cpu, mem float64) Status {\n" +
	" \tswitch {\n" +
	" \tcase pressure >= criticalThreshold:\n" +
	" \t\treturn Unhealthy\n" +
	"-\tcase pressure >= warningThreshold:\n" +
	"+\tcase pressure >= warningThreshold: // warning boundary is inclusive by design (reviewed)\n" +
	" \t\treturn Degraded\n" +
	" \tdefault:\n" +
	" \t\treturn Healthy\n"

// entityStatesBucket is the graph fact-store KV bucket the loop's decide triple
// lands in (graph-ingest's kv-write output). The journey scans it for the
// coordinator's decision.
const entityStatesBucket = "ENTITY_STATES"

// wantAgenticHealthy are the components the coordinator loop needs live before the
// front-door message can be served: the agentic-execution plane plus the graph
// fact-store the loop's tools read/write.
var wantAgenticHealthy = []string{
	"graph-ingest", "graph-query", "rule",
	"agentic-tools", "agentic-model", "agentic-loop", "agentic-dispatch",
	// The R6 deterministic-station components must be healthy before the arc drives into
	// them — a mis-wired station factory fails here, not at a late-station timeout.
	"delivery-station", "projection-station", "validation-station", "floors-station", "verify-station", "provision-station",
}

// TestBridgeProofIssueToPRAgainstMock is the mock-LLM bridge proof: publish an
// admitted issue's coordinator wake to the front door and prove the whole
// issue→PR arc CONNECTS through the real runtime — the coordinator routes, the
// run mints, the change authors + validates, the human approves, Amelia's bounded
// multi-turn loop develops → measures in-loop, the floors route advances, Quinn's
// review approves, the cold verify passes, and the delivery route opens the PR
// (delivery.pr.ref). Routing is rule-native. A bridge proof of connection, not a claim of
// completeness (G7/G10).
func TestBridgeProofIssueToPRAgainstMock(t *testing.T) {
	// The mock scripts the arc's sequential turns as a POSITIONAL sequence
	// (cursor advances per matched tool call). The reshape makes Amelia's dev loop a
	// BOUNDED MULTI-TURN loop, so the cursor now spans WITHIN her loop:
	//   1. C1 front-door coordinator → decide(issue_intake)     [mints the run]
	//   2. C2 re-woken coordinator   → decide(create_change)    [routes to author]
	//   3. A1 authoring coordinator  → create_change(<change>)  [emits the change]
	//      (validate + projection + provision are COMPONENTS now — their rules publish, no turns)
	//   4. C3 dev re-woken coord.    → decide(dev_from_task)    [post-approval kickoff]
	//   5. D-a developer (Amelia)    → apply_patch(fix diff)    [authors the fix, tool_choice=auto]
	//   6. D-b developer (Amelia)    → measure_task(index 0)    [measures IN-LOOP, real go test]
	//   7. D-c developer (Amelia)    → (no tool → completion)   [she is done; the loop ends]
	//      (floors is a COMPONENT now — the floors trigger publishes it on her terminal, no turn)
	//   8. R1 reviewer (Quinn)       → submit_review(index 0)   [per-task floored verdict]
	//      (verify is a COMPONENT now — the review-approved route publishes it, no model turn)
	//      (delivery is a COMPONENT now — the delivery route publishes it, no model turn)
	// Turns 5-7 are ONE developer loop: apply_patch and measure_task no longer StopLoop, so
	// under tool_choice=auto the loop continues; her apply_patch AND measure_task turns both
	// guard on her own prompt (journeyDeveloperMarker), and turn 7 finds no matching fixture
	// at the cursor (the next is submit_review, keyed on "SEMDEV REVIEWER" which is not in
	// Amelia's prompt) so — with tool results present — the mock returns a COMPLETION, ending
	// her loop (StatusComplete → outcome=success). The stations + route rules then chain the
	// rest with NO model turns of their own: the provision STATION cold-proves off approval →
	// sandbox.provision.ready releases the dev re-wake; her terminal → the floors STATION (06a advance)
	// spawns Quinn → review route (07a approved) PUBLISHES the verify-station component →
	// delivery route (08a coherent) PUBLISHES the delivery-station component. The
	// validate/projection/PROVISION/FLOORS/verify/delivery deterministic stations are all
	// publish-triggered components (R6), so 7 tool fixtures drive 8 model turns (turn 7 consumes
	// no fixture). An unscripted turn returns UnmatchedSentinel.
	mock := mockllm.New(
		mockllm.Fixture{
			Marker: journeyIssueRef,
			Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
				"action": journeyDecideAction,
				"reason": "new admitted issue " + journeyIssueRef + " needs a run",
			}},
		},
		mockllm.Fixture{
			Marker: journeyIssueRef,
			Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
				"action": "create_change",
				"reason": "author the change for " + journeyIssueRef,
			}},
		},
		mockllm.Fixture{
			Marker: journeyIssueRef,
			Tool:   &mockllm.ToolCall{Name: "create_change", Args: journeyChangeArgs(3)},
		},
		// Validation + projection + provision are publish-triggered COMPONENTS now (R6, group 6):
		// the validate rule (coordinator/03), the projection rule (dev-from-task/03), and the
		// provision rule (sandbox/01) each fire a plain `publish` to their station component,
		// which validates / freezes task.spec / cold-proves the sandbox with ZERO model turns —
		// so there are no validate_change / project_tasks / provision_sandbox fixtures to script.
		// The gate rule (openspec.change.validated), the provision gate (task.spec), and the dev re-wake
		// gate (sandbox.provision.ready) all wait on the components' stamped facts, not on a model turn.
		mockllm.Fixture{
			Marker: journeyDevRewakeMarker,
			Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
				"action": journeyDevAction,
				"reason": "the change is approved and the run resumed; develop the run's tasks",
			}},
		},
		// Amelia's bounded multi-turn loop: apply_patch then measure_task, BOTH guarded by her
		// own prompt (journeyDeveloperMarker) — the loop continues because neither StopLoops.
		mockllm.Fixture{
			Marker: journeyDeveloperMarker,
			Tool:   &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}},
		},
		mockllm.Fixture{
			Marker: journeyDeveloperMarker,
			Tool:   &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}},
		},
		// (Amelia's 3rd turn matches no fixture here — the cursor sits on submit_review, whose
		// marker is absent from her prompt — so the mock returns a completion and her loop ends.
		// The floors STATION then runs off her terminal, R6, no model turn — no check_floors fixture.)
		mockllm.Fixture{
			Marker: journeyReviewMarker,
			Tool:   &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}},
		},
		// Verify is a publish-triggered COMPONENT now (R6, group 6): the review-approved route
		// (dev-from-task/07a) fires a plain `publish` to the verify-station component, which
		// cold-proves the committed artifact and stamps verify.cleanroom.result with ZERO model turns — so
		// there is no verify_artifact fixture to script.
		// Delivery is likewise a publish-triggered COMPONENT (R6): the delivery route
		// (dev-from-task/08a) fires a plain `publish` to the delivery-station component, which
		// records delivery.pr.ref with ZERO model turns — so there is no open_pr fixture to script.
	)
	// The provision station builds the fixture's declared golang image and proves it cold
	// (real docker, real go build), and the measure station then runs `go test` in the warm
	// container — so this journey needs a wide budget over the pure-routing stations before it.
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)
	taskID := publishCoordinatorWake(ctx, t)

	// Station 2 — the coordinator routed. Poll the graph until the coordinator's
	// decide triple lands (the proof it routed, not merely that it reached the
	// model), bound to THIS wake's task id so a stale prior-run decision can't
	// false-green.
	requireCoordinatorDecision(ctx, t, taskID, journeyDecideAction)
	t.Logf("station 2: coordinator routed via decide: task=%s next_action=%s (mock RequestCount=%d)", taskID, journeyDecideAction, mock.RequestCount())

	// Station 3 — the decision minted a run. The issue_intake spawn rule fires
	// publish_agent run_scope=new on the coordinator loop; the framework mints the
	// run (rooted at the coordinator's loop id) and the agent-run bridge advances
	// it dispatched→executing. Read the run anchor off THIS coordinator loop, then
	// assert the run entity reaches executing — proof the mint + bridge rules fired
	// live, not just that a decision was stamped.
	runEntityID := requireRunAnchor(ctx, t, taskID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("station 3: run %s minted and reached executing", runEntityID)

	// Station 4 — the coordinator re-woke, decided create_change, and a rule
	// spawned an authoring coordinator loop that called create_change. Assert the
	// change facts landed on the run entity: proof the create_change spawn rule
	// fired, the author inherited the run anchor, and the tool stamped the whole
	// change document (beta.147 D3: openspec.change.document, decoded and content-
	// checked against the fixture) on the run (the create_change→validate→approval
	// arc hangs off these facts).
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	t.Logf("station 4: change %q authored onto run %s (mock RequestCount=%d)", journeyChangeSlug, runEntityID, mock.RequestCount())

	// Station 5 — the authored marker published the VALIDATION STATION component (R6),
	// which ran the OpenSpec CLI oracle and stamped openspec.change.validated on the run, firing
	// the existing change-approval gate (run-lifecycle/01) executing→awaiting_approval.
	// Asserting the phase reached awaiting_approval proves the whole chain WITH ZERO model
	// turns for the validate step: create_change's loop marker → the validate rule's publish
	// → the validation-station component → openspec.change.validated → the gate. (The gate requires
	// openspec.change.validated present, so awaiting_approval implies the component stamped it.)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("station 5: validation-station validated the change → run reached awaiting_approval (gate fired, no model turn); mock RequestCount=%d", mock.RequestCount())

	// Station 6 — the human approves the change. The journey stands in for the
	// group-5 approval adapter (a forge-io path, deferred): it stamps
	// run.change.decision="approve" on the RUN entity exactly as that adapter will
	// (Source approval-adapter, on the run entity per D15). The EXISTING
	// run-lifecycle/02-resume-after-change-approval rule then fires
	// awaiting_approval→executing — the release side of the first human gate.
	// Asserting the run returns to executing proves the resume rule fires live on a
	// real approval fact; the transition is rule-owned (G2), no product Go advances
	// it. (Station 5 asserted awaiting_approval immediately above, so this
	// executing assertion cannot false-match the pre-gate executing state.)
	approveChange(ctx, t, rt, runEntityID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("station 6: human approved → run resumed to executing (mock RequestCount=%d)", mock.RequestCount())

	// Station 7 — approval PROJECTS the immutable task surface BEFORE the dev loop
	// routes into development (dev-from-task spec: WHEN run.change.decision=="approve"
	// THEN tasks projected as task.spec; Codex P1 on f1eed5c). Two rules fire on the
	// run entity: dev-from-task/01 stamps the bare agent.run anchor, then
	// dev-from-task/03 fires a `publish` to the PROJECTION STATION component (R6),
	// which reads the change's task facts, binds to the validated content (D15 #0), and
	// stamps task.spec.<i>.* on the run with ZERO model turns. The projection rule threads
	// the slug via the run-level openspec.change.slug pointer create_change stamped, as a
	// publish property. Assert task.spec.test-command is present — proof the projection component ran
	// against the real change (exercising the revision guard projecttasks.Project carries)
	// and the dev loop has an immutable spec to converge on before it kicks off.
	requireTaskSpecProjected(ctx, t, runEntityID)
	t.Logf("station 7: projection-station froze task.spec on approval (no model turn); mock RequestCount=%d", mock.RequestCount())

	// Station 8 — the make-or-break: the run's sandbox is PROVISIONED AND PROVED COLD
	// before development. sandbox/01-provision (gated on task.spec presence, so it
	// runs AFTER projection) PUBLISHES the provision-station COMPONENT (R6, no model
	// turn), which materializes the run's checkout off boot's SHARED checkouts, builds
	// the fixture's DECLARED golang image, proves the Go module resolves + builds cold
	// in a fresh container, stands up the WARM dev container in boot's SHARED sandboxes
	// (the one measure_task later Execs into), then stamps sandbox.provision.ready + the
	// digest-pinned attestation. This runs the REAL cold proof (docker build + go build),
	// the exact class both predecessors faked — zero model turns. Assert sandbox.provision.ready ==
	// true AND the image attestation is present — proof the provision station ran the real
	// proof on the committed artifact and the readiness gate can release. Red-first:
	// disable sandbox/01 (or the provision-station component) and this station times out.
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("station 8: provision-station provisioned + proved cold (no model turn) — sandbox.provision.ready stamped (mock RequestCount=%d)", mock.RequestCount())

	// Station 9 — with task.spec frozen AND the sandbox proven ready, the resumed run
	// kicks off the dev loop. dev-from-task/02 (gated on task.spec.test-command ne ""
	// AND sandbox.provision.ready eq true, so it fires only AFTER projection and provisioning)
	// does the inherit publish — spawning a fresh coordinator loop bound to THIS run,
	// which re-decides and picks dev_from_task. Assert that a coordinator loop BOUND
	// TO THIS RUN (agent.run.entity-id == runEntityID) stamped next_action=dev_from_task
	// — proof the projection+readiness-gated re-wake fired and routed into development.
	// Bind by the run anchor + the distinct action so an earlier decision (issue_intake
	// / create_change) on the same run cannot false-green.
	requireRunCoordinatorDecision(ctx, t, runEntityID, journeyDevAction)
	t.Logf("station 9: dev loop kicked off — coordinator re-woke and decided %s (mock RequestCount=%d)", journeyDevAction, mock.RequestCount())

	// Station 10 — the dev loop does REAL WORK: dispatch-developer (dev-from-task/04)
	// fires on the coordinator's dev_from_task decision and spawns AMELIA (a developer
	// loop bound to this run), forced to apply_patch. Amelia authors the fixture's real
	// fix diff; the harness applies it to the run's checkout for real (git apply).
	// Assert a DEVELOPER loop bound to THIS run (agent.run.entity-id == runEntityID)
	// reached agent.loop.outcome=success — proof the dispatch fired, Amelia inherited
	// the run, apply_patch ran and applied cleanly. The fix actually working is proven
	// by the in-container measure (Amelia's in-loop turn); here we prove the author station chained.
	requireDeveloperLoopCompleted(ctx, t, runEntityID)
	t.Logf("station 10: developer dispatched — Amelia authored the fix via apply_patch (mock RequestCount=%d)", mock.RequestCount())

	// Station 11 — the make-or-break payoff: the fix is MEASURED cold, IN-LOOP, in-container.
	// The reshape moved measure INTO Amelia's bounded loop: her SECOND turn calls
	// measure_task (no StopLoop), which Execs the task's frozen test command (`go test ./...`)
	// IN the run's WARM sandbox container — the one the provision station stood up over the SAME
	// checkout apply_patch wrote — and stamps the harness-derived measurement.result as her
	// in-loop feedback. Because the developer's diff actually fixed the boundary bug, the
	// in-container `go test` PASSES: assert measurement.result.passed == "true" — proof the
	// loop did REAL work (author → apply → build cold → test), not theater. Also assert the
	// attempt counter task.attempt.instance was appended (by dispatch at spawn, R3 — the route counts
	// it against the budget). Red-first: break the warm container (or measure over unpatched
	// bytes) and passed comes back "false".
	requireMeasurementPassed(ctx, t, runEntityID)
	requireTriplePresent(ctx, t, runEntityID, "task.attempt.instance",
		"dispatch-developer (dev-from-task/04) must append the per-task attempt counter AT SPAWN (R3 — the route counts it against the budget)")
	t.Logf("station 11: task measured IN-LOOP, in-container — measurement.result.passed=true (the fix is REAL); attempt counted (mock RequestCount=%d)", mock.RequestCount())

	// Station 12 — the structural floors run on the developer's REAL diff (R6: the floors
	// STATION component, zero model turns). The floors trigger (dev-from-task/05) fires on
	// Amelia's DEVELOPER-loop L_n TERMINAL (agent.loop.outcome ne "" — measure moved in-loop, so
	// there is no separate measure loop) and PUBLISHES the floors-station component, which runs
	// the deterministic go/ast floors over the task's target files in the checkout — the SAME
	// bytes measure ran over (via boot's SHARED runspace.Checkouts, the DI seam) — stamps the
	// aggregate floor.finding.rejected + the per-floor findings on the run, AND mirrors the
	// routing inputs (route.attempt.passed/route.attempt.rejected/route.attempt.instance) onto L_n for the route. Because
	// the fix is a clean edit (real test present, source parses, no stub/mock, targets authored),
	// NO floor rejects: assert floor.finding.rejected == "false" — the structural verdict the
	// floors route advances on. Also assert the presence floor ran (the H1 gate). Red-first:
	// disable dev-from-task/05 and this station times out.
	requireFloorsPassed(ctx, t, runEntityID)
	t.Logf("station 12: floors-station ran on the diff (no model turn) — floor.finding.rejected=false (no fabrication) (mock RequestCount=%d)", mock.RequestCount())

	// Station 13 — the FLOORS ROUTE advances (rule-native, the reshape replaced check_gate).
	// The floors STATION mirrored the routing inputs onto Amelia's DEVELOPER loop L_n:
	// route.attempt.passed (=measurement.result.passed=true, bound to the current attempt.commit.sha),
	// route.attempt.rejected (=floor.finding.rejected=false), route.attempt.instance (the attempt mirror). The
	// floors ROUTE (dev-from-task/06a) then fires ON L_n (the make-or-break coupling: the route
	// moved from the deleted check_floors loop to the developer loop): because route.attempt.passed=true
	// AND route.attempt.rejected=false, it ADVANCES — spawning Quinn's reviewer loop directly (NO gate
	// tool, NO route-token, NO extra model turn for the route rule). Quinn reviews the cleared
	// task (D16): submit_review reads the task's task.spec + measurement.result and DERIVES the
	// floored verdict (G3). Because the task measured green and the mock raises no findings, the
	// verdict is approved: assert review.verdict.value == approved. Red-first: break the measurement
	// and the floors route goes not_clean→retry instead of advance→review, so no verdict lands.
	requireReviewApproved(ctx, t, runEntityID)
	t.Logf("station 13: floors route advanced on L_n → Quinn reviewed the cleared task — review.verdict.value=approved (mock RequestCount=%d)", mock.RequestCount())

	// Station 14 — the CLEAN-ROOM COLD VERIFY of the committed artifact (the make-or-break).
	// The review ROUTE (dev-from-task/07a) fires on Quinn's loop reading the route.review.verdict
	// mirror: because route.review.verdict=approved, it PUBLISHES the verify-station COMPONENT (R6, no
	// model turn), threading run_entity_id as a property (Q_n is the firing entity, so the run
	// travels as a property). The verify station CLONES the run's checkout into a fresh dir off
	// boot's SHARED checkouts (CloneForVerify), builds the operator-declared image, and proves
	// the artifact resolves + passes its own tests COLD in a SEPARATE fresh container with a
	// fresh dependency cache. Because the fix is real and self-contained, the cold verify
	// PASSES: assert verify.cleanroom.result == "pass". This runs the REAL docker cold proof — zero model
	// turns. Red-first: a non-self-contained fix (or a warm-cache-masked fabrication) comes back
	// "fail"; disable the verify-station component and verify.cleanroom.result never lands.
	requireVerifyPassed(ctx, t, runEntityID)
	t.Logf("station 14: verify-station cold-proved the committed artifact (no model turn) — verify.cleanroom.result=pass (mock RequestCount=%d)", mock.RequestCount())

	// Station 15 — the ISSUE→PR ARC CONNECTS end-to-end. The DELIVERY ROUTE (dev-from-task/08a)
	// fires ON THE RUN (delivery is terminal, so it routes on run facts directly — no loop, no
	// mirror, no check_coherence tool): because verify.cleanroom.result=pass AND review.verdict.value=approved
	// AND openspec.change.validated present, it PUBLISHES the delivery-station COMPONENT (R6), which
	// delivers the REAL adapter path with zero model turns (forge-io-real-lanes: a REAL git push of
	// the committed attempt branch to the journey's bare remote + query-by-head + create against the
	// protocol-faithful forge double; the local-delivery stub is DELETED). requirePRDelivered asserts
	// the PR URL, the evidence-bearing body, the query-before-create ordering, and the pushed branch.
	// Red-first: break any signal (verify/validate/review) and the delivery route blocks (08b
	// parks); an unconfigured forge FAILS delivery closed and the run parks (station-failure-parks).
	requirePRDelivered(ctx, t, runEntityID)
	t.Logf("station 15: ISSUE→PR ARC CONNECTS (bridge proof) — REAL delivery: branch pushed, evidence PR opened on the double (mock RequestCount=%d)", mock.RequestCount())

	// Station 15b — REPLAY across BOTH guards, live: re-dispatch the delivery
	// station for the same run; the graph guard short-circuits (same ref, no
	// second push, ZERO further API calls). The discriminator is the double's
	// REQUEST LOG, not a fixed sleep (review finding: on the failure path the
	// erroneous replay makes real find/create calls, so a NEW request appearing
	// is the failure signal — poll for growth and fail fast; "still 1 PR after
	// a nap" could false-green under load).
	client15 := connectFrontDoor(ctx, t)
	baselineRequests := len(journeyForgeDouble.Requests())
	replayEnv, _ := json.Marshal(map[string]any{"entity_id": runEntityID, "properties": map[string]any{}})
	if err := client15.Publish(ctx, "component.delivery-station.dispatch", replayEnv); err != nil {
		t.Fatalf("replay dispatch publish: %v", err)
	}
	_ = client15.Close(context.Background())
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(journeyForgeDouble.Requests()); got > baselineRequests {
			t.Fatalf("the REPLAYED delivery dispatch reached the forge (%d new API requests: %+v) — the graph guard must short-circuit BEFORE any push or API call",
				got-baselineRequests, journeyForgeDouble.Requests()[baselineRequests:])
		}
		time.Sleep(250 * time.Millisecond)
	}
	if prs := journeyForgeDouble.PRs(); len(prs) != 1 {
		t.Fatalf("after a REPLAYED delivery dispatch the double holds %d PRs, want STILL exactly 1", len(prs))
	}
	t.Logf("station 15b: replayed delivery dispatch made ZERO forge calls — one PR across replays at the graph guard (forge guard unit-pinned)")

	// Exactly eight model turns drove the FULL arc. The route rules chain the stations with NO
	// model turns of their own, and the deterministic stations validate / project / PROVISION /
	// FLOORS / VERIFY / deliver are publish-triggered COMPONENTS (R6), not forced turns (the
	// reshape also deleted the check_gate/check_coherence forced turns and the separate measure
	// loop). Turns: C1, C2, A1, C3, Amelia-apply, Amelia-measure, Amelia-stop, R1 review = 8
	// (provision is now a component, dropping the old PS turn). A spurious re-spawn (a route
	// re-firing) or an unscripted turn would push this past 8.
	if got := mock.RequestCount(); got != 8 {
		t.Fatalf("expected exactly 8 model turns (…, Amelia's 3-turn loop, R1 review; validate/projection/provision/floors/verify/delivery are components, not turns), got %d — extra turns indicate a re-spawn/loop, an errant route, or an unscripted turn", got)
	}
}

// TestBridgeProofRetryFailThenPass drives the FAIL-THEN-PASS RETRY station (task 10.1,
// design R10 — the standing MEDIUM carry-forward): Amelia's FIRST attempt measures RED
// in-container, the FLOORS ROUTE goes not_clean→RETRY (dev-from-task/06c) and re-dispatches
// a FRESH developer loop, and her SECOND attempt measures GREEN and advances to review →
// verify → delivery. This proves the retry machinery drove e2e, not merely that a green
// attempt ships. Zero paid tokens.
//
// The checkout CHAINS across attempts (apply_patch commits cumulatively — a retry applies
// on top of the prior attempt's committed tree, not a fresh pristine base, per
// applypatch.go), so attempt 1 lands a WIP diff that compiles but leaves the boundary bug
// (measures RED like pristine) and attempt 2 lands the corrective diff RELATIVE TO that WIP
// line (measures GREEN). The mock keys attempt 1 on the dispatch prompt (SEMDEV DEVELOPER)
// and attempt 2 on the RETRY prompt (journeyRetryMarker): the positional tool cursor parks
// attempt-2's fixtures until the retry loop's prompt arrives — a marker miss does not burn
// the cursor (ssmock tryRoleToolCall) — so attempt 1's loop ends on a completion (its 3rd
// turn's prompt lacks the retry marker) BEFORE the route re-dispatches. Ordering, not luck.
func TestBridgeProofRetryFailThenPass(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		// Attempt 1 (SEMDEV DEVELOPER dispatch prompt): the WIP diff compiles but leaves the boundary
		// bug → the in-container measure is RED → floors route not_clean → retry re-dispatch.
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureWipDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// Attempt 2 (the RETRY prompt — journeyRetryMarker): the corrective diff → measure GREEN →
		// advance. Keyed on the retry marker so it stays parked on the cursor until the retry loop
		// arrives (attempt 1's dispatch prompt does not contain it, so attempt 1 ends first).
		mockllm.Fixture{Marker: journeyRetryMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureRetryFixDiff}}},
		mockllm.Fixture{Marker: journeyRetryMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// Quinn reviews the cleared second attempt → approved → verify + delivery (both components).
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	// One extra failing developer loop over the bridge proof (a second apply + real in-container
	// measure + the retry re-dispatch), so budget slightly wider than the happy path's 7 minutes.
	// Both journeys share journeyIssueRef; running them in ONE `task e2e` is safe because each
	// journey resets NATS before booting (startJourneyRuntime → resetNATS), so this test's run is
	// the only one in a clean durable graph — no stale run from the prior journey to interfere.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	// Drive the shared front-of-arc exactly as the bridge proof does, through human approval,
	// projection, and the cold-proved sandbox — the retry only diverges inside the dev loop.
	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	// Wide-window RETRY gate (the direct proof a retry fired, positioned to absorb attempt 1's
	// COLD in-container compile): exactly TWO attempts were dispatched — dispatch-developer (04)
	// appended #1 at the first spawn, the retry route (06c) appended #2 at the re-dispatch, which
	// only happens AFTER attempt 1 measured RED and the floors route went not_clean. Gating here
	// (before the review gate) means the first cold `go test` compile is absorbed with a clear
	// message rather than blamed on the review route's tighter window. Exactly two: fewer = no
	// retry (the WIP diff wrongly passed on attempt 1); more = a misfire/loop (caught fast).
	requireAttemptCount(ctx, t, runEntityID, 2, 4*time.Minute)
	t.Logf("retry station: attempt 1 measured RED → floors route retried → 2 attempts dispatched on run %s", runEntityID)

	// The run RECOVERED: attempt 2 measured GREEN and advanced to review → cold verify → delivery.
	// requirePRDelivered fails loud if the run PARKED instead (run.awaiting.human, no delivery.pr.ref) — a
	// broken retry that exhausted to a park surfaces here rather than as a bare timeout. attempt 2's
	// measure is WARM (attempt 1 already compiled), so the review gate's tighter window suffices.
	requireReviewApproved(ctx, t, runEntityID)
	requireVerifyPassed(ctx, t, runEntityID)
	requirePRDelivered(ctx, t, runEntityID)

	// Turn accounting: the bridge proof's 8 turns + Amelia's EXTRA failing loop (apply, measure,
	// stop = 3) = 11. A different count means the retry route misfired, an extra loop spawned, or
	// a turn went unscripted.
	if got := mock.RequestCount(); got != 11 {
		t.Fatalf("expected exactly 11 model turns (bridge-proof 8 + the extra failing developer loop's apply/measure/stop = 3), got %d — a mismatch means the retry route misfired, an extra loop spawned, or a turn went unscripted", got)
	}
}

// TestBridgeProofReviewRejectionReentry drives the REVIEWER-REJECTION RE-ENTRY station (task
// 10.2, design R10/D16): Amelia's attempt measures GREEN and reaches review, but Quinn RAISES A
// FINDING — so the verdict is changes_requested (G3: approved ⟺ measured-pass ∧ no findings, so a
// finding rejects even a passing measurement) — and the REVIEW route re-enters DEVELOPMENT
// (dev-from-task/07b), not verify→park. This is the D16 fix the old rail got wrong. Amelia's
// fresh attempt addresses the finding, Quinn's second review approves, and the run delivers.
// Zero paid tokens.
//
// The re-entry comes from the REVIEW route, not the floors route: BOTH measurements pass, so the
// floors retry (06c) can never fire — the only thing that can drive a second attempt here is
// Quinn's rejection. So attempt count 2 IS the rejection re-entry, and the TWO Quinn reviews
// (12 turns vs the measurement-retry's 11) are its signature. The mock keys attempt 2 on 07b's
// distinctive "requested changes" substring (parked on the cursor until the re-entry prompt
// arrives); the checkout chains so attempt 2's addressing diff is a delta on attempt 1's fix.
func TestBridgeProofReviewRejectionReentry(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		// Attempt 1 (dispatch prompt): the GOOD fix → measure GREEN → floors advance → Quinn reviews.
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// Quinn's FIRST review RAISES A FINDING → changes_requested → review route 07b re-dispatches
		// Amelia (D16 re-entry) with the finding to address.
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{
			"task_index": 0, "findings": []any{journeyReviewFinding}}}},
		// Attempt 2 (07b re-entry prompt — journeyReviewRetryMarker): a delta ADDRESSING the finding →
		// measure GREEN → floors advance → Quinn reviews AGAIN.
		mockllm.Fixture{Marker: journeyReviewRetryMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureAddressDiff}}},
		mockllm.Fixture{Marker: journeyReviewRetryMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// Quinn's SECOND review — NO findings, still-passing measurement → approved → verify → delivery.
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	// Shared front-of-arc through approval, projection, and the cold-proved sandbox.
	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	// Wide-window RE-ENTRY gate (absorbs attempt 1's COLD compile): TWO attempts were dispatched —
	// 04 appended #1, the REVIEW-RETRY route 07b appended #2 after Quinn's rejection. Since both
	// measurements pass (the floors retry 06c never fires), a second attempt can ONLY come from the
	// reviewer rejection re-entering development — so count 2 here proves the D16 re-entry.
	requireAttemptCount(ctx, t, runEntityID, 2, 4*time.Minute)
	t.Logf("rejection station: Quinn rejected attempt 1 (finding) → review route re-entered dev (07b) → 2 attempts on run %s", runEntityID)

	// The run RECOVERED: attempt 2 addressed the finding, Quinn's SECOND review approved → verify →
	// delivery. Use the rejection-tolerant wait — review.verdict.value is TRANSIENTLY changes_requested
	// (Quinn's first review) before it becomes approved, which the happy-path requireReviewApproved
	// would (correctly, for its path) treat as a hard failure. requirePRDelivered fails loud if the
	// run PARKED instead (a broken 07b would exhaust to the review-park 07c).
	if sawRejection := requireReviewEventuallyApproved(ctx, t, runEntityID, 90*time.Second); sawRejection {
		t.Logf("rejection station: observed the transient review.verdict.value=changes_requested before approval on run %s", runEntityID)
	}
	requireVerifyPassed(ctx, t, runEntityID)
	requirePRDelivered(ctx, t, runEntityID)

	// Turn accounting: front-of-arc 4 + attempt-1 loop (apply/measure/stop = 3) + Quinn's first
	// review (1, terminal) + attempt-2 loop (3) + Quinn's second review (1) = 12. The TWO reviews
	// are the rejection signature — a measurement retry (10.1) has ONE review and totals 11.
	if got := mock.RequestCount(); got != 12 {
		t.Fatalf("expected exactly 12 model turns (front-of-arc 4 + two dev loops 3+3 + two Quinn reviews 1+1), got %d — a mismatch means the rejection did not re-enter dev (07b), an extra loop spawned, or a turn went unscripted", got)
	}
}

// TestBridgeProofBudgetExhaustionParks drives the BUDGET-EXHAUSTION PARK station (task 10.3,
// design R10/SB5/D6): when the shared per-task ATTEMPT budget is exhausted without the task
// converging, the run PARKS toward the human — it never ships a PR and never claims a green.
//
// The task authors an EXPLICIT budget of 2 (not the old hard-coded 3): this proves the per-task
// budget is LOAD-BEARING — the route reads route.task.budget (the mirror of the projected
// task.spec.budget), so the boundary moves with the authored value. Against the pre-#568
// constant-3 rules this journey would run a THIRD attempt and never park at 2, so it is a direct
// regression proof of the adoption. Quinn rejects TWICE (a finding each): attempt 1 re-enters
// development (07b, count 1 < 2), and the second rejection at count 2 triggers the review-exhaustion
// park (07c, route.review.verdict=changes_requested ∧ route.attempt.instance count ≥ 2 = budget).
// Zero paid tokens. A companion journey (TestBridgeProofBudgetOneEscalatesOnFirstRed) exercises the
// boundary at budget 1 on the FLOORS-escalate route, so it is proven at more than one value (D6).
//
// This journeys the REVIEW-exhaustion park (07c). The floors-exhaustion park (06d) is the SAME park
// writer (run.awaiting.human) with a different trigger — covered at budget 1 by the companion, where
// a single RED attempt escalates immediately (no consecutive 06c loops to confuse the positional mock
// cursor). All measurements here PASS (comment-only deltas on the chained checkout); the budget
// exhausts on the reviewer, not the harness.
func TestBridgeProofBudgetExhaustionParks(t *testing.T) {
	rejectWithFinding := mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review",
		Args: map[string]any{"task_index": 0, "findings": []any{journeyReviewFinding}}}}
	mock := mockllm.New(append(journeyFrontOfArcFixtures(2),
		// Attempt 1 (dispatch): fix → measure GREEN → advance → Quinn REJECTS → 07b (count→2).
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		rejectWithFinding,
		// Attempt 2 (07b re-entry): address → measure GREEN → advance → Quinn REJECTS again at count 2
		// = budget → the review-exhaustion PARK (07c, count ≥ B via length_gte), NOT another re-dispatch.
		mockllm.Fixture{Marker: journeyReviewRetryMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureAddressDiff}}},
		mockllm.Fixture{Marker: journeyReviewRetryMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		rejectWithFinding,
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	// Shared front-of-arc through approval, projection, and the cold-proved sandbox.
	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	// Wide-window EXHAUSTION gate (absorbs attempt 1's COLD compile): the budget is fully spent —
	// TWO attempts were dispatched (04 = #1, one 07b re-entry = #2). The second rejection at count 2
	// cannot re-dispatch (07b needs count < 2 = budget); it parks (07c). Exactly two — against the old
	// constant-3 rules a THIRD would run, so this count IS the load-bearing-budget proof.
	requireAttemptCount(ctx, t, runEntityID, 2, 4*time.Minute)
	t.Logf("exhaustion station: Quinn rejected 2x → budget=2 spent (2 attempts) on run %s", runEntityID)

	// The run PARKS toward the human — no PR, no false green. requireRunParked fails loud if the run
	// delivered (a stray delivery route) or if the cold verify ever ran on the unapproved run.
	// 90s (matching the rejection sibling): after the count-2 gate returns, attempt 2 still has to
	// apply + measure (warm) + floors + the second review + 07c before the park lands.
	requireRunParked(ctx, t, runEntityID, 90*time.Second)
	t.Logf("exhaustion station: run parked (run.awaiting.human), no delivery.pr.ref, no verify.cleanroom.result — fail-closed (SB5)")

	// Turn accounting: front-of-arc 4 + two dev loops (3 each = 6) + two Quinn reviews (1 each = 2)
	// = 12. A different count means the budget did not exhaust at exactly two attempts (07b/07c
	// boundary at B=2), an extra loop spawned, or a turn went unscripted.
	if got := mock.RequestCount(); got != 12 {
		t.Fatalf("expected exactly 12 model turns (front-of-arc 4 + two dev loops 3×3 + two Quinn reviews 1×2), got %d — a mismatch means the budget did not exhaust at two attempts (07b/07c boundary at B=2) or a turn went unscripted", got)
	}
}

// TestBridgeProofBudgetOneEscalatesOnFirstRed proves the per-task budget boundary at a SECOND value
// (D6): with an authored budget of 1, the FIRST failed attempt exhausts the budget, so the
// FLOORS-escalate route (06d) parks the run immediately — there is NO retry (06c needs count < 1,
// but dispatch already appended count 1 at spawn). This exercises route.attempt.instance length_gte
// $…route.task.budget.value at B=1 on the floors path (the exhaustion sibling covers 07c at B=2), and
// it is the clean way to journey 06d: a single RED attempt escalates at once, with no consecutive
// 06c loops to confuse the positional mock cursor. Zero paid tokens.
//
// Against the pre-#568 constant-3 rules this journey would RETRY (count 1 < 3) instead of parking, so
// parking after one attempt is a direct proof the authored budget is load-bearing.
func TestBridgeProofBudgetOneEscalatesOnFirstRed(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(1),
		// Attempt 1 (dispatch prompt): the WIP diff compiles but leaves the boundary bug → the
		// in-container measure is RED → floors route not_clean → 06d escalate at count 1 = budget → PARK.
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureWipDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// CURSOR GUARD (never consumed): after the RED measure, Amelia's loop takes one more turn.
		// A tool-fixture cursor left EXHAUSTED would make the mock fall back to "first advertised
		// tool, empty args" (read_workspace) and spin the loop to its iteration cap; a trailing
		// fixture whose marker the dispatch prompt does NOT contain instead yields a marker-miss →
		// completion, so the loop stops cleanly on its 3rd turn (apply/measure/stop). This RED
		// attempt escalates (06d) and never reaches review, so this Quinn guard is never consumed.
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	// Shared front-of-arc through approval, projection, and the cold-proved sandbox.
	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	// Exactly ONE attempt: dispatch (04) appended count 1 at spawn; the RED measurement makes the
	// floors route not_clean, and 06d escalates at count 1 ≥ budget 1 — it does NOT re-dispatch (06c
	// retry needs count < 1). More than one would mean a retry wrongly fired past the authored budget.
	requireAttemptCount(ctx, t, runEntityID, 1, 4*time.Minute)
	t.Logf("escalate station: attempt 1 measured RED → 06d floors-escalate at count 1 = budget on run %s", runEntityID)

	// The run PARKS toward the human via the floors-escalate route (06d) — no PR, no false green.
	// REASON-AWARE (#569): 06d's park message carries the engine's classified terminal reason via
	// $entity.triple.agent.loop.terminal-reason.value. This journey's loop COMPLETED (measured
	// red), so the reason is absent and substitutes empty — the assertion proves the TEMPLATE
	// shipped and the substitution resolved (the message explains an empty reason in place).
	parkMsg := requireRunParked(ctx, t, runEntityID, 90*time.Second)
	if !strings.Contains(parkMsg, "last engine terminal reason:") {
		t.Fatalf("06d's park message is not reason-aware — want the 'last engine terminal reason:' template (#569), got %q", parkMsg)
	}
	if strings.Contains(parkMsg, "$entity.triple") {
		t.Fatalf("06d's park message carries an UNRESOLVED substitution token — the reason must substitute (empty here, since a completed-red loop stamps no terminal reason), got %q", parkMsg)
	}
	t.Logf("escalate station: run parked (run.awaiting.human, reason-aware message) at budget 1 — fail-closed (SB5)")

	// Turn accounting: front-of-arc 4 + one failing dev loop (apply, measure RED, stop = 3) = 7. No
	// Quinn (a RED attempt never advances to review). A different count means a retry fired past the
	// budget, an extra loop spawned, or a turn went unscripted.
	if got := mock.RequestCount(); got != 7 {
		t.Fatalf("expected exactly 7 model turns (front-of-arc 4 + one failing dev loop apply/measure/stop = 3), got %d — a mismatch means a retry fired past budget 1, an extra loop spawned, or a turn went unscripted", got)
	}
}

// TestBridgeProofTransientGraceRetries drives the TRANSIENT-FAILURE GRACE station
// (adopt-reason-aware-escalate, #529/#569). The INITIAL developer dispatch (04) fails with a
// TRANSIENT model_error terminal — the mockllm error-proxy 500s its model call (and the
// agentic-model client's retries of it), which the loop engine classifies as
// agent.loop.terminal-reason="model_error". That is NOT genuine non-convergence: check_floors
// classifies the reason and stamps route.attempt.transient="true" ATOMICALLY in the route
// mirror, and the transient-retry route (06f) re-dispatches a FRESH developer loop WITHOUT
// consuming the convergence budget. The retried loop's model call carries the 06f prompt (which lacks the dispatch error marker), so
// it proceeds normally: apply → measure GREEN → advance → review → verify → deliver. Zero paid
// tokens (the 500s are injected by the loopback proxy, never a provider).
//
// The LOAD-BEARING assertions: the convergence attempt budget count stays 1 (the initial
// dispatch — the transient re-dispatch appends task.transient.instance, NOT task.attempt.instance,
// so a flaky endpoint did not burn a convergence slot) AND a transient counter appears AND the
// run DELIVERS. Against a rail with no transient grace the model_error would count as a failed
// convergence attempt (or double-dispatch with 06c), so this is a direct proof of the adoption.
func TestBridgeProofTransientGraceRetries(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		// The INITIAL dispatch (04, prompt contains journeyDispatchTaskMarker) 500s → the model
		// client retries (all 500, none reaching ssmock) → model_error terminal → the floors
		// mirror stamps route.attempt.transient="true" → 06f transient-retry (a DIFFERENT
		// prompt, not 500'd).
		mockllm.Fixture{Marker: journeyDispatchTaskMarker, Error: true},
		// The transient-retry loop (06f prompt — journeyTransientMarker): the GOOD fix → measure
		// GREEN → advance. Parked on the cursor until the transient re-dispatch arrives.
		mockllm.Fixture{Marker: journeyTransientMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}}},
		mockllm.Fixture{Marker: journeyTransientMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		// Quinn reviews the cleared attempt → approved → verify + delivery (both components).
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	// A transient model_error + the client's retry backoff before the grace re-dispatch, then a
	// full recovering dev loop + review + cold verify → a wider window than the happy path.
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	// Shared front-of-arc through approval, projection, and the cold-proved sandbox.
	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	// The transient grace fired: a transient counter is stamped (the model_error re-dispatch) and
	// the run RECOVERED — the retried loop measured green and delivered. requirePRDelivered fails
	// loud if the run PARKED instead (a broken transient route that exhausted to 06g).
	requireTriplePresent(ctx, t, runEntityID, "task.transient.instance",
		"the transient-retry route (dev-from-task/06f) must append the transient counter on its re-dispatch")
	t.Logf("transient station: initial dispatch failed model_error → 06f transient-retry re-dispatched (task.transient.instance stamped) on run %s", runEntityID)

	// Budget UNTOUCHED: exactly ONE convergence attempt (the initial dispatch appended #1 at spawn;
	// the transient re-dispatch appended task.transient.instance, NOT task.attempt.instance). More
	// than one would mean a transient failure wrongly consumed a convergence-budget slot.
	requireAttemptCount(ctx, t, runEntityID, 1, 4*time.Minute)
	t.Logf("transient station: convergence budget untouched — exactly 1 task.attempt.instance (the transient retry was free)")

	requireReviewApproved(ctx, t, runEntityID)
	requireVerifyPassed(ctx, t, runEntityID)
	requirePRDelivered(ctx, t, runEntityID)

	// Determinism hardening: re-assert the budget AFTER delivery. The earlier count check
	// returns at first observation (count==1 holds from the initial dispatch onward), so a
	// LATE convergence double-dispatch — the pre-fix race's exact signature — could land
	// after it; post-delivery, any stray task.attempt.instance append has long since landed.
	requireAttemptCount(ctx, t, runEntityID, 1, 30*time.Second)
	t.Logf("transient station: run RECOVERED from the transient failure and DELIVERED (delivery.pr.ref) — grace outside the convergence budget")
}

// TestBridgeProofTransientCapParks drives the transient-exhaustion PARK (06g): the initial
// dispatch AND both graced re-dispatches die transiently — the mockllm error gates match the
// dispatch prompt AND the transient-retry prompt, so every developer model call 500s. Three
// transient deaths: the dispatch (transient count 0 → 06f grace #1, appends
// task.transient.instance), retry #1 (count 1 → grace #2), retry #2 (count 2 → 06g length_gte
// 2 parks). Proves the grace is BOUNDED (a persistently-dead endpoint parks toward the human,
// never an unbounded retry loop), the park message substitutes a REAL non-empty reason
// (model_error — the budget-1 journey only proves the template over an empty reason), and the
// convergence budget stays untouched (all three deaths were free of task.attempt.instance).
func TestBridgeProofTransientCapParks(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		mockllm.Fixture{Marker: journeyDispatchTaskMarker, Error: true},
		mockllm.Fixture{Marker: journeyTransientMarker, Error: true},
	)...)

	// Three loop deaths each behind the model client's retry backoff, then the park.
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	rt := startJourneyRuntime(ctx, t, mock)

	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	parkMsg := requireRunParked(ctx, t, runEntityID, 4*time.Minute)
	if !strings.Contains(parkMsg, "TRANSIENT") || !strings.Contains(parkMsg, "model_error") {
		t.Fatalf("06g's park message must name the transient class and the SUBSTITUTED reason (model_error) — the reason-aware park over a real terminal reason (#569), got %q", parkMsg)
	}
	if strings.Contains(parkMsg, "$entity.triple") {
		t.Fatalf("06g's park message carries an unresolved substitution token: %q", parkMsg)
	}
	t.Logf("transient-cap station: three model_error deaths → 2 graces consumed → 06g parked with the transient reason on run %s", runEntityID)

	// Exactly the cap's worth of graces, and the convergence budget untouched (the initial
	// dispatch's single append — the transient track never touched it).
	requireRunTripleCount(ctx, t, runEntityID, "task.transient.instance", 2, 30*time.Second)
	requireAttemptCount(ctx, t, runEntityID, 1, 30*time.Second)
	t.Logf("transient-cap station: task.transient.instance=2 (the cap), task.attempt.instance=1 — bounded grace, budget free")
}

// driveSharedFrontOfArc drives the front of every negative-path journey IDENTICALLY — publish the
// front-door wake, confirm the coordinator routed, bind the minted run, author + validate the
// change to awaiting_approval, approve as the human, then wait through projection and the
// cold-proved sandbox — and returns the run entity id the caller asserts against. It is the
// driver-side mirror of journeyFrontOfArcFixtures (the mock-side prefix), so the shared front
// cannot drift between the retry/rejection/exhaustion journeys. (The bridge proof keeps its own
// inline, per-station-logged copy as the annotated reference walk-through.)
func driveSharedFrontOfArc(ctx context.Context, t *testing.T, rt *boot.Runtime) (runEntityID string) {
	t.Helper()
	taskID := publishCoordinatorWake(ctx, t)
	requireCoordinatorDecision(ctx, t, taskID, journeyDecideAction)
	runEntityID = requireRunAnchor(ctx, t, taskID)
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	approveChange(ctx, t, rt, runEntityID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	requireTaskSpecProjected(ctx, t, runEntityID)
	requireSandboxReady(ctx, t, runEntityID)
	return runEntityID
}

// startJourneyRuntime boots the REAL shared runtime against the given mock LLM and blocks
// until the agentic plane + deterministic stations report healthy. It registers teardown via
// t.Cleanup (mock Stop + runtime Stop) so the bridge proof and its negative-path siblings
// share ONE boot path. Callers create the ctx (with a wide timeout — the provision station
// builds the fixture image and cold-proves it on real docker, and measure runs `go test` in
// the warm container) and drive the arc after this returns.
func startJourneyRuntime(ctx context.Context, t *testing.T, mock *mockllm.Harness) *boot.Runtime {
	t.Helper()
	return startJourneyRuntimeExperiment(ctx, t, mock, "")
}

// startJourneyRuntimeExperiment is startJourneyRuntime with an optional
// semsource-condition injection (integrate-semsource-ab-harness): a non-empty
// semsourceEndpoint patches {"experiment": {"condition": "semsource", ...}}
// into the journey's temp bootstrap, so boot swaps the -semsource variant
// dispatch pack in and constructs the live proxy client — the exact operator
// path, not a test-only wiring.
func startJourneyRuntimeExperiment(ctx context.Context, t *testing.T, mock *mockllm.Harness, semsourceEndpoint string) *boot.Runtime {
	t.Helper()

	// Wipe NATS so THIS journey boots against a clean durable state (see resetNATS). Multiple
	// full-arc journeys in one `task e2e` run each need their own fresh NATS — a prior journey's
	// runs/change-facts/streams persist in the shared JetStream and the next boot's bootstrap
	// replay re-fires rules on those STALE entities (a leftover run gets re-validated while this
	// test's run does not). This runs BEFORE mock.Start/boot, when nothing of this test is
	// connected and the prior test's runtime has already been torn down by its t.Cleanup.
	resetNATS(ctx, t)

	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	t.Cleanup(func() { _ = mock.Stop() })

	configPath := journeyConfigPath(t, mock.Endpoint())
	if semsourceEndpoint != "" {
		configPath = injectSemsourceExperiment(t, configPath, semsourceEndpoint)
	}
	// EVERY journey delivers against the REAL adapter path (forge-io-real-lanes:
	// the local-delivery stub is deleted): a protocol-faithful local forge double
	// for the API shapes + a local BARE repository for a REAL git push. Journeys
	// that never reach delivery simply never touch either.
	configPath = injectJourneyForge(t, configPath)
	rt, err := boot.NewRuntime(ctx, boot.RunOptions{
		ConfigPath: configPath,
		// The patched config lives in a temp dir, so point persona seeding at the repo's real
		// fragment tree — else the coordinator routes on the framework default persona instead
		// of Sarah's decision contract.
		PersonasDir: journeyPersonasDir(t),
		// The run's target SOURCE at M0 is the committed Go fixture — the provision station
		// materializes the run's checkout from it and cold-proves the declared image (the same
		// value boot threads into RegisterAll so the provision-station factory captures it).
		SandboxSourceDir: journeySandboxSourceDir(t),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		// Teardown is best-effort — graceful shutdown is subject to the known semstreams
		// ComponentManager deadlock (C360Studio/semstreams#508); Stop bounds it so this
		// returns rather than hanging.
		if stopErr := rt.Stop(5 * time.Second); stopErr != nil {
			t.Logf("runtime Stop (best-effort teardown): %v", stopErr)
		}
	})

	requireAgenticHealthy(ctx, t, rt)

	// Returned so the stand-ins (approveChange, the semsource condition stamp) draw
	// their writers from THIS runtime's bound owners instead of binding their own —
	// see boundWriter.
	return rt
}

// journeyForgeDouble / journeyForgeBare expose the CURRENT test's forge double
// and bare push target for delivery-shape assertions (tests run serially; set
// by injectJourneyForge per boot).
var (
	journeyForgeDouble *forgetest.Double
	journeyForgeBare   string
)

// journeyForgeOwner/Repo name the journeys' delivery target (the double's
// namespace + the bare repo's logical identity).
const (
	journeyForgeOwner = "c360studio"
	journeyForgeRepo  = "semdev-journey"
)

// injectJourneyForge stands up the per-test delivery target — a forge double
// (API shapes) + a bare git repository (a REAL push target) — and patches the
// delivery-station's forge config and conversation-channel's outbound posting
// config in the journey bootstrap. The token env is set per-test: the clients
// require a non-empty token; the double ignores it.
func injectJourneyForge(t *testing.T, configPath string) string {
	t.Helper()

	bare := filepath.Join(t.TempDir(), "journey-forge-bare.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare journey remote: %v\n%s", err, out)
	}
	double := forgetest.Start()
	t.Cleanup(double.Close)
	journeyForgeDouble = double
	journeyForgeBare = bare
	t.Setenv("SEMDEV_JOURNEY_FORGE_TOKEN", "journey-forge-token")

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read journey config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode journey config: %v", err)
	}
	deliveryCfg := mustMap(t, mustMap(t, mustMap(t, cfg, "components"), "delivery-station"), "config")
	deliveryCfg["forge"] = map[string]any{
		"owner":       journeyForgeOwner,
		"repo":        journeyForgeRepo,
		"remote_url":  "file://" + bare,
		"base_branch": "main",
		"api_base":    double.URL(),
		"token_env":   "SEMDEV_JOURNEY_FORGE_TOKEN",
	}
	conversationCfg := mustMap(t, mustMap(t, mustMap(t, cfg, "components"), "conversation-channel"), "config")
	conversationCfg["api_base"] = double.URL()
	conversationCfg["token_env"] = "SEMDEV_JOURNEY_FORGE_TOKEN"
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap-forge.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// resetNATS wipes and restarts the JetStream NATS so the calling journey boots against clean
// durable state — the same isolation `task nats:reset` gives, applied PER TEST. Multiple
// full-arc journeys in one `task e2e` run each need their own durable NATS: a prior journey's
// runs, change facts, streams, and rule state persist in the shared JetStream, and the next
// boot's bootstrap replay re-fires rules on those stale entities (observed: a leftover run gets
// re-validated while the current test's run is never advanced). A docker volume wipe (down -v +
// up --wait) is the framework's real isolation boundary and guarantees completeness — no bucket
// or stream residue a hand-rolled programmatic purge might miss. Fails the test loud on error.
func resetNATS(ctx context.Context, t *testing.T) {
	t.Helper()
	compose := filepath.Join(repoRoot(t), "docker", "compose", "nats.yml")
	for _, args := range [][]string{
		{"compose", "-f", compose, "down", "-v"},
		{"compose", "-f", compose, "up", "-d", "--wait"},
	} {
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			t.Fatalf("reset NATS (docker %s): %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// requireAttemptCount polls up to timeout until the run carries exactly want task.attempt.instance
// triples — the per-task attempt counter each developer dispatch appends at spawn
// (dispatch-developer 04 = #1; the retry routes 06c/07b = +1 each). Exactly want proves the
// retry machinery dispatched the expected number of attempts. The objects are distinct loop
// instances, so storage does not dedupe them. Callers set timeout to cover whatever dev-loop
// work must complete first (a COLD in-container compile wants minutes, not seconds). An
// over-count fails FAST (it can only mean an errant extra spawn — waiting cannot fix it).
func requireAttemptCount(ctx context.Context, t *testing.T, runEntityID string, want int, timeout time.Duration) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const pred = "task.attempt.instance"
	deadline := time.Now().Add(timeout)
	for {
		count := 0
		if e, ok := scanEntities(ctx, client)[runEntityID]; ok {
			for _, tr := range e.Triples {
				if tr.Predicate == pred {
					count++
				}
			}
		}
		if count == want {
			return
		}
		if count > want {
			t.Fatalf("run %s has %d %s triples, want exactly %d — MORE than expected means the retry route "+
				"misfired or an extra developer loop spawned (each dispatch appends one at spawn)", runEntityID, count, pred, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s has %d %s triples, want exactly %d within %s — dispatch-developer (04) appends #1 "+
				"and each retry re-dispatch (06c) appends +1; too few means the retry route never fired (attempt "+
				"1's red measure did not drive a re-dispatch)", runEntityID, count, pred, want, timeout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// injectSemsourceExperiment patches the semsource condition into a journey's
// temp bootstrap config — the operator's exact config surface, so the boot
// path under test is the real one (variant-pack swap + live proxy client).
func injectSemsourceExperiment(t *testing.T, configPath, endpoint string) string {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read journey config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode journey config: %v", err)
	}
	cfg["experiment"] = map[string]any{"condition": experiment.ConditionSemsource, "semsource_endpoint": endpoint}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap-semsource.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// TestBridgeProofSemsourceConditionPlumbing drives the semsource-CONDITION
// plumbing end to end (integrate-semsource-ab-harness, task 5.3) — labeled
// BRIDGE PROOF: retrieval VALUE is unmeasurable against a mock LLM (the mock
// never reads the tool result); what this proves is the CONDITION MACHINERY:
// the per-signal readiness probe passes, boot swaps the -semsource variant
// dispatch pack in and constructs the live proxy client, the run is stamped
// with the condition label, the developer loop's advertised allowlist is the
// baseline set + the four proxies (post-#551 an UNADVERTISED code_search call
// would be rejected and break the arc — so delivery proves advertisement),
// and a proxy call ROUND-TRIPS against the real semsource inside the loop.
//
// GATED on a locally running semsource compose: set
// SEMDEV_SEMSOURCE_E2E_ENDPOINT (e.g. http://localhost:8080 — see the
// operator runbook, docs/semsource-ab-runbook.md); skipped otherwise. A
// DECLARED endpoint that fails the probe FAILS the test (D4: never run a
// degraded arm), it does not skip.
func TestBridgeProofSemsourceConditionPlumbing(t *testing.T) {
	endpoint := os.Getenv("SEMDEV_SEMSOURCE_E2E_ENDPOINT")
	if endpoint == "" {
		t.Skip("SEMDEV_SEMSOURCE_E2E_ENDPOINT unset — the semsource-condition plumbing journey needs a local semsource compose (docs/semsource-ab-runbook.md)")
	}

	// D4 launch order: the PER-SIGNAL readiness probe comes FIRST.
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProbe()
	if err := experiment.CheckReadiness(probeCtx, semsource.NewClient(endpoint)); err != nil {
		t.Fatalf("the declared semsource endpoint failed the per-signal readiness probe (D4 — wait for index.ready AND embedding.ready, never run a degraded arm): %v", err)
	}

	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		// The developer's FIRST turn calls code_search — a REAL proxy
		// round-trip through the advertised variant allowlist.
		mockllm.Fixture{Marker: journeyDispatchTaskMarker, Tool: &mockllm.ToolCall{Name: "code_search", Args: map[string]any{"query": "health status check"}}},
		mockllm.Fixture{Marker: journeyDispatchTaskMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}}},
		mockllm.Fixture{Marker: journeyDispatchTaskMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	rt := startJourneyRuntimeExperiment(ctx, t, mock, endpoint)

	runEntityID := driveSharedFrontOfArc(ctx, t, rt)

	// The launch path's post-bind step: stamp the condition on the minted run
	// (writer experiment-intake — the probe already passed above, D4 order).
	func() {
		client := connectFrontDoor(ctx, t)
		defer func() { _ = client.Close(context.Background()) }()
		if err := experiment.StampCondition(ctx, boundWriter(t, rt, experiment.Source), runEntityID, experiment.ConditionSemsource); err != nil {
			t.Fatalf("stamp the semsource condition: %v", err)
		}
	}()
	t.Logf("semsource station: probe passed per-signal, condition stamped on run %s", runEntityID)

	// The arc must DELIVER through the variant pack: the dev loop's code_search
	// turn only admits if the four proxies are advertised (#551), and only
	// succeeds if the live client round-trips against the real semsource.
	requireReviewApproved(ctx, t, runEntityID)
	requireVerifyPassed(ctx, t, runEntityID)
	requirePRDelivered(ctx, t, runEntityID)

	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	entities := scanEntities(ctx, client)
	e, ok := entities[runEntityID]
	if !ok {
		t.Fatalf("run %s not found for the condition assert", runEntityID)
	}
	if got := tripleString(e, experiment.ConditionPredicate); got != experiment.ConditionSemsource {
		t.Fatalf("%s = %q, want %q — the delivered run must carry its condition label (the ledger reads it)", experiment.ConditionPredicate, got, experiment.ConditionSemsource)
	}

	// POSITIVE round-trip proof: delivery alone is NOT it — a rejected
	// (unadvertised, #551) or upstream-failed code_search returns an errResult
	// the loop absorbs and the scripted arc still delivers. The trajectory
	// step entity is the harness-stamped truth: a code_search tool_call step
	// with tool-status=success proves the call was ADMITTED through the
	// variant allowlist AND round-tripped against the real semsource.
	found := false
	for _, ent := range entities {
		if tripleString(ent, agvocab.StepToolName) != "code_search" {
			continue
		}
		found = true
		if status := tripleString(ent, agvocab.StepToolStatus); status != "success" {
			t.Fatalf("the in-loop code_search step has tool-status=%q (error: %q) — the proxy call was admitted but did NOT round-trip cleanly against semsource", status, tripleString(ent, agvocab.StepErrorMessage))
		}
	}
	if !found {
		t.Fatal("no code_search tool_call trajectory step found — the scripted proxy call never executed (rejected pre-admission, or the fixture cursor desynced)")
	}
	t.Logf("semsource station: arc DELIVERED under the semsource condition — variant pack loaded, code_search step tool-status=success against the live semsource, condition label on the run")
}

// requireRunTripleCount polls until the run carries exactly want triples of predicate. An
// over-count fails FAST — an errant extra append cannot be waited away — and a transient
// scan miss (scanEntities swallows read faults into an empty map) is retried, not failed.
func requireRunTripleCount(ctx context.Context, t *testing.T, runEntityID, pred string, want int, timeout time.Duration) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	deadline := time.Now().Add(timeout)
	for {
		count := 0
		if e, ok := scanEntities(ctx, client)[runEntityID]; ok {
			for _, tr := range e.Triples {
				if tr.Predicate == pred {
					count++
				}
			}
		}
		if count == want {
			return
		}
		if count > want {
			t.Fatalf("run %s has %d %s triples, want exactly %d — an extra append means a route misfired", runEntityID, count, pred, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s has %d %s triples, want exactly %d within %s", runEntityID, count, pred, want, timeout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// journeyFrontOfArcFixtures returns the mock turns every journey drives IDENTICALLY: the
// shared front of the issue→PR arc — front-door intake (C1) → route to author (C2) → emit the
// change (A1) → post-approval dev kickoff (C3). validate + projection + provision are
// publish-triggered COMPONENTS (R6), so they consume ZERO model turns and need no fixtures.
// The negative-path journeys (retry/rejection/exhaustion) reuse this prefix and append only
// their divergent developer/review tail. (The bridge proof keeps its own annotated inline copy
// as the reference walk-through.) budget is the per-task attempt budget the change authors — the
// projection clamps it to [1,5] and the routes enforce it via route.task.budget (the exhaustion
// journeys author a small explicit budget to prove the boundary is per-task, not the old constant 3).
func journeyFrontOfArcFixtures(budget int) []mockllm.Fixture {
	return []mockllm.Fixture{
		{Marker: journeyIssueRef, Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
			"action": journeyDecideAction, "reason": "new admitted issue " + journeyIssueRef + " needs a run"}}},
		{Marker: journeyIssueRef, Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
			"action": "create_change", "reason": "author the change for " + journeyIssueRef}}},
		{Marker: journeyIssueRef, Tool: &mockllm.ToolCall{Name: "create_change", Args: journeyChangeArgs(budget)}},
		{Marker: journeyDevRewakeMarker, Tool: &mockllm.ToolCall{Name: "decide", Args: map[string]any{
			"action": journeyDevAction, "reason": "the change is approved and the run resumed; develop the run's tasks"}}},
	}
}

// publishCoordinatorWake builds the front-door coordinator wake exactly as the
// intake adapter will (intake.CoordinatorTask), then publishes it to the front
// door (intake.FrontDoorSubject on the AGENT JetStream) BaseMessage-wrapped via
// PublishToStream — the real framework contract. A bare marshal or a core publish
// is silently dropped by the loop consumer, so this mirrors it precisely. Returns
// the wake's task id so the caller can bind its assertion to this run's loop
// (the framework stamps it on the loop entity as agent.loop.task).
func publishCoordinatorWake(ctx context.Context, t *testing.T) string {
	t.Helper()

	task, err := intake.CoordinatorTask(intake.Intake{Relevant: true, IssueRef: journeyIssueRef}, "mock")
	if err != nil {
		t.Fatalf("build coordinator wake: %v", err)
	}

	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	base := message.NewBaseMessage(task.Schema(), task, "journey-frontdoor")
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal coordinator wake envelope: %v", err)
	}
	if err := client.PublishToStream(ctx, intake.FrontDoorSubject, data); err != nil {
		t.Fatalf("publish coordinator wake: %v", err)
	}
	return task.TaskID
}

// approveChange stands in for the REAL approval adapter (which SHIPPED with
// forge-io-real-lanes: internal/intake/approval.go, proven end-to-end by the
// webhook journey's comment-event station): it stamps run.change.decision="approve"
// on the run entity through the SAME contract-bound mutation client the adapter
// uses (owner approval-adapter, migrate-beta159) — impersonating its exact triple.
// It remains legitimate for journeys that do not drive the webhook lane (no
// issue ref to bind a comment to); the webhook journey is the adapter's proof.
func approveChange(ctx context.Context, t *testing.T, rt *boot.Runtime, runEntityID string) {
	t.Helper()
	writer := boundWriter(t, rt, "approval-adapter")
	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  admission.DecisionPredicate,
		Object:     admission.DecisionApprove,
		Source:     "approval-adapter",
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := writer.Replace(ctx, runEntityID, []message.Triple{tr}); err != nil {
		t.Fatalf("stamp %s on %s: %v", admission.DecisionPredicate, runEntityID, err)
	}
}

// boundWriter returns owner's write surface FROM THE RUNNING RUNTIME's clients,
// so the stand-in's write resolves through the SAME contract table production
// uses (D3b) — exactly what the real approval adapter does, so the stand-in is
// MORE faithful, not less.
func boundWriter(t *testing.T, rt *boot.Runtime, owner string) *graphown.Writer {
	t.Helper()
	w := rt.GraphWriters().Writer(owner)
	if w == nil {
		t.Fatalf("runtime resolves no contract-validated writer for owner %q", owner)
	}
	return w
}

// requireCoordinatorDecision polls the ENTITY_STATES fact-store until THIS run's
// coordinator loop carries coordinator.decision.next_action == wantAction, or
// fails naming the likely cause. The loop id is minted by the framework, so the
// journey does not know the entity id up front — it scans (the deep-research
// scenario's pattern) and binds by the loop's spawn-stamped agent.loop.task
// (== the wake's task id), so a decision left by a prior run cannot false-green.
func requireCoordinatorDecision(ctx context.Context, t *testing.T, wantTaskID, wantAction string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 45*time.Second, func() bool {
		for _, e := range scanEntities(ctx, client) {
			if tripleString(e, agvocab.LoopRole) == "coordinator" &&
				tripleString(e, agvocab.LoopTask) == wantTaskID &&
				tripleString(e, agvocab.CoordinatorNextAction) == wantAction {
				return true
			}
		}
		return false
	}, "coordinator loop (task "+wantTaskID+") never stamped decision.next_action="+wantAction+
		" — check the front-door tool config (nil tools / tool_choice=required / allowlist), "+
		"persona seeding, the decide registration, or the mock decide fixture marker")
}

// requireRunCoordinatorDecision polls until SOME coordinator loop BOUND TO
// runEntityID (agent.run.entity-id == runEntityID) carries
// coordinator.decision.next_action == wantAction. Unlike requireCoordinatorDecision
// (which binds by the front-door wake's task id), the dev re-wake coordinator is
// spawned by a rule with no journey-known task id, so it is bound by the run anchor
// instead — plus the distinct action, so an earlier decision (issue_intake /
// create_change) on the same run cannot false-green.
func requireRunCoordinatorDecision(ctx context.Context, t *testing.T, runEntityID, wantAction string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 45*time.Second, func() bool {
		for _, e := range scanEntities(ctx, client) {
			if tripleString(e, agvocab.LoopRole) == "coordinator" &&
				tripleString(e, agvocab.LoopRunEntityID) == runEntityID &&
				tripleString(e, agvocab.CoordinatorNextAction) == wantAction {
				return true
			}
		}
		return false
	}, "no coordinator loop bound to run "+runEntityID+" ever stamped decision.next_action="+wantAction+
		" — the dev re-wake did not fire; check dev-from-task/01 (agent.run stamped on the run so inherit can bind), "+
		"dev-from-task/02 (run_scope=inherit publish), and the mock decide fixture marker \""+journeyDevRewakeMarker+"\"")
}

// requireDeveloperLoopCompleted polls until SOME developer loop bound to runEntityID
// (agent.loop.role == "developer" ∧ agent.run.entity-id == runEntityID) reaches
// agent.loop.outcome == "success" — the framework's atomic loop-terminal stamp
// (WriteLoopCompletion). Its presence is the proof dispatch-developer fired, Amelia
// inherited the run, and her forced apply_patch ran and applied cleanly (a failed
// apply would not reach success). This is also the exact fact the in-loop measure
// chain keys on. Bound by the run anchor + the developer role so no coordinator loop
// on the same run can false-match.
func requireDeveloperLoopCompleted(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	// The loop-success outcome value (agentic.OutcomeSuccess).
	const loopSuccess = "success"
	requireEventually(t, 45*time.Second, func() bool {
		for _, e := range scanEntities(ctx, client) {
			if tripleString(e, agvocab.LoopRole) == "developer" &&
				tripleString(e, agvocab.LoopRunEntityID) == runEntityID &&
				tripleString(e, agvocab.LoopOutcome) == loopSuccess {
				return true
			}
		}
		return false
	}, "no developer loop bound to run "+runEntityID+" reached agent.loop.outcome=success — dispatch-developer "+
		"(dev-from-task/04) did not fire or apply_patch failed: check the rule fires on the dev_from_task decision, "+
		"run_scope=inherit binds Amelia, apply_patch is advertised/scripted, and the fixture fix diff applies cleanly")
}

// requireMeasurementPassed polls the run entity until measurement.result.passed ==
// "true" — the proof the measure-trigger rule (dev-from-task/05) fired on Amelia's
// successful terminal, the forced measure_task loop resolved the run's WARM sandbox,
// Exec'd the frozen `go test ./...` IN the container (over the patched checkout), and
// the real exit code was 0. This is the make-or-break: a passing IN-CONTAINER measure
// of the fixed artifact, the class both predecessors faked. A stamped passed="false"
// is a hard failure (the fix did not take, or measure ran over unpatched bytes /
// against a wrong sandbox), surfaced immediately rather than by timeout. The window is
// wide: the warm container's first `go test` compiles the package in a fresh cache.
func requireMeasurementPassed(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const passed = "measurement.result.passed"
	requireEventually(t, 3*time.Minute, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, passed); got == "false" {
			t.Fatalf("measure_task stamped a FAILING in-container measurement (%s=false, exit_code=%s) — the fix did not make the test pass in the warm sandbox, or measure ran over unpatched bytes / a wrong sandbox: check apply_patch wrote the SAME checkout the warm container bind-mounts, and that task.spec.test-command matches the fixture (`go test ./...`)",
				passed, tripleString(e, "measurement.result.exit-code"))
		}
		return tripleString(e, passed) == "true"
	}, "run entity "+runEntityID+" never gained "+passed+"=true — Amelia did not call measure_task in her loop, "+
		"the warm sandbox was not provisioned/resolved, or the in-container `go test` did not run: check her dispatch allowlist "+
		"includes measure_task (tool_choice=auto), that measure_task was scripted as her second turn, and that the provision station left a warm "+
		"container Up over the run's checkout")
}

// requireFloorsPassed polls the run entity until floor.finding.rejected == "false"
// — the proof the floors-trigger (dev-from-task/05) fired on the developer's
// terminal, the floors STATION ran the deterministic floors over the task's target
// files in the checkout, and NO floor rejected the developer's real diff. beta.147
// D4 FLATTENED the per-floor findings (the old per-floor floor.finding.0.presence.passed
// sub-key is GONE — floors.FormatDetail concatenates every floor's line into one
// scalar instead): floor.finding.rejected is now the AGGREGATE across every floor
// (floors.AnyRejected), so rejected=="false" is a STRONGER assertion than the old
// single per-floor presence check — it proves ALL floors passed, presence included,
// not just the one. To keep the explicit presence signal legible (not just folded
// into the aggregate), this ALSO asserts the human-legible floor.finding.detail
// (floors.FormatDetail) carries the presence floor's OWN passed line (the H1 gate) —
// so a partial/absent finding set cannot false-green the aggregate check. A stamped
// rejected="true" is a hard failure (a floor caught fabrication in the fixed code, or
// the floors ran over the wrong files), surfaced immediately rather than by timeout.
func requireFloorsPassed(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const rejected = "floor.finding.rejected"
	const detail = "floor.finding.detail"
	// The presence floor's line in floors.FormatDetail's concatenated output:
	// "<floors.FloorPresence>: passed[ — <detail>]" (floors.go pass()/FormatDetail).
	presencePassedLine := floors.FloorPresence + ": passed"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, rejected); got == "true" {
			t.Fatalf("the floors station REJECTED the developer's diff (%s=true; %s=%q) — a structural floor caught fabrication in the fixed code, or the floors ran over the wrong target files: check task.spec.target-files includes the test and that the checkout holds the patched bytes",
				rejected, detail, tripleString(e, detail))
		}
		// Require the aggregate AND the presence floor's own passed line in the
		// concatenated detail, so a partial/absent finding set cannot false-green
		// the "not true" check above.
		return tripleString(e, rejected) == "false" && strings.Contains(tripleString(e, detail), presencePassedLine)
	}, "run entity "+runEntityID+" never gained "+rejected+"=false with a full finding set (including the "+
		"\""+presencePassedLine+"\" line in "+detail+") — the floors trigger (dev-from-task/05) did not fire, "+
		"or the floors station could not resolve the attempt: check Amelia's developer loop reached a terminal "+
		"(the floors trigger fires on agent.loop.outcome ne \"\"), and the Attempts seam resolves the target files from the checkout")
}

// requireReviewApproved polls the run entity until review.verdict.value == "approved" — the
// proof the floors route advanced (dev-from-task/06a, on route.attempt.passed=true+route.attempt.rejected=
// false) and spawned Quinn's reviewer loop, which DERIVED an approving verdict from the
// task's passing measurement with no findings. A stamped changes_requested is a hard failure
// (the measurement floor blocked, or a finding was raised), surfaced immediately.
func requireReviewApproved(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const verdict = "review.verdict.value"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, verdict); got == "changes_requested" {
			t.Fatalf("submit_review stamped %s=changes_requested — the measurement floor blocked approval (measurement.result not passing) or Quinn raised a finding: check the floors route advanced on a green measurement and the mock submit_review fixture supplies no findings", verdict)
		}
		return tripleString(e, verdict) == "approved"
	}, "run entity "+runEntityID+" never gained "+verdict+"=approved — the floors advance route (dev-from-task/06a) did not fire, "+
		"or submit_review could not derive the verdict: check check_floors mirrored route.attempt.passed=true+route.attempt.rejected=false onto its loop, the advance route spawns Quinn on that mirror, "+
		"submit_review is advertised/scripted as a role=reviewer loop, and the task's measurement.result is passing")
}

// requireReviewEventuallyApproved polls until review.verdict.value == "approved", TOLERATING a
// transient "changes_requested" along the way and reporting whether one was seen. The rejection
// journey's first review rejects (a finding) before its second approves, so review.verdict.value is
// changes_requested for the duration of the re-entry — unlike requireReviewApproved, which treats
// any changes_requested as a hard failure (correct for the happy/retry paths, wrong here). timeout
// must cover attempt 2's measure + the second review. The transient verdict persists for seconds
// (the whole re-entry cycle), so the 300ms poll observes it reliably; the caller still relies on
// the deterministic turn count for the rejection proof, treating sawRejection as corroboration.
func requireReviewEventuallyApproved(ctx context.Context, t *testing.T, runEntityID string, timeout time.Duration) (sawRejection bool) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const verdict = "review.verdict.value"
	deadline := time.Now().Add(timeout)
	for {
		if e, ok := scanEntities(ctx, client)[runEntityID]; ok {
			switch tripleString(e, verdict) {
			case "changes_requested":
				sawRejection = true
			case "approved":
				return sawRejection
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s never reached %s=approved within %s (sawRejection=%v) — after Quinn's rejection "+
				"re-entered dev (07b), attempt 2 must address the finding, measure green, and Quinn's SECOND review "+
				"must approve; check the second submit_review fixture supplies NO findings and the re-entry loop measured",
				runEntityID, verdict, timeout, sawRejection)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// requireRunParked polls until the run carries run.awaiting.human — the fail-closed terminal for
// an exhausted/blocked run — while asserting it did NOT ship (no delivery.pr.ref) and never reached a false
// green (no verify.cleanroom.result). It fails FAST and loud if a delivery.pr.ref appears (a parked run that somehow
// delivered is a worse bug than a timeout). The final no-verify check catches a run that parked yet
// had already been cold-verified — a run must not both park and carry a green verify.cleanroom.result.
// Returns the park message so callers can assert its content (the reason-aware park, #569).
func requireRunParked(ctx context.Context, t *testing.T, runEntityID string, timeout time.Duration) string {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	park := ""
	requireEventually(t, timeout, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if ref := tripleString(e, "delivery.pr.ref"); ref != "" {
			t.Fatalf("run %s DELIVERED (delivery.pr.ref=%q) but the exhausted-budget journey must PARK, never ship — "+
				"the exhaustion park (dev-from-task/06d floors-escalate or 07c review-park) did not fire, or a delivery route fired on an unapproved run", runEntityID, ref)
		}
		// Capture the message inside the successful poll observation — a post-poll re-scan
		// can transiently miss (scanEntities swallows read faults into an empty map) and
		// would return "" for a park the poll already proved present.
		park = tripleString(e, "run.awaiting.human")
		return park != ""
	}, "run "+runEntityID+" never parked (run.awaiting.human absent) — the exhaustion park must fire when "+
		"route.attempt.instance count reaches the per-task budget B (route.attempt.instance length_gte "+
		"$…route.task.budget.value): 07c review-park when Quinn keeps rejecting at count ≥ B, or 06d floors-escalate "+
		"when a measurement stays RED at count ≥ B (retry 06c/07b stops at count < B)")

	// No false green: an unapproved, parked run must never carry a passing cold verify.
	if e, ok := scanEntities(ctx, client)[runEntityID]; ok {
		if v := tripleString(e, "verify.cleanroom.result"); v != "" {
			t.Fatalf("parked run %s carries verify.cleanroom.result=%q — a parked (never-approved) run must never reach the cold verify (no false green, SB5)", runEntityID, v)
		}
	}
	return park
}

// requireVerifyPassed polls the run entity until verify.cleanroom.result == "pass" — the proof the
// review approved route (dev-from-task/07a) fired on Quinn's loop and PUBLISHED the
// verify-station COMPONENT (R6, no model turn), which cloned the committed artifact and
// proved it resolves + passes its own tests COLD in a fresh throwaway container, and the
// artifact is self-contained + reproducible. A stamped "fail" is a hard failure (the fix is
// not self-contained, a fabrication survived to verify, or a forbidden build-file pattern),
// surfaced immediately rather than by timeout. The clean-room cold proof runs REAL docker on
// the committed fixture, so this allows a wide window.
func requireVerifyPassed(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const result = "verify.cleanroom.result"
	requireEventually(t, 3*time.Minute, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, result); got == "fail" {
			t.Fatalf("verify-station stamped %s=fail — the committed artifact did NOT prove cold: the fix is not self-contained, a warm-cache-masked fabrication survived to the cold verify, or a build file carries a forbidden runtime download; check the clone carries the patched bytes and the fixture's build files are clean", result)
		}
		return tripleString(e, result) == "pass"
	}, "run entity "+runEntityID+" never gained "+result+"=pass — the review approved route (dev-from-task/07a) did not fire "+
		"(agent.run.entity-id may not have propagated onto Quinn's review loop to thread the run_entity_id property), or the cold clean-room proof did not run: "+
		"check submit_review mirrored route.review.verdict=approved onto its loop, the approved route publishes component.verify-station.dispatch, the verify-station component is healthy, "+
		"and docker is available (the journey builds the fixture image and runs go test in a fresh container)")
}

// requirePRDelivered polls the run entity until delivery.pr.ref is present — the proof the DELIVERY
// ROUTE (dev-from-task/08a) fired ON THE RUN: because verify.cleanroom.result=pass AND review.verdict.value
// =approved AND openspec.change.validated present cohere, it forced open_pr, which recorded the
// delivery reference — the full issue→PR arc's terminal. A stamped run.awaiting.human (the
// blocked delivery park, 08b) alongside no delivery.pr.ref is the blocked path; here we require
// delivery. The rule-native delivery route stamps no pr.coherence decision (check_coherence
// is deleted) — the route conditions ARE the roll-up, so delivery.pr.ref present is the coherent proof.
func requirePRDelivered(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const prRef = "delivery.pr.ref"
	var ref string
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		// The blocked delivery route (08b) parks on a non-pass verify — if the run parked
		// instead of delivering, surface it rather than timing out.
		if park := tripleString(e, "run.awaiting.human"); park != "" && tripleString(e, prRef) == "" {
			t.Fatalf("the run PARKED instead of delivering (run.awaiting.human=%q, no delivery.pr.ref) — the delivery route blocked (08b) or the REAL delivery failed terminally (station-failure-parks: check the forge double/bare-remote config). The happy-path journey expects a delivered PR", park)
		}
		ref = tripleString(e, prRef)
		return ref != ""
	}, "run entity "+runEntityID+" never gained "+prRef+" — the coherent delivery route (dev-from-task/08a) did not fire or the delivery station failed: "+
		"check all three signals are on the run (verify.cleanroom.result=pass, review.verdict.value=approved, openspec.change.validated present) and the journey forge (double + bare remote) is wired")

	// The REAL delivery contract (forge-io-real-lanes): pr.ref is a live PR URL
	// from the forge (the double), never the deleted local stub.
	if strings.HasPrefix(ref, "local-delivery:") {
		t.Fatalf("delivery.pr.ref = %q — the local stub path is DELETED; a stand-in reference cannot claim delivery", ref)
	}
	if !strings.Contains(ref, "/pull/") {
		t.Fatalf("delivery.pr.ref = %q, want a forge PR URL (…/pull/<n>)", ref)
	}
	// The double holds exactly ONE PR whose body carries the evidence summary,
	// created AFTER a query-by-head (the idempotency ordering), and the bare
	// remote REALLY carries the pushed semdev/<suffix> branch.
	if journeyForgeDouble != nil {
		prs := journeyForgeDouble.PRs()
		if len(prs) != 1 {
			t.Fatalf("forge double holds %d PRs, want exactly 1", len(prs))
		}
		if !strings.Contains(prs[0].Body, "delivery evidence") || !strings.Contains(prs[0].Body, runEntityID) {
			t.Errorf("PR body must carry the evidence summary + run pointer, got %q", prs[0].Body)
		}
		kinds := []string{}
		for _, r := range journeyForgeDouble.Requests() {
			kinds = append(kinds, r.Kind)
		}
		if got := strings.Join(kinds, ","); !strings.Contains(got, "find_pr,create_pr") {
			t.Errorf("forge request order = %q, want query-by-head BEFORE create (the forge-level guard)", got)
		}
		out, err := exec.CommandContext(ctx, "git", "--git-dir", journeyForgeBare, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads/semdev/").CombinedOutput()
		if err != nil {
			t.Fatalf("list bare-remote branches: %v\n%s", err, out)
		}
		refLine := strings.TrimSpace(string(out))
		if refLine == "" {
			t.Fatalf("bare remote has no semdev/<run-suffix> branch — the REAL git push did not happen")
		}
		// The delivered tip IS the verified snapshot: the branch head must be
		// exactly the run's attempt.commit.sha (G4/G7 — the bytes measured and
		// clean-room-verified are the bytes delivered).
		if e, ok := scanEntities(ctx, client)[runEntityID]; ok {
			if sha := tripleString(e, "attempt.commit.sha"); sha != "" && !strings.Contains(refLine, sha) {
				t.Errorf("bare-remote branch tip %q != the run's attempt.commit.sha %q — delivery must push the VERIFIED commit, never bare HEAD", refLine, sha)
			}
		}
	}
}

// requireTriplePresent polls until the run entity carries at least one triple for
// predicate (any object). Used for appended multi-value predicates like
// task.attempt.<i>, where the object (a loop instance) is not known up front.
func requireTriplePresent(ctx context.Context, t *testing.T, runEntityID, predicate, hint string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 30*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		for _, tr := range e.Triples {
			if tr.Predicate == predicate {
				return true
			}
		}
		return false
	}, "run entity "+runEntityID+" never gained a "+predicate+" triple — "+hint)
}

// requireRunAnchor polls until THIS wake's coordinator loop carries an
// agent.run.entity-id triple — the run anchor stamped by publish_agent
// run_scope=new — and returns that run entity id. Its presence is the proof the
// issue_intake spawn rule fired and minted a run rooted at the coordinator loop.
func requireRunAnchor(ctx context.Context, t *testing.T, wantTaskID string) string {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	var runEntityID string
	requireEventually(t, 45*time.Second, func() bool {
		entities := scanEntities(ctx, client)
		for _, e := range entities {
			if tripleString(e, agvocab.LoopTask) != wantTaskID {
				continue
			}
			if id := tripleString(e, agvocab.LoopRunEntityID); id != "" {
				runEntityID = id
				return true
			}
		}
		return false
	}, "coordinator loop (task "+wantTaskID+") never gained an agent.run.entity-id anchor "+
		"— the issue_intake spawn rule (run_scope=new) did not fire; check the rule conditions "+
		"(coordinator role / next_action=issue_intake / no prior run anchor) and that the lifecycle manager is wired")
	return runEntityID
}

// requireRunPhase polls the run entity until agent.run.phase == wantPhase, or
// fails. Reaching `executing` proves the agent-run bridge (handoff-marker →
// dispatched-to-executing) advanced the freshly-minted run.
func requireRunPhase(ctx context.Context, t *testing.T, runEntityID, wantPhase string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 45*time.Second, func() bool {
		entities := scanEntities(ctx, client)
		e, ok := entities[runEntityID]
		if !ok {
			return false
		}
		return tripleString(e, agentrun.PhasePredicate) == wantPhase
	}, "run entity "+runEntityID+" never reached agent.run.phase="+wantPhase+
		" — no rule advanced it there (for executing: the agent-run handoff→dispatched→executing bridge; "+
		"for awaiting_approval: validate stamping openspec.change.validated → the change-approval gate)")
}

// requireChangeAuthored polls the run entity until it carries the whole authored
// change DOCUMENT (beta.147 D3: openspec.change.document, one JSON scalar — the
// old openspec.change.<slug>.* triple tree is gone) and DECODES it, asserting the
// decoded document's slug and proposal intent match what the mock's create_change
// call authored. Decoding — not mere presence — proves the change was authored
// WITH the expected content (the authoring loop's create_change call really did
// stamp THIS journey's change on the run via the inherited agent.run.entity-id),
// not just that some string landed under the predicate. Fails naming the likely
// cause.
func requireChangeAuthored(ctx context.Context, t *testing.T, runEntityID, slug string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	var doc changefacts.ChangeDocument
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		raw := tripleString(e, changefacts.DocumentPredicate)
		if raw == "" {
			return false
		}
		d, err := changefacts.UnmarshalDocument(raw)
		if err != nil {
			t.Fatalf("run %s carries an UNDECODABLE %s: %v — create_change stamped malformed JSON",
				runEntityID, changefacts.DocumentPredicate, err)
		}
		doc = d
		return true
	}, "run entity "+runEntityID+" never gained "+changefacts.DocumentPredicate+" — the create_change "+
		"spawn rule did not fire, the author did not inherit the run anchor (agent.run.entity-id), or the "+
		"create_change tool call was not scripted/advertised")

	// Presence alone only proves SOME document landed; decode the content to prove
	// it is THIS journey's change (the create_change args round-tripped), not an
	// artifact of a stale or wrong write.
	if doc.Change == nil {
		t.Fatalf("run %s decoded %s but Change is nil — create_change stamped an empty document",
			runEntityID, changefacts.DocumentPredicate)
	}
	gotIntent := ""
	if doc.Change.Proposal != nil {
		gotIntent = doc.Change.Proposal.Intent
	}
	if doc.Change.Slug != slug || gotIntent != journeyChangeIntent {
		t.Fatalf("run %s authored change slug=%q intent=%q, want slug=%q intent=%q — the create_change "+
			"fixture args did not round-trip through the document blob",
			runEntityID, doc.Change.Slug, gotIntent, slug, journeyChangeIntent)
	}
}

// requireTaskSpecProjected polls the run entity until task.spec.test-command is
// present and non-empty — the proof the approval-triggered projection rule
// (dev-from-task/03) spawned a project_tasks loop that froze the change's tasks
// into the immutable task.spec on the run. It is also the exact fact the dev
// re-wake (dev-from-task/02) gates on, so asserting it here proves the
// projection-before-kickoff ordering. Fails naming the likely cause.
func requireTaskSpecProjected(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const projected = "task.spec.test-command"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		return tripleString(e, projected) != ""
	}, "run entity "+runEntityID+" never gained "+projected+" — the projection rule (dev-from-task/03) "+
		"did not fire or project_tasks refused: check the run-level openspec.change.slug pointer (threaded into the "+
		"projection prompt), the D15 #0 revision bind (openspec.change.validated == openspec.change.revision, both flat run-level facts post-D3), and "+
		"that project_tasks was advertised/scripted")
}

// requireSandboxReady polls the run entity until sandbox.provision.ready == "true" and the
// image attestation is present — the proof the provision rule (sandbox/01) PUBLISHED
// the provision-station COMPONENT (R6, no model turn), which BUILT the declared image
// and proved the repo builds cold, stamping the harness-derived readiness/attestation.
// It also gates the dev re-wake (dev-from-task/02 requires sandbox.provision.ready eq true), so
// asserting it here proves the provision-before-kickoff ordering. The timeout is
// wide: this station runs the real docker build + cold go build. Fails naming the
// likely cause — including a sandbox.provision.blocked reason if provisioning parked instead.
func requireSandboxReady(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 4*time.Minute, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if blocked := tripleString(e, "sandbox.provision.blocked"); blocked != "" {
			t.Fatalf("the provision station blocked the run instead of proving it ready: %s", blocked)
		}
		return tripleString(e, "sandbox.provision.ready") == "true" && tripleString(e, "sandbox.attestation.image") != ""
	}, "run entity "+runEntityID+" never gained sandbox.provision.ready=true + attestation — the provision rule (sandbox/01) "+
		"did not publish or the provision-station component could not prove the fixture cold: check the sandbox.provision.marker guard, "+
		"that the provision-station component is healthy, that SandboxSourceDir points at the fixture, and that docker can "+
		"build the declared golang image")
}

// journeySandboxSourceDir is the committed Go fixture the run develops at M0 —
// the provision station materializes each run's checkout from it and cold-proves the
// declared image. Passed explicitly (like PersonasDir) because the journey's
// patched config lives in a temp dir.
func journeySandboxSourceDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "test", "fixtures", "go-health-class")
}

// journeyChangeArgs is a minimal VALID create_change payload the mock's authoring
// turn emits: a slug, a proposal with intent, one spec delta with an ADDED
// RFC-2119 requirement carrying a Given/When/Then scenario, and one task section.
// Mirrors the create_change tool's own test fixture so it passes the schema.
//
// The task item carries the full Karpathy schema (target_files / test_command /
// assumptions / non_goals / budget) — "mock the intelligence, not the plumbing":
// the model supplies these rich fields as create_change args, create_change stamps
// them, and the (real) project_tasks tool freezes them into task.spec at the
// projection station. A thin task (no rich fields) would make devtask.Project fail
// toward the human, so projection would refuse and never stamp task.spec.
func journeyChangeArgs(budget int) map[string]any {
	return map[string]any{
		"slug":     journeyChangeSlug,
		"proposal": map[string]any{"intent": journeyChangeIntent, "scope_in": []any{"the spine"}},
		"deltas": []any{map[string]any{
			"capability": "spine",
			"added": []any{map[string]any{
				"name":      "Spine authors a change",
				"statement": "The system SHALL author an OpenSpec change from an intaken issue.",
				"scenarios": []any{map[string]any{
					"name": "Issue intaken",
					"steps": []any{
						map[string]any{"kw": "WHEN", "text": "an admitted issue is routed to create_change"},
						map[string]any{"kw": "THEN", "text": "openspec.change facts land on the run"},
					},
				}},
			}},
		}},
		// The task's target_files + test_command must match the run's SANDBOX artifact
		// (the go-health-class fixture, module example.com/health): measure_task (station
		// 11) runs this frozen test_command IN the warm container, and check_floors
		// (station 12) runs the structural floors over these target files' CURRENT
		// checkout contents. The developer's apply_patch diff (journeyFixtureFixDiff)
		// fixes health.go; `go test ./...` at the fixture root goes green once the boundary
		// bug is fixed. BOTH health.go and its test are declared targets: the floors read
		// the declared targets, and the tests-must-exist floor would (correctly) reject a
		// production-only attempt — the fixture's real health_test.go is what makes the
		// floors pass, so it must be in the task's evaluation scope.
		"tasks": []any{map[string]any{
			"section": "1. Health boundary",
			"items": []any{map[string]any{
				"number":       "1.1",
				"text":         "fix the warning-threshold boundary in Classify",
				"target_files": []any{"health.go", "health_test.go"},
				"test_command": "go test ./...",
				"assumptions":  []any{"the health package classifies cpu/mem pressure"},
				"non_goals":    []any{"no production hardening at M0"},
				"budget":       budget,
			}},
		}},
	}
}

// scanEntities reads every entity in ENTITY_STATES into a map keyed by entity id.
// Returns an empty map on any transient read error (bucket not yet created, no
// keys yet) so callers poll rather than fail on a not-yet-populated graph.
func scanEntities(ctx context.Context, client *natsclient.Client) map[string]graph.EntityState {
	out := map[string]graph.EntityState{}
	bucket, err := client.GetKeyValueBucket(ctx, entityStatesBucket)
	if err != nil {
		return out
	}
	kv := client.NewKVStore(bucket)
	keys, err := kv.Keys(ctx)
	if err != nil {
		return out
	}
	for _, key := range keys {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			continue
		}
		var e graph.EntityState
		if err := json.Unmarshal(entry.Value, &e); err != nil {
			continue
		}
		out[e.ID] = e
	}
	return out
}

// tripleString returns the first string object of the entity's triple for
// predicate, or "" if absent / non-string.
func tripleString(e graph.EntityState, predicate string) string {
	for _, tr := range e.Triples {
		if tr.Predicate == predicate {
			if s, ok := tr.Object.(string); ok {
				return s
			}
		}
	}
	return ""
}

// connectFrontDoor opens a NATS client to the local runtime for publishing the
// wake and scanning the fact-store.
func connectFrontDoor(ctx context.Context, t *testing.T) *natsclient.Client {
	t.Helper()
	client, err := natsclient.NewClient(journeyNATSURL())
	if err != nil {
		t.Fatalf("NATS client: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("NATS connect: %v", err)
	}
	if err := client.WaitForConnection(ctx); err != nil {
		t.Fatalf("NATS wait for connection: %v", err)
	}
	return client
}

// journeyNATSURL mirrors boot's existing SEMSTREAMS_NATS_URLS override for
// test-side clients. The first URL is sufficient for these single-node docker
// journeys and lets the documented remap path coexist with another c360 stack.
func journeyNATSURL() string {
	raw := strings.TrimSpace(os.Getenv("SEMSTREAMS_NATS_URLS"))
	if raw == "" {
		return "nats://localhost:24222"
	}
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// requireAgenticHealthy polls the component-manager until the agentic-execution
// plane + graph substrate report healthy, or fails naming what is missing.
func requireAgenticHealthy(ctx context.Context, t *testing.T, rt *boot.Runtime) {
	t.Helper()
	svc, ok := rt.ServiceManager().GetService("component-manager")
	if !ok || svc == nil {
		t.Fatal("component-manager service not present after Start")
	}
	cm, ok := svc.(*service.ComponentManager)
	if !ok {
		t.Fatalf("component-manager service is %T, want *service.ComponentManager", svc)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		healthy := map[string]bool{}
		for _, n := range cm.GetHealthyComponents() {
			healthy[n] = true
		}
		var missing []string
		for _, n := range wantAgenticHealthy {
			if !healthy[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agentic plane not healthy within deadline: missing %v", missing)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled waiting for component health: %v", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// requireEventually polls cond until true or the timeout elapses, failing with msg.
func requireEventually(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// journeyConfigPath writes a copy of the real bootstrap config to a temp file with
// two edits: the mock model endpoint URL points at the in-process mock, and the rule
// pack paths are rewritten to absolute (so the temp-dir config still resolves the
// repo's rules). Returns the temp path. The version is left as-is; each journey resets
// NATS before booting (startJourneyRuntime → resetNATS), so the file always loads fresh
// regardless of the KV version — no per-boot version bump is needed.
func journeyConfigPath(t *testing.T, mockURL string) string {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "configs", "semdev-bootstrap.json"))
	if err != nil {
		t.Fatalf("read bootstrap config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode bootstrap config: %v", err)
	}

	// Point the mock endpoint at the in-process server.
	mustMap(t, mustMap(t, mustMap(t, cfg, "model_registry"), "endpoints"), "mock")["url"] = mockURL

	// Rewrite rules_files to absolute repo paths (the temp config lives elsewhere).
	ruleCfg := mustMap(t, mustMap(t, mustMap(t, cfg, "components"), "rule"), "config")
	files, _ := ruleCfg["rules_files"].([]any)
	abs := make([]any, len(files))
	for i, f := range files {
		abs[i] = filepath.Join(root, "configs", f.(string))
	}
	ruleCfg["rules_files"] = abs

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// journeyPersonasDir is the repo's real persona fragment tree, passed explicitly
// because the journey's patched config lives in a temp dir where the convention
// path (<configDir>/personas/fragments) would not resolve.
func journeyPersonasDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "configs", "personas", "fragments")
}

func mustMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("journey config: %q is not a JSON object", key)
	}
	return v
}

// repoRoot walks up from this test file to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked to filesystem root without finding go.mod from %s", filepath.Dir(file))
		}
		dir = parent
	}
}
