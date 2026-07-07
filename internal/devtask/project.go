// Package devtask is the deterministic projection at semdev's apply gate (T2/T3):
// it turns an approved OpenSpec change's authored, execution-rich task facts into
// immutable task.spec facts the dev loop converges on and cannot redefine. It
// plans nothing — create_change owns spec quality — it only enforces the Karpathy
// task schema at stamp time and clamps the per-task iteration budget to the
// structural bound [1,5], so a run cannot loop unbounded.
//
// The schema is enforced, not defaulted: a task authored without a required field
// (an explicit assumptions and non_goals list, at least one target_file, a
// test_command, and an iteration budget) is NOT stamped with a hiding default —
// the whole projection fails toward the human (design D14; tasks 6.1–6.3). Status
// is never authored here either: dev-from-task derives it from execution markers
// (task.attempt), so this projection introduces no second planning state machine.
package devtask

import (
	"fmt"
	"slices"
	"strings"
)

// Budget bounds. The clamp ceiling is the structural bound on the dev loop: a
// task cannot request more than MaxBudget iterations, and escalation fires by
// iteration MaxBudget+1 regardless (task 6.3). MinBudget is the floor — every
// task gets at least one attempt.
const (
	MinBudget = 1
	MaxBudget = 5
)

// RawTask is one authored task as it arrives from the approved change's
// execution-rich facts (create_change is the author; design D11/D14). Presence is
// meaningful and load-bearing: a nil Assumptions/NonGoals or a nil Budget means
// the field was not authored, and projection fails toward the human rather than
// stamping a default. An empty (non-nil) Assumptions/NonGoals is honest — the task
// declares it has none — and passes.
type RawTask struct {
	Index       int
	Goal        string
	Assumptions []string // nil = not authored (gap); empty non-nil = authored empty (ok)
	NonGoals    []string // nil = not authored (gap); empty non-nil = authored empty (ok)
	TargetFiles []string // at least one required
	TestCommand string   // required, non-whitespace
	Budget      *int     // nil = not authored (gap); present = clamped into [MinBudget,MaxBudget]
}

// TaskSpec is the immutable projected task the dev loop converges on. Budget is
// the clamped structural bound (always within [MinBudget, MaxBudget]).
type TaskSpec struct {
	Index       int
	Goal        string
	Assumptions []string
	NonGoals    []string
	TargetFiles []string
	TestCommand string
	Budget      int
}

// TaskGap names the missing required fields of one authored task.
type TaskGap struct {
	Index   int
	Missing []string
}

// SchemaError reports the Karpathy-schema gaps across one or more authored tasks.
// It carries every gap (not just the first) so a park-toward-human surfaces the
// whole list and the operator fixes the change once.
type SchemaError struct {
	Gaps []TaskGap
}

func (e *SchemaError) Error() string {
	parts := make([]string, len(e.Gaps))
	for i, g := range e.Gaps {
		parts[i] = fmt.Sprintf("task %d missing %s", g.Index, strings.Join(g.Missing, ", "))
	}
	return "task schema incomplete (fails toward human): " + strings.Join(parts, "; ")
}

// ClampBudget pins an authored iteration budget into the structural bound
// [MinBudget, MaxBudget]. It is a ceiling and a floor, never a default: callers
// MUST reject an ABSENT budget (see Project) before clamping — a missing budget is
// a gap, not a zero to be quietly clamped up.
func ClampBudget(n int) int {
	if n < MinBudget {
		return MinBudget
	}
	if n > MaxBudget {
		return MaxBudget
	}
	return n
}

// Project validates the Karpathy schema of every authored task and, when all
// pass, returns the immutable TaskSpecs (budgets clamped), sorted by index.
//
// Projection is atomic: any task missing a required field fails the WHOLE
// projection with a *SchemaError listing every gap, and returns nil specs — a
// change is stamped in full or not at all, so a partial stamp can never hide a
// gap. tasks must be non-empty and indexed contiguously from 0 (unique,
// non-negative, no holes), so the stamped task.spec.<i> facts and any
// position-derived status are stable and no authored task is silently dropped.
func Project(tasks []RawTask) ([]TaskSpec, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("devtask: no tasks to project — an approved change has at least one task")
	}

	if err := checkIndices(tasks); err != nil {
		return nil, err
	}

	var gaps []TaskGap
	for _, t := range tasks {
		if missing := schemaGaps(t); len(missing) > 0 {
			gaps = append(gaps, TaskGap{Index: t.Index, Missing: missing})
		}
	}
	if len(gaps) > 0 {
		slices.SortFunc(gaps, func(a, b TaskGap) int { return a.Index - b.Index })
		return nil, &SchemaError{Gaps: gaps}
	}

	specs := make([]TaskSpec, len(tasks))
	for i, t := range tasks {
		specs[i] = TaskSpec{
			Index:       t.Index,
			Goal:        strings.TrimSpace(t.Goal),
			Assumptions: slices.Clone(t.Assumptions),
			NonGoals:    slices.Clone(t.NonGoals),
			TargetFiles: slices.Clone(t.TargetFiles),
			TestCommand: strings.TrimSpace(t.TestCommand),
			Budget:      ClampBudget(*t.Budget),
		}
	}
	slices.SortFunc(specs, func(a, b TaskSpec) int { return a.Index - b.Index })
	return specs, nil
}

// checkIndices rejects a task set whose indices are not a contiguous 0..n-1
// permutation — a duplicate, a negative, or a hole. Uniqueness plus "every index
// in [0,n)" is exactly contiguity, and it catches a silently-dropped task (a hole)
// that per-task schema checks would miss.
func checkIndices(tasks []RawTask) error {
	seen := make(map[int]bool, len(tasks))
	for _, t := range tasks {
		if seen[t.Index] {
			return fmt.Errorf("devtask: duplicate task index %d — tasks would collide on task.spec.%d", t.Index, t.Index)
		}
		if t.Index < 0 || t.Index >= len(tasks) {
			return fmt.Errorf("devtask: task index %d out of range for %d task(s) — indices must be contiguous from 0", t.Index, len(tasks))
		}
		seen[t.Index] = true
	}
	return nil
}

// schemaGaps returns the missing required-field names of one authored task, in a
// stable order, so the failure message and the pins are deterministic.
func schemaGaps(t RawTask) []string {
	var missing []string
	if strings.TrimSpace(t.Goal) == "" {
		missing = append(missing, "goal")
	}
	if t.Assumptions == nil {
		missing = append(missing, "assumptions")
	}
	if t.NonGoals == nil {
		missing = append(missing, "non_goals")
	}
	if countNonBlank(t.TargetFiles) == 0 {
		// Presence alone is not enough: a []string{""} would satisfy a length
		// check while stamping a blank target — the hiding default this schema
		// exists to reject. Require at least one non-blank target_file.
		missing = append(missing, "target_files")
	}
	if strings.TrimSpace(t.TestCommand) == "" {
		missing = append(missing, "test_command")
	}
	if t.Budget == nil {
		missing = append(missing, "budget")
	}
	return missing
}

// countNonBlank returns how many of xs are non-whitespace.
func countNonBlank(xs []string) int {
	n := 0
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			n++
		}
	}
	return n
}
