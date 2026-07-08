// Package submitreview is the submit_review tool (harness-measurement, task 7.3):
// the reviewer persona Quinn's gate, run PER TASK. Quinn reviews one unit of work —
// one projected task — adversarially (trying to refute the attempt) against its
// immutable task.spec, and records a per-task verdict (review.verdict.<i>) — but the
// verdict is FLOORED by that task's harness fact, not by Quinn's prose. That is the
// whole point of gating review on measurements (G3): a false success claim cannot
// earn an approving verdict, however confidently the change describes itself.
//
// The asymmetry is deliberate and structural:
//
//   - Quinn's inputs are the task selector (task_index) and FINDINGS — required
//     changes it raises reviewing THAT task. A finding is an ADDITIVE constraint: it
//     can require more, never approve past a failure. The schema takes no
//     outcome/approve field (G3); the verdict is DERIVED here from the measured fact.
//   - Approval requires the deterministic floor: measurement.CanApprove over the
//     single reviewed task proves it has exactly one passing measurement, re-derived
//     from the raw exit evidence (ignoring any stored passed). A failing or missing
//     measurement blocks approval regardless of findings; an open finding blocks
//     approval regardless of the measurement. approved ⟺ CanApprove([task], observed)
//     ∧ no findings.
//   - Findings NEVER weaken task.spec (7.3): this tool's single writer is
//     reviewer-quinn and it stamps ONLY review.verdict.<i> — it holds no writer for
//     task.spec, so a finding is structurally incapable of removing or relaxing a
//     task.spec requirement (G5 single-writer).
//
// It fires no lifecycle transition (G2): it stamps the per-task verdict; the open_pr
// gate is a rule that rolls up every review.verdict.* (wired with the coordinator
// spawn rules + the clean-room verify.result at a later group). It reads evidence it
// cannot parse as a FAILURE, never a defaulted approve (ResultsFromFacts fails closed).
package submitreview

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the reviewer's verdict handler.
const ToolName = "submit_review"

// Source is stamped on every review.verdict.<i> triple. It MUST equal the writer
// declared for the review.verdict.* namespace in internal/vocab (G5) — a conformance
// pin cross-checks it.
const Source = "reviewer-quinn"

// VerdictPrefix is the namespace this tool owns on the run entity: the reviewer's
// current per-task verdict, keyed by task index (review.verdict.<i>). Per-task
// keying makes each task's verdict its own predicate — independently upserted on a
// re-review, mirroring measurement.result.* — so the open_pr gate can require every
// task's verdict without one task clobbering another (the graph merges replace
// per-(subject, predicate)).
const VerdictPrefix = "review.verdict."

// verdictPredicate returns the per-task verdict predicate for a task index.
func verdictPredicate(taskIndex int) string {
	return VerdictPrefix + strconv.Itoa(taskIndex)
}

// The two verdicts. A rule gates open_pr on VerdictApproved (wired later).
const (
	VerdictApproved         = "approved"
	VerdictChangesRequested = "changes_requested"
)

// Executor reads the run's task.spec + measurement facts, derives the floored
// verdict, and stamps review.verdict.
type Executor struct {
	reader changefacts.Reader
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// New builds the submit_review executor. reader/writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if either is nil.
func New(reader changefacts.Reader, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, writer: writer, logger: logger}
}

type payload struct {
	// TaskIndex is a pointer so an ABSENT argument is distinguishable from index 0
	// (a valid task). Absent → error; a negative index → error.
	TaskIndex *int `json:"task_index"`
	// Findings are the required changes Quinn raises against THIS task. Absent/empty
	// means none; each is an additive constraint that blocks this task's approval.
	Findings []string `json:"findings"`
}

