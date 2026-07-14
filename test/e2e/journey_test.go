//go:build e2e

// The mock-LLM BRIDGE PROOF. It boots the REAL shared runtime against the
// in-process mock LLM (zero paid tokens) and drives the whole issue→PR arc
// through it — front door → issue_intake → create_change → validate → human
// approval → project task.spec → provision + prove-cold sandbox → dispatch
// Amelia's BOUNDED MULTI-TURN dev loop (apply_patch → measure in-container →
// stop) → structural floors + route mirror → FLOORS ROUTE advance → review
// (Quinn) → REVIEW ROUTE approved → cold clean-room verify → DELIVERY ROUTE
// coherent → open_pr → pr.ref. Routing is RULE-NATIVE (the reshape deleted the
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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semdev/internal/mockllm"
	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/service"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"
)

// journeyIssueRef is the host-neutral issue ref this journey admits. It is
// distinctive so the mock can key its scripted decide turn on it as a substring
// of the coordinator prompt (intake.CoordinatorTask always embeds the ref).
const journeyIssueRef = "c360studio/semdev-journey#1"

// journeyDecideAction is the action the mock's scripted decide returns — the
// terminal a coordinator picks for a newly admitted issue with no run yet.
const journeyDecideAction = "issue_intake"

// journeyChangeSlug is the slug the mock's scripted create_change authors. The
// change facts land on the run entity under openspec.change.<slug>.*.
const journeyChangeSlug = "journey-spine-change"

// journeyDevAction is the action the re-woken coordinator decides after the human
// approves the change — the entry to the dev-loop rail.
const journeyDevAction = "dev_from_task"

// journeyProvisionMarker is a distinctive, unique substring of the provision
// prompt (sandbox/01-provision.json) the mock's provision turn guards on. That
// prompt is static (the tool reads the run from call.Metadata via run_scope=inherit,
// not the prompt), so this phrase is the stable key for the positional cursor.
const journeyProvisionMarker = "Provision the sandbox"

// journeyDevRewakeMarker is a distinctive substring of the dev re-wake prompt
// (dev-from-task/02-rewake-coordinator-dev.json) that the mock's dev-decide turn
// guards on — the re-wake prompt threads $entity.id (the run entity), not the issue
// ref or slug, so the turn is keyed on this stable phrase instead.
const journeyDevRewakeMarker = "Begin developing"

// journeyDeveloperMarker is a distinctive substring of Amelia's dispatch prompt
// (dev-from-task/04-dispatch-developer.json) the mock's apply_patch turn guards on.
const journeyDeveloperMarker = "SEMDEV DEVELOPER"

// journeyFloorsMarker is a distinctive substring of the floors prompt
// (dev-from-task/05-floors-trigger.json) the mock's check_floors turn guards on. Measure
// moved INTO Amelia's loop (the reshape), so there is no separate measure/gate turn — the
// measure_task turn is guarded by Amelia's own prompt (journeyDeveloperMarker) instead.
const journeyFloorsMarker = "structural floors"

// journeyReviewMarker is a distinctive substring of Quinn's review prompt
// (dev-from-task/06a-route-advance.json) the mock's submit_review turn guards on.
const journeyReviewMarker = "SEMDEV REVIEWER"

// journeyVerifyMarker is a distinctive substring of the verify prompt
// (dev-from-task/07a-review-approved.json) the mock's verify_artifact turn guards on.
const journeyVerifyMarker = "clean-room COLD verify"

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
	"delivery-station", "projection-station", "validation-station",
}

