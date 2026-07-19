//go:build e2e

// The REAL-LLM journey variant (first-real-llm-journey): the same fixture arc as
// the mock bridge proof, driven through the real runtime against a real Anthropic
// model with NO mock harness. This is the M1 easy-tier instrument — its asserts
// are station OUTCOMES with model-latency-tolerant windows, never the mock's
// scripted contracts (no RequestCount, no scripted slugs or diffs), and a parked
// run is a LOUD failure carrying the graph evidence needed to diagnose without a
// re-run.
//
// GATING (real money — fail loud, never silently skip a declared run):
//   - SEMDEV_REAL_LLM unset → t.Skip before any boot; zero cost. Any value
//     other than "1" FAILS loud (declared-but-malformed intent).
//   - SEMDEV_REAL_LLM=1 with GEMINI_API_KEY unset/empty → immediate t.Fatal
//     naming the variable (the D4 posture: declared intent never degrades).
//
// MODEL CONFIG (design D1, re-targeted to Gemini — operator constraint:
// Anthropic API rates are unaffordable for this project; Gemini is the paid
// provider in use). Gemini is the framework's FIRST-CLASS route: semstreams
// beta.153 ships a native GeminiAdapter (provider "gemini") over Google's
// OpenAI-compatible endpoint, and configs/gemini-example.json in the module is
// the authoritative shape this config copies — for Gemini 3.x previews the
// per-tool_call thought_signature flow REQUIRES provider "gemini" AND
// wire_backend "wire" (ADR-037 chunk 8; the framework's own live test drives
// exactly this endpoint+model with tools). The preview model slug rotates —
// update realLLMModelID (and its prices) when Google publishes the stable id.
//
// (Alternative providers, verified this change: Anthropic has NO native
// adapter in beta.153 — an Anthropic run must use its OpenAI-compat endpoint,
// provider "openai" + url https://api.anthropic.com/v1; provider "anthropic"
// validates but nothing implements it, and with no URL go-openai would dial
// api.openai.com. Kept here so nobody "fixes" a future config that way.)
//
// Run per docs/real-llm-runbook.md: mock ladder green first, sidecar armed,
// abort criteria written down BEFORE launch.

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/c360studio/semdev/internal/boot"
	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/intake"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/natsclient"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"
)

const (
	// realLLMGateEnv declares a real-LLM run: unset skips, "1" runs, any
	// other value FAILS loud (declared-but-malformed intent, D4).
	realLLMGateEnv = "SEMDEV_REAL_LLM"
	// realLLMKeyEnv is the env var the model registry resolves the API key from
	// (api_key_env — the key never appears in a config file or test source).
	// GEMINI_API_KEY is the framework's own convention (gemini-example.json +
	// its live Gemini test).
	realLLMKeyEnv = "GEMINI_API_KEY"
	// realLLMModelID is the model for all three roles on run 1 (one variable at
	// a time; per-role/cheaper-tier models are a tuning follow-up). This is the
	// framework example + live-test default; the PREVIEW SLUG ROTATES — update
	// it and the prices together when the stable id lands.
	realLLMModelID = "gemini-3.1-pro-preview"
	// realLLMInputPricePer1M / realLLMOutputPricePer1M are gemini-3.1-pro-preview's
	// published prices (≤200K-token prompts — this arc's prompts are tiny) so the
	// framework stamps true agent.loop.cost-usd facts (G3/G7: the ledger's cost
	// record is harness-stamped, never model-reported). Confirm on run day
	// (runbook §2).
	realLLMInputPricePer1M  = 2.00
	realLLMOutputPricePer1M = 12.00
)

// realLLMIssueRef is the host-neutral ref of the fixture issue this journey
// admits. It names the fixture repo honestly — the run's checkout IS
// test/fixtures/go-health-class.
const realLLMIssueRef = "c360studio/go-health-class#1"

