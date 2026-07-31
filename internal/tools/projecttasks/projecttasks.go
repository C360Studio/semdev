// Package projecttasks is the shared PROJECTION CORE (dev-from-task, task 6.1) that
// the projection station (internal/station/projection, R6) calls: the reader half of
// the create_change → openspec.change.* → devtask.Project → task.spec seam. Project
// reads an approved change's execution-rich task facts off the run entity,
// reconstructs each into a devtask.RawTask (honoring the nil-vs-authored-empty
// presence distinction the writer preserved), runs devtask.Project (Karpathy schema +
// budget clamp), and stamps the result as the IMMUTABLE task.spec.* facts the dev
// loop converges on.
//
// It plans nothing — create_change authored the tasks; Project enforces and FREEZES
// them. Immutability is structural: it refuses to re-project onto a run that already
// carries task.spec (the loop redefines nothing — dev-from-task spec). A task missing
// a required field is NOT stamped: devtask.Project returns a schema error and Project
// returns it as a plain error, so the caller fails toward the human (surfacing the gap;
// the PARK is a rule's job, G2 — this package fires no lifecycle transition). It stamps
// no outcome (G3): task.spec is the task definition, not a result. Single G5 writer of
// task.spec.* (Source == task-projector).
//
// The project_tasks TOOL executor that used to wrap this core in a forced coordinator
// turn was deleted in group 6 (R6) when the projection station took over — a forced
// model turn to call a deterministic projector decided nothing and burned a paid call
// for zero reason (G1). This package is now a pure library: no agentic.ToolCall/
// ToolResult surface, no LLM-facing schema.
package projecttasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c360studio/semstreams/message"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/graphown"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/validatechange"
)

// Source is stamped on every task.spec triple. It MUST equal the single writer
// declared for task.spec.* in internal/vocab (G5) — a conformance pin cross-checks.
const Source = "task-projector"