// TestBridgeProofIssueToPRAgainstMock is the mock-LLM bridge proof: publish an
// admitted issue's coordinator wake to the front door and prove the whole
// issue→PR arc CONNECTS through the real runtime — the coordinator routes, the
// run mints, the change authors + validates, the human approves, Amelia's bounded
// multi-turn loop develops → measures in-loop, the floors route advances, Quinn's
// review approves, the cold verify passes, and the delivery route opens the PR
// (pr.ref). Routing is rule-native. A bridge proof of connection, not a claim of
// completeness (G7/G10).
func TestBridgeProofIssueToPRAgainstMock(t *testing.T) {
	// The mock scripts the arc's sequential turns as a POSITIONAL sequence
	// (cursor advances per matched tool call). The reshape makes Amelia's dev loop a
	// BOUNDED MULTI-TURN loop, so the cursor now spans WITHIN her loop:
	//   1. C1 front-door coordinator → decide(issue_intake)     [mints the run]
	//   2. C2 re-woken coordinator   → decide(create_change)    [routes to author]
	//   3. A1 authoring coordinator  → create_change(<change>)  [emits the change]
	//      (validate + projection are COMPONENTS now — their rules publish, no model turns)
	//   4. PS provision coordinator  → provision_sandbox()      [cold-prove → sandbox.ready]
	//   5. C3 dev re-woken coord.    → decide(dev_from_task)    [post-approval kickoff]
	//   6. D-a developer (Amelia)    → apply_patch(fix diff)    [authors the fix, tool_choice=auto]
	//   7. D-b developer (Amelia)    → measure_task(index 0)    [measures IN-LOOP, real go test]
	//   8. D-c developer (Amelia)    → (no tool → completion)   [she is done; the loop ends]
	//   9. F1 floors coordinator     → check_floors(index 0)    [floors + route mirror on its loop]
	//  10. R1 reviewer (Quinn)       → submit_review(index 0)   [per-task floored verdict]
	//  11. V1 verify coordinator     → verify_artifact()        [cold clean-room verify, real]
	//      (delivery is a COMPONENT now — the delivery route publishes it, no model turn)
	// Turns 6-8 are ONE developer loop: apply_patch and measure_task no longer StopLoop, so
	// under tool_choice=auto the loop continues; her apply_patch AND measure_task turns both
	// guard on her own prompt (journeyDeveloperMarker), and turn 8 finds no matching fixture
	// at the cursor (the next is check_floors, keyed on "structural floors" which is not in
	// Amelia's prompt) so — with tool results present — the mock returns a COMPLETION, ending
	// her loop (StatusComplete → outcome=success). The route rules then chain the rest with NO
	// model turns of their own: floors terminal → floors route (06a advance) spawns Quinn →
	// review route (07a approved) spawns verify → delivery route (08a coherent) PUBLISHES the
	// delivery-station component. The validate/projection/delivery deterministic stations are
	// all publish-triggered components (R6), so 10 tool fixtures drive 11 model turns (turn 8
	// consumes no fixture). An unscripted turn returns mockllm.UnmatchedSentinel, failing loudly.
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
			Tool:   &mockllm.ToolCall{Name: "create_change", Args: journeyChangeArgs()},
		},
		// Validation + projection are publish-triggered COMPONENTS now (R6, group 6): the
		// validate rule (coordinator/03) and the projection rule (dev-from-task/03) each fire
		// a plain `publish` to their station component, which validates / freezes task.spec
		// with ZERO model turns — so there are no validate_change / project_tasks fixtures to
		// script. The gate rule (openspec.validated) and the provision gate (task.spec) wait
		// on the components' stamped facts, not on a model turn.
		mockllm.Fixture{
			Marker: journeyProvisionMarker,
			Tool:   &mockllm.ToolCall{Name: "provision_sandbox", Args: map[string]any{}},
		},
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
		// (Amelia's 3rd turn matches no fixture here — the cursor sits on check_floors, whose
		// marker is absent from her prompt — so the mock returns a completion and her loop ends.)
		mockllm.Fixture{
			Marker: journeyFloorsMarker,
			Tool:   &mockllm.ToolCall{Name: "check_floors", Args: map[string]any{"task_index": 0}},
		},
		mockllm.Fixture{
			Marker: journeyReviewMarker,
			Tool:   &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}},
		},
		mockllm.Fixture{
			Marker: journeyVerifyMarker,
			Tool:   &mockllm.ToolCall{Name: "verify_artifact", Args: map[string]any{}},
		},
		// Delivery is a publish-triggered COMPONENT now (R6, group 6): the delivery route
		// (dev-from-task/08a) fires a plain `publish` to the delivery-station component, which
		// records pr.ref with ZERO model turns — so there is no open_pr fixture to script.
	)
	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	defer func() { _ = mock.Stop() }()

	// The provision station builds the fixture's declared golang image and proves it
	// cold (real docker, real go build), and the measure station then runs `go test`
	// in the warm container — so this journey needs a wide budget over the pure-routing
	// stations before it.
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	rt, err := boot.NewRuntime(ctx, boot.RunOptions{
		ConfigPath: journeyConfigPath(t, mock.Endpoint()),
		// The patched config lives in a temp dir, so point persona seeding at the
		// repo's real fragment tree — otherwise the coordinator would route on the
		// framework default persona instead of Sarah's decision contract.
		PersonasDir: journeyPersonasDir(t),
		// The run's target SOURCE at M0 is the committed Go fixture — provision_sandbox
		// materializes the run's checkout from it and cold-proves the declared image.
		SandboxSourceDir: journeySandboxSourceDir(t),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		// Teardown is best-effort — graceful shutdown is subject to the known
		// semstreams ComponentManager deadlock (C360Studio/semstreams#508); Stop
		// bounds it so this returns rather than hanging.
		if stopErr := rt.Stop(5 * time.Second); stopErr != nil {
			t.Logf("runtime Stop (best-effort teardown): %v", stopErr)
		}
	}()

	requireAgenticHealthy(ctx, t, rt)
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
	// fired, the author inherited the run anchor, and the tool stamped
	// openspec.change.<slug>.* on the run (the create_change→validate→approval arc
	// hangs off these facts).
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	t.Logf("station 4: change %q authored onto run %s (mock RequestCount=%d)", journeyChangeSlug, runEntityID, mock.RequestCount())

	// Station 5 — the authored marker published the VALIDATION STATION component (R6),
	// which ran the OpenSpec CLI oracle and stamped openspec.validated on the run, firing
	// the existing change-approval gate (run-lifecycle/01) executing→awaiting_approval.
	// Asserting the phase reached awaiting_approval proves the whole chain WITH ZERO model
	// turns for the validate step: create_change's loop marker → the validate rule's publish
	// → the validation-station component → openspec.validated → the gate. (The gate requires
	// openspec.validated present, so awaiting_approval implies the component stamped it.)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("station 5: validation-station validated the change → run reached awaiting_approval (gate fired, no model turn); mock RequestCount=%d", mock.RequestCount())

	// Station 6 — the human approves the change. The journey stands in for the
	// group-5 approval adapter (a forge-io path, deferred): it stamps
	// run.change_approved=true on the RUN entity exactly as that adapter will
	// (Source approval-adapter, on the run entity per D15). The EXISTING
	// run-lifecycle/02-resume-after-change-approval rule then fires
	// awaiting_approval→executing — the release side of the first human gate.
	// Asserting the run returns to executing proves the resume rule fires live on a
	// real approval fact; the transition is rule-owned (G2), no product Go advances
	// it. (Station 5 asserted awaiting_approval immediately above, so this
	// executing assertion cannot false-match the pre-gate executing state.)
	approveChange(ctx, t, runEntityID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("station 6: human approved → run resumed to executing (mock RequestCount=%d)", mock.RequestCount())

	// Station 7 — approval PROJECTS the immutable task surface BEFORE the dev loop
	// routes into development (dev-from-task spec: WHEN run.change_approved present
	// THEN tasks projected as task.spec; Codex P1 on f1eed5c). Two rules fire on the
	// run entity: dev-from-task/01 stamps the bare agent.run anchor, then
	// dev-from-task/03 fires a `publish` to the PROJECTION STATION component (R6),
	// which reads the change's task facts, binds to the validated content (D15 #0), and
	// stamps task.spec.<i>.* on the run with ZERO model turns. The projection rule threads
	// the slug via the run-level openspec.change.slug pointer create_change stamped, as a
	// publish property. Assert task.spec.0 is present — proof the projection component ran
	// against the real change (exercising the revision guard projecttasks.Project carries)
	// and the dev loop has an immutable spec to converge on before it kicks off.
	requireTaskSpecProjected(ctx, t, runEntityID)
	t.Logf("station 7: projection-station froze task.spec on approval (no model turn); mock RequestCount=%d", mock.RequestCount())

	// Station 8 — the make-or-break: the run's sandbox is PROVISIONED AND PROVED COLD
	// before development. sandbox/01-provision (gated on task.spec presence, so it
	// runs AFTER projection) does the inherit publish — a forced provision_sandbox
	// loop that materializes the run's checkout, builds the fixture's DECLARED golang
	// image, and proves the Go module resolves + builds cold in a fresh container,
	// then stamps sandbox.ready + the digest-pinned attestation. This runs the REAL
	// cold proof (docker build + go build), the exact class both predecessors faked.
	// Assert sandbox.ready == true AND the image attestation is present — proof the
	// provision station ran the real proof on the committed artifact and the readiness
	// gate can release. Red-first: disable sandbox/01 and this station times out.
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("station 8: sandbox provisioned + proved cold — sandbox.ready stamped (mock RequestCount=%d)", mock.RequestCount())

	// Station 9 — with task.spec frozen AND the sandbox proven ready, the resumed run
	// kicks off the dev loop. dev-from-task/02 (gated on task.spec.0.test_command ne ""
	// AND sandbox.ready eq true, so it fires only AFTER projection and provisioning)
	// does the inherit publish — spawning a fresh coordinator loop bound to THIS run,
	// which re-decides and picks dev_from_task. Assert that a coordinator loop BOUND
	// TO THIS RUN (agent.run.entity_id == runEntityID) stamped next_action=dev_from_task
	// — proof the projection+readiness-gated re-wake fired and routed into development.
	// Bind by the run anchor + the distinct action so an earlier decision (issue_intake
	// / create_change) on the same run cannot false-green.
	requireRunCoordinatorDecision(ctx, t, runEntityID, journeyDevAction)
	t.Logf("station 9: dev loop kicked off — coordinator re-woke and decided %s (mock RequestCount=%d)", journeyDevAction, mock.RequestCount())

	// Station 10 — the dev loop does REAL WORK: dispatch-developer (dev-from-task/04)
	// fires on the coordinator's dev_from_task decision and spawns AMELIA (a developer
	// loop bound to this run), forced to apply_patch. Amelia authors the fixture's real
	// fix diff; the harness applies it to the run's checkout for real (git apply).
	// Assert a DEVELOPER loop bound to THIS run (agent.run.entity_id == runEntityID)
	// reached agent.loop.outcome=success — proof the dispatch fired, Amelia inherited
	// the run, apply_patch ran and applied cleanly. The fix actually working is proven
	// by the in-container measure (Amelia's in-loop turn); here we prove the author station chained.
	requireDeveloperLoopCompleted(ctx, t, runEntityID)
	t.Logf("station 10: developer dispatched — Amelia authored the fix via apply_patch (mock RequestCount=%d)", mock.RequestCount())

	// Station 11 — the make-or-break payoff: the fix is MEASURED cold, IN-LOOP, in-container.
	// The reshape moved measure INTO Amelia's bounded loop: her SECOND turn calls
	// measure_task (no StopLoop), which Execs the task's frozen test command (`go test ./...`)
	// IN the run's WARM sandbox container — the one provision_sandbox stood up over the SAME
	// checkout apply_patch wrote — and stamps the harness-derived measurement.result.0 as her
	// in-loop feedback. Because the developer's diff actually fixed the boundary bug, the
	// in-container `go test` PASSES: assert measurement.result.0.passed == "true" — proof the
	// loop did REAL work (author → apply → build cold → test), not theater. Also assert the
	// attempt counter task.attempt.0 was appended (by dispatch at spawn, R3 — the route counts
	// it against the budget). Red-first: break the warm container (or measure over unpatched
	// bytes) and passed comes back "false".
	requireMeasurementPassed(ctx, t, runEntityID)
	requireTriplePresent(ctx, t, runEntityID, "task.attempt.0")
	t.Logf("station 11: task measured IN-LOOP, in-container — measurement.result.0.passed=true (the fix is REAL); attempt counted (mock RequestCount=%d)", mock.RequestCount())

	// Station 12 — the structural floors run on the developer's REAL diff. The floors trigger
	// (dev-from-task/05) fires on Amelia's DEVELOPER-loop TERMINAL (agent.loop.outcome ne "" —
	// measure moved in-loop, so there is no separate measure loop) and spawns a forced
	// check_floors loop (run_scope=inherit), which runs the deterministic go/ast floors over
	// the task's target files in the checkout — the SAME bytes measure ran over — stamps the
	// aggregate floor.finding.0.rejected + the per-floor findings on the run, AND mirrors the
	// routing inputs (route.passed/route.rejected/route.attempt) onto its loop for the route.
	// Because the fix is a clean edit (real test present, source parses, no stub/mock, targets
	// authored), NO floor rejects: assert floor.finding.0.rejected == "false" — the structural
	// verdict the floors route advances on. Also assert the presence floor ran (the H1 gate).
	// Red-first: disable dev-from-task/05 and this station times out.
	requireFloorsPassed(ctx, t, runEntityID)
	t.Logf("station 12: structural floors ran on the diff — floor.finding.0.rejected=false (no fabrication) (mock RequestCount=%d)", mock.RequestCount())

	// Station 13 — the FLOORS ROUTE advances (rule-native, the reshape replaced check_gate).
	// The floors trigger (dev-from-task/05) fired on Amelia's DEVELOPER-loop terminal and
	// spawned a forced check_floors loop, which ran the deterministic floors over the checkout
	// AND MIRRORED the routing inputs onto its own loop: route.passed (=measurement.result.0.
	// passed=true), route.rejected (=floor.finding.0.rejected=false), route.attempt.0 (the
	// attempt mirror). The floors ROUTE (dev-from-task/06a) then fires ON THAT LOOP: because
	// route.passed=true AND route.rejected=false, it ADVANCES — spawning Quinn's reviewer loop
	// directly (NO gate tool, NO route-token, NO extra model turn for the route rule). Quinn
	// reviews the cleared task (D16 per-task review): submit_review reads the task's task.spec
	// + measurement.result and DERIVES the floored verdict (G3). Because the task measured
	// green and the mock raises no findings, the verdict is approved: assert review.verdict.0
	// == approved. Red-first: break the measurement and the floors route goes not_clean→retry
	// instead of advance→review, so no verdict lands and this station times out.
	requireReviewApproved(ctx, t, runEntityID)
	t.Logf("station 13: floors route advanced → Quinn reviewed the cleared task — review.verdict.0=approved (mock RequestCount=%d)", mock.RequestCount())

	// Station 14 — the CLEAN-ROOM COLD VERIFY of the committed artifact (the make-or-break).
	// The review ROUTE (dev-from-task/07a) fires on Quinn's loop reading the route.verdict
	// mirror: because route.verdict=approved, it spawns a forced verify_artifact loop
	// (run_scope=inherit — proving a role=reviewer inherit loop propagated the run anchor).
	// verify_artifact CLONES the run's checkout into a fresh dir, builds the operator-declared
	// image, and proves the artifact resolves + passes its own tests COLD in a SEPARATE fresh
	// container with a fresh dependency cache. Because the fix is real and self-contained, the
	// cold verify PASSES: assert verify.result == "pass". This runs the REAL docker cold proof.
	// Red-first: a non-self-contained fix (or a warm-cache-masked fabrication) comes back "fail".
	requireVerifyPassed(ctx, t, runEntityID)
	t.Logf("station 14: clean-room COLD verify of the committed artifact — verify.result=pass (mock RequestCount=%d)", mock.RequestCount())

	// Station 15 — the ISSUE→PR ARC CONNECTS end-to-end. The DELIVERY ROUTE (dev-from-task/08a)
	// fires ON THE RUN (delivery is terminal, so it routes on run facts directly — no loop, no
	// mirror, no check_coherence tool): because verify.result=pass AND review.verdict.0=approved
	// AND openspec.validated present, it PUBLISHES the delivery-station COMPONENT (R6), which
	// records pr.ref with zero model turns. This is the bridge proof's terminal: under the mock,
	// an issue drove all the way to a reviewed, clean-room-verified PR — the rail CONNECTS (not a
	// completeness claim). Assert pr.ref is present (an M0 local-delivery stub — the real forge-io
	// PR is a later group; the gate genuinely passed, only the delivery target is stubbed).
	// Red-first: break any signal (verify/validate/review) and the delivery route blocks (08b
	// parks) instead of delivering; disable the delivery-station component and pr.ref never lands.
	requirePRDelivered(ctx, t, runEntityID)
	t.Logf("station 15: ISSUE→PR ARC CONNECTS (bridge proof) — the run cohered and the delivery station recorded pr.ref (mock RequestCount=%d)", mock.RequestCount())

	// Exactly eleven model turns drove the FULL arc. The route rules chain the stations with
	// NO model turns of their own, and the deterministic stations validate / project / deliver
	// are publish-triggered COMPONENTS (R6), not forced turns (the reshape also deleted the
	// check_gate/check_coherence forced turns and the separate measure loop). Turns: C1, C2, A1,
	// PS, C3, Amelia-apply, Amelia-measure, Amelia-stop, F1 floors, R1 review, V verify = 11. A
	// spurious re-spawn (a route re-firing) or an unscripted turn would push this past 11.
	if got := mock.RequestCount(); got != 11 {
		t.Fatalf("expected exactly 11 model turns (…, Amelia's 3-turn loop, F1 floors, R1 review, V verify; validate/projection/delivery are components, not turns), got %d — extra turns indicate a re-spawn/loop, an errant route, or an unscripted turn", got)
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

// approveChange stands in for the group-5 approval adapter (a forge-io path,
// deferred): it stamps run.change_approved=true on the run entity via the same
// OwnedFactWriter transport the tools use, with the vocab writer Source
// (approval-adapter). The predicate + Source mirror the run-lifecycle/02 resume
// rule's condition and the vocab table; D15 puts the fact on the RUN entity so the
// run-fired resume rule reads it. This is the human half of the first gate — the
// arc is otherwise fully autonomous.
func approveChange(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	writer := agentictools.NewNATSOwnedFactWriter(client)
	tr := message.Triple{
		Subject:    runEntityID,
		Predicate:  "run.change_approved",
		Object:     "true",
		Source:     "approval-adapter",
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	if err := writer.ReplaceTriples(ctx, runEntityID, []message.Triple{tr}, nil); err != nil {
		t.Fatalf("stamp run.change_approved on %s: %v", runEntityID, err)
	}
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
// runEntityID (agent.run.entity_id == runEntityID) carries
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
// (agent.loop.role == "developer" ∧ agent.run.entity_id == runEntityID) reaches
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

// requireMeasurementPassed polls the run entity until measurement.result.0.passed ==
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

	const passed = "measurement.result.0.passed"
	requireEventually(t, 3*time.Minute, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, passed); got == "false" {
			t.Fatalf("measure_task stamped a FAILING in-container measurement (%s=false, exit_code=%s) — the fix did not make the test pass in the warm sandbox, or measure ran over unpatched bytes / a wrong sandbox: check apply_patch wrote the SAME checkout the warm container bind-mounts, and that task.spec.0.test_command matches the fixture (`go test ./...`)",
				passed, tripleString(e, "measurement.result.0.exit_code"))
		}
		return tripleString(e, passed) == "true"
	}, "run entity "+runEntityID+" never gained "+passed+"=true — Amelia did not call measure_task in her loop, "+
		"the warm sandbox was not provisioned/resolved, or the in-container `go test` did not run: check her dispatch allowlist "+
		"includes measure_task (tool_choice=auto), that measure_task was scripted as her second turn, and that provision_sandbox left a warm "+
		"container Up over the run's checkout")
}

// requireFloorsPassed polls the run entity until floor.finding.0.rejected == "false"
// — the proof the floors-trigger (dev-from-task/06) fired on the measure loop, the
// forced check_floors loop ran the deterministic floors over the task's target files
// in the checkout, and NO floor rejected the developer's real diff. A stamped
// rejected="true" is a hard failure (a floor caught fabrication in the fixed code, or
// the floors ran over the wrong files), surfaced immediately rather than by timeout. It
// also asserts the presence floor ran (the H1 gate) so the finding set is the full one.
func requireFloorsPassed(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const rejected = "floor.finding.0.rejected"
	const presencePassed = "floor.finding.0.presence.passed"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, rejected); got == "true" {
			t.Fatalf("check_floors REJECTED the developer's diff (%s=true) — a structural floor caught fabrication in the fixed code, or the floors ran over the wrong target files: check task.spec.0.target_files includes the test and that the checkout holds the patched bytes", rejected)
		}
		// Require the aggregate present AND a per-floor finding, so a partial/absent
		// finding set cannot false-green the "not true" check above.
		return tripleString(e, rejected) == "false" && tripleString(e, presencePassed) != ""
	}, "run entity "+runEntityID+" never gained "+rejected+"=false with a full finding set — the floors trigger (dev-from-task/05) did not fire, "+
		"or check_floors could not resolve the attempt: check Amelia's developer loop reached a terminal (the floors trigger fires on agent.loop.outcome ne \"\"), "+
		"check_floors is advertised/scripted, and the Attempts seam resolves the target files from the checkout")
}

