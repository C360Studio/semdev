// Package checkfloors is the shared FLOORS CORE (RunFloors): it runs semdev's
// deterministic floor library over a task's current dev-loop attempt, stamps each
// finding as a floor.finding fact, and mirrors the routing inputs onto the caller's
// loop entity, so the loop can route on harness-computed structural facts — did the
// attempt author a test, is that test vacuous, did it ship a stub, does the source
// parse, did it "test" only a mock of the code under test — rather than the
// persona's claim about its own work. Post-reshape (R6) this core is called by the
// floors STATION (internal/station/floors), a publish-triggered component; there is
// no forced coordinator tool turn.
//
// The floors are the pure core (internal/floors); RunFloors only WRAPS them with
// fact-stamping, which is what keeps the checks offline-testable and G5-clean (the
// floors package writes no facts). The verdict is COMPUTED by the deterministic
// floors, never supplied (G3 — RunFloors takes only the task index; floor.finding
// carries a harness-derived passed, not a model outcome). It stamps the findings and
// returns whether any floor rejected; it fires no lifecycle transition (G2) — the
// loop-gate that blocks advance-to-review on a rejecting floor.finding is a rule.
// Single G5 writer of floor.finding.* (floor-tools).
package checkfloors

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/c360studio/semstreams/message"
	agvocab "github.com/c360studio/semstreams/vocabulary/agentic"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/floors"
	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/measurement"
)

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
// them here as RAW facts (never a derived route decision, G2). route.attempt.passed is the
// copy of measurement.result.passed (fail-closed "false" if absent — the "never measured"
// case); route.attempt.rejected is the copy of this run's floor.finding.rejected;
// route.attempt.instance is the append-mirror of task.attempt.instance's distinct objects,
// so the route rules can count the budget via length_* on the loop.
const (
	RoutePassedPredicate   = "route.attempt.passed"
	RouteRejectedPredicate = "route.attempt.rejected"
	// RouteAttemptPredicate is the MULTI-VALUED mirrored attempt counter. The budget
	// route counts its objects via length_* — which resolves by EXACT predicate, so the
	// single-valued route.attempt.passed/rejected siblings (same route.attempt. prefix)
	// never inflate the count. Rules MUST bind this exact name, never the prefix.
	RouteAttemptPredicate = "route.attempt.instance"
	// RouteBudgetPredicate is the SINGLE-VALUED per-task attempt budget mirrored onto L_n
	// (adopt-per-task-routing-budgets, #568): a RAW copy of the run's projected
	// task.spec.budget so the retry/escalate routes read `length_lt`/`length_gte
	// $entity.triple.route.task.budget.value` instead of the constant 3. Stamped in the SAME
	// ReplaceTriples pass as route.attempt.* (the D7 atomicity invariant — the routes never
	// see the attempt count without the budget).
	RouteBudgetPredicate = "route.task.budget"
	// RouteTransientPredicate is the MULTI-VALUED mirror of the run's task.transient.instance
	// counter (adopt-reason-aware-escalate, #529/#569): the transient-retry/park routes count it
	// via length_lt/length_gte against a LITERAL cap. Like route.attempt.instance, the complete
	// current set is mirrored each pass. An absent mirror is a valid count 0 (array op over an
	// empty set), so — unlike the substituted budget — there is NO fail-open wedge and no D7 fault.
	RouteTransientPredicate = "route.transient.instance"
	// RouteTransientFlagPredicate is the SINGLE-VALUED transient classification of the loop's
	// terminal (adopt-reason-aware-escalate): "true" iff the loop's harness-stamped
	// agent.loop.terminal-reason is in the transient set (model_error/handler_error), else
	// "false" — ALWAYS stamped, and stamped in the SAME ReplaceTriples pass as
	// route.attempt.passed (the D7 atomicity invariant). The transient-retry/park routes fire
	// on eq "true"; the convergence retry/escalate routes exclude on eq "false".
	//
	// This classification lives HERE, not in a rule, by G1's escape clause — PROVEN necessary:
	// the engine executes each rule action as its OWN graph mutation (one KV revision per
	// add_triple, processor/rule/triple_mutator.go) and evaluates rules per debounce-flush
	// against freshly-fetched state, so a rule-stamped collapse (the route.attempt.unclean
	// pattern) lands in a SEPARATE revision from its same-pass siblings. A convergence route
	// excluding on that rule-stamped triple races it — the double-dispatch caught live by
	// TestBridgeProofTransientGraceRetries. Mutual exclusion between routes is only sound as a
	// CONDITION PARTITION over one atomic snapshot, and the atomic snapshot primitive is this
	// mirror's single ReplaceTriples. The classification is a fixed normalization of a
	// harness-stamped fact (no LLM input, G3; no lifecycle transition — the routes still
	// decide, G2), exactly the fail-closed posture route.attempt.passed already takes.
	RouteTransientFlagPredicate = "route.attempt.transient"
)