// Project is the shared projection core: it reads the run's approved change task
// facts for slug, enforces immutability + the D15#0 validated-content binding + the
// target_files-includes-tests contract, projects via devtask.Project, and stamps the
// IMMUTABLE task.spec. It returns the projected task count, or an error that carries
// a human-readable refusal message (a schema gap / re-projection / unvalidated
// change / contract violation) with NOTHING stamped (atomic). The projection station
// (R6) is the sole caller, so task.spec keeps one writer (G5) and one enforcement
// path. reader/writer/logger must be non-nil (the caller checks).
func Project(ctx context.Context, reader changefacts.Reader, writer *graphown.Writer, logger *slog.Logger, runEntityID, slug string) (int, error) {
	if slug == "" {
		return 0, fmt.Errorf("project_tasks: slug is required")
	}
	if err := openspec.ValidateSlug(slug); err != nil {
		return 0, fmt.Errorf("project_tasks: %w", err)
	}

	// Immutability: task.spec is projected ONCE. If it already exists on this run,
	// refuse — the dev loop converges on it, it does not redefine it.
	existing, err := writer.ReadOwnedPredicates(ctx, runEntityID, devtask.TaskSpecPrefix)
	if err != nil {
		return 0, fmt.Errorf("project_tasks: read existing task.spec on %s: %w", runEntityID, err)
	}
	if len(existing) > 0 {
		return 0, fmt.Errorf("project_tasks: task.spec already projected for this run (%d facts) — it is immutable; the dev loop converges on it, it does not redefine it", len(existing))
	}

	// Bind the projection to the run's VALIDATED CONTENT (D15 #0). project_tasks
	// freezes the IMMUTABLE task.spec, so it must refuse any change that is not the
	// exact content the validate harness blessed on this run. openspec.validated
	// carries the content REVISION the validator passed; create_change stamps this
	// slug's current content revision at openspec.change.<slug>.revision. Freeze
	// ONLY when they are equal — so a re-authored-but-not-revalidated change (its
	// revision bumped, marker stale), an unvalidated change (no marker), or an
	// alternate change on the run (its revision != the validated one) is refused.
	// Otherwise a stale or alternate package would be frozen and every downstream
	// gate (measurement / review / verify) would prove the WRONG work against an
	// immutable spec. Fail closed: absent or mismatched revision is a refusal.
	validatedRev, err := readPredicate(ctx, reader, runEntityID, validatechange.ValidatedPredicate)
	if err != nil {
		return 0, fmt.Errorf("project_tasks: read %s on %s: %w", validatechange.ValidatedPredicate, runEntityID, err)
	}
	slugRev, err := readPredicate(ctx, reader, runEntityID, createchange.RevisionPredicate)
	if err != nil {
		return 0, fmt.Errorf("project_tasks: read %s on %s: %w", createchange.RevisionPredicate, runEntityID, err)
	}
	if validatedRev == "" || slugRev == "" || validatedRev != slugRev {
		return 0, fmt.Errorf("project_tasks: change %q is not validated at its current content (%s=%q, %s=%q) — refusing to freeze task.spec from an unvalidated, re-authored-since-validation, or alternate change",
			slug, validatechange.ValidatedPredicate, validatedRev, createchange.RevisionPredicate, slugRev)
	}

	// Read the authored change DOCUMENT (beta.147 D3) and reconstruct the execution-rich
	// tasks from it — the rich per-task fields (target_files/test_command/…) plus the thin
	// task text (a task's goal) both live in the one blob now, not a task-fact tree.
	doc, err := changefacts.HydrateDocument(ctx, reader, runEntityID, slug)
	if err != nil {
		return 0, fmt.Errorf("project_tasks: read change document on %s: %w", runEntityID, err)
	}
	rawTasks := rawTasksFromDocument(doc)
	if len(rawTasks) == 0 {
		return 0, fmt.Errorf("project_tasks: no tasks in change %q on %s — was it authored?", slug, runEntityID)
	}

	specs, err := devtask.Project(rawTasks)
	if err != nil {
		// A schema gap fails TOWARD the human: surface it so the change is fixed and
		// re-authored; the projector stamps nothing. Parking is a rule's job (G2).
		return 0, fmt.Errorf("project_tasks: %w", err)
	}

	// target_files-includes-tests contract (task 3.3), M0 Go profile: a task whose
	// test_command runs the Go test tool must list at least one *_test.go in its
	// target_files — else the harness measures a test the developer was given no writable
	// contract to author, the vacuous-green route (measure over an untouchable test). Fail
	// TOWARD the human and stamp nothing (atomic, like the schema gaps above); the
	// ecosystem-specific test-file profile lives here in the M0 projector, not the neutral
	// devtask schema.
	if bad := testFileContractViolations(specs); len(bad) > 0 {
		return 0, fmt.Errorf("project_tasks: task(s) %v run `go test` but their target_files include no *_test.go the test would measure — the developer could not author the test that measures its own work (fails toward the human)", bad)
	}

	// Single-task at M0 (beta.147 D1): task.spec.<field> is flat (no per-task index), so
	// a change with >1 task would silently clobber all but the last spec (ReplaceTriples
	// is replace-per-predicate) and mislabel the survivor as task 0. Fail CLOSED toward the
	// human (G2) — stamp nothing, atomic like the schema/contract gaps above — rather than
	// develop a scrambled, partial task set. The M1 multi-task future keys the task into the
	// ENTITY ID (a per-task entity), lifting this limit at an explicit seam.
	if len(specs) > 1 {
		return 0, fmt.Errorf("project_tasks: change %q authored %d tasks, but the M0 rail develops exactly one (task.spec is single-keyed) — split it into single-task changes", slug, len(specs))
	}

	out := taskSpecTriples(runEntityID, specs, time.Now().UTC())
	if err := writer.Replace(ctx, runEntityID, out); err != nil {
		return 0, fmt.Errorf("project_tasks: stamp %d task.spec facts on %s: %w", len(out), runEntityID, err)
	}

	logger.Info("project_tasks projected task.spec",
		slog.String("run_entity_id", runEntityID),
		slog.String("slug", slug),
		slog.Int("task_count", len(specs)))
	return len(specs), nil
}

// readPredicate returns the object of the exact predicate on the run entity, or
// "" if absent. Both facts it reads (openspec.validated, the slug-scoped content
// revision) are named by their producer's own const, so the read/write sides of
// each cannot drift.
func readPredicate(ctx context.Context, reader changefacts.Reader, runEntityID, predicate string) (string, error) {
	triples, err := reader.ReadFacts(ctx, runEntityID, predicate)
	if err != nil {
		return "", err
	}
	for _, tr := range triples {
		if tr.Predicate == predicate {
			s, _ := tr.Object.(string)
			return s, nil
		}
	}
	return "", nil
}