// requireReviewApproved polls the run entity until review.verdict.0 == "approved" — the
// proof the floors route advanced (dev-from-task/06a, on route.passed=true+route.rejected=
// false) and spawned Quinn's reviewer loop, which DERIVED an approving verdict from the
// task's passing measurement with no findings. A stamped changes_requested is a hard failure
// (the measurement floor blocked, or a finding was raised), surfaced immediately.
func requireReviewApproved(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const verdict = "review.verdict.0"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, verdict); got == "changes_requested" {
			t.Fatalf("submit_review stamped %s=changes_requested — the measurement floor blocked approval (measurement.result.0 not passing) or Quinn raised a finding: check the floors route advanced on a green measurement and the mock submit_review fixture supplies no findings", verdict)
		}
		return tripleString(e, verdict) == "approved"
	}, "run entity "+runEntityID+" never gained "+verdict+"=approved — the floors advance route (dev-from-task/06a) did not fire, "+
		"or submit_review could not derive the verdict: check check_floors mirrored route.passed=true+route.rejected=false onto its loop, the advance route spawns Quinn on that mirror, "+
		"submit_review is advertised/scripted as a role=reviewer loop, and the task's measurement.result.0 is passing")
}

// requireVerifyPassed polls the run entity until verify.result == "pass" — the proof the
// review approved route (dev-from-task/07a) fired on Quinn's loop, the forced verify_artifact
// loop cloned the committed artifact and proved it resolves + passes its own tests COLD in
// a fresh throwaway container, and the artifact is self-contained + reproducible. A stamped
// "fail" is a hard failure (the fix is not self-contained, a fabrication survived to verify,
// or a forbidden build-file pattern), surfaced immediately rather than by timeout. The
// clean-room cold proof runs REAL docker on the committed fixture, so this allows a wide
// window.
func requireVerifyPassed(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const result = "verify.result"
	requireEventually(t, 3*time.Minute, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if got := tripleString(e, result); got == "fail" {
			t.Fatalf("verify_artifact stamped %s=fail — the committed artifact did NOT prove cold: the fix is not self-contained, a warm-cache-masked fabrication survived to the cold verify, or a build file carries a forbidden runtime download; check the clone carries the patched bytes and the fixture's build files are clean", result)
		}
		return tripleString(e, result) == "pass"
	}, "run entity "+runEntityID+" never gained "+result+"=pass — the review approved route (dev-from-task/07a) did not fire "+
		"(a role=reviewer inherit loop may not have propagated the run anchor), or the cold clean-room proof did not run: "+
		"check submit_review mirrored route.verdict=approved onto its loop, the approved route spawns verify on that mirror, verify_artifact is advertised/scripted, "+
		"and docker is available (the journey builds the fixture image and runs go test in a fresh container)")
}