// transientReasons is the fixed set of agent.loop.terminal-reason values classified as
// TRANSIENT (an infrastructure interruption, not a property of the attempt's work): the
// model endpoint erroring (model_error) or a tool/handler fault (handler_error). The other
// documented reasons (max_iterations, graph_state_reset_required — vocabulary/agentic
// register.go) are genuine terminals the convergence budget must absorb. Kept in lockstep
// with the transient-route rule prose; a conformance pin asserts the flag is stamped.
var transientReasons = map[string]bool{
	"model_error":   true,
	"handler_error": true,
}

// transientPredicate is the run's TRANSIENT-retry counter (task.transient.instance), appended
// by the transient-retry route. Read shape identical to the task.attempt.instance read; the
// mirror copies its distinct objects onto L_n so the transient routes count the grace.
const transientPredicate = "task.transient.instance"

// budgetPredicate is the run's projected per-task attempt budget (task.spec.budget,
// writer task-projector, clamped [1,5]). Composed from the SAME devtask consts the projector
// writes it under (projecttasks stamps devtask.TaskSpecPrefix + devtask.FactBudget), so a
// canonical-vocab rename cannot silently desync the read side. The mirror copies it RAW onto L_n.
const budgetPredicate = devtask.TaskSpecPrefix + devtask.FactBudget

// Attempts resolves a task's current dev-loop attempt — the files it authored (with
// contents) and the task's declared target files — from the run's checkout, into the
// pure floors.Attempt the deterministic checks consume. It is the seam over the
// checkout (which files this iteration authored is git/workspace state the forge-io
// checkout owns).
type Attempts interface {
	Resolve(ctx context.Context, runEntityID string, taskIndex int) (floors.Attempt, error)
}

// FloorResult reports the floors outcome for a task attempt.
type FloorResult struct {
	Rejected  bool
	Findings  []floors.Finding
	AttemptID string
}

