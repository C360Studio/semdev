package graphown_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/graph"
	"github.com/c360studio/semstreams/message"
	"github.com/c360studio/semstreams/pkg/errs"
	"github.com/c360studio/semstreams/pkg/projection"

	"github.com/c360studio/semdev/internal/graphown"
)

// The seam every migrated call site writes through. It was shipped untested in the
// first pass of groups 3–5; these pin the four behaviors other packages rely on but
// cannot themselves assert.

type recordingReplacer struct {
	got projection.ReplaceOwnedMutation
	err error
}

func (r *recordingReplacer) ReplaceOwned(_ context.Context, m projection.ReplaceOwnedMutation) (projection.MutationReceipt, error) {
	r.got = m
	if r.err != nil {
		return projection.MutationReceipt{Commit: projection.CommitNotCommitted}, r.err
	}
	return projection.MutationReceipt{Commit: projection.CommitVerified}, nil
}

type stubReader struct {
	entity *graph.EntityState
	err    error
}

func (s stubReader) ReadAuthoritative(context.Context, string) (*graph.EntityState, error) {
	return s.entity, s.err
}

// TestWriterResolvesContractAndGroup pins the whole point of the type: the call site
// supplies only an owner and an entity, and the seam derives the contract. A site
// that hardcoded the contract name would decouple the write from entityClass — the
// one thing the offline censuses cannot check (design D3b).
func TestWriterResolvesContractAndGroup(t *testing.T) {
	r := &recordingReplacer{}
	w := graphown.NewWriter("measurement-harness", r)
	tr := message.Triple{Subject: runEntity, Predicate: "measurement.result.passed", Object: "true"}
	if err := w.Replace(context.Background(), runEntity, []message.Triple{tr}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if r.got.Contract != "measurement-harness" {
		t.Errorf("contract = %q, want %q", r.got.Contract, "measurement-harness")
	}
	if r.got.Group != graphown.OwnedGroup {
		t.Errorf("group = %q, want %q — the group selection must be explicit, not the client's one-group default", r.got.Group, graphown.OwnedGroup)
	}
	if r.got.EntityID != runEntity || len(r.got.Desired) != 1 {
		t.Errorf("mutation = %+v, want the one desired triple on %s", r.got, runEntity)
	}
	// Metadata stays ZERO so each triple keeps its own Source/Timestamp — setting
	// Metadata.Source would make the client REJECT a triple carrying a different
	// one, and semdev's triples stamp their own writer (G5).
	if r.got.Metadata.Source != "" || !r.got.Metadata.Timestamp.IsZero() {
		t.Errorf("metadata = %+v, want zero — a non-zero Source rejects triples that carry their own", r.got.Metadata)
	}
}

// TestWriterRejectsAnEntityOutsideTheOwnersClass is the behavioral proof D3b rests
// on, at the seam itself: a write to an entity class the owner does not claim fails
// AT THE CALL SITE with both names in the message, rather than as the mutation
// client's generic rejection inside a retry loop.
func TestWriterRejectsAnEntityOutsideTheOwnersClass(t *testing.T) {
	r := &recordingReplacer{}
	w := graphown.NewWriter("measurement-harness", r)
	err := w.Replace(context.Background(), loopEntity, nil)
	if err == nil {
		t.Fatal("writing a run-class owner's fact onto a LOOP entity must fail")
	}
	if !strings.Contains(err.Error(), "measurement-harness") || !strings.Contains(err.Error(), loopEntity) {
		t.Errorf("error %q must name both the owner and the entity", err)
	}
	if r.got.Contract != "" {
		t.Error("a rejected write must not reach the mutation client")
	}
}

// TestNilWriterFailsLoudly pins the schema-only census posture: a tool registered
// without a NATS client holds a nil writer and must FAIL on a write, never drop the
// fact silently.
func TestNilWriterFailsLoudly(t *testing.T) {
	var w *graphown.Writer
	if err := w.Replace(context.Background(), runEntity, nil); err == nil {
		t.Error("a nil writer must fail loudly on Replace, not silently drop the fact")
	}
	// A Writer wrapping a NIL client must fail the same way — this is the shape that
	// would otherwise pass a tool's `writer == nil` guard and fail later, at the write.
	if err := graphown.NewWriter("measurement-harness", nil).Replace(context.Background(), runEntity, nil); err == nil {
		t.Error("a Writer wrapping a nil client must fail loudly on Replace")
	}
	if _, err := w.ReadOwnedPredicates(context.Background(), runEntity, "task.spec."); err == nil {
		t.Error("a nil writer must fail loudly on ReadOwnedPredicates")
	}
	// A write-only Writer has no reader bound; asking it to read must say so.
	wo := graphown.NewWriter("task-projector", &recordingReplacer{})
	if _, err := wo.ReadOwnedPredicates(context.Background(), runEntity, "task.spec."); err == nil {
		t.Error("a write-only Writer must reject a read-back rather than return an empty set")
	}
}

// TestReadOwnedPredicatesScopesToThePrefix pins the filter whose ABSENCE inverts a
// caller: project_tasks gates immutability on the result being empty, so an
// unscoped read (which returns every owner's predicates) would make it refuse on
// every run. The empty prefix is rejected for the same reason.
func TestReadOwnedPredicatesScopesToThePrefix(t *testing.T) {
	entity := &graph.EntityState{ID: runEntity, Triples: []message.Triple{
		{Subject: runEntity, Predicate: "task.spec.goal", Object: "g"},
		{Subject: runEntity, Predicate: "task.spec.budget", Object: "3"},
		{Subject: runEntity, Predicate: "measurement.result.passed", Object: "true"},
	}}
	got, err := graphown.ReadOwnedPredicates(context.Background(), stubReader{entity: entity}, runEntity, "task.spec.")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []string{"task.spec.budget", "task.spec.goal"} // sorted, and NOT measurement.*
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v — an unscoped result inverts project_tasks' emptiness gate", got, want)
	}
	if _, err := graphown.ReadOwnedPredicates(context.Background(), stubReader{entity: entity}, runEntity, ""); err == nil {
		t.Error("an empty prefix must be REJECTED — it returns predicates the caller does not own")
	}
}

