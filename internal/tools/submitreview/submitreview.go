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
	"github.com/c360studio/semstreams/types"
)

// ToolName is the registered tool name and the reviewer's verdict handler.
const ToolName = "submit_review"

// Source is stamped on every review.verdict.<i> AND review.findings.<i> triple. It MUST
// equal the writer declared for those namespaces in internal/vocab (G5) — a conformance
// pin cross-checks it.
const Source = "reviewer-quinn"

// RouteMirrorSource is stamped on the route.* facts submit_review MIRRORS onto its own
// review loop so the rule-native review route (approved / changes_requested) can fire on
// them (design R1). It MUST equal the single writer declared for the route.* mirror
// namespace in internal/vocab (G5). It is a SECOND, distinct Source than reviewer-quinn:
// review.verdict/findings (the substance, on the run) is reviewer-quinn; the route mirror
// (raw COPIES onto the loop, one logical writer route-mirror shared with check_floors) is
// route-mirror — so neither predicate has two writers.
const RouteMirrorSource = "route-mirror"

// The route-mirror predicates submit_review stamps on ITS OWN review loop (not the run):
// route.verdict is the copy of review.verdict.<i>, and route.attempt is the append-mirror
// of task.attempt.<i>'s distinct objects (review cycles share the one attempt budget, R4),
// so the review-route rules can count the budget via length_* on the loop.
const (
	RouteVerdictPredicate = "route.verdict"
	RouteAttemptPrefix    = "route.attempt."
)

// VerdictPrefix is the namespace this tool owns on the run entity: the reviewer's
// current per-task verdict, keyed by task index (review.verdict.<i>). Per-task
// keying makes each task's verdict its own predicate — independently upserted on a
// re-review, mirroring measurement.result.* — so the open_pr gate can require every
// task's verdict without one task clobbering another (the graph merges replace
// per-(subject, predicate)).
const VerdictPrefix = "review.verdict."

// FindingsPrefix is the namespace this tool owns for the reviewer's per-task PROSE
// findings (review.findings.<i>) — the required changes Quinn raised, joined into one
// scalar. A changes_requested re-entry (D16) tells the fresh Amelia to re-read them off
// the run and address them. Same single writer as the verdict (reviewer-quinn).
const FindingsPrefix = "review.findings."

// findingsPredicate returns the per-task findings predicate for a task index.
func findingsPredicate(taskIndex int) string {
	return FindingsPrefix + strconv.Itoa(taskIndex)
}

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
	reader   changefacts.Reader
	writer   agentictools.OwnedFactWriter
	platform types.PlatformMeta // builds the review loop's entity id for the chaining marker
	logger   *slog.Logger
}

