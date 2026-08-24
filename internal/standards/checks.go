package standards

// The repo-declared deterministic checks lane (standards-via-lessons D7/D7a).
//
// A target repo's standards file may declare commands whose exit status gates an attempt
// exactly like a built-in floor. Two properties make that safe to hand a repo:
//
//   - The commands come from the PROVISION-TIME snapshot (see snapshot.go), captured
//     before any model turn for the run. Not from the working tree, and deliberately not
//     from the run's base git ref either: the checkout is bind-mounted read-write into the
//     sandbox with `.git` inside it, so a ref is something the attempt can move.
//   - They run only in the run's sandbox CONTAINER. A repo-authored command never
//     executes on the host.
//
// The harness runs each command and stamps its real exit status (G3): no model asserts an
// outcome, and no schema here accepts one.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/c360studio/semdev/internal/cleanroom"
	"github.com/c360studio/semdev/internal/floors"
)

// checkTimeout bounds each declared command, matching the measure lane's discipline: a
// repo-authored command must not be able to hang a run indefinitely.
const checkTimeout = 10 * time.Minute

// FloorPrefix names a repo-declared check in the floor namespace, so its finding reads
// and routes exactly like a built-in floor's.
const FloorPrefix = "repo-check:"

// Provisioned serves the standards file captured for the run at provision time.
// ok=false means nothing was ever captured, which is a fault, not "declared none".
type Provisioned interface {
	Lookup(runEntityID string) (Snapshot, bool)
}

// Sandboxes resolves the run's warm container (the measure_task seam shape).
type Sandboxes interface {
	Resolve(ctx context.Context, runEntityID string) (cleanroom.Runner, cleanroom.Sandbox, error)
}

// Checks runs a repo's declared checks and returns their findings.
type Checks struct {
	Provisioned Provisioned
	Sandboxes   Sandboxes
	Logger      *slog.Logger
}

// Run executes every check the base revision declares and returns one finding each.
//
// It returns an error only when the LANE itself could not be evaluated (an unreadable or
// malformed base file, an unresolvable sandbox) — never for a check that simply failed,
// which is a finding. The distinction matters because a lane fault must not read as "this
// repo declared no checks": that would silently delete the gate.
func (c *Checks) Run(ctx context.Context, runEntityID string) ([]floors.Finding, error) {
	if c.Provisioned == nil || c.Sandboxes == nil {
		return nil, fmt.Errorf("standards: the repo-checks lane is partially wired (provisioned=%t sandboxes=%t)",
			c.Provisioned != nil, c.Sandboxes != nil)
	}
	snap, ok := c.Provisioned.Lookup(runEntityID)
	if !ok {
		// Never captured. That is a fault, and specifically NOT "this repo declared no
		// checks" — inferring the latter is how a gate disappears without anything saying
		// so. It reaches here only if the run was never provisioned in this process (a
		// restart), in which case its warm sandbox is gone too and nothing about it can
		// honestly be measured.
		return nil, fmt.Errorf("standards: no provision-time standards snapshot for run %s — refusing to "+
			"treat an uncaptured lane as a repo that declared no checks", runEntityID)
	}
	if !snap.Declared {
		return nil, nil
	}
	file, err := Parse(snap.Raw)
	if err != nil {
		// Provisioning parked on a malformed file, so bytes that provisioned cleanly and
		// then fail to parse are a pathological state. Failing loudly beats running zero
		// checks: the latter deletes the gate with nothing saying so.
		return nil, fmt.Errorf("standards: the provisioned standards snapshot does not parse: %w", err)
	}
	if len(file.Checks) == 0 {
		return nil, nil
	}

	runner, sb, err := c.Sandboxes.Resolve(ctx, runEntityID)
	if err != nil {
		return nil, fmt.Errorf("standards: resolve the run's sandbox to execute repo checks: %w", err)
	}

	findings := make([]floors.Finding, 0, len(file.Checks))
	for _, chk := range file.Checks {
		// A cancelled context must not turn into N rejecting findings — that would read
		// as the attempt failing every gate when in fact nothing was evaluated.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("standards: repo checks interrupted before %q: %w", chk.Name, err)
		}
		f, err := c.runOne(ctx, runner, sb, chk)
		if err != nil {
			return nil, err
		}
		findings = append(findings, f)
	}
	for _, w := range file.Warnings() {
		if c.Logger != nil {
			c.Logger.Warn("repo standards declaration warning", slog.String("run_entity_id", runEntityID), slog.String("warning", w))
		}
	}
	if c.Logger != nil {
		// Say how many ran, and how many gate. A silent lane is indistinguishable from a
		// repo that declared nothing, and "no output" is exactly how a gate that stopped
		// running would look — so it announces itself on every pass.
		c.Logger.Info("repo-declared checks executed in the run's sandbox",
			slog.String("run_entity_id", runEntityID),
			slog.Int("checks", len(findings)),
			slog.Int("required", requiredCount(file.Checks)),
			slog.Bool("rejected", floors.AnyRejected(findings)))
	}
	return findings, nil
}

