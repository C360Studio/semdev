// Package validatechange holds the shared VALIDATION CORE: it shells the real
// OpenSpec CLI validator against a run's generated change as a deterministic
// compatibility oracle, and stamps openspec.validated on the run entity ONLY when
// the CLI passes. It is the "the sponsor's own tool blessed this" step on the way
// in (D14) — semdev shells the oracle rather than re-implementing its rules.
//
// Validate is called by the validation station (internal/station/validation, R6)
// as a publish-triggered component with ZERO model turns. It used to also back a
// forced validate_change coordinator tool; that executor was deleted in the
// group-6 reshape (slice 6E) once the station took over as sole caller.
//
// It is the measurement-harness pattern (G3): callers pass only the change
// slug — NEVER a validation outcome — and this Go (which actually ran
// `openspec validate`) stamps the result from the real process exit code. The
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
// run's current revision) and project_tasks both refuse until Validate runs
// again against the new content. Reading the SLUG-scoped revision (not the run-level
// one) binds the marker to precisely what was validated, so a future multi-slug run
// fails the gate closed rather than blessing whatever was last authored.
// create_change is the SOLE computer of the revision; this harness only echoes it,
// so the two cannot drift.
package validatechange

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/openspec"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semstreams/message"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
)

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

// ErrOracleUnrunnable wraps a failure to RUN the OpenSpec CLI (binary missing,
// timeout, cancel) — a transport fault, not a verdict, so nothing is stamped or
// cleared and it is retryable. The validation station logs it and the base
// retries.
var ErrOracleUnrunnable = errors.New("openspec validate could not be run")

// Result reports the oracle verdict for a run's change. Validated true iff the CLI
// passed (openspec.validated stamped to Revision); false means the CLI rejected the
// change (the marker was cleared, Issues carries the validator output) — a definitive
// verdict, NOT an error.
type Result struct {
	Validated bool
	Revision  string
	Issues    string
}

// Validate is the shared validation core: it hydrates the run's change, materializes
// it to a throwaway workspace, shells `openspec validate <slug> --strict --json
// --no-interactive`, and — from the real exit code — stamps openspec.validated (pass)
// or clears any stale marker (fail). It returns a Result (Validated true iff the CLI
// passed). A returned error is a wiring/authoring/transport fault (the oracle-
// unrunnable case wraps ErrOracleUnrunnable); a CLI REJECTION is Result{Validated:
// false, Issues:...} with a nil error. The validation station (R6) is the sole
// caller, so openspec.validated keeps one writer (G5).
func Validate(ctx context.Context, reader changefacts.Reader, runner cliexec.Runner, writer agentictools.OwnedFactWriter, runEntityID, slug string) (Result, error) {
	if slug == "" {
		return Result{}, fmt.Errorf("validate_change: slug is required")
	}
	if err := openspec.ValidateSlug(slug); err != nil {
		return Result{}, fmt.Errorf("validate_change: %w", err)
	}

	change, err := changefacts.Hydrate(ctx, reader, runEntityID, slug)
	if err != nil {
		return Result{}, fmt.Errorf("validate_change: %w", err)
	}
	if isEmpty(change) {
		return Result{}, fmt.Errorf("validate_change: no openspec.change.%s.* facts on %s — nothing to validate (was the change authored?)", slug, runEntityID)
	}

	// Read the content revision create_change stamped over THIS slug's authored
	// change (D15 #0). openspec.validated is bound to this value, so it detects a
	// later re-author. Fail closed if it is absent: without a revision this harness
	// cannot stamp a content-bound marker, and a bare-slug marker would reopen the
	// stale-pass hole. A missing revision means the change was not authored by
	// create_change (or a partial write) — an ordering/authoring gap, not transport.
	revPredicate := createchange.SlugRevisionPredicate(slug)
	rev, err := readRevision(ctx, reader, runEntityID, revPredicate)
	if err != nil {
		return Result{}, fmt.Errorf("validate_change: read %s on %s: %w", revPredicate, runEntityID, err)
	}
	if rev == "" {
		return Result{}, fmt.Errorf("validate_change: no %s on %s — the change carries no content revision (was it authored by create_change?)", revPredicate, runEntityID)
	}

	// Materialize to a throwaway workspace so the oracle judges exactly the
	// hydrated change (openspec/changes/<slug>/ under a temp root), then remove it.
	root, err := os.MkdirTemp("", "semdev-validate-")
	if err != nil {
		return Result{}, fmt.Errorf("validate_change: create temp workspace: %w", err)
	}
	defer os.RemoveAll(root)
	changeDir := filepath.Join(root, "openspec", "changes", slug)
	if err := openspec.WriteChange(changeDir, change); err != nil {
		return Result{}, fmt.Errorf("validate_change: write change to temp workspace: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	res, err := runner.Run(runCtx, root, openspecBin, "validate", slug, "--strict", "--json", "--no-interactive")
	if err != nil {
		// The oracle could not be run (binary missing, timeout, cancel) — a
		// transport failure, NOT a verdict. Do not stamp or clear; retryable.
		return Result{}, fmt.Errorf("validate_change: run %s validate: %w: %v", openspecBin, ErrOracleUnrunnable, err)
	}

	if res.ExitCode == 0 {
		if err := stampValidated(ctx, writer, runEntityID, rev); err != nil {
			return Result{}, fmt.Errorf("validate_change: stamp %s on %s: %w", ValidatedPredicate, runEntityID, err)
		}
		return Result{Validated: true, Revision: rev}, nil
	}

	// Invalid: clear any stale pass marker so the change cannot reach the approval
	// gate, and hand back the validator's own issues for correction.
	if err := clearValidated(ctx, writer, runEntityID); err != nil {
		return Result{}, fmt.Errorf("validate_change: clear stale %s on %s: %w", ValidatedPredicate, runEntityID, err)
	}
	return Result{Validated: false, Issues: validatorOutput(res)}, nil
}

// stampValidated upserts openspec.validated=<content revision> on the run entity
// (replace-by-predicate, so a re-validation replaces rather than appends a second
// marker). The value is the revision — not the slug — so it binds to the exact
// content the validator blessed (D15 #0).
func stampValidated(ctx context.Context, writer agentictools.OwnedFactWriter, runEntityID, rev string) error {
	triple := message.Triple{
		Subject:    runEntityID,
		Predicate:  ValidatedPredicate,
		Object:     rev,
		Source:     Source,
		Timestamp:  time.Now().UTC(),
		Confidence: 1.0,
	}
	return writer.ReplaceTriples(ctx, runEntityID, []message.Triple{triple}, nil)
}

// readRevision returns the object of the exact revision predicate on the run
// entity (the slug-scoped openspec.change.<slug>.revision create_change stamped),
// or "" if absent. It is named via createchange's own helper so the read/write
// sides cannot drift.
func readRevision(ctx context.Context, reader changefacts.Reader, runEntityID, predicate string) (string, error) {
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

// clearValidated removes the openspec.validated marker this harness owns, so a
// now-failing change does not retain a stale pass.
func clearValidated(ctx context.Context, writer agentictools.OwnedFactWriter, runEntityID string) error {
	return writer.ReplaceTriples(ctx, runEntityID, nil, []string{ValidatedPredicate})
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
