package standards

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/floors"
)

// fakeRunner records every command it was asked to run and replays canned results keyed
// by the command string.
type fakeRunner struct {
	results map[string]cleanroom.Result
	errs    map[string]error
	ran     []string
}

// Up and Down satisfy cleanroom.Runner; the checks lane execs into an ALREADY-warm
// sandbox and must never provision or tear one down, so both fail loudly if called.
func (f *fakeRunner) Up(context.Context, string, []string) (cleanroom.Sandbox, error) {
	return cleanroom.Sandbox{}, errors.New("the checks lane must not provision a sandbox")
}

func (f *fakeRunner) Down(context.Context, cleanroom.Sandbox) error {
	return errors.New("the checks lane must not tear the warm sandbox down")
}

func (f *fakeRunner) Exec(_ context.Context, _ cleanroom.Sandbox, argv []string) (cleanroom.Result, error) {
	cmd := argv[len(argv)-1]
	f.ran = append(f.ran, cmd)
	if err, ok := f.errs[cmd]; ok {
		return cleanroom.Result{}, err
	}
	if r, ok := f.results[cmd]; ok {
		return r, nil
	}
	return cleanroom.Result{ExitCode: 0}, nil
}

type fakeSandboxes struct {
	runner *fakeRunner
	err    error
}

func (f fakeSandboxes) Resolve(context.Context, string) (cleanroom.Runner, cleanroom.Sandbox, error) {
	if f.err != nil {
		return nil, cleanroom.Sandbox{}, f.err
	}
	return f.runner, cleanroom.Sandbox{}, nil
}

// provisioned builds a real *Snapshots holding what provisioning captured for the run —
// the production type, not a stub, so these tests exercise the actual lookup contract
// (including the "never captured" case, which must NOT read as "declared nothing").
func provisioned(body string, declared bool) *Snapshots {
	s := NewSnapshots()
	if declared {
		s.Capture("run-1", []byte(body))
	} else {
		s.Capture("run-1", nil)
	}
	return s
}

func newChecks(snaps *Snapshots, runner *fakeRunner) *Checks {
	return &Checks{Provisioned: snaps, Sandboxes: fakeSandboxes{runner: runner}}
}

func findingFor(findings []floors.Finding, name string) (floors.Finding, bool) {
	for _, f := range findings {
		if f.Floor == "repo-check:"+name {
			return f, true
		}
	}
	return floors.Finding{}, false
}

const oneRequiredCheck = "version: 1\nchecks:\n  - name: go-vet\n    command: go vet ./...\n    required: true\n"

// (a) A required check that exits non-zero rejects the attempt exactly like a built-in floor.
func TestRepoChecksRequiredFailureRejects(t *testing.T) {
	runner := &fakeRunner{results: map[string]cleanroom.Result{
		"go vet ./...": {ExitCode: 2, Stderr: "vet: composite literal uses unkeyed fields"},
	}}
	got, err := newChecks(provisioned(oneRequiredCheck, true), runner).Run(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	f, ok := findingFor(got, "go-vet")
	if !ok {
		t.Fatalf("no repo-check finding stamped; got %+v", got)
	}
	if f.Passed {
		t.Error("a required check that exited non-zero did not reject")
	}
	if !strings.Contains(f.Detail, "2") {
		t.Errorf("the finding must carry the command's REAL exit status (G3 — the harness ran it); got %q", f.Detail)
	}
}

// (b) A non-required failure is visible but does not gate.
func TestRepoChecksNonRequiredFailureDoesNotGate(t *testing.T) {
	yaml := "version: 1\nchecks:\n  - name: gofmt\n    command: gofmt -l .\n    required: false\n"
	runner := &fakeRunner{results: map[string]cleanroom.Result{"gofmt -l .": {ExitCode: 1}}}
	got, err := newChecks(provisioned(yaml, true), runner).Run(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	f, ok := findingFor(got, "gofmt")
	if !ok {
		t.Fatalf("no finding stamped for the non-required check")
	}
	if floors.AnyRejected(got) {
		t.Error("a non-required check failure rejected the attempt")
	}
	if !strings.Contains(strings.ToLower(f.Detail), "fail") {
		t.Errorf("the finding must still SAY it failed — a failure rendered as a pass is the dishonest "+
			"half of advisory; got %q", f.Detail)
	}
}

// (c) A repo declaring no checks leaves the floors exactly as they were.
func TestRepoChecksAbsentFileAddsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		base *Snapshots
	}{
		{"absent file", provisioned("", false)},
		{"file with zero checks", provisioned("version: 1\n", true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			got, err := newChecks(tc.base, runner).Run(context.Background(), "run-1")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("declared no checks but stamped %d findings: %+v", len(got), got)
			}
			if len(runner.ran) != 0 {
				t.Errorf("ran %v with no checks declared", runner.ran)
			}
		})
	}
}

