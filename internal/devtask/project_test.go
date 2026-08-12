package devtask

import (
	"errors"
	"slices"
	"testing"
)

func budget(n int) *int { return &n }

// mentions reports whether the error records field as missing on the given task.
// Test-only: the pins assert exact gap contents, the production code only formats.
func (e *SchemaError) mentions(index int, field string) bool {
	for _, g := range e.Gaps {
		if g.Index == index && slices.Contains(g.Missing, field) {
			return true
		}
	}
	return false
}

// hasTask reports whether the error records any gap for the given task index.
func (e *SchemaError) hasTask(index int) bool {
	for _, g := range e.Gaps {
		if g.Index == index {
			return true
		}
	}
	return false
}

// karpathyTask returns a fully-authored, schema-valid RawTask so a test can knock
// out exactly one field and prove the gap is caught in isolation.
func karpathyTask() RawTask {
	return RawTask{
		Index:       0,
		Goal:        "Add a /health endpoint",
		Assumptions: []string{"the router is already wired"},
		NonGoals:    []string{},
		TargetFiles: []string{"health.go"},
		TestCommand: "go test ./...",
		Budget:      budget(3),
	}
}

// ClampBudget is the structural bound: a present budget is pinned into [1,5]. It
// is a ceiling AND a floor, never a default — callers reject an absent budget
// before clamping.
func TestClampBudget(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-3, MinBudget},
		{0, MinBudget},
		{1, 1},
		{3, 3},
		{5, 5},
		{6, MaxBudget},
		{100, MaxBudget},
	}
	for _, c := range cases {
		if got := ClampBudget(c.in); got != c.want {
			t.Errorf("ClampBudget(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// A budget above the ceiling is clamped at stamp time; escalation fires by
// iteration 6 regardless (spec scenario: "Budget is clamped at stamp time").
func TestProjectClampsBudgetAtStamp(t *testing.T) {
	rt := karpathyTask()
	rt.Budget = budget(9)
	specs, err := Project([]RawTask{rt})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("want 1 spec, got %d", len(specs))
	}
	if specs[0].Budget != MaxBudget {
		t.Errorf("budget = %d, want clamped to %d", specs[0].Budget, MaxBudget)
	}
}

// A present-but-too-low budget is clamped up to the floor, not failed — only an
// ABSENT budget is a gap.
func TestProjectClampsLowBudgetToFloor(t *testing.T) {
	rt := karpathyTask()
	rt.Budget = budget(0)
	specs, err := Project([]RawTask{rt})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if specs[0].Budget != MinBudget {
		t.Errorf("budget = %d, want clamped to %d", specs[0].Budget, MinBudget)
	}
}

// Spec scenario "Missing test_command fails toward the human": projection does
// not stamp; it returns a schema error naming the gap.
func TestProjectMissingTestCommandFailsTowardHuman(t *testing.T) {
	rt := karpathyTask()
	rt.TestCommand = "   " // whitespace-only is not a command
	specs, err := Project([]RawTask{rt})
	if err == nil {
		t.Fatalf("expected schema error for missing test_command, got specs %+v", specs)
	}
	if specs != nil {
		t.Errorf("failed projection must stamp nothing, got %+v", specs)
	}
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("want *SchemaError, got %T: %v", err, err)
	}
	if !se.mentions(0, "test_command") {
		t.Errorf("gap list %v does not name test_command on task 0", se.Gaps)
	}
}

// A missing budget fails toward the human — it is NOT stamped with a clamped
// default that would hide the gap (spec: "A task missing its budget or
// test_command SHALL fail toward the human rather than be stamped with a default
// that hides the gap").
func TestProjectMissingBudgetFailsTowardHuman(t *testing.T) {
	rt := karpathyTask()
	rt.Budget = nil
	_, err := Project([]RawTask{rt})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("want *SchemaError for absent budget, got %T: %v", err, err)
	}
	if !se.mentions(0, "budget") {
		t.Errorf("gap list %v does not name budget on task 0", se.Gaps)
	}
}

// The Karpathy shape requires at least one target_file.
func TestProjectRequiresTargetFile(t *testing.T) {
	rt := karpathyTask()
	rt.TargetFiles = nil
	_, err := Project([]RawTask{rt})
	var se *SchemaError
	if !errors.As(err, &se) || !se.mentions(0, "target_files") {
		t.Fatalf("want target_files gap, got %v", err)
	}
}

