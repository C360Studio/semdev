package checkfloors

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/floors"
)

// stubRepoChecks serves canned repo-check findings (or a lane fault).
type stubRepoChecks struct {
	findings []floors.Finding
	err      error
	called   int
}

func (s *stubRepoChecks) Run(context.Context, string) ([]floors.Finding, error) {
	s.called++
	return s.findings, s.err
}

// TestRepoCheckFindingsJoinTheFloorVerdict pins that a repo's declared check routes
// through the SAME aggregate as a built-in floor — there is deliberately no second gate
// to keep in sync, so a required failure must flip floor.finding.rejected.
func TestRepoCheckFindingsJoinTheFloorVerdict(t *testing.T) {
	w := &fakeWriter{}
	repo := &stubRepoChecks{findings: []floors.Finding{
		{Floor: "repo-check:go-vet", Passed: false, Detail: "proven — \"go vet ./...\" FAILED with exit status 2"},
	}}
	res, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, repo, fakeReader{},
		findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", 0)
	if err != nil {
		t.Fatalf("run floors: %v", err)
	}
	if repo.called != 1 {
		t.Fatalf("the repo-checks lane ran %d times, want exactly 1", repo.called)
	}
	if !res.Rejected {
		t.Fatal("a failing REQUIRED repo check did not reject the attempt — the repo's gate is decorative")
	}
	detail := objectOf(w, floors.DetailPredicate)
	if !strings.Contains(detail, "repo-check:go-vet") {
		t.Errorf("the repo check is absent from the durable finding detail; got %q", detail)
	}
}

// TestAdvisoryRepoCheckDoesNotFlipTheVerdict is the other half: a non-required failure is
// recorded and RENDERED as a failure, but must not gate. The rendering assertion matters
// because floor.finding.detail is the only durable record of the distinction — if it said
// "passed" for a failed check, the evidence would be lying in the reader's favour.
func TestAdvisoryRepoCheckDoesNotFlipTheVerdict(t *testing.T) {
	w := &fakeWriter{}
	repo := &stubRepoChecks{findings: []floors.Finding{
		{Floor: "repo-check:gofmt", Passed: false, Advisory: true, Detail: "unproven — \"gofmt -l .\" FAILED with exit status 1"},
	}}
	res, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, repo, fakeReader{},
		findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", 0)
	if err != nil {
		t.Fatalf("run floors: %v", err)
	}
	if res.Rejected {
		t.Fatal("an advisory (non-required) repo check rejected the attempt")
	}
	detail := objectOf(w, floors.DetailPredicate)
	if !strings.Contains(detail, "repo-check:gofmt") {
		t.Fatalf("the advisory check is absent from the record entirely; got %q", detail)
	}
	if strings.Contains(detail, "repo-check:gofmt: passed") {
		t.Errorf("a FAILED advisory check is recorded as passed — the gate and the report may disagree, "+
			"but only in the honest direction; got %q", detail)
	}
	if !strings.Contains(detail, "FAILED") {
		t.Errorf("the advisory failure is not rendered as a failure; got %q", detail)
	}
}

// TestRepoCheckLaneFaultClearsStaleFindings mirrors the resolve-fault pin: a lane that
// could not be evaluated must not leave a PRIOR attempt's pass readable as this one's,
// and must surface as a retryable error rather than an empty (green) finding set.
func TestRepoCheckLaneFaultClearsStaleFindings(t *testing.T) {
	// Seed the stale findings a PRIOR attempt left, so the clear has something to remove —
	// the whole hazard is that those stay readable as this attempt's verdict.
	w := &fakeWriter{owned: []string{floors.AttemptPredicate, floors.RejectedPredicate, floors.DetailPredicate}}
	repo := &stubRepoChecks{err: errors.New("no provision-time standards snapshot")}
	_, err := RunFloors(context.Background(), fakeAttempts{attempt: passingAttempt()}, repo, fakeReader{},
		findingsWriterFor(w), mirrorWriterFor(w), slog.Default(), runEntity, "", 0)
	if err == nil {
		t.Fatal("a repo-checks lane fault produced a floors verdict — an unevaluated gate must never read as passed")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("the fault must carry its cause; got %v", err)
	}
	if len(w.replaces) == 0 {
		t.Fatal("no write occurred, so a prior attempt's findings stand unchanged and stale")
	}
	// The clear is an EMPTY Desired (the group wipe), so nothing is left to read at all —
	// which is the point: absent beats a stale "false".
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == floors.RejectedPredicate {
				t.Errorf("the lane fault stamped %s=%v instead of clearing it — a prior attempt's verdict "+
					"must not survive an unevaluated gate", tr.Predicate, tr.Object)
			}
		}
	}
}

// objectOf returns the last stamped string object for predicate across the fake's writes.
func objectOf(w *fakeWriter, predicate string) string {
	out := ""
	for _, batch := range w.replaces {
		for _, tr := range batch {
			if tr.Predicate == predicate {
				if s, ok := tr.Object.(string); ok {
					out = s
				}
			}
		}
	}
	return out
}
