// Package projecttasks is the project_tasks tool (dev-from-task, task 6.1): the
// reader half of the create_change → openspec.change.* → devtask.Project →
// task.spec seam. It reads an approved change's execution-rich task facts off the
// run entity, reconstructs each into a devtask.RawTask (honoring the nil-vs-
// authored-empty presence distinction the writer preserved), runs devtask.Project
// (Karpathy schema + budget clamp), and stamps the result as the IMMUTABLE
// task.spec.* facts the dev loop converges on.
//
// It plans nothing — create_change authored the tasks; this tool enforces and
// FREEZES them. Immutability is structural: it refuses to re-project onto a run
// that already carries task.spec (the loop redefines nothing — dev-from-task spec).
// A task missing a required field is NOT stamped: devtask.Project returns a schema
// error and the tool fails toward the human (surfacing the gap as a tool error; the
// PARK is a rule's job, G2 — this tool fires no lifecycle transition). It stamps no
// outcome (G3): task.spec is the task definition, not a result. Single G5 writer of
// task.spec.* (Source == task-projector).
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

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/devtask"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the dev_from_task projection handler.
const ToolName = "project_tasks"

// Source is stamped on every task.spec triple. It MUST equal the single writer
// declared for task.spec.* in internal/vocab (G5) — a conformance pin cross-checks.
const Source = "task-projector"

// thinTextKey is the format engine's thin task-text sub-key (openspec.change.<slug>.
// task.<i>.text) — the projector's goal comes from it (create_change writes it; the
// round-trip test pins it).
const thinTextKey = "text"

// Executor reads a run's change task facts and stamps the projected task.spec.
type Executor struct {
	reader changefacts.Reader
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// New builds the project_tasks executor. reader/writer may be nil for schema-only
// registration (the tool censuses inspect ListTools without a live NATS client);
// Execute fails loudly if either is nil.
func New(reader changefacts.Reader, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, writer: writer, logger: logger}
}

type payload struct {
	Slug string `json:"slug"`
}

// Execute reads the run's approved change task facts, projects them, and stamps the
// immutable task.spec — or fails toward the human on a schema gap, or refuses to
// re-project onto an already-projected run.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "project_tasks: harness not fully wired (reader/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "project_tasks: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "project_tasks: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "project_tasks: decode arguments: %v", err)
	}
	if p.Slug == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "project_tasks: slug is required")
	}
	if err := openspec.ValidateSlug(p.Slug); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "project_tasks: %v", err)
	}

	// Immutability: task.spec is projected ONCE. If it already exists on this run,
	// refuse — the dev loop converges on it, it does not redefine it.
	existing, err := e.writer.ReadOwnedPredicates(ctx, runEntityID, devtask.TaskSpecPrefix)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "project_tasks: read existing task.spec on %s: %v", runEntityID, err)
	}
	if len(existing) > 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs,
			"project_tasks: task.spec already projected for this run (%d facts) — it is immutable; the dev loop converges on it, it does not redefine it", len(existing))
	}

	// Bind the projection to the run's VALIDATED change. project_tasks freezes the
	// IMMUTABLE task.spec, so it must refuse any slug that is not the change the
	// validate harness blessed on this run (openspec.validated). A stale or alternate
	// authored package still present on the run would otherwise be frozen, and every
	// downstream gate (measurement / review / verify) would then prove the WRONG work
	// against an immutable spec. Fail closed: no validated marker, or a marker naming a
	// different slug, is a refusal — the model chooses WHICH validated change to freeze,
	// never an unvalidated one.
	validated, err := e.readValidatedSlug(ctx, runEntityID)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "project_tasks: read %s on %s: %v", validatechange.ValidatedPredicate, runEntityID, err)
	}
	if validated != p.Slug {
		return errResult(call, agentic.ToolErrorInvalidArgs,
			"project_tasks: slug %q is not this run's validated change (%s=%q) — refusing to freeze task.spec from an unvalidated or alternate change", p.Slug, validatechange.ValidatedPredicate, validated)
	}

	prefix := openspec.ChangeEntityPrefix(p.Slug) + "task."
	triples, err := e.reader.ReadFacts(ctx, runEntityID, prefix)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "project_tasks: read change task facts under %q on %s: %v", prefix, runEntityID, err)
	}
	rawTasks, err := rawTasksFromFacts(prefix, triples)
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "project_tasks: %v", err)
	}
	if len(rawTasks) == 0 {
		return errResult(call, agentic.ToolErrorInvalidArgs, "project_tasks: no task facts for change %q on %s — was it authored?", p.Slug, runEntityID)
	}

	specs, err := devtask.Project(rawTasks)
	if err != nil {
		// A schema gap fails TOWARD the human: surface it so the change is fixed and
		// re-authored; the projector stamps nothing. Parking is a rule's job (G2).
		return errResult(call, agentic.ToolErrorInvalidArgs, "project_tasks: %v", err)
	}

	out := taskSpecTriples(runEntityID, specs, time.Now().UTC())
	if err := e.writer.ReplaceTriples(ctx, runEntityID, out, nil); err != nil {
		return errResult(call, writeErrKind(err), "project_tasks: stamp %d task.spec facts on %s: %v", len(out), runEntityID, err)
	}

	e.logger.Info("project_tasks projected task.spec",
		slog.String("run_entity_id", runEntityID),
		slog.String("slug", p.Slug),
		slog.Int("task_count", len(specs)))
	summary, _ := json.Marshal(map[string]any{"slug": p.Slug, "tasks": len(specs), "run_entity": runEntityID})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary)}, nil
}

