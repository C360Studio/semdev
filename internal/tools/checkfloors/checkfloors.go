// Package checkfloors is the check_floors tool (dev-from-task, tasks 6.5/6.6): the
// floor-tools wrapper. It runs semdev's deterministic floor library over a task's
// current dev-loop attempt and stamps each finding as a floor.finding fact, so the
// loop can route on harness-computed structural facts — did the attempt author a
// test, is that test vacuous, did it ship a stub, does the source parse, did it
// "test" only a mock of the code under test — rather than the persona's claim about
// its own work.
//
// The floors are the pure core (internal/floors); this tool only WRAPS them with
// fact-stamping, which is what keeps the checks offline-testable and G5-clean (the
// floors package writes no facts). The verdict is COMPUTED by the deterministic
// floors, never supplied (G3 — the schema takes only the task index; floor.finding
// carries a harness-derived passed, not a model outcome). It stamps the findings and
// returns whether any floor rejected; it fires no lifecycle transition (G2) — the
// loop-gate that blocks advance-to-review on a rejecting floor.finding is a rule
// (task 6.6, wired with the bounded dev loop). Single G5 writer of floor.finding.*
// (floor-tools).
package checkfloors

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/measurement"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/types"
)

// ToolName is the registered tool name and the dev loop's floor-check handler.
const ToolName = "check_floors"

// Source is stamped on every floor.finding triple. It MUST equal the writer declared for
// floor.finding.* in internal/vocab (G5) — a conformance pin cross-checks it.
const Source = "floor-tools"

// RouteMirrorSource is stamped on the route.* facts check_floors MIRRORS onto its own loop
// so the rule-native floors route can fire on them (design R1). It MUST equal the single
// writer declared for the route.* mirror namespace in internal/vocab (G5). It is a SECOND,
// distinct Source than floor-tools: floor.finding.* (the substance, on the run) is
// floor-tools; the route mirror (raw COPIES onto the loop, one logical writer route-mirror
// shared with submit_review) is route-mirror — so neither predicate has two writers.
const RouteMirrorSource = "route-mirror"

// The route-mirror predicates check_floors stamps on ITS OWN floors loop (not the run). A
// rule condition reads only the firing entity's triples, so the floors-route rules (which
// fire on this loop) cannot read the run's measurement / attempt facts — the harness copies
// them here as RAW facts (never a derived route decision, G2). route.passed is the copy of
// measurement.result.<i>.passed (fail-closed "false" if absent — the "never measured"
// case); route.rejected is the copy of this run's floor.finding.<i>.rejected; route.attempt
// is the append-mirror of task.attempt.<i>'s distinct objects, so the route rules can count
// the budget via length_* on the loop.
const (
	RoutePassedPredicate   = "route.passed"
	RouteRejectedPredicate = "route.rejected"
	RouteAttemptPrefix     = "route.attempt."
)

// Attempts resolves a task's current dev-loop attempt — the files it authored (with
// contents) and the task's declared target files — from the run's checkout, into the
// pure floors.Attempt the deterministic checks consume. It is the seam over the
// checkout (which files this iteration authored is git/workspace state the forge-io
// checkout owns); its production implementation lands with the bounded dev loop. nil
// at M0 (schema-only, like write_change's resolver); Execute fails loudly if absent.
type Attempts interface {
	Resolve(ctx context.Context, runEntityID string, taskIndex int) (floors.Attempt, error)
}

// Executor runs the floors over a task's attempt and stamps the findings.
type Executor struct {
	attempts Attempts
	reader   changefacts.Reader // reads the run's measurement + attempt facts to MIRROR onto the floors loop
	writer   agentictools.OwnedFactWriter
	platform types.PlatformMeta // builds the floors loop's entity id for the route mirror
	logger   *slog.Logger
}

// New builds the check_floors executor. attempts/reader/writer may be nil for schema-only
// registration (the censuses inspect ListTools without a live checkout or NATS client);
// Execute fails loudly if any is nil. reader reads the run's measurement.result.<i>.passed
// and task.attempt.<i> to mirror onto the floors loop; platform builds that loop's entity id.
func New(attempts Attempts, reader changefacts.Reader, writer agentictools.OwnedFactWriter, platform types.PlatformMeta, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{attempts: attempts, reader: reader, writer: writer, platform: platform, logger: logger}
}