// New builds the submit_review executor. reader/writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if either is nil. platform builds the review loop's entity id for
// the dev.reviewed chaining marker.
func New(reader changefacts.Reader, writer agentictools.OwnedFactWriter, platform types.PlatformMeta, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, writer: writer, platform: platform, logger: logger}
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

	if err := e.stampVerdict(ctx, runEntityID, idx, verdict, findings); err != nil {
		return errResult(call, writeErrKind(err), "submit_review: stamp %s on %s: %v", verdictPredicate(idx), runEntityID, err)
	}

	e.logger.Info("submit_review recorded verdict",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", idx),
		slog.String("verdict", verdict),
		slog.Bool("measurement_pass", measurementPass),
		slog.Int("findings", len(findings)))

	// MIRROR the routing inputs onto THIS review loop so the rule-native review route
	// (approved → verify / changes_requested → retry-or-park / no-verdict → park) can fire
	// on them (design R1/R4: a rule reads only the firing entity's triples, so the run-level
	// verdict + attempt facts are copied onto the review loop). RAW copies (never a derived
	// route decision, G2): route.verdict (the copy of review.verdict.<i>) and route.attempt
	// (the append-mirror of the run's task.attempt.<i> — review cycles share the one attempt
	// budget, R4). The verdict (the substance) is written to the run FIRST; the mirror (the
	// chaining signal the route triggers on) follows. Failure posture: a mirror error returns
	// errResult WITHOUT StopLoop, so the loop re-runs (re-stamping idempotently) until it
	// lands — never a silent green. A missing LoopID (unit-test-only) skips the mirror.
	if call.LoopID == "" {
		e.logger.Warn("submit_review: no loop_id on the tool call — skipping the route mirror; the review route will not fire",
			slog.String("run_entity_id", runEntityID), slog.Int("task_index", idx))
	} else {
		loopEntityID, lerr := agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if lerr != nil {
			return errResult(call, agentic.ToolErrorInternal, "submit_review: construct review loop entity id: %v", lerr)
		}
		attempts, aerr := e.readAttemptObjects(ctx, runEntityID, idx)
		if aerr != nil {
			return errResult(call, changefacts.ReadErrorKind(aerr), "submit_review: read task.attempt for the route mirror on %s: %v", runEntityID, aerr)
		}
		now := time.Now().UTC()
		mirror := []message.Triple{{Subject: loopEntityID, Predicate: RouteVerdictPredicate, Object: verdict, Source: RouteMirrorSource, Timestamp: now, Confidence: 1.0}}
		attemptPred := RouteAttemptPrefix + idxStr
		for _, obj := range attempts {
			mirror = append(mirror, message.Triple{Subject: loopEntityID, Predicate: attemptPred, Object: obj, Source: RouteMirrorSource, Timestamp: now, Confidence: 1.0})
		}
		if merr := e.writer.ReplaceTriples(ctx, loopEntityID, mirror, []string{RouteVerdictPredicate}); merr != nil {
			return errResult(call, writeErrKind(merr), "submit_review: stamp the route mirror on %s: %v", loopEntityID, merr)
		}
	}

	summary, _ := json.Marshal(map[string]any{
		"task_index":       idx,
		"verdict":          verdict,
		"approved":         approved,
		"measurement_pass": measurementPass,
		"findings":         findings,
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// stampVerdict upserts review.verdict.<taskIndex> AND review.findings.<taskIndex> on the
// run entity (replace-by-predicate, so a re-review of that task replaces its prior verdict
// and findings rather than appending; other tasks' facts, being distinct predicates, are
// untouched). The findings are Quinn's prose (joined into one scalar) — model JUDGMENT the
// changes_requested re-entry (D16) tells the fresh Amelia to re-read and address.
func (e *Executor) stampVerdict(ctx context.Context, runEntityID string, taskIndex int, verdict string, findings []string) error {
	now := time.Now().UTC()
	mk := func(pred, obj string) message.Triple {
		return message.Triple{Subject: runEntityID, Predicate: pred, Object: obj, Source: Source, Timestamp: now, Confidence: 1.0}
	}
	// review.findings.<i> is always stamped (empty string when Quinn raised none), so a
	// re-review that clears prior findings does not leave a stale set readable.
	triples := []message.Triple{
		mk(verdictPredicate(taskIndex), verdict),
		mk(findingsPredicate(taskIndex), strings.Join(findings, "\n")),
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, triples, nil)
}

// readAttemptObjects reads the distinct objects of the run's task.attempt.<idx> counter
// (each object is a developer-loop instance, one per attempt) so the review-route mirror
// can append them onto the review loop and the route rules count the shared attempt budget
// via length_* (R4: review cycles and measurement retries share the one budget).
func (e *Executor) readAttemptObjects(ctx context.Context, runEntityID string, idx int) ([]string, error) {
	want := "task.attempt." + strconv.Itoa(idx)
	triples, err := e.reader.ReadFacts(ctx, runEntityID, want)
	if err != nil {
		return nil, err
	}
	var objs []string
	for _, tr := range triples {
		if tr.Predicate != want {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			objs = append(objs, s)
		}
	}
	return objs, nil
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
