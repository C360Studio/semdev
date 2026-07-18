package devtask

// Fact-key suffixes for the execution-rich per-task facts that live on the run
// entity under openspec.change.<slug>.task.<i>.<suffix>. These are GRAPH-ONLY
// (design: not represented in tasks.md, see internal/openspec.model_change — the
// permanent decision the architect reaffirmed): create_change authors them and the
// task projector reads them into a RawTask. Sharing these consts between the writer
// (create_change) and the reader (the projector tool) keeps the two from drifting.
//
// The <i> key is the SAME global zero-based flat index the format engine keys thin
// task facts by (sections in order, items in order) — so a task's thin facts
// (number/text/section) and its rich facts (below) always share one <i>, and that
// <i> is the projector's contiguous RawTask.Index.
// beta.147 D1: field suffixes are lower-kebab (the canonical predicate grammar
// forbids underscores); target-files/test-command/non-goals were target_files/etc.
const (
	FactTargetFiles = "target-files"
	FactTestCommand = "test-command"
	FactAssumptions = "assumptions"
	FactNonGoals    = "non-goals"
	FactBudget      = "budget"
)

// FactGoal is the goal field name of a PROJECTED task on the run entity under
// task.spec.goal. On the input side the goal is the change task's thin text; the
// projector copies it into task.spec so the frozen task carries its own goal without
// a back-reference to the change.
const FactGoal = "goal"

// TaskSpecPrefix is the owned namespace the PROJECTED task.spec facts live under on
// the run entity: task.spec.<suffix>. It anchors to the task.spec.* vocab family
// (writer task-projector, G5). The projector (project_tasks) writes it; downstream
// tools (measure_task) read the frozen fields back — sharing the prefix between the
// write and read sides keeps them from drifting off the family.
const TaskSpecPrefix = "task.spec."

// TaskSpecKeyPrefix returns the predicate prefix a task's frozen fields are keyed
// under. Single-task at M0 (beta.147 D1): the per-task index is out of the predicate,
// so this is the flat task.spec. prefix (i is retained for caller/schema continuity).
func TaskSpecKeyPrefix(i int) string {
	_ = i
	return TaskSpecPrefix
}

// TaskAttemptPrefix is the owned namespace the attempt counter lives under on the run
// entity. It anchors to the task.attempt.* vocab family (writer dev-dispatch-rule, G5).
// The dispatching rules APPEND one triple to task.attempt.instance per attempt with a
// distinct object (the developer loop instance); the budget route counts the DISTINCT
// OBJECTS via length_*. The rules write the predicate as a JSON literal
// (task.attempt.instance), so this const is the read-side anchor a conformance pin
// cross-checks against.
const TaskAttemptPrefix = "task.attempt."

// TaskAttemptKey returns the attempt-counter predicate task.attempt.instance — the
// multi-valued predicate whose distinct objects the budget route counts. Single-task
// at M0 (beta.147 D1): the index is out of the predicate (i retained for continuity).
func TaskAttemptKey(i int) string {
	_ = i
	return TaskAttemptPrefix + "instance"
}