type payload struct {
	// TaskIndex is a pointer so an ABSENT argument is distinguishable from index 0.
	TaskIndex *int `json:"task_index"`
}

// Execute resolves the task's attempt, runs every floor, and stamps the findings as
// floor.finding.<task_index>.<floor> facts on the run entity. The verdicts are the
// deterministic floors' own; the tool records them and reports whether any rejected.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.attempts == nil || e.reader == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "check_floors: harness not fully wired (attempts/reader/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "check_floors: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: decode arguments: %v", err)
	}
	if p.TaskIndex == nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: task_index is required")
	}
	idx := *p.TaskIndex
	if idx < 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "check_floors: task_index must be non-negative, got %d", idx)
	}

	attempt, err := e.attempts.Resolve(ctx, runEntityID, idx)
	if err != nil {
		// Fail closed: a resolve/check failure must NOT leave a prior attempt's PASS
		// readable as if it were the current attempt's — that is a direct route to a
		// stale false-green once the gate reads these facts. Clear this task's findings
		// so no current verdict is readable, then surface the resolve fault (retryable;
		// the loop re-runs, and a persistent failure parks toward the human). The
		// attempt-id binding is the belt to this clear's suspenders.
		if cerr := e.clearFindings(ctx, runEntityID, idx); cerr != nil {
			e.logger.Warn("check_floors: could not clear stale findings after a resolve fault",
				slog.Int("task_index", idx), slog.Any("clear_error", cerr))
		}
		return errResult(call, agentic.ToolErrorInternal, "check_floors: resolve attempt for task %d: %v", idx, err)
	}

	findings := floors.CheckAll(attempt)
	rejected := floors.AnyRejected(findings)
	out := findingTriples(runEntityID, idx, floors.AttemptID(attempt), rejected, findings, time.Now().UTC())
	if err := e.writer.ReplaceTriples(ctx, runEntityID, out, nil); err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "check_floors: stamp floor.finding for task %d on %s: %v", idx, runEntityID, err)
	}

	e.logger.Info("check_floors evaluated attempt",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", idx),
		slog.Bool("rejected", rejected))

	// MIRROR the routing inputs onto THIS floors loop so the rule-native floors route
	// (advance / not_clean → retry / escalate) can fire on them (design R1: a rule reads
	// only the firing entity's triples, so the run-level measurement + attempt facts must
	// be copied onto the loop the route rules fire on). These are RAW copies (never a
	// derived route decision, G2): route.passed (the measurement, fail-closed "false" when
	// absent — the "model never measured" case routes like any red), route.rejected (this
	// floors run's own aggregate), and route.attempt.<i> (the append-mirror of the run's
	// task.attempt.<i> distinct objects, so the route rules count the budget via length_*).
	// The findings (the substance) are written to the run FIRST; the mirror (the chaining
	// signal the route rules trigger on) follows, so a mirror-write failure surfaces only
	// AFTER the findings are durable. Failure posture: a mirror error returns errResult
	// WITHOUT StopLoop, so the forced loop re-runs check_floors (re-stamping idempotently)
	// until it lands or MaxIterations trips — never a silent green. A missing LoopID
	// (unit-test-only) skips the mirror with a loud warn rather than failing the findings.
	if call.LoopID == "" {
		e.logger.Warn("check_floors: no loop_id on the tool call — skipping the route mirror; the floors route will not fire",
			slog.String("run_entity_id", runEntityID), slog.Int("task_index", idx))
	} else {
		loopEntityID, lerr := agentic.TryLoopExecutionEntityID(e.platform.Org, e.platform.Platform, call.LoopID)
		if lerr != nil {
			return errResult(call, agentic.ToolErrorInternal, "check_floors: construct floors loop entity id: %v", lerr)
		}
		passed, err := e.readMeasuredPassed(ctx, runEntityID, idx)
		if err != nil {
			return errResult(call, changefacts.ReadErrorKind(err), "check_floors: read measurement for the route mirror on %s: %v", runEntityID, err)
		}
		attempts, err := e.readAttemptObjects(ctx, runEntityID, idx)
		if err != nil {
			return errResult(call, changefacts.ReadErrorKind(err), "check_floors: read task.attempt for the route mirror on %s: %v", runEntityID, err)
		}
		mirror := routeMirrorTriples(loopEntityID, idx, passed, rejected, attempts, time.Now().UTC())
		// ReplaceTriples → MergeTriples does FULL-SET-REPLACE PER PREDICATE: every predicate
		// present in the write has its whole prior value-set on the loop replaced by this
		// write's set. So route.passed/route.rejected (single-valued) are replaced, and
		// route.attempt.<i> is set to the COMPLETE current attempt set (all N objects) — NOT
		// appended. This is why the mirror stamps the FULL attempt set every run: writing only
		// the newest object would DROP the prior ones (full-set-replace, not append). It is
		// idempotent on an error-retry re-run (the same complete set replaces itself) regardless
		// of the per-triple timestamps. removePredicates for the scalars is redundant-but-harmless
		// (they are always re-supplied); route.attempt is not listed (it is replaced by presence).
		if merr := e.writer.ReplaceTriples(ctx, loopEntityID, mirror, []string{RoutePassedPredicate, RouteRejectedPredicate}); merr != nil {
			return errResult(call, changefacts.ReadErrorKind(merr), "check_floors: stamp the route mirror on %s: %v", loopEntityID, merr)
		}
	}

	summary, _ := json.Marshal(map[string]any{
		"task_index": idx,
		"rejected":   rejected,
		"findings":   findings,
	})
	// StopLoop: the floors trigger (dev-from-task/05) forces a single-turn floors loop
	// (this tool is NOT in Amelia's loop — it runs in its own forced coordinator loop);
	// ending the turn here keeps it one model call (mirrors project_tasks / provision). A
	// rejecting verdict is DATA, not a tool error; the floors ROUTE reads the stamped
	// route.passed/route.rejected mirror, not this StopLoop, to decide advance/retry.
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
}

