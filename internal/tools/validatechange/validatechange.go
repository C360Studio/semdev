// Package validatechange is the validate_change tool (openspec-io): it runs the
// real OpenSpec CLI validator against a run's generated change as a deterministic
// compatibility oracle, and stamps openspec.validated on the run entity ONLY when
// the CLI passes. It is the "the sponsor's own tool blessed this" step on the way
// in (D14) — semdev shells the oracle rather than re-implementing its rules.
//
// It is the measurement-harness pattern (G3): the tool's schema takes only the
// change slug — NEVER a validation outcome — and the HARNESS (this tool's Go,
// which actually ran `openspec validate`) stamps the result from the real process
// exit code. The model triggers the check; it cannot supply the verdict. The
// lifecycle transition it enables (executing → awaiting_approval) is fired by a
// rule that reads openspec.validated (G2) — this Go only stamps the fact.
//
// The marker is kept honest (G7): a PASS upserts openspec.validated on the run
// entity (subject = agent.run.entity_id, so the change-approval gate rule, which
// evaluates the run, sees it — design D15); a FAIL CLEARS any stale marker and
// returns the validator's issues, so an invalid change cannot reach the approval
// gate. Validation runs against a fresh temp materialization of the hydrated
// change, so the oracle judges exactly what is in the graph, independent of any
// workspace the write_change tool may have left.
//
// The marker's VALUE is the change's content REVISION (D15 forward-contract #0),
// not the slug: on PASS this harness reads openspec.change.<slug>.revision — the
// revision create_change stamped over the content of the slug THIS validation
// judged — and echoes it into openspec.validated. So the marker binds to the exact
// content the CLI blessed. A re-author bumps that revision, so the stale
// openspec.validated no longer equals it — the gate rule (openspec.validated eq the
// run's current revision) and project_tasks both refuse until validate_change runs
// again against the new content. Reading the SLUG-scoped revision (not the run-level
// one) binds the marker to precisely what was validated, so a future multi-slug run
// fails the gate closed rather than blessing whatever was last authored.
// create_change is the SOLE computer of the revision; this harness only echoes it,
// so the two cannot drift.
package validatechange

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semstreams/agentic"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

// ToolName is the registered tool name and the coordinator's validate action
// handler.
const ToolName = "validate_change"

// Source is the value stamped on the openspec.validated triple. It MUST equal the
// writer declared for openspec.validated in internal/vocab (G5) — a conformance
// pin cross-checks it.
const Source = "openspec-validate-harness"

// ValidatedPredicate is the milestone fact this harness owns: present on the run
// entity == the run's change passed the OpenSpec CLI validator.
const ValidatedPredicate = "openspec.validated"

// openspecBin is the OpenSpec CLI binary shelled as the oracle.
const openspecBin = "openspec"

// validateTimeout bounds one CLI validation.
const validateTimeout = 30 * time.Second

// Executor runs the OpenSpec validator over a run's change and stamps the result.
type Executor struct {
	reader changefacts.Reader
	runner cliexec.Runner
	writer agentictools.OwnedFactWriter
	logger *slog.Logger
}

// New builds the validate_change executor. reader/runner/writer may be nil for
// schema-only registration (the tool censuses inspect ListTools without a live
// NATS client); Execute fails loudly if any is nil.
func New(reader changefacts.Reader, runner cliexec.Runner, writer agentictools.OwnedFactWriter, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{reader: reader, runner: runner, writer: writer, logger: logger}
}

