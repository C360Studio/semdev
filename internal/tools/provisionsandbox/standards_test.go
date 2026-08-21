package provisionsandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/coldproof"
	"github.com/c360studio/semdev/internal/harness"
	"github.com/c360studio/semdev/internal/secrets"
)

// fakeStandards records the sync call and returns a canned classification.
type fakeStandards struct {
	blockReason string
	err         error
	calls       []standardsCall
	// seenCheckout is set from the checkout root the core hands over, so a test can
	// prove the sync runs with the materialized checkout in hand.
	seenCheckout string
}

type standardsCall struct{ runEntityID, checkoutRoot string }

func (f *fakeStandards) Sync(_ context.Context, runEntityID, checkoutRoot string) (string, error) {
	f.calls = append(f.calls, standardsCall{runEntityID: runEntityID, checkoutRoot: checkoutRoot})
	f.seenCheckout = checkoutRoot
	return f.blockReason, f.err
}

// orderedProver records whether the cold proof ran, so the ordering pins can prove the
// standards sync gates it rather than merely preceding it.
type orderedProver struct {
	inner  Prover
	called bool
}

func (p *orderedProver) ProveBaseline(ctx context.Context, docker, repoRoot string, m harness.Manifest, store secrets.Store) (coldproof.Baseline, error) {
	p.called = true
	return p.inner.ProveBaseline(ctx, docker, repoRoot, m, store)
}

// TestProvisionSyncsStandardsAfterMaterializeBeforeColdProof pins the slot: the sync sees
// the materialized checkout root, and it runs before the expensive cold proof so a repo's
// malformed declaration parks without paying for a docker build.
func TestProvisionSyncsStandardsAfterMaterializeBeforeColdProof(t *testing.T) {
	w := &fakeWriter{}
	std := &fakeStandards{}
	prover := &orderedProver{inner: &fakeProver{baseline: readyBaseline("sha256:x")}}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"},
		fakeManifests{m: harness.GoProfile()}, prover, fakeReader{}, w)
	deps.Standards = std

	res, err := Provision(context.Background(), deps, runEntity)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !res.Ready {
		t.Fatalf("expected a ready provision, got blocked: %s", res.Reason)
	}
	if len(std.calls) != 1 {
		t.Fatalf("expected exactly one standards sync, got %d", len(std.calls))
	}
	if std.calls[0].runEntityID != runEntity {
		t.Errorf("standards sync ran for %q, want the provisioned run %q", std.calls[0].runEntityID, runEntity)
	}
	if std.seenCheckout != "/checkout" {
		t.Errorf("standards sync saw checkout %q, want the materialized root %q — the sync must run "+
			"with the run's own checkout in hand", std.seenCheckout, "/checkout")
	}
}

// TestProvisionBlocksOnMalformedStandardsBeforeAnyBuild is the spec's malformed-parks
// scenario at the provision core: a declaration fault is the REPO's mistake, so it parks
// toward the human with the parser's exact defect — and it must gate the cold proof, not
// merely precede it.
func TestProvisionBlocksOnMalformedStandardsBeforeAnyBuild(t *testing.T) {
	w := &fakeWriter{}
	std := &fakeStandards{blockReason: `standards: check name "Go Vet" is not a lower-kebab token`}
	prover := &orderedProver{inner: &fakeProver{baseline: readyBaseline("sha256:x")}}
	deps, warmers := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"},
		fakeManifests{m: harness.GoProfile()}, prover, fakeReader{}, w)
	deps.Standards = std

	res, err := Provision(context.Background(), deps, runEntity)
	if err != nil {
		t.Fatalf("a malformed standards file must be a STAMPED block, not an error: %v", err)
	}
	if res.Ready {
		t.Fatal("provision reported ready despite a malformed standards file")
	}
	if !strings.Contains(res.Reason, "lower-kebab") {
		t.Errorf("the block reason must carry the parser's exact defect so the human can fix the file; got %q", res.Reason)
	}
	if prover.called {
		t.Error("the cold proof ran despite a malformed standards file — the sync must GATE the build, " +
			"not merely precede it (a docker build for a file we already know is unusable)")
	}
	if warmers.called {
		t.Error("the warm container was stood up despite a malformed standards file")
	}
	if firstObject(w.calls, BlockedPredicate) == "" {
		t.Error("no sandbox.blocked fact was stamped for the malformed standards file")
	}
}

// TestProvisionRetriesOnStandardsSubstrateFault separates the two fault classes. A write or
// transport fault in semdev's OWN substrate is not the repo's mistake: it returns an error so
// the station's bounded retry re-attempts (and, exhausted, parks via station.dispatch.failed).
// Blocking here would park permanently on a NATS hiccup.
func TestProvisionRetriesOnStandardsSubstrateFault(t *testing.T) {
	w := &fakeWriter{}
	std := &fakeStandards{err: errors.New("graph mutation request timed out")}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"},
		fakeManifests{m: harness.GoProfile()}, &fakeProver{baseline: readyBaseline("sha256:x")}, fakeReader{}, w)
	deps.Standards = std

	res, err := Provision(context.Background(), deps, runEntity)
	if err == nil {
		t.Fatalf("a substrate fault must return a retryable error, not a stamped block (got %+v)", res)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("the returned error must carry the underlying fault; got %v", err)
	}
	if firstObject(w.calls, BlockedPredicate) != "" {
		t.Error("a retryable substrate fault stamped sandbox.blocked — that parks the run permanently " +
			"on a transient fault")
	}
}

// TestProvisionBlocksWhenStandardsSeamIsUnwired keeps an unwired seam fail-CLOSED. A nil
// Standards would otherwise skip the target repo's declared law silently, and a run that
// never saw the repo's musts is not a run that satisfied them (SB5).
func TestProvisionBlocksWhenStandardsSeamIsUnwired(t *testing.T) {
	w := &fakeWriter{}
	prover := &orderedProver{inner: &fakeProver{baseline: readyBaseline("sha256:x")}}
	deps, _ := newDeps(fakeSources{dir: "/src"}, &fakeCheckouts{root: "/checkout"},
		fakeManifests{m: harness.GoProfile()}, prover, fakeReader{}, w)
	deps.Standards = nil

	res, err := Provision(context.Background(), deps, runEntity)
	if err != nil {
		t.Fatalf("an unwired seam must be a stamped block, not an error: %v", err)
	}
	if res.Ready {
		t.Fatal("provision reported ready with no standards seam wired — the repo's declared standards " +
			"were never read, and nothing says so")
	}
	if !strings.Contains(res.Reason, "standards") {
		t.Errorf("the block reason must name the unwired standards seam; got %q", res.Reason)
	}
	if prover.called {
		t.Error("the cold proof ran with no standards seam wired")
	}
}

// cleanStandards is the default seam for tests about other provisioning concerns: a repo
// that declares nothing syncs cleanly. It is deliberately NOT the production default —
// Provision blocks on a nil seam, so every real construction has to say what it wired.
type cleanStandards struct{}

func (cleanStandards) Sync(context.Context, string, string) (string, error) { return "", nil }