// findingTriples projects the floor findings into the owned per-task package on the
// run entity: floor.finding.<idx>.{attempt,rejected} plus floor.finding.<idx>.<floor>.
// {passed,detail}. attempt binds the whole set to the source it evaluated; rejected is
// the aggregate verdict the dev-loop gate reads (true iff any floor rejected), so the
// gate stays a single literal read rather than re-deriving from the per-floor keys. The
// floor set is fixed (CheckAll always returns the same floors), so the sub-keys upsert
// by predicate and re-evaluating the task replaces its prior findings without leaving a
// stale sub-key.
func findingTriples(runEntityID string, idx int, attemptID string, rejected bool, findings []floors.Finding, now time.Time) []message.Triple {
	base := floors.FindingPrefix + strconv.Itoa(idx) + "."
	out := make([]message.Triple, 0, len(findings)*2+2)
	mk := func(pred, obj string) {
		out = append(out, message.Triple{
			Subject:    runEntityID,
			Predicate:  pred,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		})
	}
	mk(base+floors.FactAttempt, attemptID)
	mk(base+floors.FactRejected, strconv.FormatBool(rejected))
	for _, f := range findings {
		p := base + f.Floor + "."
		mk(p+floors.FactPassed, strconv.FormatBool(f.Passed))
		mk(p+floors.FactDetail, f.Detail)
	}
	return out
}

