//go:build e2e

// The SELF-TARGET journey (self-target-provisioning-and-launch-driver task 6.1):
// the run provisions its sandbox by CLONING a REAL target repository — not the
// committed Go fixture dir — resolved from the run's own coordinate
// (run.issue.ref), develops the fix in that clone, and delivers a PR back to the
// SAME repository. This is the GitHub-faithful shape: clone acme/repo, branch,
// push back, open a PR whose diff is measured against acme/repo's base branch.
//
// The target is a LOCAL bare git remote seeded WITH HISTORY (two commits on
// `main`) reachable over file:// transport — a forge double for the git host, no
// paid tokens, no network. Because the clone source AND the delivery push target
// are the SAME bare remote, the delivered branch shares the seeded history with
// `main`, so `git diff main..semdev/<suffix>` on the remote is the FIX ALONE:
// the proof that self-target provisioning preserves the clone's history (design
// D2's refs/semdev/base) yet a real PR would diff cleanly to just the change.
//
// HONESTY (review M3): the file:// transport never prompts for credentials, so
// the D3 no-argv-leak token path is NOT exercised here — only the unit pin
// (clone.TestResolveTokenRidesEnvNotArgv) and the operator-gated live run (5.4)
// exercise it. This journey proves the clone->develop->diff->deliver MECHANICS
// for the full-clone case with zero paid tokens; see docs/evidence-ledger.md.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/forge/forgetest"
	"github.com/c360studio/semdev/internal/mockllm"
)

// seedSelfTargetRemote stands up the run's TARGET as a local bare git remote at
// <remotesDir>/<owner>/<repo>.git seeded with the go-health-class fixture and a
// SECOND commit (real history the delivered diff must exclude). owner/repo match
// the journey's run coordinate (c360studio/semdev-journey#1) so the forge-clone
// source resolves this exact remote. Returns the remotes ROOT (the clone-source
// base URL points here) and the bare repo path (the delivery push target + the
// diff-assertion git dir). Parallels internal/forge/clone's unexported
// seedBareRemote, but seeds the FULL fixture tree, two commits, and forces `main`.
func seedSelfTargetRemote(t *testing.T) (remotesDir, bareRepo string) {
	t.Helper()
	requireGitForSelfTarget(t)

	remotesDir = t.TempDir()
	work := t.TempDir()
	runGit := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// The seed's default branch MUST be `main` — the delivery base branch and the
	// diff-assertion base both name it, and the forge-clone source checks out the
	// remote's default branch.
	runGit(work, "-c", "init.defaultBranch=main", "init", "-q")
	runGit(work, "config", "user.email", "seed@example.com")
	runGit(work, "config", "user.name", "seed")

	// Commit 1 — the buildable target (the go-health-class fixture: health.go with
	// the real boundary bug, its test, go.mod, and the .devcontainer the provision
	// station builds the warm image from).
	if err := os.CopyFS(work, os.DirFS(journeySandboxSourceDir(t))); err != nil {
		t.Fatalf("copy fixture into seed worktree: %v", err)
	}
	runGit(work, "add", "-A")
	runGit(work, "commit", "-q", "--no-gpg-sign", "-m", "seed: go-health-class target")

	// Commit 2 — real history that the delivered PR diff MUST exclude. A top-level
	// marker file (never a target_files entry, never a .go file) so it cannot
	// perturb the build, the measure, the floors, or the fix diff.
	if err := os.WriteFile(filepath.Join(work, ".semdev-seed-history"), []byte("prior history the delivered diff excludes\n"), 0o644); err != nil {
		t.Fatalf("write seed history marker: %v", err)
	}
	runGit(work, "add", "-A")
	runGit(work, "commit", "-q", "--no-gpg-sign", "-m", "seed: second commit (history to exclude)")

	bareRepo = filepath.Join(remotesDir, "c360studio", "semdev-journey.git")
	if err := os.MkdirAll(filepath.Dir(bareRepo), 0o755); err != nil {
		t.Fatalf("mkdir bare remote parent: %v", err)
	}
	if out, err := exec.Command("git", "clone", "--bare", "-q", work, bareRepo).CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare seed: %v\n%s", err, out)
	}
	return remotesDir, bareRepo
}

// injectSelfTargetForge patches the delivery-station forge config to push BACK to
// the seeded target bare remote (base_branch main) while standing up the forge
// double for the API shapes (find/create PR). Because remote_url == the clone
// source, the delivered branch descends from the cloned history — the shape a
// real GitHub clone/push/PR has. Sets the package journeyForgeBare/Double the
// shared requirePRDelivered assertion reads.
func injectSelfTargetForge(t *testing.T, configPath, bareRepo string) string {
	t.Helper()

	double := forgetest.Start()
	t.Cleanup(double.Close)
	journeyForgeDouble = double
	journeyForgeBare = bareRepo
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
		"owner":       journeyForgeOwner, // c360studio
		"repo":        journeyForgeRepo,  // semdev-journey — the SAME repo the run cloned
		"remote_url":  "file://" + bareRepo,
		"base_branch": "main",
		"api_base":    double.URL(),
		"token_env":   "SEMDEV_JOURNEY_FORGE_TOKEN",
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode journey config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "journey-bootstrap-selftarget.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write journey config: %v", err)
	}
	return path
}