// requirePRDelivered polls the run entity until pr.ref is present — the proof the DELIVERY
// ROUTE (dev-from-task/08a) fired ON THE RUN: because verify.result=pass AND review.verdict.0
// =approved AND openspec.validated present cohere, it forced open_pr, which recorded the
// delivery reference — the full issue→PR arc's terminal. A stamped run.awaiting_human (the
// blocked delivery park, 08b) alongside no pr.ref is the blocked path; here we require
// delivery. The rule-native delivery route stamps no pr.coherence decision (check_coherence
// is deleted) — the route conditions ARE the roll-up, so pr.ref present is the coherent proof.
func requirePRDelivered(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const prRef = "pr.ref"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		// The blocked delivery route (08b) parks on a non-pass verify — if the run parked
		// instead of delivering, surface it rather than timing out.
		if park := tripleString(e, "run.awaiting_human"); park != "" && tripleString(e, prRef) == "" {
			t.Fatalf("the run PARKED instead of delivering (run.awaiting_human=%q, no pr.ref) — the delivery route blocked (08b): a delivery signal did not cohere (verify not pass, change unvalidated, or the task unapproved). The happy-path journey expects all three green", park)
		}
		return tripleString(e, prRef) != ""
	}, "run entity "+runEntityID+" never gained "+prRef+" — the coherent delivery route (dev-from-task/08a) did not fire or open_pr did not record it: "+
		"check all three signals are on the run (verify.result=pass, review.verdict.0=approved, openspec.validated present), the delivery route fires on the run reading them, and open_pr is advertised/scripted")
}