// runOne evaluates a single check: its negative control first (when declared), then the
// check itself. It returns a lane error only for a repo-DECLARATION defect (a control
// that does not fail), which must park rather than reject — see the comment at that
// branch.
func (c *Checks) runOne(ctx context.Context, runner cleanroom.Runner, sb cleanroom.Sandbox, chk Check) (floors.Finding, error) {
	// The control runs FIRST and gates the check's meaning. A control that passes means
	// this gate has never been demonstrated able to reject anything, so its green says
	// nothing — and reporting that as a clean pass is the fail-open shape the whole D7a
	// discipline exists to refuse.
	status := "unproven"
	if chk.Proof != "" {
		res, err := c.exec(ctx, runner, sb, chk.Proof)
		switch {
		case err != nil:
			// Transport: the same class as a check that could not run, and retryable for
			// the same reason.
			return c.finding(chk, false, fmt.Sprintf(
				"not-run — the negative control could not be executed in the sandbox (%v); the gate is unevaluated", err)), nil
		case res.ExitCode == 0:
			// A control that passes is a defect in the REPO's declaration, and it is
			// deterministic: it will pass again on every retry. Routing it as an attempt
			// rejection would re-dispatch the developer against a fault she cannot reach
			// (the standards file is not in target_files), burning the whole attempt
			// budget on real model turns and then escalating with a reason that reads as
			// her work failing. It parks toward the human instead — the same posture
			// provisioning takes for a malformed standards file.
			return floors.Finding{}, fmt.Errorf(
				"check %q declares a negative control (%q) that exited 0: the gate has never been shown to reject "+
					"anything, so its pass would mean nothing — fix the control in the repo's standards file",
				chk.Name, chk.Proof)
		default:
			status = "proven"
		}
	}

	res, err := c.exec(ctx, runner, sb, chk.Command)
	if err != nil {
		// Could-not-run is neither a pass nor a rejection on the merits. It is never a
		// pass (measure_task's exit-vs-transport rule: a dead container must not
		// false-green), and the detail says so rather than reporting an exit status that
		// no command ever produced. Retryable, like measure's transport faults.
		return c.finding(chk, false, fmt.Sprintf("not-run — the command could not be executed in the sandbox: %v", err)), nil
	}
	if res.ExitCode == 0 {
		return c.finding(chk, true, fmt.Sprintf("%s — %q exited 0", status, chk.Command)), nil
	}
	return c.finding(chk, false, fmt.Sprintf("%s — %q FAILED with exit status %d%s",
		status, chk.Command, res.ExitCode, snippet(res))), nil
}

// finding builds the check's finding. `required` is the SOLE rejection axis: a
// non-required check is advisory whatever happened, so it is reported (and rendered as a
// failure when it failed) without gating.
func (c *Checks) finding(chk Check, passed bool, detail string) floors.Finding {
	return floors.Finding{
		Floor:    FloorPrefix + chk.Name,
		Passed:   passed,
		Detail:   detail,
		Advisory: !chk.Required,
	}
}

// exec runs one repo-authored command in the container, never on the host.
func (c *Checks) exec(ctx context.Context, runner cleanroom.Runner, sb cleanroom.Sandbox, command string) (cleanroom.Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return runner.Exec(runCtx, sb, []string{"sh", "-c", command})
}

// snippet appends a short tail of the command's own output so the human reading a park
// has the failure in front of them rather than only its exit code.
//
// Two things it must not do. The output is produced by a repo command run over
// ATTEMPT-AUTHORED code, and it lands in floor.finding.detail — a scalar that is
// newline-separated per floor and that a retry prompt feeds back to the developer model.
// Raw newlines there forge additional floor lines (a fabricated "presence: passed"), so
// every control byte is flattened. And it must stay valid UTF-8: a byte-slice tail can
// start mid-rune, which JSON silently replaces rather than rejecting.
func snippet(res cleanroom.Result) string {
	out := strings.TrimSpace(res.Stderr + "\n" + res.Stdout)
	if out == "" {
		return ""
	}
	const tail = 400
	if len(out) > tail {
		out = out[len(out)-tail:]
	}
	return ": " + flattenControl(strings.ToValidUTF8(out, "\uFFFD"))
}

// flattenControl replaces every control byte (newlines included) with a space, so a
// command's output cannot inject structure into the finding detail it is quoted in.
func flattenControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// requiredCount reports how many declared checks actually gate an attempt.
func requiredCount(checks []Check) int {
	n := 0
	for _, c := range checks {
		if c.Required {
			n++
		}
	}
	return n
}