// realLLMIssueBody is the admitted issue's authored content — the G8-realistic
// bug report an operator would file against the fixture, in the author's own
// words, WITHOUT prescribing the diff. It rides Intake.Event.AuthoredText into
// the wake prompt (the forge-io content lane) and must give a real model enough
// to author a change whose tasks target the real files and the real test
// command.
const realLLMIssueBody = `The health classifier reports Healthy for a service sitting exactly at the warning threshold.

In health.go, Classify(cpu, mem) blends the two pressures and only classifies Degraded when the blended pressure is strictly ABOVE warningThreshold — so pressure == warningThreshold falls through to Healthy. Operators expect the warning boundary to be inclusive: at the threshold the service is already Degraded.

The package's own test suite pins the expected behavior — 'go test ./...' currently FAILS on the TestClassify/warning-boundary case (Classify(0.75, 0.10) should be Degraded). Please make the warning comparison inclusive so the suite passes. No other behavior should change, and the existing tests are the acceptance signal.`

// TestRealLLMJourneyIssueToPR drives the issue→PR arc with real model turns:
// front door → decide → run mint → change authored (real authoring!) → CLI
// validation → stood-in human approval → projection + cold-proved sandbox →
// dev re-wake → Amelia's real dev loop (apply_patch + in-container measure) →
// floors → route → Quinn's real review → cold clean-room verify → delivery.
// Bounded by the arc's own containment: authored attempt budget clamped [1,5],
// loop iteration cap 8, transient grace 2 — every failure path parks toward the
// human, and a park FAILS this journey loudly with the run's facts.
func TestRealLLMJourneyIssueToPR(t *testing.T) {
	switch gate := os.Getenv(realLLMGateEnv); gate {
	case "":
		t.Skipf("real-LLM journey not declared (%s unset) — zero-cost skip; see docs/real-llm-runbook.md", realLLMGateEnv)
	case "1":
		// declared — proceed to the key check.
	default:
		t.Fatalf("%s=%q is a declared-but-malformed gate — declared intent never silently skips (D4); set %s=1 to run or unset it to skip", realLLMGateEnv, gate, realLLMGateEnv)
	}
	if strings.TrimSpace(os.Getenv(realLLMKeyEnv)) == "" {
		t.Fatalf("%s=1 declares a real-LLM run but %s is unset/empty — a declared run never silently skips; export the key or unset the gate", realLLMGateEnv, realLLMKeyEnv)
	}

	// The whole arc: real model turns + docker cold proofs + bounded retries.
	// The sidecar (runbook), not this timeout, is the abort authority — this is
	// the hard backstop. It MUST exceed the sum of every station window below
	// (4+8+3+4+25 = 44m) plus boot/reset/health and the rule-driven stations,
	// and stay under the runbook's `go test -timeout` so expiry fails loud with
	// evidence instead of a goroutine dump (semstreams-reviewer HIGH).
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Minute)
	defer cancel()

	startRealLLMRuntime(ctx, t)
	taskID := publishRealCoordinatorWake(ctx, t)
	t.Logf("real-llm: wake published (task=%s, model endpoint=gemini/%s) — first paid turn is in flight", taskID, realLLMModelID)

	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	// Station: the coordinator ROUTES. A real decide turn: API latency + the
	// rule fire + the KV write.
	realEventually(ctx, t, client, 4*time.Minute, func(entities map[string]entityStateMap) bool {
		for _, e := range entities {
			if tripleString(e, agvocab.LoopRole) == "coordinator" &&
				tripleString(e, agvocab.LoopTask) == taskID &&
				tripleString(e, agvocab.CoordinatorNextAction) == "issue_intake" {
				return true
			}
		}
		return false
	}, func() { dumpTaskLoopEvidence(t, client, taskID) },
		"the real coordinator never stamped decision.next_action=issue_intake for task "+taskID+
			" — the model did not route a fresh admitted issue to issue_intake (persona decision contract), the decide call failed, or the endpoint is unreachable")
	t.Log("real-llm station: coordinator routed issue_intake (first real decide landed)")

	// Rule-driven stations reuse the mock journey's helpers (no model turn in
	// the window): mint + bridge to executing.
	runEntityID := requireRunAnchor(ctx, t, taskID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	t.Logf("real-llm station: run %s minted and executing", runEntityID)

	// Station: the change is AUTHORED — by the real model. ANY non-empty slug
	// with a decodable document counts (never the mock's scripted slug); the
	// content's fitness is judged by the CLI validation oracle next, not by
	// string equality here. Two real turns live in this window (the re-wake
	// decide + the authoring create_change call).
	var authoredSlug string
	realEventually(ctx, t, client, 8*time.Minute, func(entities map[string]entityStateMap) bool {
		e, ok := entities[runEntityID]
		if !ok {
			return false
		}
		raw := tripleString(e, changefacts.DocumentPredicate)
		if raw == "" {
			return false
		}
		doc, err := changefacts.UnmarshalDocument(raw)
		if err != nil {
			t.Fatalf("run %s carries an UNDECODABLE %s: %v — create_change stamped malformed JSON", runEntityID, changefacts.DocumentPredicate, err)
		}
		if doc.Change == nil || doc.Change.Slug == "" {
			return false
		}
		authoredSlug = doc.Change.Slug
		return true
	}, func() { dumpRunEvidence(t, client, runEntityID) }, "run "+runEntityID+" never gained a decodable authored change — the re-woken coordinator did not route create_change, "+
		"the authoring turn produced no create_change call (tool_choice=function terminates a text-only turn), or the authored "+
		"content failed the tool's schema; the dump below carries the run's facts")
	t.Logf("real-llm station: change authored by the model (slug=%q)", authoredSlug)

	// Station: the CLI oracle validates the model's change, firing the approval
	// gate. A validation FAILURE clears openspec.change.validated and the run
	// never reaches awaiting_approval — that window expiring IS the "the model
	// authored an invalid change" verdict, evidence in the dump.
	realEventually(ctx, t, client, 3*time.Minute, func(entities map[string]entityStateMap) bool {
		e, ok := entities[runEntityID]
		return ok && tripleString(e, "agent.run.phase") == "awaiting_approval"
	}, func() { dumpRunEvidence(t, client, runEntityID) }, "run "+runEntityID+" never reached awaiting_approval — the validation station rejected the model-authored change "+
		"(openspec.change.validated absent in the dump = the CLI oracle said invalid), or the gate rule did not fire")
	t.Log("real-llm station: model-authored change VALIDATED by the CLI oracle → awaiting_approval")

	// The human gate, stood in exactly as the mock journeys do.
	approveChange(ctx, t, runEntityID)
	requireRunPhase(ctx, t, runEntityID, "executing")
	requireTaskSpecProjected(ctx, t, runEntityID)
	requireSandboxReady(ctx, t, runEntityID)
	t.Log("real-llm station: approved → task.spec projected → sandbox provisioned + proved cold")

	// Station: the dev re-wake routes into development (one real decide turn).
	realEventually(ctx, t, client, 4*time.Minute, func(entities map[string]entityStateMap) bool {
		for _, e := range entities {
			if tripleString(e, agvocab.LoopRole) == "coordinator" &&
				tripleString(e, agvocab.LoopRunEntityID) == runEntityID &&
				tripleString(e, agvocab.CoordinatorNextAction) == "dev_from_task" {
				return true
			}
		}
		return false
	}, func() { dumpRunEvidence(t, client, runEntityID) }, "no coordinator bound to run "+runEntityID+" decided dev_from_task — the re-wake fired (sandbox ready) but the real model "+
		"did not route into development")
	t.Log("real-llm station: dev loop kicked off (dev_from_task)")

	// Station: CONVERGE TO DELIVERY. Amelia's real dev loop authors a real fix,
	// measures in-container, floors + routes run, Quinn reviews, the clean-room
	// verify cold-proves, delivery records the ref. Bounded retries and review
	// re-entry are LEGAL arc paths, so this poll tolerates transient RED
	// measurements and changes_requested verdicts — but a PARK fails
	// immediately (the arc gave up before the backstop), and milestones are
	// logged as they first appear so the sidecar sees forward progress.
	milestones := map[string]bool{}
	milestone := func(key, msg string) {
		if !milestones[key] {
			milestones[key] = true
			t.Logf("real-llm milestone: %s", msg)
		}
	}
	realEventually(ctx, t, client, 25*time.Minute, func(entities map[string]entityStateMap) bool {
		e, ok := entities[runEntityID]
		if !ok {
			return false
		}
		if park := tripleString(e, "run.awaiting.human"); park != "" {
			dumpRunEvidence(t, client, runEntityID)
			t.Fatalf("run %s PARKED toward the human instead of delivering: %q — the arc exhausted its budget or hit a "+
				"terminal it could not route; the dump above carries the route facts (attempts, transient counts, verdicts)", runEntityID, park)
		}
		if tripleString(e, "measurement.result.passed") == "true" {
			milestone("measure", "in-container measurement GREEN (the model's fix is real)")
		}
		if tripleString(e, "floor.finding.rejected") == "false" {
			milestone("floors", "structural floors passed on the model's diff")
		}
		if v := tripleString(e, "review.verdict.value"); v == "approved" {
			milestone("review", "Quinn's review verdict: approved")
		} else if v == "changes_requested" {
			milestone("review-retry", "Quinn requested changes (re-entry is a legal path; still converging)")
		}
		if tripleString(e, "verify.cleanroom.result") == "pass" {
			milestone("verify", "clean-room cold verify PASSED")
		}
		return tripleString(e, "delivery.pr.ref") != ""
	}, func() { dumpRunEvidence(t, client, runEntityID) }, "run "+runEntityID+" never recorded delivery.pr.ref — the arc did not converge within the window and did not park "+
		"(check the milestones logged above for the last station reached; the dump carries the run's facts)")

	// The delivery route gates on validated ∧ approved ∧ verify=pass, so the
	// ref implies the chain; the milestones prove each was OBSERVED. The
	// attempt count is the projector-clamped budget's proof: 1..5, never more.
	attempts := countDistinctObjects(client, runEntityID, "task.attempt.instance")
	if attempts < 1 || attempts > 5 {
		t.Fatalf("run %s delivered with %d task.attempt.instance triples — outside the projector's [1,5] budget clamp; the budget was not load-bearing", runEntityID, attempts)
	}
	t.Logf("real-llm TERMINAL: delivery.pr.ref recorded — the ISSUE→PR ARC CONNECTED WITH REAL MODEL TURNS (slug=%q, attempts=%d)", authoredSlug, attempts)

	// The ledger's cost record (design D6): every loop the framework stamped
	// with tokens/cost, plus the sum. Copy this block into the evidence-ledger
	// entry (kind real-llm) — costs are harness-stamped, never model-reported.
	logRunSpend(t, client, runEntityID)
}