// requireTriplePresent polls until the run entity carries at least one triple for
// predicate (any object). Used for appended multi-value predicates like
// task.attempt.<i>, where the object (a loop instance) is not known up front.
func requireTriplePresent(ctx context.Context, t *testing.T, runEntityID, predicate string) {
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
	}, "run entity "+runEntityID+" never gained a "+predicate+" triple — dispatch-developer (dev-from-task/04) "+
		"must append the per-task attempt counter AT SPAWN (R3 — the route counts it against the budget)")
}

// requireRunAnchor polls until THIS wake's coordinator loop carries an
// agent.run.entity_id triple — the run anchor stamped by publish_agent
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
	}, "coordinator loop (task "+wantTaskID+") never gained an agent.run.entity_id anchor "+
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
		"for awaiting_approval: validate stamping openspec.validated → the change-approval gate)")
}

// requireChangeAuthored polls the run entity until it carries at least one
// openspec.change.<slug>.* triple — the proof the authoring loop's create_change
// call stamped the change package on the run (it targets the run entity via the
// inherited agent.run_entity_id). Fails naming the likely cause.
func requireChangeAuthored(ctx context.Context, t *testing.T, runEntityID, slug string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	prefix := "openspec.change." + slug + "."
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		for _, tr := range e.Triples {
			if strings.HasPrefix(tr.Predicate, prefix) {
				return true
			}
		}
		return false
	}, "run entity "+runEntityID+" never gained openspec.change."+slug+".* facts — the create_change "+
		"spawn rule did not fire, the author did not inherit the run anchor (agent.run_entity_id), or the "+
		"create_change tool call was not scripted/advertised")
}

