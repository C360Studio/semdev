//go:build e2e

// The WEBHOOK-LANE journey (forge-io-real-lanes tasks 2.5 + 3.2): the front of
// the arc drives from a FLATTENED WEBHOOK EVENT on the GITHUB stream — no
// CoordinatorTask call, no approveChange stand-in write anywhere in the test.
// The issue-intake COMPONENT consumes the event, admits it through the real
// gate (allowlist), births the admission record, and publishes the wake; the
// issue-ref rule stamps run.issue.ref on the minted run; a flattened COMMENT
// event ("/semdev approve" by the same allowlisted actor) releases the
// change-approval gate through the APPROVAL ADAPTER — the journey's old
// stand-in write is exactly what this proves away. Zero paid tokens.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/forge/githubwebhook"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/mockllm"
)

// webhookJourneyActor is the allowlisted human driving the journey's events.
const webhookJourneyActor = "journey-human"

// webhookJourneyRepo must combine with issue number 1 to yield EXACTLY
// journeyIssueRef ("c360studio/semdev-journey#1") so the shared front-of-arc
// mock fixtures key on the same ref.
const webhookJourneyRepo = "c360studio/semdev-journey"

// patchIntakeJourneyConfig patches the admission knobs (allowlist + repo binding)
// into BOTH front-door components — issue-intake (the issue lane) and
// conversation-channel (the /semdev approve comment lane) — in the journey's temp
// bootstrap, the operator's exact config surface, so the boot path under test is
// the real one. The comment-approval half now lives in conversation-channel, so it
// needs the same allowlist/repo or the '/semdev approve' comment is rejected.
func patchIntakeJourneyConfig(t *testing.T, configPath string) string {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read journey config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode journey config: %v", err)
	}
	components := mustMap(t, cfg, "components")
	for _, name := range []string{"issue-intake", "conversation-channel"} {
		compCfg := mustMap(t, mustMap(t, components, name), "config")
		compCfg["allowlist"] = []any{webhookJourneyActor}
		compCfg["repo"] = webhookJourneyRepo
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap-webhook.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// startWebhookJourneyRuntime mirrors startJourneyRuntime with the intake
// config patch applied.
func startWebhookJourneyRuntime(ctx context.Context, t *testing.T, mock *mockllm.Harness) {
	t.Helper()
	resetNATS(ctx, t)
	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	t.Cleanup(func() { _ = mock.Stop() })

	configPath := patchIntakeJourneyConfig(t, journeyConfigPath(t, mock.Endpoint()))
	rt, err := boot.NewRuntime(ctx, boot.RunOptions{
		ConfigPath:       configPath,
		PersonasDir:      journeyPersonasDir(t),
		SandboxSourceDir: journeySandboxSourceDir(t),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := rt.Stop(5 * time.Second); stopErr != nil {
			t.Logf("runtime Stop (best-effort teardown): %v", stopErr)
		}
	})
	requireAgenticHealthy(ctx, t, rt)
}

// publishFlattenedIssueEvent puts an admitted-shape flattened issues event on
// the GITHUB stream — the receiver's output byte-shape, no HTTP in the path.
func publishFlattenedIssueEvent(ctx context.Context, t *testing.T) {
	t.Helper()
	ev := githubwebhook.IssueEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issues",
			Action:     "opened",
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-journey", FullName: webhookJourneyRepo},
			Sender:     webhookJourneyActor,
			DeliveryID: "journey-delivery-1",
			ReceivedAt: time.Now().UTC(),
		},
		Issue: githubwebhook.IssuePayload{
			Number: 1,
			Title:  "Health boundary misclassifies at the warning threshold",
			Body:   "/semdev Classify(0.80, 0.10) returns ok but the spec says warning — fix the boundary.",
			State:  "open",
			Labels: []string{"semdev"},
			Author: webhookJourneyActor,
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal issue event: %v", err)
	}
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	if err := client.PublishToStream(ctx, admission.SubjectIssue, data); err != nil {
		t.Fatalf("publish issue event: %v", err)
	}
}