// readValidatedSlug returns the slug the validate harness stamped as this run's
// validated change (openspec.validated on the run entity), or "" if no marker is
// present. It reads the producer's own predicate name (validatechange) so the
// reader and writer of that fact cannot drift.
func (e *Executor) readValidatedSlug(ctx context.Context, runEntityID string) (string, error) {
	triples, err := e.reader.ReadFacts(ctx, runEntityID, validatechange.ValidatedPredicate)
	if err != nil {
		return "", err
	}
	for _, tr := range triples {
		if tr.Predicate == validatechange.ValidatedPredicate {
			s, _ := tr.Object.(string)
			return s, nil
		}
	}
	return "", nil
}

// rawTasksFromFacts reconstructs the authored tasks from the change's task facts,
// keyed by the flat index in each predicate (openspec.change.<slug>.task.<i>.<field>,
// stripped of prefix). Presence is honored on the READ side exactly as the writer
// preserved it: an ABSENT list/budget fact leaves the RawTask field nil (a gap the
// projector rejects), while a present "[]" reconstructs a non-nil empty slice (an
// authored-empty value the projector accepts). Tasks come back sorted by index.
func rawTasksFromFacts(prefix string, triples []message.Triple) ([]devtask.RawTask, error) {
	byIndex := map[int]map[string]string{}
	for _, tr := range triples {
		rest := strings.TrimPrefix(tr.Predicate, prefix)
		idxStr, field, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		obj, ok := tr.Object.(string)
		if !ok {
			return nil, fmt.Errorf("task fact %q has non-string object %T", tr.Predicate, tr.Object)
		}
		if byIndex[i] == nil {
			byIndex[i] = map[string]string{}
		}
		byIndex[i][field] = obj
	}

	indices := make([]int, 0, len(byIndex))
	for i := range byIndex {
		indices = append(indices, i)
	}
	sort.Ints(indices)

	out := make([]devtask.RawTask, 0, len(indices))
	for _, i := range indices {
		f := byIndex[i]
		rt := devtask.RawTask{Index: i, Goal: f[thinTextKey], TestCommand: f[devtask.FactTestCommand]}
		var err error
		if rt.TargetFiles, err = optArray(f, devtask.FactTargetFiles); err != nil {
			return nil, fmt.Errorf("task %d %s: %w", i, devtask.FactTargetFiles, err)
		}
		if rt.Assumptions, err = optArray(f, devtask.FactAssumptions); err != nil {
			return nil, fmt.Errorf("task %d %s: %w", i, devtask.FactAssumptions, err)
		}
		if rt.NonGoals, err = optArray(f, devtask.FactNonGoals); err != nil {
			return nil, fmt.Errorf("task %d %s: %w", i, devtask.FactNonGoals, err)
		}
		if rt.Budget, err = optInt(f, devtask.FactBudget); err != nil {
			return nil, fmt.Errorf("task %d %s: %w", i, devtask.FactBudget, err)
		}
		out = append(out, rt)
	}
	return out, nil
}

// optArray returns nil when field is ABSENT (a projector gap), else the parsed
// slice forced non-nil (a present fact — even "[]" — is an authored value).
func optArray(f map[string]string, field string) ([]string, error) {
	v, ok := f[field]
	if !ok {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, fmt.Errorf("not a JSON string array (%q): %w", v, err)
	}
	if out == nil {
		out = []string{} // a present fact is authored-empty, never a gap
	}
	return out, nil
}

// optInt returns nil when field is ABSENT (a projector gap), else the parsed int.
func optInt(f map[string]string, field string) (*int, error) {
	v, ok := f[field]
	if !ok {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, fmt.Errorf("not an integer (%q): %w", v, err)
	}
	return &n, nil
}

// taskSpecTriples projects the immutable task.spec facts on the run entity:
// task.spec.<i>.<field> for each projected task, budgets already clamped.
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

// writeErrKind classifies a stamp failure at a WRITE site: a handler-classified
// graph error (entity_not_found and friends) is internal/ordering, not retryable
// transport. It shares changefacts' classifier (same errs.ClassifiedError logic)
// and reads correctly where ReplaceTriples fails, mirroring create_change.
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
