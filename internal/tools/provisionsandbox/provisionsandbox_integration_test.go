//go:build integration

// Docker-gated integration tests for provisionsandbox, split out under the `integration`
// build tag so plain `go test ./...` never needs a docker daemon — the same
// convention internal/boot/runtime_integration_test.go established for a live NATS.
//
// These stand up REAL containers and build REAL ~1.29GB images. They previously
// lived untagged alongside the unit tests, where a `DockerAvailable` skip made them
// invisible on a machine with docker down and silently promoted them into the unit
// suite on a machine with docker up. On CI, several such packages ran in PARALLEL and
// exhausted a per-user kernel resource, which surfaced as an ENOSPC nobody could place.
//
// Run them with `task test:integration`.

package provisionsandbox

import (
	"context"
	"testing"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/runspace"
)

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