// requireTaskSpecProjected polls the run entity until task.spec.0.test_command is
// present and non-empty — the proof the approval-triggered projection rule
// (dev-from-task/03) spawned a project_tasks loop that froze the change's tasks
// into the immutable task.spec on the run. It is also the exact fact the dev
// re-wake (dev-from-task/02) gates on, so asserting it here proves the
// projection-before-kickoff ordering. Fails naming the likely cause.
func requireTaskSpecProjected(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	const projected = "task.spec.0.test_command"
	requireEventually(t, 45*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		return tripleString(e, projected) != ""
	}, "run entity "+runEntityID+" never gained "+projected+" — the projection rule (dev-from-task/03) "+
		"did not fire or project_tasks refused: check the run-level openspec.change.slug pointer (threaded into the "+
		"projection prompt), the D15 #0 revision bind (openspec.validated == openspec.change.<slug>.revision), and "+
		"that project_tasks was advertised/scripted")
}

// requireSandboxReady polls the run entity until sandbox.ready == "true" and the
// image attestation is present — the proof the provision station (sandbox/01)
// spawned a provision_sandbox loop that BUILT the declared image and proved the
// repo builds cold, stamping the harness-derived readiness/attestation. It also
// gates the dev re-wake (dev-from-task/02 requires sandbox.ready eq true), so
// asserting it here proves the provision-before-kickoff ordering. The timeout is
// wide: this station runs the real docker build + cold go build. Fails naming the
// likely cause — including a sandbox.blocked reason if provisioning parked instead.
func requireSandboxReady(ctx context.Context, t *testing.T, runEntityID string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	requireEventually(t, 4*time.Minute, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		if !ok {
			return false
		}
		if blocked := tripleString(e, "sandbox.blocked"); blocked != "" {
			t.Fatalf("provision_sandbox blocked the run instead of proving it ready: %s", blocked)
		}
		return tripleString(e, "sandbox.ready") == "true" && tripleString(e, "sandbox.attestation.image") != ""
	}, "run entity "+runEntityID+" never gained sandbox.ready=true + attestation — the provision rule (sandbox/01) "+
		"did not fire or provision_sandbox could not prove the fixture cold: check the sandbox.provisioned marker/guard, "+
		"that provision_sandbox was advertised/scripted, that SandboxSourceDir points at the fixture, and that docker can "+
		"build the declared golang image")
}

// journeySandboxSourceDir is the committed Go fixture the run develops at M0 —
// provision_sandbox materializes each run's checkout from it and cold-proves the
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
func journeyChangeArgs() map[string]any {
	return map[string]any{
		"slug":     journeyChangeSlug,
		"proposal": map[string]any{"intent": "make the spine journey author a real change", "scope_in": []any{"the spine"}},
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
				"budget":       3,
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
	client, err := natsclient.NewClient("nats://localhost:4222")
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
// two edits: the mock model endpoint URL points at the in-process mock, and the
// rule pack paths are rewritten to absolute (so the temp-dir config still resolves
// the repo's rules). Returns the temp path. The version is left as-is; `task e2e`
// resets NATS so the file loads fresh regardless of the KV version.
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
