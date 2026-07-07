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
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/hydratechange"
	"github.com/c360studio/semdev/internal/tools/listcomments"
	"github.com/c360studio/semdev/internal/tools/measuretask"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
	"github.com/c360studio/semdev/internal/tools/validatechange"
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
func RegisterTools(ctx context.Context, reg *agentictools.ExecutorRegistry, deps executors.ToolDependencies, githubToken string) error {
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
	if err := reg.RegisterExecutor(createchange.New(changeWriter, deps.Logger)); err != nil {
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

	// measure_task (harness-measurement) runs a projected task's IMMUTABLE
	// task.spec.<i>.test_command and stamps the OS-level outcome as measurement.result
	// (derived from the real exit code — G3). It reads the frozen command via the
	// shared changefacts.Reader, runs it with the plain os/exec seam (cliexec.OSRunner,
	// as validate_change does), and upserts the measurement via the shared
	// OwnedFactWriter (its own Source, measurement-harness — G5-safe). WHERE the
	// command runs — the run's checkout root — is the forge-io / clean-room workspace
	// (nil here, schema-only like write_change's resolver; the group-11 journey injects
	// a real one). Each nil dep makes Execute fail loudly, never silently drop a fact.
	var checkout measuretask.Workspace // nil until the checkout seam lands
	if err := reg.RegisterExecutor(measuretask.New(factReader, cliexec.OSRunner{}, changeWriter, checkout, deps.Logger)); err != nil {
		return fmt.Errorf("register %s: %w", measuretask.ToolName, err)
	}

	// More semdev tools register here as later groups add them (floors, verify) —
	// each G1-gated (registry.Entries) and G3-scanned.
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
