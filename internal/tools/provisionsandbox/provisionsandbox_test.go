package provisionsandbox

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/secrets"
	"github.com/c360studio/semdev/internal/verify"

	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

const runEntity = "org.plat.agent.chain.execution.run-1"

// --- fakes ---

type fakeSources struct {
	dir string
	err error
}

func (f fakeSources) Resolve(_ context.Context, _ string) (string, error) { return f.dir, f.err }

type fakeCheckouts struct {
	root   string
	err    error
	called bool
}

func (f *fakeCheckouts) Materialize(_ context.Context, _, _ string) (string, error) {
	f.called = true
	return f.root, f.err
}

type fakeManifests struct {
	m   harness.Manifest
	err error
}

func (f fakeManifests) Resolve(_ context.Context, _ string) (harness.Manifest, error) {
	return f.m, f.err
}

type fakeProver struct {
	baseline coldproof.Baseline
	err      error
	called   bool
}

func (f *fakeProver) ProveBaseline(_ context.Context, _, _ string, _ harness.Manifest, _ secrets.Store) (coldproof.Baseline, error) {
	f.called = true
	return f.baseline, f.err
}

// fakeWarmers records the warm dev-container stand-up (the SB4 warm container the
// loop measures in), so a test can prove provisioning stands it up on a proven-cold
// baseline and parks when it faults — without docker.
type fakeWarmers struct {
	called    bool
	gotImage  string
	gotDir    string
	gotCaches []string
	err       error
}

func (f *fakeWarmers) Provision(_ context.Context, _, image, workDir string, cacheEnvs []string) (cleanroom.Sandbox, error) {
	f.called = true
	f.gotImage, f.gotDir, f.gotCaches = image, workDir, cacheEnvs
	if f.err != nil {
		return cleanroom.Sandbox{}, f.err
	}
	return cleanroom.Sandbox{WorkDir: "/work", Handle: "warm-container"}, nil
}

type fakeReader struct {
	triples []message.Triple
	err     error
}

func (f fakeReader) ReadFacts(_ context.Context, _, prefix string) ([]message.Triple, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []message.Triple
	for _, t := range f.triples {
		if len(prefix) == 0 || t.Predicate == prefix || (len(t.Predicate) >= len(prefix) && t.Predicate[:len(prefix)] == prefix) {
			out = append(out, t)
		}
	}
	return out, nil
}

type replaceCall struct {
	add    []message.Triple
	remove []string
}

type fakeWriter struct {
	calls []replaceCall
	err   error
}

