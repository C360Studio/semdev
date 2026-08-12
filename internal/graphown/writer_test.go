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

// The seam every migrated call site writes through. These pin the behaviors other
// packages rely on but cannot themselves assert.

// recordingReconciler scripts one error per attempt (nil = success) and records
// the LAST mutation, so the retry pins can assert both the attempt count and the
// final desired set.
type recordingReconciler struct {
	got    projection.ReconcileMutation
	calls  int
	script []error
}

func (r *recordingReconciler) Reconcile(_ context.Context, m projection.ReconcileMutation) (projection.MutationReceipt, error) {
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

type stubReader struct {
	entity *graph.EntityState
	err    error
}

func (s stubReader) ReadAuthoritative(context.Context, string) (*graph.ExactEntity, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.entity == nil {
		return nil, nil
	}
	return &graph.ExactEntity{Entity: s.entity, KVRevision: 1}, nil
}

// TestWriterResolvesContractAndGroup pins the whole point of the type: the call site
// supplies only an owner and an entity, and the seam derives the contract. A site
// that hardcoded the contract name would decouple the write from entityClass — the
// one thing the offline censuses cannot check (design D3b).
func TestWriterResolvesContractAndGroup(t *testing.T) {
	r := &recordingReconciler{}
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
	if r.calls != 1 {
		t.Errorf("a clean write must issue exactly one reconcile, got %d", r.calls)
	}
}

// TestWriterRejectsAnEntityOutsideTheOwnersClass is the behavioral proof D3b rests
// on, at the seam itself: a write to an entity class the owner does not claim fails
// AT THE CALL SITE with both names in the message, rather than as the mutation
// client's generic rejection inside a retry loop.
func TestWriterRejectsAnEntityOutsideTheOwnersClass(t *testing.T) {
	r := &recordingReconciler{}
	w := graphown.NewWriter("measurement-harness", r)
	err := w.Replace(context.Background(), loopEntity, nil)
	if err == nil {
		t.Fatal("writing a run-class owner's fact onto a LOOP entity must fail")
	}
	if !strings.Contains(err.Error(), "measurement-harness") || !strings.Contains(err.Error(), loopEntity) {
		t.Errorf("error %q must name both the owner and the entity", err)
	}
	if r.calls != 0 {
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
	wo := graphown.NewWriter("task-projector", &recordingReconciler{})
	if _, err := wo.ReadOwnedPredicates(context.Background(), runEntity, "task.spec."); err == nil {
		t.Error("a write-only Writer must reject a read-back rather than return an empty set")
	}
}

// TestReplaceRetriesRevisionConflictBounded pins the D2 retry: a revision conflict
// is interleaving noise under G5 (another writer bumped the ENTITY, never this
// group), so the seam re-enters Reconcile — which re-reads and re-fences — up to
// three attempts total, and the write lands with the caller's desired set intact.
func TestReplaceRetriesRevisionConflictBounded(t *testing.T) {
	conflict := &projection.MutationError{Kind: projection.MutationRevisionConflict}
	r := &recordingReconciler{script: []error{conflict, conflict, nil}}
	w := graphown.NewWriter("measurement-harness", r)
	tr := message.Triple{Subject: runEntity, Predicate: "measurement.result.passed", Object: "true"}
	if err := w.Replace(context.Background(), runEntity, []message.Triple{tr}); err != nil {
		t.Fatalf("two conflicts then success must land the write, got: %v", err)
	}
	if r.calls != 3 {
		t.Errorf("attempts = %d, want 3 (two retries after two conflicts)", r.calls)
	}
	if len(r.got.Desired) != 1 || r.got.Desired[0].Predicate != "measurement.result.passed" {
		t.Errorf("final attempt carried %+v, want the caller's desired set unchanged", r.got.Desired)
	}
}

// TestReplaceSurfacesExhaustedRevisionConflict pins the exhaustion half: three
// conflicts surface the classified error to the caller UNCHANGED — the seam never
// converts exhaustion into silence, and the caller's retry/park routing owns it.
func TestReplaceSurfacesExhaustedRevisionConflict(t *testing.T) {
	conflict := &projection.MutationError{Kind: projection.MutationRevisionConflict}
	r := &recordingReconciler{script: []error{conflict, conflict, conflict}}
	w := graphown.NewWriter("measurement-harness", r)
	err := w.Replace(context.Background(), runEntity, nil)
	if err == nil {
		t.Fatal("three conflicts must surface, never silently succeed")
	}
	var me *projection.MutationError
	if !errors.As(err, &me) || me.Kind != projection.MutationRevisionConflict {
		t.Errorf("error %v must carry the classified revision-conflict unchanged", err)
	}
	if r.calls != 3 {
		t.Errorf("attempts = %d, want exactly 3 — unbounded spinning hides sustained contention", r.calls)
	}
}

// TestReplaceRetriesTransportKinds pins parity with the deleted framework retry:
// the pre-beta.160 write path rode out graph-ingest blips via the client's retry
// config, which beta.160's single-request client no longer carries. A no-responder
// (the write did not land) and a commit-unknown (it may have landed, and a
// reconcile is an idempotent full-group set) both converge on a later attempt. A
// bug-shaped kind must NOT be retried.
func TestReplaceRetriesTransportKinds(t *testing.T) {
	unavailable := &projection.MutationError{Kind: projection.MutationUnavailable}
	r := &recordingReconciler{script: []error{unavailable, nil}}
	w := graphown.NewWriter("measurement-harness", r)
	if err := w.Replace(context.Background(), runEntity, nil); err != nil {
		t.Fatalf("one no-responder then success must land the write, got: %v", err)
	}
	if r.calls != 2 {
		t.Errorf("attempts = %d, want 2", r.calls)
	}

	invalid := &projection.MutationError{Kind: projection.MutationInvalid}
	r2 := &recordingReconciler{script: []error{invalid, nil}}
	w2 := graphown.NewWriter("measurement-harness", r2)
	if err := w2.Replace(context.Background(), runEntity, nil); err == nil {
		t.Fatal("an invalid mutation is a wiring bug and must surface on the FIRST attempt")
	}
	if r2.calls != 1 {
		t.Errorf("attempts = %d, want 1 — retrying a mutation the client can never accept burns the caller's budget", r2.calls)
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

// TestReadOwnedPredicatesMapsNotFoundToEmpty pins the beta.160 semantic shift: the
// authority read now CLASSIFIES a missing entity as not-found where the old
// surface returned an empty entity. The emptiness-gating callers (project_tasks'
// immutability, check_floors' clear) treat "not born yet" as "nothing owned on
// it", so absence maps to an empty read here — and every OTHER failure stays loud.
func TestReadOwnedPredicatesMapsNotFoundToEmpty(t *testing.T) {
	notFound := &projection.MutationError{Kind: projection.MutationNotFound}
	got, err := graphown.ReadOwnedPredicates(context.Background(), stubReader{err: notFound}, runEntity, "task.spec.")
	if err != nil {
		t.Fatalf("a missing entity must read as EMPTY, not fail: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	boom := &projection.MutationError{Kind: projection.MutationInternal}
	if _, err := graphown.ReadOwnedPredicates(context.Background(), stubReader{err: boom}, runEntity, "task.spec."); err == nil {
		t.Error("a non-absence read failure must stay loud — mapping it to empty would invert the immutability gate")
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
		{"not-found on a write is wiring, not timing", &projection.MutationError{Kind: projection.MutationNotFound}, agentic.ToolErrorInternal},
		{"strict-create conflict is unreachable on the reconcile lane", &projection.MutationError{Kind: projection.MutationConflict}, agentic.ToolErrorInternal},
		{"revision-conflict past the seam's retry stays retryable", &projection.MutationError{Kind: projection.MutationRevisionConflict}, agentic.ToolErrorNetwork},
		{"unavailable is transport", &projection.MutationError{Kind: projection.MutationUnavailable}, agentic.ToolErrorNetwork},
		{"commit-unknown is transport (a reconcile is idempotent)", &projection.MutationError{Kind: projection.MutationCommitUnknown}, agentic.ToolErrorNetwork},
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

// TestRequireWritersNamesTheMissingOwners pins the boot census: a Source with no
// derived contract, or a typo'd Source, must fail at boot rather than as a nil
// writer at the first write inside a station handler that can only log.
func TestRequireWritersNamesTheMissingOwners(t *testing.T) {
	var none *graphown.Clients // the census path: nothing constructed
	err := none.RequireWriters("measurement-harness", "route-mirror")
	if err == nil {
		t.Fatal("RequireWriters must fail when nothing is constructed")
	}
	for _, want := range []string{"measurement-harness", "route-mirror"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the missing owner %q", err, want)
		}
	}
	if err := none.RequireWriters(); err != nil {
		t.Errorf("RequireWriters() with no owners wanted must pass, got %v", err)
	}
	if got := none.Writer("measurement-harness"); got != nil {
		t.Error("a nil *Clients must yield a NIL *Writer — a Writer wrapping a nil client would pass a tool's nil guard and fail later, at the write")
	}
}
