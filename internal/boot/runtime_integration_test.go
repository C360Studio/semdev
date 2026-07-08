//go:build integration

// This file is gated behind the "integration" build tag (see Taskfile.yml's
// `test` target, which does not pass it) so plain `go test ./...` never needs
// a live NATS. Run it with `task test:integration`, which resets NATS FIRST so
// the runtime loads the config file under test rather than a stale versioned-KV
// copy (the framework's config.Manager ignores a same-version file — see
// Taskfile `nats:reset`). Running it against a dirty NATS can silently validate
// a stale config.

package boot

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/c360studio/semstreams/service"
)

// onlyKnownStopWedge reports whether err is EXACTLY the known #508 StopAll wedge
// (errStopServicesWedged) and nothing else. Stop returns errors.Join, whose
// Unwrap() []error we walk: if any joined sub-error is something other than the
// known wedge (a configMgr/NATS fault, or a different failure), it is NOT a
// tolerable teardown outcome and the caller fails the test. A lone wrapped wedge
// (no Join) is handled by the errors.Is fallback.
func onlyKnownStopWedge(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			if !errors.Is(e, errStopServicesWedged) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, errStopServicesWedged)
}

// bootstrapConfigPath returns the absolute path to the same config both
// semdev binaries boot from. config.Loader rejects any relative path whose
// ".."-resolution escapes the process working directory (path-traversal
// hardening in the framework's safeReadFile) — "../../configs/..." trips that
// check regardless of where it actually lands, so this resolves an absolute
// path off the test file's own location instead (mirrors test/conformance's
// repoRoot helper).
func bootstrapConfigPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo root")
	}
	// this file lives at <repoRoot>/internal/boot/runtime_integration_test.go
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return filepath.Join(repoRoot, "configs", "semdev-bootstrap.json")
}

// wantAdvertisedTools are the semdev-owned tools RegisterTools wires in. A live
// Runtime must advertise all of them through its ExecutorRegistry — this proves
// the tool-registration seam ran through the shared boot path and the full semdev
// tool set reached the registry. (It does NOT by itself distinguish live-writer
// from schema-only registration — RegisterExecutor records the name either way; the
// live-NATS path is proven separately by wantHealthyComponents reaching healthy,
// which those processors cannot do without a real NATS client.)
var wantAdvertisedTools = []string{
	"create_change",
	"render_openspec",
	"write_change",
	"validate_change",
	"github_list_comments",
	"project_tasks",
	"measure_task",
	"submit_review",
	"verify_artifact",
	"check_floors",
}

// wantHealthyComponents are the processors the runtime must bring to healthy: the
// graph fact-store, the rule engine, and the four agentic-execution components.
// Reaching healthy proves the runtime actually ASSEMBLED against live NATS (each
// needs a real NATS client and, for rule, a rule pack that resolved and loaded) —
// not merely that the service objects exist. rule is load-bearing: it goes healthy
// only if rules_files resolved (the CWD-independent path fix, resolveRulePackPaths),
// so this assertion is also the regression guard for the swallowed-rule-load class.
// The agentic-* entries prove the newly-added execution plane (agentic-tools/model/
// loop/dispatch over the AGENT/TOOL/USER streams) constructs and binds its consumers
// against the real framework — the first end-to-end proof that config is valid. They
// go healthy on consumer bind alone (no LLM traffic needed), so an unreachable mock
// endpoint does not gate this; a bad port/stream/model_registry declaration does.
var wantHealthyComponents = []string{
	"graph-ingest",
	"graph-query",
	"rule",
	"agentic-tools",
	"agentic-model",
	"agentic-loop",
	"agentic-dispatch",
}

// TestRuntimeStartsCleanlyAgainstLiveNATS is the NATS-gated boot smoke test (run
// via `task test:integration`, which resets NATS first): wire a Runtime against the
// real bootstrap config and live NATS, start it, wait for the substrate to become
// healthy, assert the semdev tool set is advertised, and stop cleanly. It is the
// first test to exercise NewRuntime/Start/Stop end to end — every other boot test
// is a source scan or a registration census against in-memory registries.
func TestRuntimeStartsCleanlyAgainstLiveNATS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rt, err := NewRuntime(ctx, RunOptions{ConfigPath: bootstrapConfigPath(t)})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Teardown. This test proves the runtime STARTS cleanly and the plane reaches
	// healthy — that is its whole contract. Graceful shutdown is currently subject
	// to a known semstreams ComponentManager deadlock on cold boot (a leaked cm.mu
	// reader in the framework's health check; filed upstream C360Studio/semstreams#508
	// — see runtime.Stop / design.md D13). runtime.Stop bounds StopAll so it returns
	// instead of hanging. We TOLERATE only that specific known wedge (errStopServicesWedged)
	// and fail on anything else, so this NATS-gated test cannot let a NOVEL shutdown
	// fault (a configMgr/NATS error or a different deadlock) ride green. Tighten back
	// to a hard "no error" assertion when #508 lands and the bound is removed.
	defer func() {
		switch err := rt.Stop(5 * time.Second); {
		case err == nil:
			// Clean shutdown (e.g. a warm boot that did not trip #508).
		case onlyKnownStopWedge(err):
			t.Logf("Stop hit the known semstreams shutdown deadlock (#508), tolerated: %v", err)
		default:
			t.Errorf("Stop returned an unexpected shutdown fault (not the known #508 wedge): %v", err)
		}
	}()

	// Component start is async — poll the component-manager until the substrate
	// reports healthy. This is what actually proves the runtime assembled, and it
	// fails LOUD on a swallowed component-init failure instead of booting green.
	requireEventuallyHealthy(ctx, t, rt, wantHealthyComponents)

	tools := rt.ToolRegistry().ListTools()
	got := make(map[string]bool, len(tools))
	for _, def := range tools {
		got[def.Name] = true
	}
	for _, name := range wantAdvertisedTools {
		if !got[name] {
			t.Errorf("tool %q not advertised by the ExecutorRegistry; got %d tools", name, len(tools))
		}
	}
}

// requireEventuallyHealthy polls the runtime's component-manager until every named
// component reports healthy, or fails at the deadline naming what is missing (a
// missing "rule" almost always means the rules_files did not resolve).
func requireEventuallyHealthy(ctx context.Context, t *testing.T, rt *Runtime, want []string) {
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
		healthy := make(map[string]bool)
		for _, n := range cm.GetHealthyComponents() {
			healthy[n] = true
		}
		var missing []string
		for _, n := range want {
			if !healthy[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("components not healthy within deadline: missing %v (a missing rule engine usually means rules_files did not resolve)", missing)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled waiting for component health: %v", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}
