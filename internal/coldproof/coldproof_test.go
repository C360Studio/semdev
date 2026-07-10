package coldproof

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/verify"
)

var (
	resolveCmd = []string{"go", "mod", "download"}
	proveCmd   = []string{"go", "build", "./..."}
)

// gather runs Gather against a scripted MockRunner in a temp-free way and returns the
// evidence + the runner (for call assertions).
func gather(runner *cleanroom.MockRunner) Evidence {
	return Gather(context.Background(), runner, "/checkout", []string{"GOMODCACHE", "GOCACHE"}, resolveCmd, proveCmd)
}

// Happy path: resolve cold + prove pass in one fresh sandbox → completed, resolved,
// prove passed; both the manifest's own commands ran, in order, and the sandbox was
// torn down.
func TestGatherPass(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}}, // resolve
		{Result: cleanroom.Result{ExitCode: 0}}, // prove (build)
	}}
	ev := gather(runner)
	if !ev.Completed || !ev.Resolved || !ev.ProvePassed {
		t.Errorf("evidence = %+v, want completed+resolved+prove-passed", ev)
	}
	if len(ev.FreshCacheHomes) != 2 {
		t.Errorf("fresh cache homes = %v, want 2 (GOMODCACHE, GOCACHE)", ev.FreshCacheHomes)
	}
	if want := [][]string{resolveCmd, proveCmd}; !reflect.DeepEqual(runner.ExecArgv, want) {
		t.Errorf("ran %v, want the manifest's own resolve+prove commands %v", runner.ExecArgv, want)
	}
	if runner.DownCalls != 1 {
		t.Errorf("Down called %d times, want 1", runner.DownCalls)
	}
	if got := verify.Decide(ev.ToVerifyInput()).Outcome; got != verify.OutcomePass {
		t.Errorf("verify outcome = %q, want pass", got)
	}
}

// The cache-masked-fabrication reject (SB4): a coordinate that resolves only against a
// warm cache does NOT resolve cold — the genuine resolve failure short-circuits before
// the prove step, and Decide fails.
func TestGatherColdResolveFabricationFails(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: `go: example.com/fabricated@v9.9.9: no matching versions`}},
	}}
	ev := gather(runner)
	if !ev.Completed || ev.Resolved {
		t.Errorf("evidence = %+v, want completed + NOT resolved (genuine cold fail)", ev)
	}
	if runner.ExecCalls != 1 {
		t.Errorf("ran %d steps, want 1 — a genuine resolve fail short-circuits before the prove step", runner.ExecCalls)
	}
	if got := verify.Decide(ev.ToVerifyInput()).Outcome; got != verify.OutcomeFail {
		t.Errorf("verify outcome = %q, want fail", got)
	}
}

// A transport-class resolve fault does NOT complete → Retry, never a terminal reject.
func TestGatherResolveTransportRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "go: dial tcp: lookup proxy.golang.org: i/o timeout"}},
	}}
	ev := gather(runner)
	if ev.Completed || ev.Transport == "" {
		t.Errorf("evidence = %+v, want not-completed with a transport reason", ev)
	}
	if got := verify.Decide(ev.ToVerifyInput()).Outcome; got != verify.OutcomeRetry {
		t.Errorf("verify outcome = %q, want retry", got)
	}
}

// A provisioning fault is transport — no steps run, retry.
func TestGatherProvisionFaultRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{UpErr: errors.New("sandbox backend unreachable")}
	ev := gather(runner)
	if ev.Completed || runner.ExecCalls != 0 {
		t.Errorf("evidence = %+v, execCalls=%d — a failed Up runs no steps and does not complete", ev, runner.ExecCalls)
	}
	if got := verify.Decide(ev.ToVerifyInput()).Outcome; got != verify.OutcomeRetry {
		t.Errorf("verify outcome = %q, want retry", got)
	}
}

// A prove step that ran and exited non-zero is a genuine failure (tests failed / build
// broke), not transport.
func TestGatherProveFails(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}},                         // resolve ok
		{Result: cleanroom.Result{ExitCode: 2, Stderr: "build failed"}}, // prove fails
	}}
	ev := gather(runner)
	if !ev.Completed || !ev.Resolved || ev.ProvePassed {
		t.Errorf("evidence = %+v, want completed+resolved+prove-FAILED", ev)
	}
	if got := verify.Decide(ev.ToVerifyInput()).Outcome; got != verify.OutcomeFail {
		t.Errorf("verify outcome = %q, want fail", got)
	}
}

// A prove step that could not RUN (transport) retries.
func TestGatherProveRunErrorRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}},     // resolve ok
		{Err: errors.New("sandbox died mid-build")}, // prove couldn't run
	}}
	ev := gather(runner)
	if ev.Completed || ev.Transport == "" {
		t.Errorf("evidence = %+v, want not-completed with a transport reason", ev)
	}
	if got := verify.Decide(ev.ToVerifyInput()).Outcome; got != verify.OutcomeRetry {
		t.Errorf("verify outcome = %q, want retry", got)
	}
}

// An empty command is a harness misconfiguration → transport-class (the step could not
// run), never a silent green.
func TestGatherEmptyCommandIsTransport(t *testing.T) {
	ev := Gather(context.Background(), &cleanroom.MockRunner{}, "/checkout", []string{"GOMODCACHE"}, nil, proveCmd)
	if ev.Completed || ev.Transport == "" {
		t.Errorf("evidence = %+v, want a transport fault for an empty resolve command", ev)
	}
}