// assumptions and non_goals must be AUTHORED (the field present), but an
// explicit empty list is honest and passes — the task states it has none.
func TestProjectRequiresAuthoredAssumptionsAndNonGoals(t *testing.T) {
	// nil (not authored) → gaps on both
	rt := karpathyTask()
	rt.Assumptions = nil
	rt.NonGoals = nil
	_, err := Project([]RawTask{rt})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("want *SchemaError, got %v", err)
	}
	if !se.mentions(0, "assumptions") || !se.mentions(0, "non_goals") {
		t.Errorf("gaps %v must name both assumptions and non_goals", se.Gaps)
	}

	// present-but-empty → authored, passes
	rt2 := karpathyTask()
	rt2.Assumptions = []string{}
	rt2.NonGoals = []string{}
	if _, err := Project([]RawTask{rt2}); err != nil {
		t.Errorf("empty-but-authored assumptions/non_goals must pass, got %v", err)
	}
}

// Projection is atomic across the change: one gappy task fails the whole
// projection and the error lists EVERY gap, so the operator fixes the change once.
func TestProjectReportsAllGapsAtomically(t *testing.T) {
	good := karpathyTask()
	bad := karpathyTask()
	bad.Index = 1
	bad.TestCommand = ""
	bad.Budget = nil
	specs, err := Project([]RawTask{good, bad})
	if specs != nil {
		t.Errorf("a single gap must fail the whole projection, got %+v", specs)
	}
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("want *SchemaError, got %v", err)
	}
	if !se.mentions(1, "test_command") || !se.mentions(1, "budget") {
		t.Errorf("gaps %v must name both fields on task 1", se.Gaps)
	}
	if se.hasTask(0) {
		t.Errorf("task 0 is complete and must not appear in gaps %v", se.Gaps)
	}
}

// An empty task list is not a valid projection — an approved change has tasks.
func TestProjectRejectsEmptyTaskList(t *testing.T) {
	if _, err := Project(nil); err == nil {
		t.Fatal("expected error projecting zero tasks")
	}
}

// Duplicate indices are an authoring integrity failure — two tasks would collide
// on the same task.spec.<i> namespace.
func TestProjectRejectsDuplicateIndex(t *testing.T) {
	a := karpathyTask()
	b := karpathyTask() // same Index 0
	if _, err := Project([]RawTask{a, b}); err == nil {
		t.Fatal("expected error for duplicate task index")
	}
}

// Non-contiguous indices (a hole) are rejected — a hole means an authored task
// was silently dropped, which per-task schema checks would miss.
func TestProjectRejectsNonContiguousIndices(t *testing.T) {
	a := karpathyTask()
	a.Index = 0
	b := karpathyTask()
	b.Index = 2 // hole at 1
	if _, err := Project([]RawTask{a, b}); err == nil {
		t.Fatal("expected error for non-contiguous task indices {0,2}")
	}
}

// A negative index is out of the task.spec.<i> domain.
func TestProjectRejectsNegativeIndex(t *testing.T) {
	a := karpathyTask()
	a.Index = -1
	if _, err := Project([]RawTask{a}); err == nil {
		t.Fatal("expected error for negative task index")
	}
}

// A target_files list of only blanks is a hidden gap: presence without a real
// target. It fails the same as an absent target_files.
func TestProjectRejectsBlankTargetFiles(t *testing.T) {
	rt := karpathyTask()
	rt.TargetFiles = []string{"", "  "}
	_, err := Project([]RawTask{rt})
	var se *SchemaError
	if !errors.As(err, &se) || !se.mentions(0, "target_files") {
		t.Fatalf("want target_files gap for all-blank targets, got %v", err)
	}
}

// The projected spec does not alias the caller's slices — a later mutation of the
// input must not reach into the "immutable" TaskSpec.
func TestProjectClonesSlices(t *testing.T) {
	rt := karpathyTask()
	rt.Assumptions = []string{"original"}
	specs, err := Project([]RawTask{rt})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	rt.Assumptions[0] = "mutated"
	if specs[0].Assumptions[0] != "original" {
		t.Errorf("TaskSpec aliased the caller's slice: got %q", specs[0].Assumptions[0])
	}
}

// Projection returns tasks in index order regardless of input order, so the
// stamped task.spec.<i> facts and any position-derived status are stable.
func TestProjectSortsByIndex(t *testing.T) {
	a := karpathyTask()
	a.Index = 2
	b := karpathyTask()
	b.Index = 0
	c := karpathyTask()
	c.Index = 1
	specs, err := Project([]RawTask{a, b, c})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	for i, s := range specs {
		if s.Index != i {
			t.Errorf("specs[%d].Index = %d, want %d", i, s.Index, i)
		}
	}
}

// A complete, in-range task round-trips its authored values unchanged (except the
// budget, which is already in range).
func TestProjectHappyPath(t *testing.T) {
	specs, err := Project([]RawTask{karpathyTask()})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	s := specs[0]
	if s.Goal != "Add a /health endpoint" || s.TestCommand != "go test ./..." || s.Budget != 3 {
		t.Errorf("unexpected projected spec %+v", s)
	}
	if len(s.TargetFiles) != 1 || s.TargetFiles[0] != "health.go" {
		t.Errorf("target files not carried through: %+v", s.TargetFiles)
	}
}
