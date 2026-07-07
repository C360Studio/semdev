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
const (
	FactTargetFiles = "target_files"
	FactTestCommand = "test_command"
	FactAssumptions = "assumptions"
	FactNonGoals    = "non_goals"
	FactBudget      = "budget"
)