// readMeasuredPassed reads measurement.result.<idx>.passed off the run for the route
// mirror and FAILS CLOSED to "false" in three cases (SB5): the measurement is absent (the
// "model never measured" case), passed is not exactly "true", OR the measurement is STALE —
// it was bound (measurement.result.<idx>.commit) to a DIFFERENT snapshot than the run's
// current attempt.commit. The staleness check is the semstreams-reviewer HIGH fix: because
// the measurement is keyed by task index and persists across attempts, a green from attempt
// N could otherwise advance an attempt N+1 that was re-applied but never re-measured. Only a
// measurement whose recorded commit equals the current (non-empty) attempt.commit is trusted.
func (e *Executor) readMeasuredPassed(ctx context.Context, runEntityID string, idx int) (string, error) {
	prefix := measurement.ResultPrefix + strconv.Itoa(idx) + "."
	triples, err := e.reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return "", err
	}
	var passed, measuredCommit string
	for _, tr := range triples {
		s, _ := tr.Object.(string)
		switch tr.Predicate {
		case prefix + measurement.FactPassed:
			passed = s
		case prefix + measurement.FactCommit:
			measuredCommit = s
		}
	}
	if passed != "true" {
		return "false", nil // absent or red measurement → fail closed
	}
	currentCommit, err := e.readAttemptCommit(ctx, runEntityID)
	if err != nil {
		return "", err
	}
	// A green measurement is trusted ONLY when it was bound to the run's CURRENT committed
	// snapshot. A missing/empty commit on either side, or a mismatch, is a stale green → red.
	if currentCommit == "" || measuredCommit == "" || measuredCommit != currentCommit {
		return "false", nil
	}
	return "true", nil
}

// readAttemptCommit reads the run's current attempt.commit (the SHA apply_patch last
// committed) so a green measurement can be correlated to the snapshot it ran against.
// Returns "" (no error) when absent.
func (e *Executor) readAttemptCommit(ctx context.Context, runEntityID string) (string, error) {
	const attemptCommit = "attempt.commit"
	triples, err := e.reader.ReadFacts(ctx, runEntityID, attemptCommit)
	if err != nil {
		return "", err
	}
	for _, tr := range triples {
		if tr.Predicate != attemptCommit {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			return s, nil
		}
	}
	return "", nil
}

// readAttemptObjects reads the distinct objects of the run's task.attempt.<idx> counter
// (each object is a developer-loop instance, one per attempt) so the route mirror can
// append them onto the loop and the route rules count the budget via length_*.
func (e *Executor) readAttemptObjects(ctx context.Context, runEntityID string, idx int) ([]string, error) {
	want := attemptPredicate(idx)
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

// attemptPredicate is the run's per-task attempt counter predicate (task.attempt.<idx>).
func attemptPredicate(idx int) string { return "task.attempt." + strconv.Itoa(idx) }

// routeMirrorTriples builds the route mirror the floors-route rules fire on: the scalar
// route.passed / route.rejected copies plus one route.attempt.<idx> triple per distinct
// attempt object (the count the budget route reads via length_*). All carry RouteMirrorSource
// (the single logical writer route-mirror, shared with submit_review) so neither the floor
// facts nor the measurement facts gain a second writer (G5).
func routeMirrorTriples(loopEntityID string, idx int, passed string, rejected bool, attempts []string, now time.Time) []message.Triple {
	mk := func(pred, obj string) message.Triple {
		return message.Triple{Subject: loopEntityID, Predicate: pred, Object: obj, Source: RouteMirrorSource, Timestamp: now, Confidence: 1.0}
	}
	out := []message.Triple{
		mk(RoutePassedPredicate, passed),
		mk(RouteRejectedPredicate, strconv.FormatBool(rejected)),
	}
	attemptPred := RouteAttemptPrefix + strconv.Itoa(idx)
	for _, obj := range attempts {
		out = append(out, mk(attemptPred, obj))
	}
	return out
}

// clearFindings removes this task's entire floor.finding.<idx>.* package (the "clear
// my prefix" pattern), so a stale earlier attempt's pass is not left readable when
// the current attempt cannot be evaluated. A no-op when nothing is stamped yet.
func (e *Executor) clearFindings(ctx context.Context, runEntityID string, idx int) error {
	prefix := floors.FindingPrefix + strconv.Itoa(idx) + "."
	preds, err := e.writer.ReadOwnedPredicates(ctx, runEntityID, prefix)
	if err != nil {
		return err
	}
	if len(preds) == 0 {
		return nil
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, nil, preds)
}

func errResult(call agentic.ToolCall, kind agentic.ToolErrorKind, format string, args ...any) (agentic.ToolResult, error) {
	return agentic.ToolResult{
		CallID:    call.ID,
		Name:      ToolName,
		Error:     fmt.Sprintf(format, args...),
		ErrorKind: kind,
	}, nil
}
