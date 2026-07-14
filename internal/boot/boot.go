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
	"github.com/c360studio/semdev/internal/station/delivery"
	"github.com/c360studio/semdev/internal/station/floors"
	"github.com/c360studio/semdev/internal/station/projection"
	"github.com/c360studio/semdev/internal/station/validation"
	stationverify "github.com/c360studio/semdev/internal/station/verify"
	"github.com/c360studio/semdev/internal/tools/applypatch"
	"github.com/c360studio/semdev/internal/tools/checkfloors"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/hydratechange"
	"github.com/c360studio/semdev/internal/tools/listcomments"
	"github.com/c360studio/semdev/internal/tools/measuretask"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
	"github.com/c360studio/semdev/internal/tools/provisionsandbox"
	"github.com/c360studio/semdev/internal/tools/readdiff"
	"github.com/c360studio/semdev/internal/tools/readworkspace"
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
//
// checkouts and sandboxes are boot's SHARED, run-scoped, PROCESS-LOCAL runspace
// instances (the same ones RegisterTools wires into the dev-loop tools). The R6
// deterministic-station components that read the run's checkout or warm container
// (floors and verify, and later provision) MUST capture the SAME instance the tools use —
// a component that built its own would get a different empty map and never find the
// run's checkout. Self-sufficient stations (delivery/projection/validation) ignore
// them (their deps build from the NATS client at construction). Both are nil on the
// schema-scanning census path: the checkout/sandbox factories still register (so the
// G1 census sees them) but fail loud if ever CONSTRUCTED without the seam — which the
// census never does (it only inspects the registry). The live boot passes the shared
// instances, created before this call.
func RegisterAll(reg *component.Registry, checkouts *runspace.Checkouts, sandboxes *runspace.Sandboxes) error {
	if err := componentregistry.Register(reg); err != nil {
		return fmt.Errorf("register framework components: %w", err)
	}
	// Self-sufficient R6 stations (publish-triggered, zero model turns) — deps build
	// from the NATS client via component.Dependencies at component-manager start.
	if err := delivery.Register(reg); err != nil {
		return fmt.Errorf("register delivery station: %w", err)
	}
	if err := projection.Register(reg); err != nil {
		return fmt.Errorf("register projection station: %w", err)
	}
	if err := validation.Register(reg); err != nil {
		return fmt.Errorf("register validation station: %w", err)
	}
	// Checkout/sandbox-dependent R6 stations — capture boot's shared runspace instances.
	if err := floors.Register(reg, checkouts); err != nil {
		return fmt.Errorf("register floors station: %w", err)
	}
	if err := stationverify.Register(reg, checkouts); err != nil {
		return fmt.Errorf("register verify station: %w", err)
	}
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
// checkouts (the run's on-disk working-copy registry) and sandboxes (the WARM
// dev-container registry) are boot's SHARED, run-scoped, PROCESS-LOCAL runspace
// instances, created by the runtime BEFORE RegisterAll so the R6 checkout/sandbox
// station components capture the SAME instances these tools use. The census passes
// nil for both, so those seams stay literal-nil and the tools register schema-only;
// the live boot passes the shared instances.
func RegisterTools(ctx context.Context, reg *agentictools.ExecutorRegistry, deps executors.ToolDependencies, githubToken string, sandboxSourceDir string, checkouts *runspace.Checkouts, sandboxes *runspace.Sandboxes) error {
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
		// read_workspace/read_diff (multi-turn dev loop, design simplify-m0-execution-rail
		// R7/tasks 4.2-4.3) are Amelia's and Quinn's read-only windows into the SAME
		// checkout instance apply_patch mutates: read_workspace resolves the checkout root
		// (mirroring measure_task's Workspace seam) and read_diff resolves base..HEAD over
		// it. Neither writes a fact or fires a transition (G3/G2) — they exist only because
		// no existing primitive can put checkout bytes or the authored diff into a loop
		// (G1).
		rwWorkspace readworkspace.Workspace
		rdDiffer    readdiff.Differ
	)
	if deps.NATSClient != nil {
		// checkouts is boot's SHARED run-checkout registry, created before RegisterAll and
		// passed in (the floors/verify/provision station components capture the SAME
		// instance). A live client with a nil checkouts is a wiring fault — fail loud
		// rather than materialize into an instance the components can't see.
		if checkouts == nil {
			return fmt.Errorf("register tools: live NATS client but nil run checkouts (wiring fault — the shared instance must be created before RegisterTools)")
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
		patcher = runspace.NewPatcher(checkouts, cliexec.OSRunner{}, factReader)
		// read_workspace/read_diff share this SAME *runspace.Checkouts instance — the
		// checkout apply_patch mutates and provision materializes is exactly the one they
		// read from.
		rwWorkspace = checkouts
		rdDiffer = checkouts
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
	if err := reg.RegisterExecutor(measuretask.New(factReader, measureSandboxes, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", measuretask.ToolName, err)
	}

	// submit_review (harness-measurement) is Quinn's PER-TASK adversarial gate: given
	// a task_index it reads that task's task.spec + measurement.result facts, derives
	// the FLOORED verdict (measurement.CanApprove over that task ∧ no findings), and
	// stamps review.verdict.<i>. It reads via the shared changefacts.Reader and writes
	// via the shared OwnedFactWriter (its own Source, reviewer-quinn — G5-safe). Takes
	// no runner/workspace (it runs nothing). Both nil in the census (schema-only);
	// Execute fails loudly if either is missing.
	if err := reg.RegisterExecutor(submitreview.New(factReader, changeWriter, deps.Platform, deps.Logger)); err != nil {
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
	// library over a task's current attempt and stamps floor.finding, AND (the reshape,
	// R1) mirrors the routing inputs onto its own loop — route.passed (the measurement it
	// reads via the shared changefacts.Reader), route.rejected (its aggregate), route.attempt
	// (the append-mirror of task.attempt) — under a distinct route-mirror Source so the
	// rule-native floors route (advance/not_clean/retry/escalate) fires on them. WHICH files
	// the attempt authored is the runspace Attempts seam wired above. It writes via the shared
	// OwnedFactWriter (floor.finding under floor-tools, the mirror under route-mirror — G5-safe).
	// deps.Platform builds the floors loop's entity id for the mirror. Each nil dep fails loud.
	if err := reg.RegisterExecutor(checkfloors.New(attempts, factReader, changeWriter, deps.Platform, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", checkfloors.ToolName, err)
	}

	// open_pr (forge-io, group 8D) is the run's delivery step: on a coherent run a router
	// rule forces it to record pr.ref. At M0 pr.ref is a deterministic LOCAL delivery stub
	// (the real forge-io PR is M2); the coherence gate that got here genuinely passed, only
	// the delivery target is stubbed (NOT semspec's placeholder-pass). G3 (no args), G2 (no
	// transition), single writer pr.ref (open-pr). Nil writer fails loud.
	if err := reg.RegisterExecutor(openpr.New(changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", openpr.ToolName, err)
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
	// unified diff to the run's checkout path-guarded to inside it (never the host),
	// COMMITS the applied attempt under the harness identity, and reports the touched
	// files. It measures no outcome (G3) and fires no transition (G2); it stamps exactly
	// the commit SHA the harness created as attempt.commit via the shared OwnedFactWriter
	// (its own Source, patch-committer — G5-safe), the immutable-snapshot pointer the cold
	// verify + read_diff target. Nil patcher/writer (the census) makes Execute fail loudly.
	if err := reg.RegisterExecutor(applypatch.New(patcher, changeWriter, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", applypatch.ToolName, err)
	}

	// read_workspace (multi-turn dev loop, design simplify-m0-execution-rail R7/task 4.2)
	// is the read sibling of apply_patch's write guard: it reads a repo-relative file or
	// directory listing out of the run's checkout, path-guarded through the SAME
	// containment (runspace.SafeJoin) the patcher enforces, paginated under the tool-result
	// byte cap. It is READ-ONLY — no writer, no fact, no transition (G3/G2) — and its G1
	// justification is that no existing primitive can put checkout bytes into a model's
	// turn (a rule only routes/aggregates facts; prompt templating carries triples, not
	// file contents). Nil workspace (the census) makes Execute fail loudly.
	if err := reg.RegisterExecutor(readworkspace.New(rwWorkspace, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", readworkspace.ToolName, err)
	}

	// read_diff (multi-turn dev loop, design simplify-m0-execution-rail R7/task 4.3) is the
	// reviewer's (Quinn's) window onto the cumulative authored change: `git diff
	// <base>..HEAD` over the run's committed checkout, which under the M0 one-in-flight
	// serialization invariant is exactly base..attempt.commit. READ-ONLY — no writer, no
	// fact, no transition (G3/G2) — and its G1 justification is the same as
	// read_workspace's: no existing primitive can put the authored diff into a loop. Nil
	// differ (the census) makes Execute fail loudly.
	if err := reg.RegisterExecutor(readdiff.New(rdDiffer, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", readdiff.ToolName, err)
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