// Execute hydrates the run's change, materializes it to a throwaway workspace,
// shells `openspec validate <slug> --strict --json --no-interactive`, and — from
// the real exit code — stamps openspec.validated on the run (pass) or clears it
// (fail). It is terminal for the turn (StopLoop); the gate rule advances the run.
func (e *Executor) Execute(ctx context.Context, call agentic.ToolCall) (agentic.ToolResult, error) {
	if e.reader == nil || e.runner == nil || e.writer == nil {
		return errResult(call, agentic.ToolErrorInternal, "validate_change: harness not fully wired (reader/runner/writer)")
	}
	runEntityID, ok := call.Metadata[agentic.MetadataKeyRunEntityID].(string)
	if !ok || runEntityID == "" {
		return errResult(call, agentic.ToolErrorInternal, "validate_change: %s missing on the tool call — cannot target the run entity", agentic.MetadataKeyRunEntityID)
	}

	var p payload
	raw, err := json.Marshal(call.Arguments)
	if err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "validate_change: encode arguments: %v", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "validate_change: decode arguments: %v", err)
	}
	if p.Slug == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "validate_change: slug is required")
	}
	if err := openspec.ValidateSlug(p.Slug); err != nil {
		return errResult(call, agentic.ToolErrorInvalidArgs, "validate_change: %v", err)
	}

	change, err := changefacts.Hydrate(ctx, e.reader, runEntityID, p.Slug)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "validate_change: %v", err)
	}
	if isEmpty(change) {
		return errResult(call, agentic.ToolErrorInvalidArgs, "validate_change: no openspec.change.%s.* facts on %s — nothing to validate (was the change authored?)", p.Slug, runEntityID)
	}

	// Read the content revision create_change stamped over THIS slug's authored
	// change (D15 #0). openspec.validated is bound to this value, so it detects a
	// later re-author. Fail closed if it is absent: without a revision this harness
	// cannot stamp a content-bound marker, and a bare-slug marker would reopen the
	// stale-pass hole. A missing revision means the change was not authored by
	// create_change (or a partial write) — an ordering/authoring gap, not transport.
	revPredicate := createchange.SlugRevisionPredicate(p.Slug)
	rev, err := e.readRevision(ctx, runEntityID, revPredicate)
	if err != nil {
		return errResult(call, changefacts.ReadErrorKind(err), "validate_change: read %s on %s: %v", revPredicate, runEntityID, err)
	}
	if rev == "" {
		return errResult(call, agentic.ToolErrorInvalidArgs, "validate_change: no %s on %s — the change carries no content revision (was it authored by create_change?)", revPredicate, runEntityID)
	}

	// Materialize to a throwaway workspace so the oracle judges exactly the
	// hydrated change (openspec/changes/<slug>/ under a temp root), then remove it.
	root, err := os.MkdirTemp("", "semdev-validate-")
	if err != nil {
		return errResult(call, agentic.ToolErrorInternal, "validate_change: create temp workspace: %v", err)
	}
	defer os.RemoveAll(root)
	changeDir := filepath.Join(root, "openspec", "changes", p.Slug)
	if err := openspec.WriteChange(changeDir, change); err != nil {
		return errResult(call, agentic.ToolErrorInternal, "validate_change: write change to temp workspace: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	res, err := e.runner.Run(runCtx, root, openspecBin, "validate", p.Slug, "--strict", "--json", "--no-interactive")
	if err != nil {
		// The oracle could not be run (binary missing, timeout, cancel) — a
		// transport failure, NOT a verdict. Do not stamp or clear; retryable.
		return errResult(call, agentic.ToolErrorNetwork, "validate_change: run %s validate: %v", openspecBin, err)
	}

	if res.ExitCode == 0 {
		if err := e.stampValidated(ctx, runEntityID, rev); err != nil {
			return errResult(call, writeErrKind(err), "validate_change: stamp %s on %s: %v", ValidatedPredicate, runEntityID, err)
		}
		summary, _ := json.Marshal(map[string]any{"slug": p.Slug, "validated": true, "revision": rev, "run_entity": runEntityID})
		return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(summary), StopLoop: true}, nil
	}

	// Invalid: clear any stale pass marker so the change cannot reach the approval
	// gate, and hand back the validator's own issues for correction.
	if err := e.clearValidated(ctx, runEntityID); err != nil {
		return errResult(call, writeErrKind(err), "validate_change: clear stale %s on %s: %v", ValidatedPredicate, runEntityID, err)
	}
	content, _ := json.Marshal(map[string]any{
		"slug":      p.Slug,
		"validated": false,
		"issues":    validatorOutput(res),
	})
	return agentic.ToolResult{CallID: call.ID, Name: ToolName, Content: string(content), StopLoop: true}, nil
}

// stampValidated upserts openspec.validated=<content revision> on the run entity
// (replace-by-predicate, so a re-validation replaces rather than appends a second
// marker). The value is the revision — not the slug — so it binds to the exact
// content the validator blessed (D15 #0).
func (e *Executor) stampValidated(ctx context.Context, runEntityID, rev string) error {
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  ValidatedPredicate,
		Object:     rev,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return e.writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil)
}

// readRevision returns the object of the exact revision predicate on the run
// entity (the slug-scoped openspec.change.<slug>.revision create_change stamped),
// or "" if absent. It is named via createchange's own helper so the read/write
// sides cannot drift.
func (e *Executor) readRevision(ctx context.Context, runEntityID, predicate string) (string, error) {
	triples, err := e.reader.ReadFacts(ctx, runEntityID, predicate)
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

// clearValidated removes the openspec.validated marker this harness owns, so a
// now-failing change does not retain a stale pass.
func (e *Executor) clearValidated(ctx context.Context, runEntityID string) error {
	return e.writer.ReplaceTriples(ctx, runEntityID, nil, []string{ValidatedPredicate})
}

// validatorOutput returns the CLI's JSON stdout verbatim (its issue list) when
// present, else stderr — the honest failure detail for correction, never a
// paraphrase.
func validatorOutput(res cliexec.Result) string {
	if res.Stdout != "" {
		return res.Stdout
	}
	return res.Stderr
}

// isEmpty reports whether a hydrated Change carries no artifact facts.
func isEmpty(c *openspec.Change) bool {
	return c.Proposal == nil && c.Design == nil && c.Tasks == nil && len(c.Deltas) == 0
}

// writeErrKind mirrors the create_change classifier: a handler-classified graph
// error (entity_not_found and friends) is internal/ordering, not transport.
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