func (w *fakeWriter) ReplaceOwned(_ context.Context, m projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	if w.err != nil {
		return projection.MutationReceipt{Commit: projection.CommitNotCommitted}, w.err
	}
	// remove is empty by construction now: sandbox-provisioner's ready/block paths
	// each reconcile the WHOLE four-predicate group, which is what the old explicit
	// remove lists achieved — [blocked] on the ready path, and the readiness/
	// attestation trio on the block path (migrate-beta159 D3a).
	w.calls = append(w.calls, replaceCall{add: m.Desired})
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

// writerFor wraps the fake in the owner-bound seam the production code takes, so
// every test exercises graphown.ContractFor for real — the behavioral proof that
// this owner's predicates are classed onto the entity class it actually writes.
func writerFor(w *fakeWriter) *graphown.Writer { return graphown.NewWriter(Source, w) }

// ownedGroup is sandbox-provisioner's replace-owned predicate group — the exact
// blast radius of each ReplaceOwned it issues (migrate-beta159 D3a).
var ownedGroup = []string{ReadyPredicate, BlockedPredicate, AttestationImagePredicate, AttestationTierPredicate}

// okDocker is the injected docker probe for the unit tests (no real daemon).
func okDocker(context.Context, string) error { return nil }

// readyBaseline is a canned Ready cold-proof result (built image + Pass outcome).
func readyBaseline(digest string) coldproof.Baseline {
	return coldproof.Baseline{
		Image:   cleanroom.BuiltImage{Ref: "semdev-sandbox:test", Digest: digest},
		Outcome: verify.OutcomePass,
	}
}

// newDeps builds a ProvisionDeps over the fakes with the real docker probe bypassed and a
// recording warm-container stand-up. Returns the warmers so a test can assert the warm dev
// container was (or was not) stood up.
func newDeps(sources Sources, checkouts Checkouts, manifests Manifests, prover Prover, reader fakeReader, w *fakeWriter) (ProvisionDeps, *fakeWarmers) {
	warmers := &fakeWarmers{}
	deps := ProvisionDeps{
		Sources:     sources,
		Checkouts:   checkouts,
		Manifests:   manifests,
		Warmers:     warmers,
		Prover:      prover,
		Reader:      reader,
		Writer:      writerFor(w),
		DockerCheck: okDocker,
	}
	return deps, warmers
}

// firstObject returns the object of the first stamped triple for predicate, or "".
func firstObject(calls []replaceCall, predicate string) string {
	for _, c := range calls {
		for _, tr := range c.add {
			if tr.Predicate == predicate {
				if s, ok := tr.Object.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// removed reports that predicate is absent from the entity after the LAST write.
//
// Under ReplaceOwned each write clears sandbox-provisioner's whole four-predicate
// group and re-adds only its Desired, so survival is decided by the FINAL mutation
// alone — not by absence from every one. (An earlier version scanned all calls,
// which would wrongly report "not removed" for a ready()-then-block() sequence
// where block's wipe did clear it.) It is also scoped to the owner's group: a
// predicate outside it was never in the wipe's blast radius, so this write had no
// power to clear it and reporting true would be a lie.
func removed(calls []replaceCall, predicate string) bool {
	if len(calls) == 0 {
		return false // nothing was written, so nothing was cleared
	}
	if !slices.Contains(ownedGroup, predicate) {
		return false // outside sandbox-provisioner's group — not this write's to clear
	}
	for _, t := range calls[len(calls)-1].add {
		if t.Predicate == predicate {
			return false
		}
	}
	return true
}

// --- tests ---

// A proven-cold environment with a sandbox-scope tier stamps the full readiness
// package (ready + attestation), with the harness Source on the run entity (G5),
// clearing any prior block.
func TestReadyStampsAttestationPackage(t *testing.T) {
	w := &fakeWriter{}
	prover := &fakeProver{baseline: readyBaseline("sha256:abc123")}
	deps, warmers := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"}, fakeManifests{m: harness.GoProfile()}, prover, fakeReader{}, w)

	res, err := Provision(context.Background(), deps, runEntity)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !res.Ready || res.NoOp {
		t.Fatalf("res = %+v, want Ready=true NoOp=false", res)
	}
	if got := firstObject(w.calls, ReadyPredicate); got != "true" {
		t.Errorf("%s = %q, want \"true\"", ReadyPredicate, got)
	}
	// A proven-cold baseline stands the WARM dev container up (the loop measures in it),
	// over the SAME checkout and declared image, before sandbox.ready is stamped (7B).
	if !warmers.called {
		t.Error("a proven-cold baseline must stand up the warm dev container before stamping ready")
	}
	if warmers.gotDir != "/checkout" {
		t.Errorf("warm container stood up over %q, want the run's checkout root /checkout", warmers.gotDir)
	}
	if warmers.gotImage != "semdev-sandbox:test" {
		t.Errorf("warm container image = %q, want the baseline's built image ref", warmers.gotImage)
	}
	if got := firstObject(w.calls, AttestationImagePredicate); got != "sha256:abc123" {
		t.Errorf("%s = %q, want the digest pin (harness-derived, G3)", AttestationImagePredicate, got)
	}
	if got := firstObject(w.calls, AttestationTierPredicate); got != "unit" {
		t.Errorf("%s = %q, want the sandbox-scope tier \"unit\"", AttestationTierPredicate, got)
	}
	if !removed(w.calls, BlockedPredicate) {
		t.Errorf("ready must clear any prior %s so a recovered run has no stale block", BlockedPredicate)
	}
	// Every stamped triple carries the harness Source on the run entity (G5/D15).
	for _, c := range w.calls {
		for _, tr := range c.add {
			if tr.Source != Source {
				t.Errorf("triple %s Source = %q, want %q (G5)", tr.Predicate, tr.Source, Source)
			}
			if tr.Subject != runEntity {
				t.Errorf("triple %s subject = %q, want the run entity", tr.Predicate, tr.Subject)
			}
		}
	}
}

// A proven-cold baseline whose WARM container fails to stand up blocks the run
// (park toward the human, a retryable infra fault) — never sandbox.ready: the loop
// must not proceed to measure without a resolvable warm container (SB5).
func TestWarmProvisionFailureBlocks(t *testing.T) {
	w := &fakeWriter{}
	warmers := &fakeWarmers{err: errors.New("docker run: no space left on device")}
	deps := ProvisionDeps{
		Sources:     fakeSources{dir: "/src"},
		Checkouts:   &fakeCheckouts{root: "/checkout"},
		Manifests:   fakeManifests{m: harness.GoProfile()},
		Warmers:     warmers,
		Prover:      &fakeProver{baseline: readyBaseline("sha256:x")},
		Reader:      fakeReader{},
		Writer:      writerFor(w),
		DockerCheck: okDocker,
	}

	res, err := Provision(context.Background(), deps, runEntity)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !warmers.called {
		t.Error("a proven-cold baseline must attempt the warm stand-up")
	}
	if res.Ready {
		t.Error("a failed warm stand-up must NOT report Ready (the loop has no container to measure in)")
	}
	if firstObject(w.calls, ReadyPredicate) != "" {
		t.Error("a failed warm stand-up must NOT stamp sandbox.ready (the loop has no container to measure in)")
	}
	reason := firstObject(w.calls, BlockedPredicate)
	if reason == "" {
		t.Fatal("a failed warm stand-up must stamp sandbox.blocked (park toward the human)")
	}
	if res.Reason != reason {
		t.Errorf("ProvisionResult.Reason = %q, want the stamped block reason %q", res.Reason, reason)
	}
	if !strings.Contains(reason, "human") {
		t.Errorf("a warm stand-up fault (infra) must route toward the human; got: %q", reason)
	}
}

// A built image that did NOT prove cold blocks the run (park toward the operator) —
// never a readiness fact, and any stale readiness is cleared (SB5, no false green).
func TestBaselineNotReadyBlocks(t *testing.T) {
	w := &fakeWriter{}
	notReady := coldproof.Baseline{Image: cleanroom.BuiltImage{Digest: "sha256:x"}, Outcome: verify.OutcomeFail}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"}, fakeManifests{m: harness.GoProfile()}, &fakeProver{baseline: notReady}, fakeReader{}, w)

	if _, err := Provision(context.Background(), deps, runEntity); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if firstObject(w.calls, ReadyPredicate) != "" {
		t.Error("a not-ready baseline must NOT stamp sandbox.ready (no false green, SB5)")
	}
	if firstObject(w.calls, BlockedPredicate) == "" {
		t.Error("a not-ready baseline must stamp sandbox.blocked (park toward the operator)")
	}
	if !removed(w.calls, ReadyPredicate) || !removed(w.calls, AttestationImagePredicate) {
		t.Error("block must clear any stale readiness/attestation")
	}
}

// An infra/transport Retry baseline blocks with a reason routed toward the HUMAN
// (retryable), NOT the operator — a container/network hiccup is not a declared-image
// defect, so it must not send a human to debug the Dockerfile.
func TestRetryBaselineBlocksTowardHuman(t *testing.T) {
	w := &fakeWriter{}
	retry := coldproof.Baseline{Image: cleanroom.BuiltImage{Digest: "sha256:x"}, Outcome: verify.OutcomeRetry}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"}, fakeManifests{m: harness.GoProfile()}, &fakeProver{baseline: retry}, fakeReader{}, w)

	if _, err := Provision(context.Background(), deps, runEntity); err != nil {
		t.Fatalf("provision: %v", err)
	}
	reason := firstObject(w.calls, BlockedPredicate)
	if reason == "" {
		t.Fatal("a retry baseline must block (never proceed on an unproven environment)")
	}
	// A retry routes toward the human and is labelled retryable — NOT the
	// operator-declared-image-fix phrasing the genuine-Fail path uses.
	if !strings.Contains(reason, "human") || !strings.Contains(reason, "retry") {
		t.Errorf("a retry (infra) block must route toward the human and say retryable; got: %q", reason)
	}
	if strings.Contains(reason, "fix the declared image") {
		t.Errorf("a retry (infra) block must NOT use the operator declared-image-fix phrasing; got: %q", reason)
	}
	if firstObject(w.calls, ReadyPredicate) != "" {
		t.Error("a retry baseline must NOT stamp sandbox.ready")
	}
}

// An infra/declaration fault from the cold proof (undeclared image, missing secret)
// blocks the run rather than proceeding on an unproven environment.
func TestProverErrorBlocks(t *testing.T) {
	w := &fakeWriter{}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"}, fakeManifests{m: harness.GoProfile()}, &fakeProver{err: errors.New("declare an image")}, fakeReader{}, w)

	if _, err := Provision(context.Background(), deps, runEntity); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if firstObject(w.calls, BlockedPredicate) == "" {
		t.Error("a cold-proof error must stamp sandbox.blocked (fail closed, never proceed)")
	}
	if firstObject(w.calls, ReadyPredicate) != "" {
		t.Error("a cold-proof error must NOT stamp sandbox.ready")
	}
}

// A cold-built environment whose manifest declares NO sandbox-scope tier proving the
// claim is deferred toward the operator (blocked), never gated in-sandbox (SB5).
func TestReadyBaselineButNoSandboxTierBlocks(t *testing.T) {
	w := &fakeWriter{}
	// A manifest whose only tier is operator-ci scope → no in-sandbox proof of the claim.
	m := harness.GoProfile()
	m.Tiers = []harness.Tier{{Name: "sitl", Scope: harness.TierOperatorCI, Proves: []string{harness.ClaimUnit}}}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"}, fakeManifests{m: m}, &fakeProver{baseline: readyBaseline("sha256:x")}, fakeReader{}, w)

	if _, err := Provision(context.Background(), deps, runEntity); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if firstObject(w.calls, ReadyPredicate) != "" {
		t.Error("a claim only an operator-ci tier proves must NOT be sandbox-ready (SB5)")
	}
	if firstObject(w.calls, BlockedPredicate) == "" {
		t.Error("a deferred-only claim must block (deferred toward the operator)")
	}
}

// Docker absent parks the run (blocked), never a false verify (SB5). The injected
// probe returns an error; the proof path is never reached.
func TestDockerAbsentBlocks(t *testing.T) {
	w := &fakeWriter{}
	prover := &fakeProver{baseline: readyBaseline("sha256:x")}
	checkouts := &fakeCheckouts{root: "/checkout"}
	deps := ProvisionDeps{
		Sources:   fakeSources{dir: "/src"},
		Checkouts: checkouts,
		Manifests: fakeManifests{m: harness.GoProfile()},
		Warmers:   &fakeWarmers{},
		Prover:    prover,
		Reader:    fakeReader{},
		Writer:    writerFor(w),
		DockerCheck: func(context.Context, string) error {
			return errors.New("cannot connect to the docker daemon")
		},
	}

	if _, err := Provision(context.Background(), deps, runEntity); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if firstObject(w.calls, BlockedPredicate) == "" {
		t.Error("docker absent must stamp sandbox.blocked (park toward the human, SB5)")
	}
	if prover.called || checkouts.called {
		t.Error("docker absent must short-circuit BEFORE materializing or proving")
	}
}

// The source resolving to nothing (unset/missing target) blocks — a guessed source
// is the sandbox==nil disease (SB5).
func TestSourceResolveFailureBlocks(t *testing.T) {
	w := &fakeWriter{}
	checkouts := &fakeCheckouts{root: "/checkout"}
	deps, _ := newDeps(fakeSources{err: errors.New("no source configured")}, checkouts, fakeManifests{m: harness.GoProfile()}, &fakeProver{}, fakeReader{}, w)

	if _, err := Provision(context.Background(), deps, runEntity); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if firstObject(w.calls, BlockedPredicate) == "" {
		t.Error("an unresolved source must block (never guess a target)")
	}
	if checkouts.called {
		t.Error("a failed source resolve must not materialize a checkout")
	}
}

// Idempotency guard: a run already proven ready is a no-op — Provision does NOT
// re-materialize (which would wipe in-progress apply_patch work) or re-prove.
func TestAlreadyReadyIsNoOp(t *testing.T) {
	w := &fakeWriter{}
	prover := &fakeProver{baseline: readyBaseline("sha256:x")}
	checkouts := &fakeCheckouts{root: "/checkout"}
	reader := fakeReader{triples: []message.Triple{{Predicate: ReadyPredicate, Object: "true"}}}
	deps, _ := newDeps(fakeSources{dir: "/src"}, checkouts, fakeManifests{m: harness.GoProfile()}, prover, reader, w)

	res, err := Provision(context.Background(), deps, runEntity)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !res.NoOp || !res.Ready {
		t.Errorf("res = %+v, want NoOp=true Ready=true", res)
	}
	if prover.called || checkouts.called {
		t.Error("an already-ready run must not re-prove or re-materialize (destructive)")
	}
	if len(w.calls) != 0 {
		t.Errorf("an already-ready run must stamp nothing, got %d writes", len(w.calls))
	}
}

// Docker-gated integration: the FULL provision core proves the committed fixture builds
// cold and stamps a real readiness attestation — materialize (real Checkouts) →
// resolve (real Manifests) → ProveBaseline (real docker cold build) → stamp. This is
// the exact end-to-end path the journey drives, minus NATS.
func TestProvisionRealFixtureReady(t *testing.T) {
	ctx := context.Background()
	if err := cleanroom.DockerAvailable(ctx, "docker"); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	checkouts, err := runspace.NewCheckouts("", cliexec.OSRunner{})
	if err != nil {
		t.Fatalf("new checkouts: %v", err)
	}
	// A REAL warm-sandbox registry: on a proven-cold baseline the core stands up an
	// actual warm docker container here (the loop's measure target). Reaped at the end
	// so this test leaks nothing.
	sandboxes := runspace.NewSandboxes()
	defer func() { _ = sandboxes.CloseAll(context.Background()) }()
	w := &fakeWriter{}
	deps := ProvisionDeps{
		Sources:     fakeSources{dir: fixtureDir(t)},
		Checkouts:   checkouts,
		Manifests:   runspace.Manifests{},
		Warmers:     sandboxes,
		Prover:      DefaultProver(),
		Reader:      fakeReader{},
		Writer:      writerFor(w),
		DockerCheck: cleanroom.DockerAvailable,
	}

	res, provErr := Provision(ctx, deps, runEntity)
	if provErr != nil {
		t.Fatalf("provision: %v", provErr)
	}
	if !res.Ready {
		t.Fatalf("fixture did not prove ready cold — blocked: %s", res.Reason)
	}
	if got := firstObject(w.calls, ReadyPredicate); got != "true" {
		if reason := firstObject(w.calls, BlockedPredicate); reason != "" {
			t.Fatalf("fixture did not prove ready cold — blocked: %s", reason)
		}
		t.Fatalf("%s = %q, want \"true\" (the committed fixture must build cold)", ReadyPredicate, got)
	}
	if firstObject(w.calls, AttestationImagePredicate) == "" {
		t.Error("a ready sandbox must attest the digest-pinned image it proved")
	}
	// The warm dev container is really Up and resolvable — the exact handle measure_task
	// Execs the test command into (proven cold, then reused warm).
	if _, _, rerr := sandboxes.Resolve(ctx, runEntity); rerr != nil {
		t.Errorf("a ready sandbox must leave a resolvable warm container for measure_task: %v", rerr)
	}
}

// fixtureDir returns the absolute path to the committed go-health-class fixture.
func fixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/tools/provisionsandbox → repo root is three levels up.
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return filepath.Join(root, "test", "fixtures", "go-health-class")
}
