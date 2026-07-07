package verifyartifact

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/verify"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

type fakeWorkspace struct {
	root string
	err  error
}

func (w fakeWorkspace) Root(_ context.Context, _ string) (string, error) { return w.root, w.err }

type fakeManifests struct {
	m   harness.Manifest
	err error
}

func (f fakeManifests) Resolve(_ context.Context, _ string) (harness.Manifest, error) {
	return f.m, f.err
}

type fakeWriter struct {
	replaces [][]message.Triple
}

func (w *fakeWriter) ReplaceTriples(_ context.Context, _ string, add []message.Triple, _ []string) error {
	w.replaces = append(w.replaces, add)
	return nil
}
func (w *fakeWriter) ReadOwnedPredicates(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func callVerify() agentic.ToolCall {
	return agentic.ToolCall{
		ID:       "c1",
		Name:     ToolName,
		Metadata: map[string]any{agentic.MetadataKeyRunEntityID: runEntity},
	}
}

// exec runs the tool with a scripted runner and returns the stamped verify.result
// (or "") plus the runner (for argv assertions).
func exec(t *testing.T, runner *cleanroom.MockRunner, w *fakeWriter) string {
	t.Helper()
	e := New(runner, fakeWorkspace{root: "/checkout"}, fakeManifests{m: harness.GoProfile()}, w, nil)
	res, err := e.Execute(context.Background(), callVerify())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("tool error: %s", res.Error)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == ResultPredicate {
				return tr.Object.(string)
			}
		}
	}
	return ""
}

// Happy path: resolve cold + tests pass in fresh isolation → verify.result = pass,
// stamped with the harness Source on the run entity. And the harness ran the
// MANIFEST's own commands (not a model-supplied command).
func TestVerifyPassStampsResult(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}}, // go mod download
		{Result: cleanroom.Result{ExitCode: 0}}, // go test ./...
	}}
	w := &fakeWriter{}
	if got := exec(t, runner, w); got != string(verify.OutcomePass) {
		t.Fatalf("verify.result = %q, want pass", got)
	}
	if want := [][]string{{"go", "mod", "download"}, {"go", "test", "./..."}}; !reflect.DeepEqual(runner.ExecArgv, want) {
		t.Errorf("ran %v, want the manifest's own resolve+test commands %v", runner.ExecArgv, want)
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Source != Source {
				t.Errorf("verify.result Source = %q, want %q (G5)", tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("verify.result subject = %q, want run entity (D15)", tr.Subject)
			}
		}
	}
	if runner.DownCalls != 1 {
		t.Errorf("Down called %d times, want 1 (sandbox torn down)", runner.DownCalls)
	}
}

// The cache-masked-fabrication reject (G4 / 8.7): a dependency that resolves only
// against a warm cache does NOT resolve cold — the genuine resolve failure records a
// terminal fail, and the test step is not even reached.
func TestVerifyCacheMaskedFabricationFails(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "go: example.com/fabricated@v9.9.9: no matching versions for query \"v9.9.9\""}},
	}}
	if got := exec(t, runner, &fakeWriter{}); got != string(verify.OutcomeFail) {
		t.Fatalf("verify.result = %q, want fail (cache-masked fabrication rejected cold)", got)
	}
	if runner.ExecCalls != 1 {
		t.Errorf("ran %d steps, want 1 — a genuine resolve failure short-circuits before tests", runner.ExecCalls)
	}
}

// A transport fault during resolve retries, it does NOT terminal-reject the artifact
// (spec: transient infrastructure error retries).
func TestVerifyResolveTransportRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 1, Stderr: "go: dial tcp: lookup proxy.golang.org: i/o timeout"}},
	}}
	if got := exec(t, runner, &fakeWriter{}); got != string(verify.OutcomeRetry) {
		t.Fatalf("verify.result = %q, want retry (a transport fault must not terminal-reject)", got)
	}
}

// A sandbox that will not provision is a transport fault → retry, no terminal reject.
func TestVerifyProvisionFaultRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{UpErr: errors.New("sandbox backend unreachable")}
	if got := exec(t, runner, &fakeWriter{}); got != string(verify.OutcomeRetry) {
		t.Fatalf("verify.result = %q, want retry (provisioning fault is transport)", got)
	}
	if runner.ExecCalls != 0 {
		t.Errorf("ran %d steps after a failed Up, want 0", runner.ExecCalls)
	}
}

// A step that could not RUN (a transport fault, not an exit code) retries.
func TestVerifyResolveRunErrorRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Err: errors.New("exec: \"go\": executable file not found in $PATH")},
	}}
	if got := exec(t, runner, &fakeWriter{}); got != string(verify.OutcomeRetry) {
		t.Fatalf("verify.result = %q, want retry (resolve could not run)", got)
	}
}

// Resolve cold but the artifact's own tests fail → terminal fail (a genuine
// artifact failure).
func TestVerifyTestsFailFails(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}},                     // resolve ok
		{Result: cleanroom.Result{ExitCode: 1, Stdout: "--- FAIL"}}, // tests fail
	}}
	if got := exec(t, runner, &fakeWriter{}); got != string(verify.OutcomeFail) {
		t.Fatalf("verify.result = %q, want fail (tests failed in isolation)", got)
	}
}

// Tests that could not RUN (transport) retries, not a terminal reject.
func TestVerifyTestRunErrorRetries(t *testing.T) {
	runner := &cleanroom.MockRunner{Execs: []cleanroom.MockExec{
		{Result: cleanroom.Result{ExitCode: 0}},    // resolve ok
		{Err: errors.New("sandbox died mid-test")}, // tests couldn't run
	}}
	if got := exec(t, runner, &fakeWriter{}); got != string(verify.OutcomeRetry) {
		t.Fatalf("verify.result = %q, want retry (tests could not run)", got)
	}
}

// Re-verify upserts the verify.result (replace-by-predicate): a passing re-run
// replaces a prior retry.
func TestVerifyReVerifyUpserts(t *testing.T) {
	w := &fakeWriter{}
	// First: a transport retry.
	exec(t, &cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 1, Stderr: "connection refused"}}}}, w)
	// Then: a clean pass on the same writer.
	e := New(&cleanroom.MockRunner{Execs: []cleanroom.MockExec{{Result: cleanroom.Result{ExitCode: 0}}, {Result: cleanroom.Result{ExitCode: 0}}}},
		fakeWorkspace{root: "/checkout"}, fakeManifests{m: harness.GoProfile()}, w, nil)
	res, err := e.Execute(context.Background(), callVerify())
	if err != nil || res.Error != "" {
		t.Fatalf("re-verify: err=%v toolErr=%s", err, res.Error)
	}
	if len(w.replaces) != 2 {
		t.Fatalf("want two verify.result upserts, got %d", len(w.replaces))
	}
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate != ResultPredicate {
				t.Errorf("verify wrote %q — it must write only %q", tr.Predicate, ResultPredicate)
			}
		}
	}
}

// Schema-only registration (nil workspace/manifests/writer) fails loudly.
func TestVerifyFailsLoudlyWithoutHarness(t *testing.T) {
	res, err := New(&cleanroom.MockRunner{}, nil, nil, nil, nil).Execute(context.Background(), callVerify())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Error("a nil-harness verify must fail loudly")
	}
}

// G3: the schema takes no arguments at all — the model can only trigger the proof.
func TestVerifySchemaTakesNoInput(t *testing.T) {
	defs := (&Executor{}).ListTools()
	if len(defs) != 1 {
		t.Fatalf("want one tool definition, got %d", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("schema exposes %d properties, want 0 — verify takes no input (G3): %v", len(props), props)
	}
}
