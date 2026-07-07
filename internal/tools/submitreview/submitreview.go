// Package submitreview is the submit_review tool (harness-measurement, tasks
// 7.3/7.4): the reviewer persona Quinn's gate. Quinn reviews the developed work
// against the immutable task.spec and records a verdict (review.verdict) — but the
// verdict is FLOORED by the harness facts, not by Quinn's prose. That is the whole
// point of gating review on measurements (G3): a false success claim cannot earn an
// approving verdict, however confidently the change describes itself.
//
// The asymmetry is deliberate and structural:
//
//   - Quinn's only input is FINDINGS — required changes it raises reviewing the
//     work. A finding is an ADDITIVE constraint: it can require more, never approve
//     past a failure. The schema takes no outcome/approve field (G3); the verdict is
//     DERIVED here from the measured facts.
//   - Approval requires the deterministic floor: measurement.CanApprove proves every
//     required task (each projected task.spec.<i>) has exactly one passing
//     measurement, re-derived from the raw exit evidence (ignoring any stored
//     passed). A failing or missing measurement blocks approval regardless of
//     findings; an open finding blocks approval regardless of the measurements.
//     approved ⟺ CanApprove(required, observed) ∧ no findings.
//   - Findings NEVER weaken task.spec (7.3): this tool's single writer is
//     reviewer-quinn and it stamps ONLY review.verdict — it holds no writer for
//     task.spec, so a finding is structurally incapable of removing or relaxing a
//     task.spec requirement (G5 single-writer).
//
// It fires no lifecycle transition (G2): it stamps the verdict; the open_pr gate is
// a rule reading review.verdict (wired with the coordinator spawn rules + the
// clean-room verify.result at a later group). It reads evidence it cannot parse as a
// FAILURE, never a defaulted approve (measurement.ResultsFromFacts fails closed).
package submitreview

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// Source is stamped on the review.verdict triple. It MUST equal the writer declared
// for review.verdict in internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "reviewer-quinn"

// VerdictPredicate is the milestone fact this tool owns on the run entity: the
// reviewer's current verdict. Exact predicate → latest-wins (a re-review upserts).
const VerdictPredicate = "review.verdict"

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
	// Findings are the required changes Quinn raises. Absent/empty means none;
	// each is an additive constraint that blocks approval until addressed.
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
	findings := nonBlank(p.Findings)
	if dropped := len(p.Findings) - len(findings); dropped > 0 {
		// A blank finding carries no objection and cannot block (nonBlank is safe —
		// dropping only moves toward approval, never past the measurement floor). But
		// surface it: a finding stripped to whitespace upstream is a would-be block
		// that silently evaporated, worth seeing rather than swallowing.
		e.logger.Warn("submit_review dropped blank findings",
			slog.Int("dropped", dropped), slog.Int("kept", len(findings)))
	}

	// The required work is every projected task. No task.spec is an ordering error,
	// not a verdict — you cannot review work that was never projected.
	specTriples, err := e.reader.ReadFacts(ctx, runEntityID, devtask.TaskSpecPrefix)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "submit_review: read task.spec on %s: %v", runEntityID, err)
	}
	required := requiredTaskIDs(specTriples)
	if len(required) == 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "submit_review: no task.spec on %s — nothing to review (was the change projected?)", runEntityID)
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

	// The floor: every required task has exactly one passing measurement (re-derived
	// from raw exit evidence). Approval also requires that Quinn raised no finding.
	measurementsPass := measurement.CanApprove(required, observed)
	approved := measurementsPass && len(findings) == 0
	verdict := VerdictChangesRequested
	if approved {
		verdict = VerdictApproved
	}

	if err := e.stampVerdict(ctx, runEntityID, verdict); err != nil {
		return errResult(call, writeErrKind(err), "submit_review: stamp %s on %s: %v", VerdictPredicate, runEntityID, err)
	}

	e.logger.Info("submit_review recorded verdict",
		slog.String("run_entity_id", runEntityID),
		slog.String("verdict", verdict),
		slog.Bool("measurements_pass", measurementsPass),
		slog.Int("findings", len(findings)),
		slog.Int("required_tasks", len(required)))

	summary, _ := json.Marshal(map[string]any{
		"verdict":           verdict,
		"approved":          approved,
		"measurements_pass": measurementsPass,
		"required_tasks":    required,
		"findings":          findings,
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// stampVerdict upserts review.verdict on the run entity (replace-by-predicate, so a
// re-review replaces the prior verdict rather than appending a second one).
func (e *Executor) stampVerdict(ctx context.Context, runEntityID, verdict string) error {
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  VerdictPredicate,
		Object:     verdict,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil)
}

// requiredTaskIDs returns the distinct projected task indices (as strings, matching
// measurement Result.TaskID) present under the task.spec.* namespace, sorted. Each
// is a task whose passing measurement CanApprove requires.
func requiredTaskIDs(triples []message.Triple) []string {
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