// publishFlattenedApprovalComment puts the "/semdev approve" comment event on
// the stream — the approval adapter (not a test write) must release the gate.
func publishFlattenedApprovalComment(ctx context.Context, t *testing.T) {
	t.Helper()
	ev := githubwebhook.CommentEvent{
		WebhookEvent: githubwebhook.WebhookEvent{
			EventType:  "issue_comment",
			Action:     "created",
			Repository: githubwebhook.Repository{Owner: "c360studio", Name: "semdev-journey", FullName: webhookJourneyRepo},
			Sender:     webhookJourneyActor,
			DeliveryID: "journey-delivery-2",
			ReceivedAt: time.Now().UTC(),
		},
		IssueNumber: 1,
		Comment:     githubwebhook.CommentPayload{Body: "/semdev approve", Author: webhookJourneyActor},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal comment event: %v", err)
	}
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	if err := client.PublishToStream(ctx, admission.SubjectComment, data); err != nil {
		t.Fatalf("publish comment event: %v", err)
	}
}

// requireRunIssueRef polls until the run carries run.issue.ref == wantRef —
// the issue-ref RULE's stamp (forge-io-real-lanes D2; red-first: unregister
// coordinator/04 and this times out).
func requireRunIssueRef(ctx context.Context, t *testing.T, runEntityID, wantRef string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	requireEventually(t, 30*time.Second, func() bool {
		e, ok := scanEntities(ctx, client)[runEntityID]
		return ok && tripleString(e, "run.issue.ref") == wantRef
	}, "run "+runEntityID+" never gained run.issue.ref="+wantRef+" — the issue-ref rule (coordinator/04) must "+
		"substitute the front-door loop's agent.loop.task (the wake's bare-ref TaskID) onto the run once the anchor lands")
}

// requireAdmissionRecord polls until the intake component's admission-record
// entity exists for the ref — the spec's recorded intake.actor.admitted.
func requireAdmissionRecord(ctx context.Context, t *testing.T, wantRef, wantActor string) {
	t.Helper()
	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()
	requireEventually(t, 30*time.Second, func() bool {
		for id, e := range scanEntities(ctx, client) {
			if !strings.Contains(id, ".forge.intake.event.") {
				continue
			}
			if tripleString(e, intake.EventRefPredicate) == wantRef &&
				tripleString(e, intake.ActorAdmittedPredicate) == "true" &&
				tripleString(e, intake.ActorLoginPredicate) == wantActor {
				return true
			}
		}
		return false
	}, "no admission record (forge.intake.event entity) carries intake.event.ref="+wantRef+
		" admitted=true actor="+wantActor+" — the intake component must record the admission before the wake (G7 evidence + idempotency backstop)")
}

// TestBridgeProofWebhookIssueToApprovedRun drives the front of the arc from
// flattened webhook events end-to-end: intake component → admission → wake →
// mint → issue-ref stamp → authored change → validated → COMMENT approval →
// resumed run → projected task.spec → cold-proved sandbox.
func TestBridgeProofWebhookIssueToApprovedRun(t *testing.T) {
	mock := mockllm.New(journeyFrontOfArcFixtures(2)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	startWebhookJourneyRuntime(ctx, t, mock)

	// Station W1 — the webhook event drives admission + the wake (no
	// CoordinatorTask call in this test).
	publishFlattenedIssueEvent(ctx, t)
	requireCoordinatorDecision(ctx, t, journeyIssueRef, journeyDecideAction)
	runEntityID := requireRunAnchor(ctx, t, journeyIssueRef)
	t.Logf("webhook station 1: flattened issue event admitted by the COMPONENT; coordinator woke (TaskID = bare ref) and minted run %s", runEntityID)

	// Station W2 — the admission record + the rule-stamped issue ref.
	requireAdmissionRecord(ctx, t, journeyIssueRef, webhookJourneyActor)
	requireRunIssueRef(ctx, t, runEntityID, journeyIssueRef)
	t.Logf("webhook station 2: admission recorded (intake.actor.admitted) and run.issue.ref stamped by coordinator/04")

	// Station W3 — the arc reaches the authored, validated change exactly as
	// the CoordinatorTask-driven journeys do (same fixtures, same byte shapes).
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("webhook station 3: change authored + validated off the webhook-driven wake — awaiting the human")

	// Station W4 — the human approves ON THE ISSUE: a comment event, consumed
	// by the approval adapter. The stand-in approveChange write is GONE from
	// this path.
	publishFlattenedApprovalComment(ctx, t)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("webhook station 4: '/semdev approve' comment released the gate via the approval adapter (no stand-in write)")

	// Station W5 — the resumed run projects + provisions (the M1-proven tail;
	// waiting for the sandbox keeps teardown clean of in-flight docker work).
	requireTaskSpecProjected(ctx, t, runEntityID)
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("webhook station 5: task.spec projected + sandbox cold-proved — the webhook-driven front connects to the dev rail")
}
