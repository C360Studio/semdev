// Package boot centralizes registration for semdev's binaries. Both cmd/semdev
// and cmd/e2e-semdev MUST register components through RegisterAll and nothing
// else: a component present in one binary but not the other is the
// half-wired-binary silent-flow-break class — it compiles, passes review, and
// then drops facts on the floor in the binary that skipped it. One function, two
// callers, identical registration by construction.
//
// RegisterTools (the agentic tool registry) and RegisterLifecycle (the run-entity
// workflow) are SEPARATE seams: they need a live NATS client / lifecycle Manager,
// so they cannot sit in the static RegisterAll(reg) call and are wired into a
// shared runtime-boot path at group 11 — guarded by a binary-parity pin like the
// one RegisterAll already carries, so a binary cannot wire one and forget the
// other. Until then they are exercised only by the conformance censuses.
package boot

import (
	"context"
	"fmt"

	"github.com/c360studio/semdev/internal/changefacts"
	"github.com/c360studio/semdev/internal/cliexec"
	"github.com/c360studio/semdev/internal/forge/github"
	"github.com/c360studio/semdev/internal/runspace"
	"github.com/c360studio/semdev/internal/tools/applypatch"
	"github.com/c360studio/semdev/internal/tools/checkfloors"
	"github.com/c360studio/semdev/internal/tools/checkgate"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/hydratechange"
	"github.com/c360studio/semdev/internal/tools/listcomments"
	"github.com/c360studio/semdev/internal/tools/measuretask"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
	"github.com/c360studio/semdev/internal/tools/provisionsandbox"
	"github.com/c360studio/semdev/internal/tools/submitreview"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semdev/internal/tools/verifyartifact"
	"github.com/c360studio/semdev/internal/tools/writechange"
	"github.com/c360studio/semstreams/agentic/agentrun"
	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/componentregistry"
	"github.com/c360studio/semstreams/pkg/lifecycle"
	agentictools "github.com/c360studio/semstreams/processor/agentic-tools"
	"github.com/c360studio/semstreams/processor/agentic-tools/executors"
)

// RegisterAll registers every component semdev's binaries run into reg. It wraps
// semstreams' framework registration (componentregistry.Register); semdev's own
// components — the G1-gated tools and processors — register here as the
// capability groups land, so both binaries pick them up together.
func RegisterAll(reg *component.Registry) error {
	if err := componentregistry.Register(reg); err != nil {
		return fmt.Errorf("register framework components: %w", err)
	}
	// semdev's own components register here (task 2.1 onward). Keep every
	// addition inside this function so both binaries stay in lockstep.
	return nil
}