// RunFloors runs the deterministic floors over the task's current attempt, stamps
// floor.finding.<idx>.* on the run, and MIRRORS the routing inputs
// (route.passed/route.rejected/route.attempt.<idx>) onto routeLoopEntityID for the
// rule-native floors route. Called by the floors station (R6). routeLoopEntityID is
// the entity the floors-route rules fire on — the DEVELOPER loop L_n (the published
// dispatch entity_id, fresh per attempt); the mirror is skipped when it is "" (a
// unit test with no loop to mirror onto).
//
// The mirror is RAW copies (never a derived route decision, G2): route.passed (the
// measurement, fail-closed "false" when absent OR bound to a stale snapshot — the g4+5
// staleness fix, preserved), route.rejected (this run's own aggregate), and
// route.attempt.<i> (the full attempt set, so the route rules count the budget via
// length_*). The findings (the substance) are written to the run FIRST; the mirror (the
// chaining signal the route rules trigger on) follows, so a mirror-write failure surfaces
// only AFTER the findings are durable. Fails closed: a resolve/check fault CLEARS this
// task's findings (so no stale pass is readable) before returning the error.
func RunFloors(ctx context.Context, attempts Attempts, reader changefacts.Reader, findingsWriter, mirrorWriter *graphown.Writer, logger *slog.Logger, runEntityID, routeLoopEntityID string, taskIndex int) (FloorResult, error) {
	attempt, err := attempts.Resolve(ctx, runEntityID, taskIndex)
	if err != nil {
		// A resolve/check failure must NOT leave a prior attempt's PASS readable as if it
		// were the current attempt's — a direct route to a stale false-green once the route
		// reads these facts. Clear this task's findings, then surface the fault (retryable).
		if cerr := clearFindings(ctx, findingsWriter, runEntityID, taskIndex); cerr != nil {
			logger.Warn("check_floors: could not clear stale findings after a resolve fault",
				slog.Int("task_index", taskIndex), slog.Any("clear_error", cerr))
		}
		return FloorResult{}, fmt.Errorf("check_floors: resolve attempt for task %d: %w", taskIndex, err)
	}

	findings := floors.CheckAll(attempt)
	rejected := floors.AnyRejected(findings)
	attemptID := floors.AttemptID(attempt)
	out := findingTriples(runEntityID, taskIndex, attemptID, rejected, findings, time.Now().UTC())
	if err := findingsWriter.Replace(ctx, runEntityID, out); err != nil {
		return FloorResult{}, fmt.Errorf("check_floors: stamp floor.finding for task %d on %s: %w", taskIndex, runEntityID, err)
	}

	logger.Info("check_floors evaluated attempt",
		slog.String("run_entity_id", runEntityID),
		slog.Int("task_index", taskIndex),
		slog.Bool("rejected", rejected))

	if routeLoopEntityID == "" {
		return FloorResult{Rejected: rejected, Findings: findings, AttemptID: attemptID}, nil
	}

	passed, err := readMeasuredPassed(ctx, reader, runEntityID, taskIndex)
	if err != nil {
		return FloorResult{}, fmt.Errorf("check_floors: read measurement for the route mirror on %s: %w", runEntityID, err)
	}
	attemptObjs, err := readAttemptObjects(ctx, reader, runEntityID, taskIndex)
	if err != nil {
		return FloorResult{}, fmt.Errorf("check_floors: read task.attempt for the route mirror on %s: %w", runEntityID, err)
	}
	// D7 / migrate-beta159 task 4.6: an EMPTY attempt set is a projection-contract
	// violation, never a normal input — the dispatching rule appends one
	// task.attempt.instance AT SPAWN, so a loop that is running has at least one.
	// Under the OLD writer an empty read was harmless: route.attempt.instance was not
	// in the remove list, so the prior mirror survived. Under ReplaceOwned the group
	// wipe DELETES it, and a zeroed count reads as budget-unexhausted — the run then
	// retries past its budget on paid tokens, with a CommitVerified receipt on the
	// deletion. So withhold the whole mirror and fault loudly, exactly as the budget
	// read below does.
	if len(attemptObjs) == 0 {
		return FloorResult{}, fmt.Errorf("check_floors: task.attempt.instance is EMPTY on %s — the dispatch rule appends one at spawn, so this is a projection-contract violation; mirroring now would group-wipe the prior attempt count and read as budget-unexhausted", runEntityID)
	}
	// D7: the per-task budget is authored + clamped [1,5] on every task, so an absent or
	// unparseable value on the run is a projection-contract violation, never a normal input.
	// Fault LOUDLY before any mirror write (findings already durable — NOT cleared; the
	// resolve-fault clear above is for a stale PRIOR pass, not this current attempt's genuine
	// findings). The route substitution fails OPEN on absence ($…value→"" → length_* coerce
	// error swallowed → neither retry nor escalate fires → silent stall), so withholding the
	// whole mirror is the only safe posture: no route.attempt.* is stamped without the budget.
	budget, err := readTaskBudget(ctx, reader, runEntityID)
	if err != nil {
		return FloorResult{}, fmt.Errorf("check_floors: read task.spec.budget for the route mirror on %s: %w", runEntityID, err)
	}
	// The TRANSIENT-retry counter (adopt-reason-aware-escalate). An absent counter is a valid 0
	// (the first transient failure), so a read fault is the only error worth surfacing — an empty
	// set is normal and mirrors nothing (no fail-open, so no D7 fault like the budget).
	transientObjs, err := readTransientObjects(ctx, reader, runEntityID)
	if err != nil {
		return FloorResult{}, fmt.Errorf("check_floors: read task.transient for the route mirror on %s: %w", runEntityID, err)
	}
	// The loop's harness-stamped terminal reason (read from L_n, NOT the run — the loop
	// entity is where WriteLoopFailure stamps it, atomically with the outcome that
	// triggered the floors dispatch, so it is always readable by now). Absent is the
	// normal completed-loop case → classified NOT transient.
	reason, err := readTerminalReason(ctx, reader, routeLoopEntityID)
	if err != nil {
		return FloorResult{}, fmt.Errorf("check_floors: read the loop terminal reason for the route mirror on %s: %w", routeLoopEntityID, err)
	}
	mirror := routeMirrorTriples(routeLoopEntityID, taskIndex, passed, rejected, budget, transientReasons[reason], attemptObjs, transientObjs, time.Now().UTC())
	// ReplaceOwned wipes route-mirror's whole loop group and re-adds Desired, so:
	// route.passed/rejected,
	// route.task.budget, and route.attempt.transient (single-valued) are replaced, and
	// route.attempt.<i> is set to the COMPLETE current attempt set (all N objects) — writing
	// only the newest would DROP the prior ones. The budget AND the transient flag ride the
	// SAME pass as route.attempt.* (D7 atomicity — the routes never see the count without
	// the budget, and never see unclean's inputs without the transient classification).
	// Idempotent on a retry (the same complete set replaces itself).
	if merr := mirrorWriter.Replace(ctx, routeLoopEntityID, mirror); merr != nil {
		return FloorResult{}, fmt.Errorf("check_floors: stamp the route mirror on %s: %w", routeLoopEntityID, merr)
	}
	return FloorResult{Rejected: rejected, Findings: findings, AttemptID: attemptID}, nil
}