// entityStateMap is the scanEntities value type, aliased so realEventually's
// callback signature stays readable.
type entityStateMap = graph.EntityState

// startRealLLMRuntime boots the REAL shared runtime against the real-model
// config: fresh NATS, no mock anywhere in the path, the same personas +
// sandbox source the mock journeys use.
func startRealLLMRuntime(ctx context.Context, t *testing.T) {
	t.Helper()

	resetNATS(ctx, t)

	rt, err := boot.NewRuntime(ctx, boot.RunOptions{
		ConfigPath:       realLLMConfigPath(t),
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

// realLLMConfigPath writes a copy of the bootstrap config with the model
// registry REPLACED by the single real endpoint (design D1): the framework's
// native Gemini route over Google's OpenAI-compatible surface, key resolved
// at runtime via api_key_env, real prices so cost facts are true, bounded
// output and a hang-proof request timeout. The dead mock endpoint is REMOVED
// so any stale preference fails loud at registry validation instead of
// dialing a mock that is not there.
// Rule paths are rewritten absolute exactly as journeyConfigPath does.
func realLLMConfigPath(t *testing.T) string {
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

	// The endpoint copies the framework's own configs/gemini-example.json
	// gemini-3-pro-preview shape: provider "gemini" engages the native
	// GeminiAdapter and wire_backend "wire" the framework-owned wire client —
	// BOTH required for the 3.x preview thought_signature contract.
	cfg["model_registry"] = map[string]any{
		"endpoints": map[string]any{
			"gemini": map[string]any{
				"provider":                   "gemini",
				"url":                        "https://generativelanguage.googleapis.com/v1beta/openai",
				"model":                      realLLMModelID,
				"api_key_env":                realLLMKeyEnv,
				"max_tokens":                 1048576,
				"supports_tools":             true,
				"tool_format":                "openai",
				"stream":                     false,
				"reasoning_effort":           "medium",
				"wire_backend":               "wire",
				"max_output_tokens":          8192,
				"request_timeout":            "300s",
				"input_price_per_1m_tokens":  realLLMInputPricePer1M,
				"output_price_per_1m_tokens": realLLMOutputPricePer1M,
			},
		},
		"capabilities": map[string]any{
			"coordinator": map[string]any{"preferred": []any{"gemini"}, "requires_tools": true},
			"developer":   map[string]any{"preferred": []any{"gemini"}, "requires_tools": true},
			"reviewer":    map[string]any{"preferred": []any{"gemini"}, "requires_tools": true},
		},
		"defaults": map[string]any{"model": "gemini"},
	}

	ruleCfg := mustMap(t, mustMap(t, mustMap(t, cfg, "components"), "rule"), "config")
	files, _ := ruleCfg["rules_files"].([]any)
	abs := make([]any, len(files))
	for i, f := range files {
		abs[i] = filepath.Join(root, "configs", f.(string))
	}
	ruleCfg["rules_files"] = abs

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("re-encode real-llm config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "realllm-bootstrap.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write real-llm config: %v", err)
	}
	return path
}

// publishRealCoordinatorWake is the intake adapter's stand-in for the fixture
// issue: the wake is built exactly as production will build it — via
// intake.CoordinatorTask, with the issue's authored content riding
// Intake.Event.AuthoredText into the prompt (the forge-io content lane).
func publishRealCoordinatorWake(ctx context.Context, t *testing.T) string {
	t.Helper()

	in := intake.Intake{Relevant: true, IssueRef: realLLMIssueRef}
	in.Event.AuthoredText = realLLMIssueBody
	task, err := intake.CoordinatorTask(in, "gemini")
	if err != nil {
		t.Fatalf("build real coordinator wake: %v", err)
	}

	client := connectFrontDoor(ctx, t)
	defer func() { _ = client.Close(context.Background()) }()

	base := message.NewBaseMessage(task.Schema(), task, "realllm-frontdoor")
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal coordinator wake envelope: %v", err)
	}
	if err := client.PublishToStream(ctx, intake.FrontDoorSubject, data); err != nil {
		t.Fatalf("publish coordinator wake: %v", err)
	}
	return task.TaskID
}

// realEventually polls cond every 2s until true, the window elapses, or the
// journey's ctx backstop expires. On either failure it runs dump (the graph
// post-mortem) BEFORE failing, so a parked or wedged real run is diagnosable
// without re-spending. The backstop expiry gets its OWN message — after ctx
// death scanEntities returns empty maps and a station message would
// misdiagnose (semstreams-reviewer HIGH / go-reviewer B1).
func realEventually(ctx context.Context, t *testing.T, client *natsclient.Client, window time.Duration, cond func(map[string]entityStateMap) bool, dump func(), msg string) {
	t.Helper()
	deadline := time.Now().Add(window)
	for {
		if err := ctx.Err(); err != nil {
			dump()
			t.Fatalf("the journey's ctx backstop expired mid-poll (%v) — this is NOT a station verdict: the arc may have been "+
				"progressing legally but slowly; raise the backstop/windows before re-spending. Pending station: %s", err, msg)
		}
		if cond(scanEntities(ctx, client)) {
			return
		}
		if time.Now().After(deadline) {
			dump()
			t.Fatal(msg)
		}
		time.Sleep(2 * time.Second)
	}
}

// evidenceCtx returns a fresh short-lived context for post-mortem reads: the
// evidence dump must survive the journey ctx's death (NATS outlives the
// runtime), or the paid run's diagnosis is lost exactly when it matters.
func evidenceCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// dumpTaskLoopEvidence prints every loop entity carrying the given task id —
// the post-mortem for a failure BEFORE the run exists (a failed first paid
// turn): the coordinator loop already carries outcome/terminal-reason/tokens.
// Reads on a fresh evidence context so it survives the journey ctx's death.
func dumpTaskLoopEvidence(t *testing.T, client *natsclient.Client, taskID string) {
	t.Helper()
	ectx, cancel := evidenceCtx()
	defer cancel()
	found := 0
	for id, e := range scanEntities(ectx, client) {
		if tripleString(e, agvocab.LoopTask) != taskID {
			continue
		}
		found++
		t.Logf("EVIDENCE loop %s (task %s): role=%s next-action=%s outcome=%s terminal-reason=%s tokens-in=%s tokens-out=%s cost-usd=%s",
			id, taskID,
			tripleString(e, agvocab.LoopRole),
			tripleString(e, agvocab.CoordinatorNextAction),
			tripleString(e, agvocab.LoopOutcome),
			tripleString(e, agvocab.LoopTerminalReason),
			tripleValue(e, agvocab.LoopTokensIn),
			tripleValue(e, agvocab.LoopTokensOut),
			tripleValue(e, agvocab.LoopCostUSD))
	}
	if found == 0 {
		t.Logf("EVIDENCE: no loop entity carries task %s — the wake never spawned a loop (front-door subject/consumer, or the loop entity never persisted)", taskID)
	}
}

// dumpRunEvidence prints the run entity's full triple set and every loop bound
// to the run (role, outcome, terminal reason, tokens, cost) — the post-mortem a
// failed paid run must carry so diagnosis never needs a second paid run. Reads
// on a fresh evidence context so it survives the journey ctx's death.
func dumpRunEvidence(t *testing.T, client *natsclient.Client, runEntityID string) {
	t.Helper()
	ectx, cancel := evidenceCtx()
	defer cancel()
	entities := scanEntities(ectx, client)

	if e, ok := entities[runEntityID]; ok {
		lines := make([]string, 0, len(e.Triples))
		for _, tr := range e.Triples {
			lines = append(lines, tr.Predicate+" = "+fmt.Sprintf("%v", tr.Object))
		}
		sort.Strings(lines)
		t.Logf("EVIDENCE run %s (%d triples):\n  %s", runEntityID, len(lines), strings.Join(lines, "\n  "))
	} else {
		t.Logf("EVIDENCE: run entity %s not found in ENTITY_STATES", runEntityID)
	}

	for id, e := range entities {
		if tripleString(e, agvocab.LoopRunEntityID) != runEntityID {
			continue
		}
		t.Logf("EVIDENCE loop %s: role=%s outcome=%s terminal-reason=%s tokens-in=%s tokens-out=%s cost-usd=%s",
			id,
			tripleString(e, agvocab.LoopRole),
			tripleString(e, agvocab.LoopOutcome),
			tripleString(e, agvocab.LoopTerminalReason),
			tripleValue(e, agvocab.LoopTokensIn),
			tripleValue(e, agvocab.LoopTokensOut),
			tripleValue(e, agvocab.LoopCostUSD))
	}
}

// tripleValue formats the first object of the entity's triple for predicate via
// %v ("" if absent) — token/cost objects are numeric, so the string-typed
// tripleString would drop them.
func tripleValue(e graph.EntityState, predicate string) string {
	for _, tr := range e.Triples {
		if tr.Predicate == predicate {
			return fmt.Sprintf("%v", tr.Object)
		}
	}
	return ""
}

// logRunSpend logs every run-bound loop's harness-stamped token/cost facts and
// their sum — the source of the evidence-ledger entry's cost record.
func logRunSpend(t *testing.T, client *natsclient.Client, runEntityID string) {
	t.Helper()
	ectx, cancel := evidenceCtx()
	defer cancel()
	entities := scanEntities(ectx, client)

	var totalCost float64
	loops, uncosted := 0, 0
	for id, e := range entities {
		if tripleString(e, agvocab.LoopRunEntityID) != runEntityID {
			continue
		}
		loops++
		cost := tripleValue(e, agvocab.LoopCostUSD)
		t.Logf("LEDGER loop %s: role=%s tokens-in=%s tokens-out=%s cost-usd=%s",
			id, tripleString(e, agvocab.LoopRole),
			tripleValue(e, agvocab.LoopTokensIn), tripleValue(e, agvocab.LoopTokensOut), cost)
		if c, err := strconv.ParseFloat(cost, 64); err == nil {
			totalCost += c
		} else {
			uncosted++
		}
	}
	t.Logf("LEDGER total: %d run-bound loops, summed cost-usd=%.4f, %d loop(s) WITHOUT a parseable cost fact (their spend is NOT in the sum — reconcile from tokens) — copy into docs/evidence-ledger.md, kind real-llm", loops, totalCost, uncosted)
}

// countDistinctObjects returns how many DISTINCT objects the entity carries
// under the predicate — the same accounting the arc's own budget partition
// uses for task.attempt.instance (each dispatch appends a distinct loop
// instance), so the [1,5] assert measures the thing it claims to (go-reviewer).
func countDistinctObjects(client *natsclient.Client, entityID, predicate string) int {
	ectx, cancel := evidenceCtx()
	defer cancel()
	e, ok := scanEntities(ectx, client)[entityID]
	if !ok {
		return 0
	}
	seen := map[string]bool{}
	for _, tr := range e.Triples {
		if tr.Predicate == predicate {
			seen[fmt.Sprintf("%v", tr.Object)] = true
		}
	}
	return len(seen)
}