// startSelfTargetJourneyRuntime boots the runtime in FORGE-SOURCE mode: the
// provision station materializes each run's checkout by CLONING the run's target
// (RunOptions.ForgeSource) instead of copying a fixture dir (SandboxSourceDir is
// intentionally UNSET — design D5 fails closed if both are set). The intake
// allowlist patch drives the webhook front door; the self-target forge patch
// points delivery back at the seeded remote.
func startSelfTargetJourneyRuntime(ctx context.Context, t *testing.T, mock *mockllm.Harness, remotesDir, bareRepo string) {
	t.Helper()
	resetNATS(ctx, t)
	if err := mock.Start(); err != nil {
		t.Fatalf("start mock LLM: %v", err)
	}
	t.Cleanup(func() { _ = mock.Stop() })

	configPath := patchIntakeJourneyConfig(t, journeyConfigPath(t, mock.Endpoint()))
	configPath = injectSelfTargetForge(t, configPath, bareRepo)
	rt, err := boot.NewRuntime(ctx, boot.RunOptions{
		ConfigPath:  configPath,
		PersonasDir: journeyPersonasDir(t),
		// FORGE-SOURCE mode: clone <BaseURL>/<owner>/<repo>.git from the run's
		// coordinate. Empty TokenEnv → unauthenticated (correct for file://).
		ForgeSource: &boot.ForgeSourceConfig{BaseURL: "file://" + remotesDir},
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

// requireDeliveredDiffIsFixAlone is the SELF-TARGET proof: on the seeded remote,
// the delivered semdev/<suffix> branch sits exactly one commit atop the cloned
// history, and the PR-equivalent diff (main..branch) is the fix ALONE — never the
// seeded history/files. Red-first: if Materialize did not preserve the clone's
// history (design D2) the branch would not descend from main's tip; if the base
// ref were wrong the diff would spill the whole tree.
func requireDeliveredDiffIsFixAlone(ctx context.Context, t *testing.T, bareRepo string) {
	t.Helper()
	git := func(args ...string) string {
		full := append([]string{"--git-dir", bareRepo}, args...)
		out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// gitAllow tolerates a non-zero exit (returns ok=false) — needed for
	// merge-base, which exits 1 with NO output when the two commits share no
	// ancestry (the orphan-branch regression: history NOT preserved). Routing
	// that through git() would fatal with a raw "exit status 1" and swallow the
	// crafted diagnostic below.
	gitAllow := func(args ...string) (string, bool) {
		full := append([]string{"--git-dir", bareRepo}, args...)
		out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
		return strings.TrimSpace(string(out)), err == nil
	}

	branches := nonEmptyLines(git("for-each-ref", "--format=%(refname)", "refs/heads/semdev/"))
	if len(branches) != 1 {
		t.Fatalf("seeded remote holds %d refs/heads/semdev/* branches, want exactly 1 (the one delivery): %v", len(branches), branches)
	}
	branch := branches[0]

	// The test is MEANINGFUL only if there is real history to exclude: main carries
	// the two seed commits.
	if n, _ := strconv.Atoi(git("rev-list", "--count", "main")); n < 2 {
		t.Fatalf("seed main carries %d commits, want >= 2 — need real history for the exclusion to prove anything", n)
	}

	// The delivered branch is exactly ONE commit ahead of main (the fix); history
	// was neither re-committed onto the branch nor lost.
	if ahead := git("rev-list", "--count", "main.."+branch); ahead != "1" {
		t.Fatalf("delivered branch %s is %s commits ahead of main, want exactly 1 (the fix alone) — history leaked into the delivery", branch, ahead)
	}

	// The branch descends from main's tip → the clone's history is PRESERVED beneath
	// the fix (design D2: the fix commits atop refs/semdev/base == the cloned tip).
	// An ok=false here is the orphan case — no shared ancestry, i.e. history lost.
	if mb, ok := gitAllow("merge-base", "main", branch); !ok || mb != git("rev-parse", "main") {
		t.Fatalf("merge-base(main, %s)=%q (shared-ancestry=%v) != main tip — the delivered branch does not sit atop the cloned history (base ref wrong, history rewritten, or orphaned)", branch, mb, ok)
	}

	// The PR-equivalent diff touches ONLY health.go — the fix, never the cloned
	// history files (the .semdev-seed-history marker, go.mod, the test, …).
	if names := nonEmptyLines(git("diff", "--name-only", "main.."+branch)); len(names) != 1 || names[0] != "health.go" {
		t.Fatalf("delivered diff (main..%s) touches %v, want exactly [health.go] — the fix alone, not the cloned history", branch, names)
	}
}

// nonEmptyLines splits git output on newlines and drops blank lines (an empty
// for-each-ref result is one blank line, not zero refs).
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// requireGitForSelfTarget skips the journey if git is unavailable (the offline
// journeys already require docker; git is the same class of host prerequisite).
func requireGitForSelfTarget(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
}

// TestBridgeProofSelfTargetForgeCloneToPR drives the full issue->PR arc where the
// run provisions by CLONING a real target repo (with history) resolved from its
// own coordinate, develops the fix in the clone, and delivers a PR back to the
// same repo whose diff is the fix ALONE. The front of the arc drives from a
// flattened WEBHOOK event (so coordinator/04 stamps run.issue.ref, which the
// forge-clone source reads); the tail is the M1-proven dev rail. Zero paid tokens.
func TestBridgeProofSelfTargetForgeCloneToPR(t *testing.T) {
	mock := mockllm.New(append(journeyFrontOfArcFixtures(3),
		// Amelia's bounded loop: apply the fix to the CLONED health.go, then measure.
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "apply_patch", Args: map[string]any{"diff": journeyFixtureFixDiff}}},
		mockllm.Fixture{Marker: journeyDeveloperMarker, Tool: &mockllm.ToolCall{Name: "measure_task", Args: map[string]any{"task_index": 0}}},
		mockllm.Fixture{Marker: journeyReviewMarker, Tool: &mockllm.ToolCall{Name: "submit_review", Args: map[string]any{"task_index": 0}}},
	)...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	remotesDir, bareRepo := seedSelfTargetRemote(t)
	startSelfTargetJourneyRuntime(ctx, t, mock, remotesDir, bareRepo)

	// Station 1 — the webhook event admits + wakes the coordinator, which mints the
	// run; coordinator/04 stamps run.issue.ref = the coordinate the clone resolves.
	publishFlattenedIssueEvent(ctx, t)
	requireCoordinatorDecision(ctx, t, journeyIssueRef, journeyDecideAction)
	runEntityID := requireRunAnchor(ctx, t, journeyIssueRef)
	requireAdmissionRecord(ctx, t, journeyIssueRef, webhookJourneyActor)
	requireRunIssueRef(ctx, t, runEntityID, journeyIssueRef)
	t.Logf("station 1: webhook issue admitted; run %s minted; run.issue.ref=%s stamped (the clone coordinate)", runEntityID, journeyIssueRef)

	// Station 2 — authored + validated change → the human gate.
	requireChangeAuthored(ctx, t, runEntityID, journeyChangeSlug)
	requireRunPhase(ctx, t, runEntityID, "awaiting_approval")
	t.Logf("station 2: change %q authored + validated — awaiting approval", journeyChangeSlug)

	// Station 3 — the human approves on the issue (comment → approval adapter).
	publishFlattenedApprovalComment(ctx, t)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("station 3: '/semdev approve' released the gate via the approval adapter")

	// Station 4 — projection + the make-or-break: the sandbox is provisioned by
	// CLONING the seeded target (not the fixture dir) and cold-proved.
	requireTaskSpecProjected(ctx, t, runEntityID)
	requireSandboxReady(ctx, t, runEntityID)
	t.Logf("station 4: task.spec projected + sandbox cold-proved FROM THE FORGE CLONE (the run's checkout is a clone of the seeded target, history preserved)")

	// Station 5 — the dev rail: develop in the clone, measure in-container, floors,
	// review, cold clean-room verify.
	requireRunCoordinatorDecision(ctx, t, runEntityID, journeyDevAction)
	requireDeveloperLoopCompleted(ctx, t, runEntityID)
	requireMeasurementPassed(ctx, t, runEntityID)
	requireFloorsPassed(ctx, t, runEntityID)
	requireReviewApproved(ctx, t, runEntityID)
	requireVerifyPassed(ctx, t, runEntityID)
	t.Logf("station 5: developed in the clone → measured GREEN in-container → floors → review approved → cold clean-room verify passed")

	// Station 6 — delivery pushes the verified commit BACK to the seeded remote and
	// opens the evidence PR on the double (query-by-head before create).
	requirePRDelivered(ctx, t, runEntityID)
	t.Logf("station 6: delivered a PR back to the SAME repository — verified commit pushed, evidence PR opened")

	// Station 7 — THE SELF-TARGET PROOF: on the seeded remote the delivered branch
	// sits one commit atop the cloned history and its PR diff is the fix ALONE.
	requireDeliveredDiffIsFixAlone(ctx, t, bareRepo)
	t.Logf("station 7: SELF-TARGET ARC CONNECTS — cloned a real target with history, developed, and delivered a PR whose diff (main..semdev/<suffix>) is the fix alone, history preserved")

	// The webhook front adds no model turns over the CoordinatorTask-driven arc
	// (admission is deterministic), so the full arc is the same 8 turns the mock
	// happy journey asserts (C1, C2, A1, C3, Amelia apply/measure/stop, R1 review).
	// A re-spawn or errant route would push this past 8.
	if got := mock.RequestCount(); got != 8 {
		t.Fatalf("expected exactly 8 model turns (same arc as the fixture-source happy journey), got %d — a re-spawn, an errant route, or an unscripted turn", got)
	}
}