// findingTriples projects the floor findings into the owned FLAT package on the run
// entity (beta.147 D4): floor.finding.{attempt,rejected,detail}. attempt binds the set
// to the source it evaluated; rejected is the aggregate verdict the dev-loop route
// reads (true iff any floor rejected), so the route stays a single literal read; detail
// is the per-floor prose concatenated into one human-legible scalar (the per-floor
// sub-keys were never matched in a condition). The fixed key set upserts by predicate,
// so re-evaluating replaces the prior findings without leaving a stale sub-key. idx is
// retained for logging/schema continuity but no longer keys the predicate (single-task).
func findingTriples(runEntityID string, idx int, attemptID string, rejected bool, findings []floors.Finding, now time.Time) []message.Triple {
	_ = idx
	mk := func(pred, obj string) message.Triple {
		return message.Triple{
			Subject:    runEntityID,
			Predicate:  pred,
			Object:     obj,
			Source:     Source,
			Timestamp:  now,
			Confidence: 1.0,
		}
	}
	return []message.Triple{
		mk(floors.AttemptPredicate, attemptID),
		mk(floors.RejectedPredicate, strconv.FormatBool(rejected)),
		mk(floors.DetailPredicate, floors.FormatDetail(findings)),
	}
}

// readMeasuredPassed reads measurement.result.passed off the run for the route
// mirror and FAILS CLOSED to "false" in three cases (SB5): the measurement is absent (the
// "model never measured" case), passed is not exactly "true", OR the measurement is STALE —
// it was bound (measurement.result.commit) to a DIFFERENT snapshot than the run's
// current attempt.commit.sha. The staleness check is the semstreams-reviewer HIGH fix: because
// the measurement is keyed by task index and persists across attempts, a green from attempt
// N could otherwise advance an attempt N+1 that was re-applied but never re-measured. Only a
// measurement whose recorded commit equals the current (non-empty) attempt.commit is trusted.
func readMeasuredPassed(ctx context.Context, reader changefacts.Reader, runEntityID string, idx int) (string, error) {
	_ = idx
	triples, err := reader.ReadFacts(ctx, runEntityID, measurement.ResultPrefix)
	if err != nil {
		return "", err
	}
	var passed, measuredCommit string
	for _, tr := range triples {
		s, _ := tr.Object.(string)
		switch tr.Predicate {
		case measurement.ResultPrefix + measurement.FactPassed:
			passed = s
		case measurement.ResultPrefix + measurement.FactCommit:
			measuredCommit = s
		}
	}
	if passed != "true" {
		return "false", nil // absent or red measurement → fail closed
	}
	currentCommit, err := readAttemptCommit(ctx, reader, runEntityID)
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
func readAttemptCommit(ctx context.Context, reader changefacts.Reader, runEntityID string) (string, error) {
	const attemptCommit = "attempt.commit.sha"
	triples, err := reader.ReadFacts(ctx, runEntityID, attemptCommit)
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
func readAttemptObjects(ctx context.Context, reader changefacts.Reader, runEntityID string, idx int) ([]string, error) {
	want := attemptPredicate(idx)
	triples, err := reader.ReadFacts(ctx, runEntityID, want)
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

// attemptPredicate is the run's attempt counter predicate (task.attempt.instance).
// Single-task at M0 (beta.147 D1): the index is out of the predicate (idx retained
// for call-site continuity).
func attemptPredicate(idx int) string { _ = idx; return "task.attempt.instance" }

// routeMirrorTriples builds the route mirror the floors-route rules fire on: the scalar
// route.passed / route.rejected copies, the single-valued route.task.budget copy, plus one
// route.attempt.<idx> triple per distinct attempt object (the count the budget route reads
// via length_*). All carry RouteMirrorSource (the single logical writer route-mirror, shared
// with submit_review) so neither the floor facts nor the measurement facts gain a second
// writer (G5). budget is the RAW authored task.spec.budget string (never re-rendered).
func routeMirrorTriples(loopEntityID string, idx int, passed string, rejected bool, budget string, transient bool, attempts, transients []string, now time.Time) []message.Triple {
	mk := func(pred, obj string) message.Triple {
		return message.Triple{Subject: loopEntityID, Predicate: pred, Object: obj, Source: RouteMirrorSource, Timestamp: now, Confidence: 1.0}
	}
	_ = idx
	out := []message.Triple{
		mk(RoutePassedPredicate, passed),
		mk(RouteRejectedPredicate, strconv.FormatBool(rejected)),
		mk(RouteBudgetPredicate, budget),
		// The transient classification of THIS loop's terminal — always present, so the
		// convergence routes' eq "false" exclusion never reads an absent field (see
		// RouteTransientFlagPredicate for why this cannot be a rule-stamped collapse).
		mk(RouteTransientFlagPredicate, strconv.FormatBool(transient)),
	}
	for _, obj := range attempts {
		out = append(out, mk(RouteAttemptPredicate, obj))
	}
	// The transient-retry mirror (adopt-reason-aware-escalate). Empty on the common path (no
	// transient failures yet) → nothing appended → an absent mirror the transient routes read as 0.
	for _, obj := range transients {
		out = append(out, mk(RouteTransientPredicate, obj))
	}
	return out
}

// readTerminalReason reads the LOOP entity's agent.loop.terminal-reason — the classified
// failure reason the agentic-loop graph writer stamps atomically with the outcome
// (semstreams #569, buildLoopFailureTriples). Returns "" (no error) when absent: a
// successfully completed loop stamps no reason, and "" classifies NOT transient.
func readTerminalReason(ctx context.Context, reader changefacts.Reader, loopEntityID string) (string, error) {
	triples, err := reader.ReadFacts(ctx, loopEntityID, agvocab.LoopTerminalReason)
	if err != nil {
		return "", err
	}
	for _, tr := range triples {
		if tr.Predicate != agvocab.LoopTerminalReason {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			return s, nil
		}
	}
	return "", nil
}

// readTransientObjects reads the distinct objects of the run's task.transient.instance counter
// (one per transient re-dispatch) so the mirror can append them onto L_n and the transient-retry
// route counts the grace via length_*. Identical read shape to readAttemptObjects.
func readTransientObjects(ctx context.Context, reader changefacts.Reader, runEntityID string) ([]string, error) {
	triples, err := reader.ReadFacts(ctx, runEntityID, transientPredicate)
	if err != nil {
		return nil, err
	}
	var objs []string
	for _, tr := range triples {
		if tr.Predicate != transientPredicate {
			continue
		}
		if s, ok := tr.Object.(string); ok {
			objs = append(objs, s)
		}
	}
	return objs, nil
}

// readTaskBudget reads the run's projected per-task attempt budget (task.spec.budget) as a
// RAW string for the route mirror. It PARSE-VALIDATES the EXACT value it will stamp — strconv.Atoi
// on the raw string, matching the engine's coerceToInt (evaluator.go, no trim) that the route's
// $…value substitution feeds — but NEVER re-renders it (no re-clamp, no derived value — G3/G5), so
// what validates here is exactly what the route later coerces. Absent, blank, or non-canonical
// (anything Atoi rejects, e.g. " 3 " or "3\n") is a projection-contract violation (D7), returned as
// a non-nil error so the caller faults loudly rather than stamping a value the route fails OPEN on.
func readTaskBudget(ctx context.Context, reader changefacts.Reader, runEntityID string) (string, error) {
	triples, err := reader.ReadFacts(ctx, runEntityID, budgetPredicate)
	if err != nil {
		return "", err
	}
	for _, tr := range triples {
		if tr.Predicate != budgetPredicate {
			continue
		}
		s, _ := tr.Object.(string)
		if s == "" {
			return "", fmt.Errorf("%s present but empty/non-string — a projection-contract violation (D7)", budgetPredicate)
		}
		if _, perr := strconv.Atoi(s); perr != nil {
			return "", fmt.Errorf("%s %q is not a bare integer the route can coerce — a projection-contract violation (D7): %w", budgetPredicate, s, perr)
		}
		return s, nil
	}
	return "", fmt.Errorf("%s absent — the projection station authors a clamped [1,5] budget on every task; its absence is a projection-contract violation (D7)", budgetPredicate)
}

// clearFindings removes the entire floor.finding.* package (the "clear my prefix"
// pattern), so a stale earlier attempt's pass is not left readable when the current
// attempt cannot be evaluated. A no-op when nothing is stamped yet.
func clearFindings(ctx context.Context, writer *graphown.Writer, runEntityID string, idx int) error {
	_ = idx
	preds, err := writer.ReadOwnedPredicates(ctx, runEntityID, floors.FindingPrefix)
	if err != nil {
		return err
	}
	if len(preds) == 0 {
		return nil
	}
	// Desired nil clears floor-tools' whole replace-owned group on the run — which
	// is exactly the floor.finding.* package preds enumerates (design D3a). The
	// read-back is retained as the no-op gate: it keeps a clear that has nothing to
	// clear off the mutation lane entirely.
	return writer.Replace(ctx, runEntityID, nil)
}
