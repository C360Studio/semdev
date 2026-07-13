// Package checkcoherence is the check_coherence tool (dev-from-task, group 8D): the m0
// open_pr COHERENCE GATE. Before a run delivers a PR, it rolls up the three independent
// delivery signals — the clean-room cold verify.result, the OpenSpec openspec.validated,
// and EVERY projected task's review.verdict — and derives coherent (open the PR) or blocked
// (park toward the human). It is check_gate's twin: a tool, not rule conditions, because a
// rule firing on a LOOP entity cannot read RUN facts, the review roll-up counts projected
// tasks vs approved verdicts (not a length_* on one predicate), and a wildcard field-to-
// field compare floods semstreams #519 — all three force the roll-up into Go.
//
// It FAILS CLOSED (D16 / SB5): a non-pass or absent verify, an unvalidated change, no
// projected task, or ANY projected task without an approved verdict all block — a missing
// signal never opens a PR (semspec encoded "couldn't prove" as a pass; this inverts that).
// The decision is HARNESS-DERIVED (G3 — the schema takes no arguments; every input is a
// fact the harness stamped) and fires no lifecycle transition (G2): it records the decision
// as evidence + a loop marker, and two router rules act on the marker (coherent → open_pr,
// blocked → the run.awaiting_human park). Single G5 writer of pr.coherence.* and
// dev.coherence_decided (coherence-tools).
package checkcoherence

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/types"
)

// ToolName is the registered tool name and the run's coherence-gate handler.
const ToolName = "check_coherence"

// Source is stamped on every pr.coherence.* triple AND the dev.coherence_decided loop
// marker. It MUST equal the single writer declared for those predicates in internal/vocab
// (G5) — a conformance pin cross-checks it.
const Source = "coherence-tools"

// Fact predicates. VerifyResultPred / ValidatedPred mirror the sibling tools' terminal
// facts (duplicated as consts to keep the read side from importing every producer); the
// review/task-spec prefixes come from their owning packages.
const (
	VerifyResultPred = "verify.result"
	ValidatedPred    = "openspec.validated"
	reviewPrefix     = "review.verdict."
)

// CoherencePrefix is the owned namespace the gate's decision evidence lives under on the
// run entity: pr.coherence.<field>. Honest evidence (G7) the human who gets parked, and the
// delivery step, read.
const CoherencePrefix = "pr.coherence."

// FactDecision and FactReason are the two evidence fields the coherence gate stamps
// under CoherencePrefix on the run entity (G7).
const (
	FactDecision = "decision" // coherent | blocked
	FactReason   = "reason"   // the harness's plain-language justification (G7)
)

// DecidedMarker is the chaining marker check_coherence stamps on ITS OWN loop entity (value
// = the decision) — the signal the two router rules (dev-from-task/12a/b) fire on. Mirrors
// check_gate's dev.gate_decision.
const DecidedMarker = "dev.coherence_decided"

// Executor reads the run's delivery signals, derives coherence, and records it.
type Executor struct {
	reader   changefacts.Reader
	writer   agentictools.OwnedFactWriter
	platform types.PlatformMeta
	logger   *slog.Logger
}

// New builds the check_coherence executor. reader/writer may be nil for schema-only
// registration; Execute fails loudly if either is nil. platform builds the coherence
// loop's entity id for the dev.coherence_decided marker.
func New(reader changefacts.Reader, writer agentictools.OwnedFactWriter, platform types.PlatformMeta, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, writer: writer, platform: platform, logger: logger}
}