// RegisterTools registers every agentic tool executor semdev exposes into reg:
// the framework builtins plus semdev's own G1-gated tools. The G3 schema census
// builds the tool registry through this same seam, so a semdev tool cannot land
// uncovered by the outcome-field pin — the census and production registration
// cannot drift.
//
// githubToken is INJECTED (not read from the environment here) so this function is
// hermetic: the census passes "" and deterministically takes each host tool's
// schema-only nil path regardless of the ambient env, while the runtime boot
// (cmd/*) reads os.Getenv("GITHUB_TOKEN") at the composition edge and passes it in.
// (The framework's own RegisterBuiltins still reads GITHUB_TOKEN internally for its
// github_read/write tools — that is framework behavior, outside this seam.)
//
// sandboxes is the run-scoped WARM dev-container registry (created by the runtime so
// Stop can reap it): provision_sandbox writes to it, measure_task reads from it. The
// census passes nil so those seams stay literal-nil and the tools register
// schema-only; the live boot passes the shared instance.
func RegisterTools(ctx context.Context, reg *agentictools.ExecutorRegistry, deps executors.ToolDependencies, githubToken string, sandboxSourceDir string, sandboxes *runspace.Sandboxes) error {
	if err := executors.RegisterBuiltins(ctx, reg, deps); err != nil {
		return fmt.Errorf("register builtin tools: %w", err)
	}

	// A tool that OWNS a mutable fact package holds an OwnedFactWriter (replace-by-
	// predicate) built from the NATS client. When there is no client (the
	// schema-scanning censuses), the writer is nil and the tool registers
	// schema-only; its Execute fails loudly if ever called without one, so a fact
	// is never silently dropped. RegisterExecutor derives the tool name from the
	// executor's own ListTools, so the registered name cannot drift from its
	// advertised schema.
	var changeWriter agentictools.OwnedFactWriter
	if deps.NATSClient != nil {
		changeWriter = agentictools.NewNATSOwnedFactWriter(deps.NATSClient)
	}
	if err := reg.RegisterExecutor(createchange.New(changeWriter, deps.Platform, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", createchange.ToolName, err)
	}

	// The read-side openspec-io tools take a changefacts.Reader (query-only, full
	// triples) — the read analogue of the OwnedFactWriter. Nil without a client
	// (the schema-scanning censuses); Execute fails loudly if called without one.
	var factReader changefacts.Reader
	if deps.NATSClient != nil {
		factReader = changefacts.NewNATSReader(deps.NATSClient)
	}
	if err := reg.RegisterExecutor(hydratechange.New(factReader, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", hydratechange.ToolName, err)
	}

	// write_change materializes the change to the run's target-repo workspace. The
	// workspace resolver (which locates the run's checkout) is forge-io / clean-room
	// runtime state (groups 5/8), so it is nil here; the tool registers schema-only
	// and fails loudly if executed without one, and the group-11 journey injects a
	// real resolver.
	var workspace writechange.WorkspaceResolver // nil until the checkout seam lands
	if err := reg.RegisterExecutor(writechange.New(factReader, workspace, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", writechange.ToolName, err)
	}

	// validate_change shells the real OpenSpec CLI as the compatibility oracle and
	// stamps openspec.validated from the real exit code (harness-measured, G3). The
	// exec runner is a plain os/exec seam (no NATS); the owned-fact writer stamps/
	// clears the marker and is nil without a client (schema-only census). It reuses
	// the same OwnedFactWriter transport as create_change — each tool stamps its own
	// Source per triple (G5), so sharing the transport is safe.
	if err := reg.RegisterExecutor(validatechange.New(factReader, cliexec.OSRunner{}, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", validatechange.ToolName, err)
	}

	// github_list_comments (forge-io) reads an issue/PR thread via the semdev
	// GitHub client, built from the injected githubToken. Without a token the tool
	// registers schema-only (pass a literal nil interface — NOT a typed-nil *Client
	// — so the executor's nil-check fires) and fails loudly if executed.
	if githubToken != "" {
		if err := reg.RegisterExecutor(listcomments.New(github.NewClient(githubToken).WithLogger(deps.Logger), deps.Logger)); err != nil {
			return fmt.Errorf("register %s: %w", listcomments.ToolName, err)
		}
	} else if err := reg.RegisterExecutor(listcomments.New(nil, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", listcomments.ToolName, err)
	}
	// project_tasks (dev-from-task) reads a run's approved change task facts, projects
	// them through devtask.Project, and stamps the immutable task.spec. It reads via
	// the shared changefacts.Reader and writes task.spec via the shared OwnedFactWriter
	// (its own Source, task-projector — sharing the transport is G5-safe). Both nil in
	// the census (schema-only); Execute fails loudly if either is missing.
	if err := reg.RegisterExecutor(projecttasks.New(factReader, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", projecttasks.ToolName, err)
	}

	// The run's CHECKOUT + everything the dev-loop/verify tools read from it are the
	// runspace seams (group 4): Checkouts (the per-run materialized working copy —
	// measure_task/verify_artifact's Workspace), Manifests (the declared image + run
	// fields — verify_artifact's semdev-init seam), and Attempts (the authored files for
	// the floors). They are the concrete forge-io-checkout / semdev-init implementations
	// the tools declared nil at M0. Wired only with a live client so the schema-scanning
	// censuses keep a LITERAL-nil interface (a typed-nil would slip past the tools'
	// nil-checks); Execute fails loudly if a seam is missing, and each seam fails closed
	// when a run has no checkout (park toward the human, never a silent host-path guess).
	var (
		verifyClones verifyartifact.VerifyClones // verify_artifact's fresh cold clone of the committed artifact
		manifests    verifyartifact.Manifests    // verify_artifact's reproducibility manifest
		attempts     checkfloors.Attempts        // check_floors' authored-attempt files
		// measure_task (group 7B) runs the frozen test command IN the run's WARM sandbox
		// container — provision_sandbox stood it up over the checkout. The warm-sandbox
		// registry is the SAME *runspace.Sandboxes instance provision writes to and
		// measure reads from (one run, one warm container), passed in so the runtime can
		// reap it on Stop.
		measureSandboxes measuretask.Sandboxes
		// provision_sandbox (group 5/7B) stands the checkout UP and, on a proven-cold
		// baseline, stands the WARM dev container up: it materializes the run's fresh
		// copy from its source (provCheckouts, from provSources), cold-proves the
		// declared image, then leaves the warm container Up (provWarmers) for the loop.
		// It shares the SAME *runspace.Checkouts instance the Workspace seams use, so the
		// checkout it materializes is exactly the one measure_task/verify later resolve —
		// one run, one on-disk working copy.
		provSources   provisionsandbox.Sources
		provCheckouts provisionsandbox.Checkouts
		provManifests provisionsandbox.Manifests
		provWarmers   provisionsandbox.Warmers
		// apply_patch (group 6) MUTATES the run's checkout — the same instance provision
		// stood up and measure/verify read, so the developer's authored diff lands in the
		// checkout the loop then measures.
		patcher applypatch.Patcher
	)
	if deps.NATSClient != nil {
		checkouts, cerr := runspace.NewCheckouts("")
		if cerr != nil {
			return fmt.Errorf("create run checkouts: %w", cerr)
		}
		verifyClones = checkouts
		manifests = runspace.Manifests{}
		attempts = runspace.NewAttempts(factReader, checkouts)
		// The warm-sandbox registry (created by the runtime and passed in so Stop can
		// reap it) is shared by provision (writer) and measure (reader).
		measureSandboxes = sandboxes
		provWarmers = sandboxes
		// The run's SOURCE (what to materialize the checkout from) is a StaticSource at
		// M0 — the operator-configured target dir (the in-repo fixture the journey
		// drives); forge-io's per-run `--recursive` PR clone lands behind this seam at
		// M2. An empty sandboxSourceDir makes Resolve fail closed → the run parks (SB5).
		provSources = runspace.StaticSource{Dir: sandboxSourceDir}
		provCheckouts = checkouts
		provManifests = runspace.Manifests{}
		// git apply runs on the host checkout root (= the container's /work bind-mount),
		// so the plain os/exec runner authors into the sandbox the loop measures.
		patcher = runspace.NewPatcher(checkouts, cliexec.OSRunner{})
	}

	// measure_task (harness-measurement) runs a projected task's IMMUTABLE
	// task.spec.<i>.test_command and stamps the OS-level outcome as measurement.result
	// (derived from the real exit code — G3). It reads the frozen command via the
	// shared changefacts.Reader, Execs it IN the run's warm sandbox container
	// (measureSandboxes — provision_sandbox stood it up over the checkout apply_patch
	// wrote), and upserts the measurement via the shared OwnedFactWriter (its own
	// Source, measurement-harness — G5-safe). Measuring in-container is the make-or-break:
	// the outcome is of the artifact built cold in the operator-declared image, never a
	// host process over an unproven environment. Each nil dep makes Execute fail loudly;
	// an unprovisioned sandbox makes Resolve fail closed (park) — never a silent host exec.
	if err := reg.RegisterExecutor(measuretask.New(factReader, measureSandboxes, changeWriter, deps.Platform, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", measuretask.ToolName, err)
	}

	// submit_review (harness-measurement) is Quinn's PER-TASK adversarial gate: given
	// a task_index it reads that task's task.spec + measurement.result facts, derives
	// the FLOORED verdict (measurement.CanApprove over that task ∧ no findings), and
	// stamps review.verdict.<i>. It reads via the shared changefacts.Reader and writes
	// via the shared OwnedFactWriter (its own Source, reviewer-quinn — G5-safe). Takes
	// no runner/workspace (it runs nothing). Both nil in the census (schema-only);
	// Execute fails loudly if either is missing.
	if err := reg.RegisterExecutor(submitreview.New(factReader, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", submitreview.ToolName, err)
	}

	// verify_artifact (clean-room-verify) is the G4 gate — the THIRD sandbox instance
	// (design SB4.3): it CLONES the run's committed artifact into a fresh dir
	// (verifyClones = the same *runspace.Checkouts, non-destructive of the warm checkout),
	// builds the declared image and runs resolve then the artifact's own tests cold (the
	// test step compiles the artifact) in a fresh throwaway
	// container with a fresh dependency cache (DefaultProver → coldproof.ProveArtifact),
	// judges the evidence with verify.Decide, and stamps verify.result. The fresh clone +
	// fresh cache is what makes a cache-masked fabrication or a harness-only fixup FAIL
	// here (SB3, the semspec grave). HOW to prove it (manifests) is resolved from the
	// clone. It writes via the shared OwnedFactWriter (its own Source, verify-harness —
	// G5-safe). store is nil at M0 (no governed secrets). Execute fails loudly if any nil
	// seam is missing; a proof that cannot run is retryable, never a silent green.
	if err := reg.RegisterExecutor(verifyartifact.New(verifyClones, manifests, verifyartifact.DefaultProver(), nil, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", verifyartifact.ToolName, err)
	}

	// check_floors (dev-from-task) is the floor-tools wrapper: it runs the pure floor
	// library over a task's current attempt and stamps floor.finding. WHICH files the
	// attempt authored is the runspace Attempts seam wired above (it reads the task's
	// declared target files from the run's checkout). It writes via the shared
	// OwnedFactWriter (its own Source, floor-tools — G5-safe). Execute fails loudly if
	// either seam is missing.
	if err := reg.RegisterExecutor(checkfloors.New(attempts, changeWriter, deps.Platform, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", checkfloors.ToolName, err)
	}

	// check_gate (dev-from-task, group 7D) is the dev loop's budget-and-route gate: it
	// reads the recorded measurement.result/floor.finding verdicts and the distinct
	// attempt count against task.spec.<i>.budget (all off the run via the shared
	// changefacts.Reader) and derives advance/retry/escalate in Go — the model supplies
	// only the task index (G3), and it fails CLOSED (escalate) on a missing judgment
	// fact. It fires no lifecycle transition (G2): it stamps the decision as dev.gate.*
	// evidence + a dev.gate_decision loop marker (its own Source, gate-tools — G5-safe),
	// and three router rules act on the marker. deps.Platform builds the gate loop's
	// entity id for the marker. Each nil dep makes Execute fail loudly.
	if err := reg.RegisterExecutor(checkgate.New(factReader, changeWriter, deps.Platform, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", checkgate.ToolName, err)
	}

	// provision_sandbox (sandbox) is the provision-and-prove-cold station: on an
	// approved run a rule forces it to materialize the run's checkout (provCheckouts,
	// from provSources), build the operator-declared image (provManifests), prove the
	// repo builds COLD (DefaultProver → coldproof.ProveBaseline), and — on a proven
	// baseline — stand up the WARM dev container the loop measures in (provWarmers, the
	// shared Sandboxes), then stamp the derived sandbox.ready/attestation or a
	// sandbox.blocked reason (G3). It shares the run's changefacts.Reader (idempotency
	// guard) and the OwnedFactWriter (its own Source, sandbox-provisioner — G5-safe).
	// store is nil at M0 (no governed secrets, SB2c). Each nil seam makes Execute fail
	// loudly — a sandbox it cannot prove or stand up is a park, never a silent skip (SB5).
	if err := reg.RegisterExecutor(provisionsandbox.New(provSources, provCheckouts, provManifests, provWarmers, provisionsandbox.DefaultProver(), nil, factReader, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", provisionsandbox.ToolName, err)
	}

	// apply_patch (sandbox, SB6) is the developer's code-authoring tool: it applies a
	// unified diff to the run's checkout path-guarded to inside it (never the host) and
	// reports the touched files. It measures no outcome (G3) and stamps no fact (G2) —
	// the dev loop's measure_task/floors read the mutated checkout. Nil patcher (the
	// census) makes Execute fail loudly.
	if err := reg.RegisterExecutor(applypatch.New(patcher, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", applypatch.ToolName, err)
	}

	return nil
}

// RegisterLifecycle registers semdev's run-entity workflow into the lifecycle
// Manager: the framework's agent-run workflow is the run entity (design D2).
// Rules own every transition on it (G2) — product Go registers the workflow
// declaration here but fires no transition. The binaries call this when they
// wire the runtime (with a live Manager); the run-lifecycle rule pack drives the
// phase transitions.
func RegisterLifecycle(mgr *lifecycle.Manager) error {
	if err := agentrun.Register(mgr); err != nil {
		return fmt.Errorf("register agent-run workflow: %w", err)
	}
	return nil
}