// rawTasksFromDocument reconstructs the authored tasks from the change document (beta.147
// D3): the execution-rich per-task fields carry their own flat index, and the thin task
// text at that index is the task's goal. Presence is preserved by the DTO's JSON round-trip
// (an ABSENT list/budget stays nil — a projector gap it rejects; an authored-empty list
// stays non-nil — a value it accepts). Tasks come back sorted by index (the projector
// requires a contiguous 0..n-1 set).
func rawTasksFromDocument(doc changefacts.ChangeDocument) []devtask.RawTask {
	texts := flattenTaskTexts(doc.Change)
	out := make([]devtask.RawTask, 0, len(doc.RichTasks))
	for _, rt := range doc.RichTasks {
		out = append(out, devtask.RawTask{
			Index:       rt.Index,
			Goal:        texts[rt.Index],
			TestCommand: rt.TestCommand,
			TargetFiles: rt.TargetFiles,
			Assumptions: rt.Assumptions,
			NonGoals:    rt.NonGoals,
			Budget:      rt.Budget,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// flattenTaskTexts maps each task's flat index (sections in order, tasks in order — the
// SAME walk create_change's toChange used to assign the rich-task index) to its thin text,
// which the projector reads as the task's goal.
func flattenTaskTexts(change *openspec.Change) map[int]string {
	texts := map[int]string{}
	if change == nil || change.Tasks == nil {
		return texts
	}
	i := 0
	for _, sec := range change.Tasks.Sections {
		for _, t := range sec.Tasks {
			texts[i] = t.Text
			i++
		}
	}
	return texts
}

// testFileContractViolations returns the indices of tasks whose test_command mentions the
// literal `go test` but whose target_files declare no *_test.go — the M0-Go realization of
// "target_files omits every test file its test_command measures" (task 3.3). Non-Go test
// commands are not checked (M0 is Go-only; a broader ecosystem profile is future work).
func testFileContractViolations(specs []devtask.TaskSpec) []int {
	var bad []int
	for _, s := range specs {
		if mentionsGoTest(s.TestCommand) && !targetsIncludeGoTest(s.TargetFiles) {
			bad = append(bad, s.Index)
		}
	}
	return bad
}

// mentionsGoTest reports whether cmd literally contains "go test". This is a COARSE M0-Go
// match, deliberately: it does NOT recognize go-test wrappers (`gotestsum`, `make test`),
// and it errs toward parking on degenerate substrings (`echo go test`). Both directions are
// safe — a false NEGATIVE (a wrapper slips) does not produce a vacuous green (the
// TestsMustExist floor rejects an attempt that authored no test, and the patcher's
// target_files guard forbids authoring a test outside the contract, so the case escalates
// to the human); a false POSITIVE parks toward the human, the safe direction. A precise
// per-ecosystem test-file profile is future work when non-Go repos land.
func mentionsGoTest(cmd string) bool { return strings.Contains(cmd, "go test") }

// targetsIncludeGoTest reports whether any target file is a Go test file (*_test.go).
func targetsIncludeGoTest(targets []string) bool {
	for _, t := range targets {
		if strings.HasSuffix(strings.TrimSpace(t), "_test.go") {
			return true
		}
	}
	return false
}

// taskSpecTriples projects the immutable task.spec facts on the run entity:
// task.spec.<field> for the projected task, budgets already clamped. Single-task at
// M0 (beta.147 D1): the index is out of the predicate; the M1 multi-task future keys
// the task into the entity ID (a per-task entity), so multiple specs do not collide.
func taskSpecTriples(runEntityID string, specs []devtask.TaskSpec, now time.Time) []message.Triple {
	var out []message.Triple
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
	for _, s := range specs {
		p := devtask.TaskSpecKeyPrefix(s.Index)
		mk(p+devtask.FactGoal, s.Goal)
		mk(p+devtask.FactTargetFiles, jsonArray(s.TargetFiles))
		mk(p+devtask.FactTestCommand, s.TestCommand)
		mk(p+devtask.FactAssumptions, jsonArray(s.Assumptions))
		mk(p+devtask.FactNonGoals, jsonArray(s.NonGoals))
		mk(p+devtask.FactBudget, strconv.Itoa(s.Budget))
	}
	return out
}

func jsonArray(xs []string) string {
	if xs == nil {
		xs = []string{}
	}
	b, _ := json.Marshal(xs)
	return string(b)
}