// Execute reads verify.result, openspec.validated, the projected task.spec set, and each
// review.verdict off the run entity, derives the coherence route (fail-closed), stamps the
// decision as pr.coherence.* evidence + the dev.coherence_decided loop marker, and StopLoops.
// It stamps no outcome from the caller (G3) and fires no transition (G2).
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "check_coherence: harness not fully wired (reader/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "check_coherence: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	in, err := e.readInputs(ctx, runEntityID)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "check_coherence: read delivery signals on %s: %v", runEntityID, err)
	}

	decision, reason := Decide(in)

	out := coherenceTriples(runEntityID, decision, reason, time.Now().UTC())
	if err := e.writer.ReplaceTriples(ctx, runEntityID, out, nil); err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "check_coherence: stamp %s on %s: %v", CoherencePrefix, runEntityID, err)
	}

	e.logger.Info("check_coherence derived a delivery route",
		slog.String("run_entity_id", runEntityID),
		slog.String("decision", string(decision)),
		slog.String("reason", reason))

	// Stamp the chaining marker on THIS loop (value = the decision) so the router rules
	// (12a/b) fire and inherit the run. Decision evidence written FIRST (check_gate's
	// discipline); a marker error returns errResult WITHOUT StopLoop so the forced loop
	// re-runs (re-deriving idempotently). A missing LoopID skips the marker with a warn.
	if call.LoopID == "" {
		e.logger.Warn("check_coherence: no loop_id on the tool call — skipping the coherence-decided marker; no router will fire",
			slog.String("run_entity_id", runEntityID))
	} else {
		loopEntityID, lerr := agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if lerr != nil {
			return errResult(call, agentic.ToolErrorInternal, "check_coherence: construct coherence loop entity id: %v", lerr)
		}
		marker := []message.Triple{{
			Subject:    loopEntityID,
			Predicate:  DecidedMarker,
			Object:     string(decision),
			Source:     Source,
			Timestamp:  time.Now().UTC(),
			Confidence: 1.0,
		}}
		if merr := e.writer.ReplaceTriples(ctx, loopEntityID, marker, []string{DecidedMarker}); merr != nil {
			return errResult(call, changefacts.ReadErrorKind(merr), "check_coherence: stamp %s on %s: %v", DecidedMarker, loopEntityID, merr)
		}
	}

	summary, _ := json.Marshal(map[string]any{
		"decision":        string(decision),
		"reason":          reason,
		"verify_result":   in.VerifyResult,
		"validated":       in.Validated != "",
		"projected_tasks": in.ProjectedTasks,
		"verdicts":        in.Verdicts,
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// readInputs reads the four delivery-signal fact families off the run entity. A read error
// propagates (retryable); a missing scalar/verdict is folded into a fail-closed block.
func (e *Executor) readInputs(ctx context.Context, runEntityID string) (Inputs, error) {
	var in Inputs

	vr, err := e.readOne(ctx, runEntityID, VerifyResultPred, VerifyResultPred)
	if err != nil {
		return in, err
	}
	in.VerifyResult = vr

	val, err := e.readOne(ctx, runEntityID, ValidatedPred, ValidatedPred)
	if err != nil {
		return in, err
	}
	in.Validated = val

	specTriples, err := e.reader.ReadFacts(ctx, runEntityID, devtask.TaskSpecPrefix)
	if err != nil {
		return in, fmt.Errorf("read %s on %s: %w", devtask.TaskSpecPrefix, runEntityID, err)
	}
	in.ProjectedTasks = projectedTaskIDs(specTriples)

	verdictTriples, err := e.reader.ReadFacts(ctx, runEntityID, reviewPrefix)
	if err != nil {
		return in, fmt.Errorf("read %s on %s: %w", reviewPrefix, runEntityID, err)
	}
	in.Verdicts = verdictsByTask(verdictTriples)

	return in, nil
}

// readOne reads the run's facts under prefix and returns the string object of the triple
// whose predicate EXACTLY equals pred. Absent → "" (folded into a fail-closed block).
func (e *Executor) readOne(ctx context.Context, runEntityID, prefix, pred string) (string, error) {
	triples, err := e.reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return "", fmt.Errorf("read %s on %s: %w", prefix, runEntityID, err)
	}
	for _, tr := range triples {
		if tr.Predicate == pred {
			return objectString(tr.Object), nil
		}
	}
	return "", nil
}

// projectedTaskIDs returns the distinct projected task indices (as strings) under the
// task.spec.* namespace — the tasks that must each carry an approved verdict.
func projectedTaskIDs(triples []message.Triple) []string {
	seen := map[string]bool{}
	var ids []string
	for _, tr := range triples {
		rest, ok := strings.CutPrefix(tr.Predicate, devtask.TaskSpecPrefix)
		if !ok {
			continue
		}
		idStr, _, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(idStr); err != nil {
			continue
		}
		if !seen[idStr] {
			seen[idStr] = true
			ids = append(ids, idStr)
		}
	}
	return ids
}

// verdictsByTask maps each review.verdict.<i> predicate to its verdict object.
func verdictsByTask(triples []message.Triple) map[string]string {
	out := map[string]string{}
	for _, tr := range triples {
		idStr, ok := strings.CutPrefix(tr.Predicate, reviewPrefix)
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(idStr); err != nil {
			continue
		}
		out[idStr] = objectString(tr.Object)
	}
	return out
}

// coherenceTriples projects the decision into the owned pr.coherence.* package on the run.
func coherenceTriples(runEntityID string, decision Decision, reason string, now time.Time) []message.Triple {
	mk := func(field, obj string) message.Triple {
		return message.Triple{
			Subject:    runEntityID,
			Predicate:  CoherencePrefix + field,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		}
	}
	return []message.Triple{
		mk(FactDecision, string(decision)),
		mk(FactReason, reason),
	}
}

func objectString(o any) string {
	if s, ok := o.(string); ok {
		return s
	}
	if o == nil {
		return ""
	}
	return fmt.Sprint(o)
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