// Execute derives the review verdict from the run's harness facts and Quinn's
// findings, and stamps review.verdict. approved requires the measurement floor
// (CanApprove) AND no open finding; anything else is changes_requested.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "submit_review: harness not fully wired (reader/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "submit_review: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "submit_review: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "submit_review: decode arguments: %v", err)
	}
	if p.TaskIndex == nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "submit_review: task_index is required")
	}
	idx := *p.TaskIndex
	if idx < 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "submit_review: task_index must be non-negative, got %d", idx)
	}
	idxStr := strconv.Itoa(idx)

	findings := nonBlank(p.Findings)
	if dropped := len(p.Findings) - len(findings); dropped > 0 {
		// A blank finding carries no objection and cannot block (nonBlank is safe —
		// dropping only moves toward approval, never past the measurement floor). But
		// surface it: a finding stripped to whitespace upstream is a would-be block
		// that silently evaporated, worth seeing rather than swallowing.
		e.logger.Warn("submit_review dropped blank findings",
			slog.Int("dropped", dropped), slog.Int("kept", len(findings)), slog.Int("task_index", idx))
	}

	// The reviewed task must be a projected task — you cannot review work that was
	// never projected. Read the task.spec.* namespace and confirm this index is in it.
	specTriples, err := e.reader.ReadFacts(ctx, runEntityID, devtask.TaskSpecPrefix)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "submit_review: read task.spec on %s: %v", runEntityID, err)
	}
	if !slices.Contains(projectedTaskIDs(specTriples), idxStr) {
		return errResult(call, agentic.ToolErrorInvalidArgs, "submit_review: task.spec.%d not on %s — nothing to review (was the change projected, and is %d a real task?)", idx, runEntityID, idx)
	}

	// Reconstruct the measurements. A fact we cannot parse fails the review CLOSED —
	// the reviewer must not approve on evidence it cannot read.
	measTriples, err := e.reader.ReadFacts(ctx, runEntityID, measurement.ResultPrefix)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "submit_review: read measurements on %s: %v", runEntityID, err)
	}
	observed, err := measurement.ResultsFromFacts(measTriples)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "submit_review: unparseable measurement evidence on %s: %v", runEntityID, err)
	}

	// The floor for THIS task: it has exactly one passing measurement (re-derived from
	// raw exit evidence). Approval also requires Quinn raised no finding against it.
	measurementPass := measurement.CanApprove([]string{idxStr}, observed)
	approved := measurementPass && len(findings) == 0
	verdict := VerdictChangesRequested
	if approved {
		verdict = VerdictApproved
	}

	if err := e.stampVerdict(ctx, runEntityID, idx, verdict); err != nil {
		return errResult(call, writeErrKind(err), "submit_review: stamp %s on %s: %v", verdictPredicate(idx), runEntityID, err)
	}

	e.logger.Info("submit_review recorded verdict",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", idx),
		slog.String("verdict", verdict),
		slog.Bool("measurement_pass", measurementPass),
		slog.Int("findings", len(findings)))

	summary, _ := json.Marshal(map[string]any{
		"task_index":       idx,
		"verdict":          verdict,
		"approved":         approved,
		"measurement_pass": measurementPass,
		"findings":         findings,
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// stampVerdict upserts review.verdict.<taskIndex> on the run entity (replace-by-
// predicate, so a re-review of that task replaces its prior verdict rather than
// appending a second one; other tasks' verdicts, being distinct predicates, are
// untouched).
func (e *Executor) stampVerdict(ctx context.Context, runEntityID string, taskIndex int, verdict string) error {
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  verdictPredicate(taskIndex),
		Object:     verdict,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil)
}

// projectedTaskIDs returns the distinct projected task indices (as strings, matching
// measurement Result.TaskID) present under the task.spec.* namespace, sorted — the
// set a reviewed task_index must belong to.
func projectedTaskIDs(triples []message.Triple) []string {
	seen := map[int]bool{}
	for _, tr := range triples {
		rest, ok := strings.CutPrefix(tr.Predicate, devtask.TaskSpecPrefix)
		if !ok {
			continue
		}
		idxStr, _, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		seen[i] = true
	}
	ids := make([]int, 0, len(seen))
	for i := range seen {
		ids = append(ids, i)
	}
	sort.Ints(ids)
	out := make([]string, len(ids))
	for k, i := range ids {
		out[k] = strconv.Itoa(i)
	}
	return out
}

// nonBlank drops empty/whitespace-only findings so a stray "" cannot count as a
// blocking constraint, and returns a non-nil slice for a clean result payload.
func nonBlank(findings []string) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		if strings.TrimSpace(f) != "" {
			out = append(out, f)
		}
	}
	return out
}

// writeErrKind mirrors the sibling tools: a handler-classified graph error is
// internal/ordering, not retryable transport.
func writeErrKind(err error) agentic.ToolErrorKind {
	return changefacts.ReadErrorKind(err)
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