// (d) THE PROVENANCE PROPERTY, and the reason it is not a git ref. The checkout is
// bind-mounted read-WRITE into the sandbox with .git inside it, and model-authored code
// runs in that container — so `git update-ref refs/semdev/base <other>` in a TestMain
// would move any ref-based read. What the gate executes is what PROVISIONING captured,
// held in a store the container has no address for.
func TestRepoChecksRunWhatProvisioningCaptured(t *testing.T) {
	snaps := NewSnapshots()
	snaps.Capture("run-1", []byte(oneRequiredCheck))

	// Whatever the attempt did to the checkout afterwards — edited the file, rewrote the
	// base ref, deleted both — is unreachable from here: the lane never consults the
	// checkout at all. Capture a WEAKENED file for a different run to prove the store is
	// keyed and this run's law is untouched.
	snaps.Capture("run-other", []byte("version: 1\n"))

	runner := &fakeRunner{results: map[string]cleanroom.Result{"go vet ./...": {ExitCode: 2}}}
	got, err := newChecks(snaps, runner).Run(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !floors.AnyRejected(got) {
		t.Fatal("the captured required check did not gate")
	}
	if len(runner.ran) == 0 || runner.ran[0] != "go vet ./..." {
		t.Errorf("the captured command was not the one executed; ran %v", runner.ran)
	}
}

// A run the lane has no capture for must FAULT. Treating it as "this repo declared no
// checks" is how a gate disappears silently, and it is the exact inference the lane's
// doc forbids.
func TestRepoChecksUncapturedRunFaults(t *testing.T) {
	runner := &fakeRunner{}
	_, err := newChecks(NewSnapshots(), runner).Run(context.Background(), "never-provisioned")
	if err == nil {
		t.Fatal("a run with no provision-time capture ran zero checks and reported success — the repo's " +
			"gate would be gone with nothing saying so")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("the fault must name the missing capture; got %v", err)
	}
	if len(runner.ran) != 0 {
		t.Errorf("commands ran for an uncaptured run: %v", runner.ran)
	}
}

// D7a: a declared control that FAILS proves the gate can reach its failure path.
func TestRepoChecksProvenWhenTheControlFails(t *testing.T) {
	yaml := "version: 1\nchecks:\n  - name: go-vet\n    command: go vet ./...\n    required: true\n    proof: go vet ./testdata/bad\n"
	runner := &fakeRunner{results: map[string]cleanroom.Result{
		"go vet ./testdata/bad": {ExitCode: 2},
		"go vet ./...":          {ExitCode: 0},
	}}
	got, err := newChecks(provisioned(yaml, true), runner).Run(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	f, _ := findingFor(got, "go-vet")
	if !f.Passed {
		t.Errorf("a passing check with a failing control must pass; got %+v", f)
	}
	if !strings.Contains(f.Detail, "proven") {
		t.Errorf("the finding must record the gate status; got %q", f.Detail)
	}
	if len(runner.ran) != 2 || runner.ran[0] != "go vet ./testdata/bad" {
		t.Errorf("the control must run BEFORE the check it certifies; ran %v", runner.ran)
	}
}

// D7a: a control that PASSES means the gate was never demonstrated able to fail — and it
// is a REPO-declaration defect, so it must park rather than reject the attempt. Rejecting
// would re-dispatch the developer against a fault she cannot reach (the standards file is
// not in target_files) and burn the whole attempt budget on real model turns before
// escalating with a reason that reads as her work failing.
func TestRepoChecksUnfailingControlParksInsteadOfBurningTheBudget(t *testing.T) {
	yaml := "version: 1\nchecks:\n  - name: go-vet\n    command: go vet ./...\n    required: true\n    proof: go vet ./testdata/bad\n"
	runner := &fakeRunner{results: map[string]cleanroom.Result{
		"go vet ./testdata/bad": {ExitCode: 0}, // the control did NOT fail
		"go vet ./...":          {ExitCode: 0},
	}}
	got, err := newChecks(provisioned(yaml, true), runner).Run(context.Background(), "run-1")
	if err == nil {
		t.Fatalf("an un-failing control was reported as a finding (%+v) instead of faulting the lane — "+
			"the defect is deterministic, so every retry re-runs it and the run escalates blaming the developer", got)
	}
	if !strings.Contains(err.Error(), "control") {
		t.Errorf("the fault must name the un-failing control; got %v", err)
	}
}

// A broken control is a declaration defect whatever the check's severity: a non-required
// check with an un-failing control is still certifying a gate it never exercised.
func TestRepoChecksUnfailingControlParksEvenOnANonRequiredCheck(t *testing.T) {
	yaml := "version: 1\nchecks:\n  - name: gofmt\n    command: gofmt -l .\n    proof: gofmt -l ./testdata\n"
	runner := &fakeRunner{results: map[string]cleanroom.Result{
		"gofmt -l ./testdata": {ExitCode: 0},
		"gofmt -l .":          {ExitCode: 0},
	}}
	if _, err := newChecks(provisioned(yaml, true), runner).Run(context.Background(), "run-1"); err == nil {
		t.Fatal("an un-failing control on a non-required check was accepted")
	}
}

// D7a: no control declared means the check still gates, but its green is never clean.
func TestRepoChecksUnprovenWhenNoControlDeclared(t *testing.T) {
	runner := &fakeRunner{results: map[string]cleanroom.Result{"go vet ./...": {ExitCode: 0}}}
	got, err := newChecks(provisioned(oneRequiredCheck, true), runner).Run(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	f, _ := findingFor(got, "go-vet")
	if !f.Passed {
		t.Errorf("an undeclared control must not reject — ergonomics are a hard requirement; got %+v", f)
	}
	if !strings.Contains(f.Detail, "unproven") {
		t.Errorf("a gate never demonstrated able to fail must not render as a clean pass; got %q", f.Detail)
	}
}

// D7a: could-not-run is neither a pass nor a rejection on the merits, and it must stay
// distinguishable from ran-and-failed so the evidence says which instrument reported.
func TestRepoChecksCouldNotRunIsNeverAPass(t *testing.T) {
	runner := &fakeRunner{errs: map[string]error{"go vet ./...": errors.New("container is not running")}}
	got, err := newChecks(provisioned(oneRequiredCheck, true), runner).Run(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("a check that could not run is a finding, not a core fault: %v", err)
	}
	f, _ := findingFor(got, "go-vet")
	if f.Passed {
		t.Fatal("a check the harness could not execute was recorded as a pass")
	}
	if !strings.Contains(f.Detail, "not-run") {
		t.Errorf("the finding must distinguish could-not-run from ran-and-failed; got %q", f.Detail)
	}
	if strings.Contains(f.Detail, "exit status") {
		t.Errorf("a check that never ran must not report an exit status; got %q", f.Detail)
	}
}

// A malformed file at the BASE revision is a pathological state: HEAD's copy already
// parked at provision, so a differing malformed base copy must fail loudly rather than
// silently running zero checks.
func TestRepoChecksMalformedBaseFileFaults(t *testing.T) {
	runner := &fakeRunner{}
	_, err := newChecks(provisioned("version: 9\n", true), runner).Run(context.Background(), "run-1")
	if err == nil {
		t.Fatal("a malformed standards file at the base revision silently ran zero checks — the gate " +
			"would disappear with nothing saying so")
	}
}

// A sandbox that cannot be resolved is a substrate fault, not zero checks.
func TestRepoChecksSandboxFaultFaults(t *testing.T) {
	c := &Checks{Provisioned: provisioned(oneRequiredCheck, true), Sandboxes: fakeSandboxes{err: errors.New("no warm sandbox")}}
	if _, err := c.Run(context.Background(), "run-1"); err == nil {
		t.Fatal("an unresolvable sandbox ran zero checks and reported success")
	}
}
