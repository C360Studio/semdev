//go:build e2e

// The BY-POLL journey (pull-first-transport task 4.1): a webhook-UNREACHABLE
// deployment — issue-intake http_port 0 (no receiver) + conversation-channel poll
// enabled — drives the /semdev approve gate by POLLING. The run is minted from a
// front-door issue publish and parks at awaiting_approval; the human's comment is
// posted ONLY to the forge double (never to the stream, no stand-in write), and the
// POLLER Reads it off the thread and releases the gate. Byte-for-byte the same arc
// the webhook journey proves — a second transport, not a changed one. Zero paid tokens.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/forge/forgetest"
	"github.com/c360studio/semdev/internal/mockllm"
)

// pollForgeTokenEnv names the env the poll journey's conversation-channel reads its
// (double-ignored) forge token from — so it builds a real Channel pointed at the
// double for the poll Read.
const pollForgeTokenEnv = "SEMDEV_POLL_FORGE_TOKEN"

// patchPollJourneyConfig patches the operator's exact pull-first config surface into
// the journey bootstrap: the admission knobs (allowlist + repo) on BOTH front-door
// components, and — on conversation-channel — the poll transport (enabled + a short
// interval), the token env, and the api_base pointed at the forge double so the
// poller's Read hits the double, not real GitHub. issue-intake http_port stays 0 (the
// shipped default — no receiver), which is exactly the pull-first deployment shape.
func patchPollJourneyConfig(t *testing.T, configPath, apiBase string) string {
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
	ccCfg := mustMap(t, mustMap(t, components, "conversation-channel"), "config")
	ccCfg["poll"] = map[string]any{"enabled": true, "interval": "5s"}
	ccCfg["token_env"] = pollForgeTokenEnv
	ccCfg["api_base"] = apiBase

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap-poll.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// startPollJourneyRuntime boots the runtime in pull-first mode (poll on, no
// receiver) over a forge double the poller Reads the approval comment from.
func startPollJourneyRuntime(ctx context.Context, t *testing.T, mock *mockllm.Harness) *forgetest.Double {
	t.Helper()
	resetNATS(ctx, t)
	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	t.Cleanup(func() { _ = mock.Stop() })

	double := forgetest.Start()
	t.Cleanup(double.Close)
	t.Setenv(pollForgeTokenEnv, "poll-forge-token") // the double ignores it; the client requires non-empty

	configPath := patchPollJourneyConfig(t, journeyConfigPath(t, mock.Endpoint()), double.URL())
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
	return double
}

// TestBridgeProofApprovalByPollNoWebhook proves the pull-first inbound transport
// end-to-end: front-door issue publish → mint → authored + validated change →
// awaiting_approval → a /semdev approve comment on the FORGE DOUBLE (no webhook, no
// stream comment publish, no stand-in write) → the POLLER Reads it → the gate
// releases → the resumed run projects + provisions.
func TestBridgeProofApprovalByPollNoWebhook(t *testing.T) {
	mock := mockllm.New(journeyFrontOfArcFixtures(2)...)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	double := startPollJourneyRuntime(ctx, t, mock)

	// Station P1 — the issue event drives admission + the wake. http_port 0 means no
	// receiver; the flattened issue event goes straight onto the GITHUB stream (the
	// pull-first deployment mints from a launch/publish, not a webhook).
	publishFlattenedIssueEvent(ctx, t)
	requireCoordinatorDecision(ctx, t, journeyIssueRef, journeyDecideAction)
	runEntityID := requireRunAnchor(ctx, t, journeyIssueRef)
	t.Logf("poll station 1: issue admitted, run %s minted (no webhook receiver — http_port 0)", runEntityID)

	// Station P2 — the arc authors + validates the change, parking at the gate.
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("poll station 2: change authored + validated — parked at awaiting_approval, awaiting the human")

	// Station P3 — the human approves ON THE ISSUE THREAD: a comment posted ONLY to
	// the forge double. No webhook, no stream publish, no stand-in write. The poller
	// enumerates the awaiting-approval run, Reads the thread, and releases the gate.
	double.AddComment(1, webhookJourneyActor, "/semdev approve")
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("poll station 3: the POLLER Read '/semdev approve' off the thread and released the gate (no webhook)")

	// The release came via the poll transport: the double served ≥1 ListComments GET
	// (the poller's Read), and the test never published a comment event to the stream.
	if !doubleServedListComments(double) {
		t.Error("the forge double never served a ListComments GET — the poller did not Read the thread; the gate release did not come from the poll transport")
	}

	// Station P4 — the resumed run projects + provisions (the M1-proven tail; waiting
	// for the sandbox keeps teardown clean of in-flight docker work).
	requireTaskSpecProjected(ctx, t, runEntityID)
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("poll station 4: task.spec projected + sandbox cold-proved — the poll-driven front connects to the dev rail")
}

// doubleServedListComments reports whether the poller issued at least one
// ListComments GET against the double (recorded as a "list_comments" request).
func doubleServedListComments(d *forgetest.Double) bool {
	for _, r := range d.Requests() {
		if r.Kind == "list_comments" {
			return true
		}
	}
	return false
}
