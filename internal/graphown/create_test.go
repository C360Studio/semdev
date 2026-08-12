package graphown_test

import (
	"context"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

// recordingCreator scripts one error per attempt (nil = success) and records
// the LAST mutation, mirroring recordingReconciler.
type recordingCreator struct {
	got    projection.CreateMutation
	calls  int
	script []error
}

func (r *recordingCreator) Create(_ context.Context, m projection.CreateMutation) (projection.MutationReceipt, error) {
	r.got = m
	r.calls++
	var err error
	if len(r.script) > 0 {
		err, r.script = r.script[0], r.script[1:]
	}
	if err != nil {
		return projection.MutationReceipt{Commit: projection.CommitNotCommitted}, err
	}
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

const admissionEntity = "c360.semdev.forge.intake.event.deadbeef-1"

func admissionMsgType() message.Type {
	return message.Type{Domain: "semdev", Category: "intake_event", Version: "v1"}
}

// TestCreatorResolvesContractAndMetadata pins the birth seam's D3b half: the
// call site supplies only its owner and the entity, the seam derives the birth
// contract, and the metadata is DETERMINISTIC — RequestID is the
// content-derived entity ID (a retried logical create presents the identical
// request) and Source is the owner (matching what each triple stamps, G5).
func TestCreatorResolvesContractAndMetadata(t *testing.T) {
	r := &recordingCreator{}
	c := graphown.NewCreator("admission-check", r)
	tr := message.Triple{Subject: admissionEntity, Predicate: "intake.actor.admitted", Object: "true"}
	if err := c.Create(context.Background(), admissionEntity, admissionMsgType(), []message.Triple{tr}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.got.Contract != "admission-check" {
		t.Errorf("contract = %q, want %q", r.got.Contract, "admission-check")
	}
	if r.got.Metadata.RequestID != admissionEntity || r.got.Metadata.Source != "admission-check" {
		t.Errorf("metadata = %+v, want RequestID=the entity ID and Source=the owner — a regenerated RequestID changes tuple identity across redeliveries", r.got.Metadata)
	}
	if r.got.Entity == nil || r.got.Entity.ID != admissionEntity || len(r.got.Entity.Triples) != 0 {
		t.Errorf("entity = %+v, want the bare envelope (ID+type only) — CreateMutation.Triples is the sole fact source", r.got.Entity)
	}
	if r.calls != 1 {
		t.Errorf("a clean birth must issue exactly one create, got %d", r.calls)
	}
}

// TestCreatorSurfacesConflictUnretried pins the duplicate signal end to end: a
// strict-create CONFLICT is never retried, and it survives the seam's error
// wrap so graphown.IsConflict — the exact check intake's ErrAlreadyRecorded
// mapping keys on — still reads it. If the wrap ever breaks (%v for %w), every
// webhook redelivery becomes NAK-to-exhaustion and the crash-window wake
// republish never fires — offline-invisible without this pin.
func TestCreatorSurfacesConflictUnretried(t *testing.T) {
	conflict := &projection.MutationError{Kind: projection.MutationConflict}
	r := &recordingCreator{script: []error{conflict, nil}}
	c := graphown.NewCreator("admission-check", r)
	err := c.Create(context.Background(), admissionEntity, admissionMsgType(), nil)
	if err == nil {
		t.Fatal("a conflict must surface — it is the caller's duplicate signal")
	}
	if !graphown.IsConflict(err) {
		t.Errorf("IsConflict(%v) = false — the wrap broke the classified chain the intake lane keys ErrAlreadyRecorded on", err)
	}
	if r.calls != 1 {
		t.Errorf("attempts = %d, want 1 — retrying a conflict burns a request on an answer that cannot change", r.calls)
	}
}

// TestCreatorRetriesTransportKindsBounded pins parity with Replace: a
// no-responder (never landed) retries and converges; a commit-unknown retry
// that DID land converges to the conflict signal the caller already handles.
func TestCreatorRetriesTransportKindsBounded(t *testing.T) {
	unavailable := &projection.MutationError{Kind: projection.MutationUnavailable}
	r := &recordingCreator{script: []error{unavailable, nil}}
	c := graphown.NewCreator("admission-check", r)
	if err := c.Create(context.Background(), admissionEntity, admissionMsgType(), nil); err != nil {
		t.Fatalf("one no-responder then success must land the birth, got: %v", err)
	}
	if r.calls != 2 {
		t.Errorf("attempts = %d, want 2", r.calls)
	}

	commitUnknown := &projection.MutationError{Kind: projection.MutationCommitUnknown}
	conflict := &projection.MutationError{Kind: projection.MutationConflict}
	r2 := &recordingCreator{script: []error{commitUnknown, conflict}}
	c2 := graphown.NewCreator("admission-check", r2)
	err := c2.Create(context.Background(), admissionEntity, admissionMsgType(), nil)
	if !graphown.IsConflict(err) {
		t.Errorf("a commit-unknown retry that finds the birth landed must converge to the conflict signal, got: %v", err)
	}
}

// TestCreatorRejectsANonCreateOwner pins the seam gate (review M1): the
// framework client validates predicates against the contract's WHOLE allowed
// set, so without this gate a reconcile-owner's Creator could strict-create a
// bogus entity carrying its group predicates — an entity the rules would act
// on. Misuse fails AT THE WRITE with the owner named, before any wire request.
func TestCreatorRejectsANonCreateOwner(t *testing.T) {
	r := &recordingCreator{}
	c := graphown.NewCreator("station-harness", r)
	err := c.Create(context.Background(), "c360.semdev.agent.chain.execution.run9", admissionMsgType(), nil)
	if err == nil {
		t.Fatal("a non-create owner must not strict-create")
	}
	if !strings.Contains(err.Error(), "station-harness") || !strings.Contains(err.Error(), "create owner") {
		t.Errorf("error %q must name the owner and the gate", err)
	}
	if r.calls != 0 {
		t.Error("a rejected birth must not reach the mutation client")
	}
}

// TestNilCreatorFailsLoudly pins the census posture, including the
// typed-nil-in-interface shape the intake composition uses: a nil *Creator
// assigned into the EntityCreator interface must ERROR at the write, never
// panic and never silently drop the record.
func TestNilCreatorFailsLoudly(t *testing.T) {
	var c *graphown.Creator
	if err := c.Create(context.Background(), admissionEntity, admissionMsgType(), nil); err == nil {
		t.Error("a nil creator must fail loudly on Create")
	}
	if err := graphown.NewCreator("admission-check", nil).Create(context.Background(), admissionEntity, admissionMsgType(), nil); err == nil {
		t.Error("a Creator wrapping a nil client must fail loudly on Create")
	}
	var none *graphown.Clients
	if got := none.Creator("admission-check"); got != nil {
		t.Error("a nil *Clients must yield a NIL *Creator")
	}
}