// TestWriteErrorKindSeparatesWiringBugsFromTransport pins the retry posture. A
// mutation the client can never accept must not be retried to the loop's iteration
// cap on paid tokens; a genuinely transient one must stay retryable.
func TestWriteErrorKindSeparatesWiringBugsFromTransport(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want agentic.ToolErrorKind
	}{
		{"invalid mutation is a wiring bug", &projection.MutationError{Kind: projection.MutationInvalid}, agentic.ToolErrorInternal},
		{"stale owner token must not be retried", &projection.MutationError{Kind: projection.MutationStaleOwnerToken}, agentic.ToolErrorInternal},
		{"committed-unverified will not converge on retry", &projection.MutationError{Kind: projection.MutationCommittedUnverified}, agentic.ToolErrorInternal},
		{"unavailable is transport", &projection.MutationError{Kind: projection.MutationUnavailable}, agentic.ToolErrorNetwork},
		{"commit-unknown is transport (ReplaceOwned is idempotent)", &projection.MutationError{Kind: projection.MutationCommitUnknown}, agentic.ToolErrorNetwork},
		{"a classified handler error is internal", &errs.ClassifiedError{Code: "entity_not_found"}, agentic.ToolErrorInternal},
		{"a graphown resolution failure is a wiring bug", errors.New("graphown: owner \"x\" has no projection contract"), agentic.ToolErrorInternal},
		{"an unknown transport error stays retryable", errors.New("connection reset"), agentic.ToolErrorNetwork},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := graphown.WriteErrorKind(tc.err); got != tc.want {
				t.Errorf("WriteErrorKind = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBindOwnersRejectsASecondBindInProcess pins that a FAILED bind leaves no claim
// residue: both binds here fail at the nil-client check, and the second must not
// report ErrOwnersAlreadyBoundInProcess — a transient boot failure must never poison
// the owner for the rest of the process.
//
// It cannot exercise the LIVE-claim rejection: BindOwners checks the client for nil
// BEFORE taking the claim, so no claim is ever held here. That half — and the
// release/re-claim semantics — is pinned in-package by TestInProcessClaimLedger,
// which drives the claim ledger directly.
func TestBindOwnersRejectsASecondBindInProcess(t *testing.T) {
	t.Cleanup(graphown.ResetInProcessBindingsForTest)
	graphown.ResetInProcessBindingsForTest()

	// First bind fails at the NATS step (nil client), and must RELEASE its claim so
	// a failed bind does not poison the owner for the rest of the process.
	if _, err := graphown.BindOwners(context.Background(), nil, nil, "measurement-harness"); err == nil {
		t.Fatal("binding with a nil NATS client must fail")
	}
	if _, err := graphown.BindOwners(context.Background(), nil, nil, "measurement-harness"); err == nil {
		t.Fatal("second bind must still fail on the nil client")
	} else if errors.Is(err, graphown.ErrOwnersAlreadyBoundInProcess) {
		t.Error("a FAILED bind must release its in-process claim — otherwise a transient boot failure permanently blocks the owner")
	}
}

// TestRequireBoundNamesTheMissingOwners pins the boot census: an owner that failed
// to bind, or a typo'd Source, must fail at boot rather than as a nil writer at the
// first write inside a station handler that can only log.
func TestRequireBoundNamesTheMissingOwners(t *testing.T) {
	var none *graphown.Clients // the census path: nothing bound
	err := none.RequireBound("measurement-harness", "route-mirror")
	if err == nil {
		t.Fatal("RequireBound must fail when nothing is bound")
	}
	for _, want := range []string{"measurement-harness", "route-mirror"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the missing owner %q", err, want)
		}
	}
	if err := none.RequireBound(); err != nil {
		t.Errorf("RequireBound() with no owners wanted must pass, got %v", err)
	}
	if got := none.Writer("measurement-harness"); got != nil {
		t.Error("a nil *Clients must yield a NIL *Writer — a Writer wrapping a nil client would pass a tool's nil guard and fail later, at the write")
	}
}
