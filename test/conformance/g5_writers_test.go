package conformance

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c360studio/semdev/internal/conversationchannel"
	"github.com/c360studio/semdev/internal/conversationintent"
	"github.com/c360studio/semdev/internal/experiment"
	"github.com/c360studio/semdev/internal/intake/admission"
	"github.com/c360studio/semdev/internal/station"
	"github.com/c360studio/semdev/internal/tools/applypatch"
	"github.com/c360studio/semdev/internal/tools/checkfloors"
	"github.com/c360studio/semdev/internal/tools/classifyintent"
	"github.com/c360studio/semdev/internal/tools/createchange"
	"github.com/c360studio/semdev/internal/tools/measuretask"
	"github.com/c360studio/semdev/internal/tools/openpr"
	"github.com/c360studio/semdev/internal/tools/projecttasks"
	"github.com/c360studio/semdev/internal/tools/provisionsandbox"
	"github.com/c360studio/semdev/internal/tools/submitreview"
	"github.com/c360studio/semdev/internal/tools/validatechange"
	"github.com/c360studio/semdev/internal/tools/verifyartifact"
	"github.com/c360studio/semdev/internal/vocab"
)

// G5 — single writer per fact. Every predicate maps to exactly one writer in the
// checked-in table. semspec's audit found four multi-writer facts, one whose
// "sole writer" claim was a false comment; here the property is a census, not a
// comment.
func TestSingleWriterPerPredicate(t *testing.T) {
	if len(vocab.Predicates) == 0 {
		t.Fatal("vocabulary is empty; the writers census would pass vacuously")
	}
	for _, v := range singleWriterViolations(vocab.Predicates) {
		t.Error(v)
	}
	for _, v := range duplicateNameViolations(vocab.Predicates) {
		t.Error(v)
	}
	for _, v := range namespaceWriterViolations(vocab.Predicates) {
		t.Error(v)
	}
}

// G5 verifiability — the Source a fact-writing tool stamps on its triples must
// equal the single writer the vocab declares for that tool's predicate namespace.
// Without this the sole-writer claim is a table string nothing ties to what
// actually lands on the graph — the "sole writer was a false comment" disease.
// One entry per fact-writing tool.
func TestToolSourceMatchesVocabWriter(t *testing.T) {
	cases := []struct {
		tool      string
		source    string
		predicate string // any predicate under the tool's namespace
	}{
		{"create_change", createchange.Source, "openspec.change.document"},
		{"validate_change", validatechange.Source, validatechange.ValidatedPredicate},
		{"project_tasks", projecttasks.Source, "task.spec.goal"},
		{"measure_task", measuretask.Source, "measurement.result.passed"},
		{"submit_review", submitreview.Source, "review.verdict.value"},
		{"submit_review", submitreview.Source, "review.findings.value"},
		// classify_intent stamps the routing intent under conversation-classifier
		// (nl-conversation-intent D2/D10) — the tie between the tool's Source const
		// and the single writer vocab declares for the conversation.intent.* namespace.
		{"classify_intent", classifyintent.Source, conversationintent.IntentValuePredicate},
		{"classify_intent", classifyintent.Source, conversationintent.IntentClassifiedPredicate},
		// conversation.pending.*: the NL bridge (handleMessage, group 3) stamps the
		// authorized human message under conversation-adapter — a DISTINCT Source from
		// the gate writer (approval-adapter) the same struct also emits. The tie
		// between AdapterSource and the single writer vocab declares for the pending
		// namespace (nl-conversation-intent D5).
		{"conversation-adapter", conversationintent.AdapterSource, conversationintent.PendingMessageIDPredicate},
		{"conversation-adapter", conversationintent.AdapterSource, conversationintent.PendingAuthorPredicate},
		{"conversation-adapter", conversationintent.AdapterSource, conversationintent.PendingBodyPredicate},
		// The GATE facts: run.change.approved / run.change.rejected are ONE logical
		// writer (approval-adapter) realized at TWO code sites — the exact-command
		// fast-path and the apply consumer — both routed through the shared
		// stampGateFact (structurally pinned by TestOnlySanctionedGateWriters). These
		// ties keep the Source const from drifting off what vocab declares
		// (nl-conversation-intent grp3-review L4 / D11).
		{"approval-adapter", conversationchannel.ApprovedSource, admission.ApprovedPredicate},
		{"approval-adapter", conversationchannel.ApprovedSource, admission.RejectedPredicate},
		{"verify_artifact", verifyartifact.Source, verifyartifact.ResultPredicate},
		{"check_floors", checkfloors.Source, "floor.finding.rejected"},
		// The route mirror (design R1): check_floors + submit_review both stamp the route.*
		// facts onto their firing loop under ONE logical writer route-mirror (a distinct
		// Source from floor-tools / reviewer-quinn, so no predicate gains two writers, G5).
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RoutePassedPredicate},
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteRejectedPredicate},
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteAttemptPredicate},
		// route.task.budget: the per-task attempt budget, mirrored by BOTH sanctioned
		// route-mirror sites (adopt-per-task-routing-budgets, #568) — one logical writer,
		// two code sites (the route.attempt.instance precedent).
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteBudgetPredicate},
		// route.transient.instance: the transient-retry counter mirror (adopt-reason-aware-escalate),
		// stamped by check_floors under route-mirror alongside the other route.* mirrors.
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteTransientPredicate},
		// route.attempt.transient: the transient classification of the loop's terminal —
		// stamped by check_floors IN the atomic mirror (never by a rule: a rule-stamped
		// sibling lands in its own KV revision and the convergence routes race it).
		{"check_floors", checkfloors.RouteMirrorSource, checkfloors.RouteTransientFlagPredicate},
		// experiment.run.condition: the A/B evidence label, stamped by the launch
		// path (experiment-intake) — the tie between the vocab writer string and
		// the Source the transport actually lands on the graph.
		{"experiment-intake", experiment.Source, experiment.ConditionPredicate},
		{"submit_review", submitreview.RouteMirrorSource, submitreview.RouteVerdictPredicate},
		{"submit_review", submitreview.RouteMirrorSource, submitreview.RouteAttemptPredicate},
		{"submit_review", submitreview.RouteMirrorSource, submitreview.RouteBudgetPredicate},
		{"open_pr", openpr.Source, openpr.RefPredicate},
		{"provision_sandbox", provisionsandbox.Source, provisionsandbox.ReadyPredicate},
		{"apply_patch", applypatch.Source, applypatch.CommitPredicate},
		// station.dispatch.failed: the generic station BASE (not a handler) stamps
		// the terminal dispatch outcome under the station-harness writer
		// (station-failure-parks D1) — the tie between the harness Source const
		// and the vocab's single-writer entry.
		{"station-harness", station.DispatchFailedSource, station.DispatchFailedPredicate},
	}
	for _, c := range cases {
		writer, ok := vocab.WriterOf(c.predicate)
		if !ok {
			t.Errorf("%s: predicate %q has no vocab writer", c.tool, c.predicate)
			continue
		}
		if c.source != writer {
			t.Errorf("%s stamps Source %q but vocab declares writer %q for %q — G5 unverifiable drift", c.tool, c.source, writer, c.predicate)
		}
	}
}

// Red-first: the namespace census must flag a concrete predicate under a
// declared namespace that declares a different writer.
func TestNamespaceWriterCensusCatchesConflict(t *testing.T) {
	preds := []vocab.Predicate{
		{Name: "openspec.change.*", Writer: "author-tool", Capability: "openspec-io", IntroducedBy: "m0-walking-skeleton-spine"},
		{Name: "openspec.change.title", Writer: "some-other-writer", Capability: "openspec-io", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	if len(namespaceWriterViolations(preds)) == 0 {
		t.Error("census passed a namespace member with a conflicting writer; G5 namespace pin does not fire")
	}
}

// Red-first: the census must catch a predicate stamped by two writers — the
// exact shape that bred semspec's wedge families — and a writer-less predicate.
func TestSingleWriterCensusCatchesViolations(t *testing.T) {
	multiWriter := []vocab.Predicate{
		{Name: "x.y", Writer: "alpha", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"},
		{Name: "x.y", Writer: "beta", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"},
	}
	if len(singleWriterViolations(multiWriter)) == 0 {
		t.Error("census passed a predicate with two writers; G5 pin does not fire")
	}
	noWriter := []vocab.Predicate{{Name: "x.y", Capability: "forge-io", IntroducedBy: "m0-walking-skeleton-spine"}}
	if len(singleWriterViolations(noWriter)) == 0 {
		t.Error("census passed a writer-less predicate; G5 pin does not fire")
	}
}

// isTripleLit reports whether a composite literal constructs a message.Triple
// (by type name), so the positional-element scan cannot fire on an unrelated
// slice or map literal that merely mentions a gate predicate.
func isTripleLit(lit *ast.CompositeLit) bool {
	switch typ := lit.Type.(type) {
	case *ast.Ident:
		return typ.Name == "Triple"
	case *ast.SelectorExpr:
		return typ.Sel.Name == "Triple"
	}
	return false
}

// TestOnlySanctionedGateWriters is the D11 gate-writer census
// (nl-conversation-intent task 5.5, semstreams confirmed-clean requirement).
//
// run.change.approved and run.change.rejected are ONE logical writer
// (approval-adapter) realized at TWO code sites: the exact-command fast-path
// (releaseGate) and the deterministic apply consumer (handleDispatch). That is the
// sanctioned "one logical writer, multiple realizing sites" precedent — but it only
// holds if the sites cannot drift their Source apart, which is exactly what a
// comment claiming "we both use approval-adapter" fails to guarantee.
//
// This pin asserts the sharing STRUCTURALLY within internal/conversationchannel:
// the sole way either site there writes a gate fact is approvalAdapter.stampGateFact.
// SCOPE, stated honestly (grp5-review M3/M4): the scan is package-scoped, so it
// cannot see a gate write introduced in some OTHER package — TestSingleWriterPerPredicate
// censuses the vocab table rather than code, so that gap is real. It is bounded by
// the fact that both gate predicates live in the admission core and both writing
// lanes are in this package by construction; a new writer elsewhere would be a
// deliberate architectural move, not a drift. Within the package the matcher covers
// composite-literal keys, field assignments, positional literals, and raw string
// literals, so the obvious evasions are closed. The complementary SOURCE check
// catches the remaining shape — a writer that takes its predicate as a parameter
// and so never names a gate fact — because any such writer must still stamp
// ApprovedSource to satisfy the vocab census.
//
// RESIDUAL GAP, stated precisely rather than papered over: a writer that takes its
// predicate as a PARAMETER and stamps either a rogue Source OR NO Source at all
// would evade both checks (a param-named predicate with the sanctioned Source IS
// caught) —
// but it would then be writing a gate fact under an unsanctioned Source, which is
// the plain G5 violation TestSingleWriterPerPredicate's vocab table exists to make
// reviewable. No static check closes this completely; these two close every shape
// that could arise from ordinary drift.
func TestOnlySanctionedGateWriters(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "conversationchannel")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	// The ONE sanctioned realization. Any other site building a message.Triple
	// with a gate predicate is a second writer.
	const sharedWriter = "stampGateFact"
	fset := token.NewFileSet()
	var constructors []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			// Does this function construct a triple carrying a gate predicate? The
			// matcher deliberately covers every FORM a gate write can take, because
			// the first version of this census matched only composite-literal
			// `Predicate:` keys and let three trivial evasions through
			// (grp5-review, semstreams M4): a positional literal, a field assignment,
			// and a differently-named predicate parameter.
			var buildsGateTriple bool
			stampsGateSource := func(expr ast.Expr) bool {
				var buf strings.Builder
				_ = printer.Fprint(&buf, fset, expr)
				return strings.Contains(buf.String(), "ApprovedSource")
			}
			namesGate := func(expr ast.Expr) bool {
				var buf strings.Builder
				_ = printer.Fprint(&buf, fset, expr)
				v := strings.TrimSpace(buf.String())
				return strings.Contains(v, "ApprovedPredicate") ||
					strings.Contains(v, "RejectedPredicate") ||
					strings.Contains(v, `"run.change.`) // the raw-literal evasion
			}
			ast.Inspect(fn, func(inner ast.Node) bool {
				switch node := inner.(type) {
				case *ast.KeyValueExpr:
					// Triple{Predicate: <gate>}
					if key, ok := node.Key.(*ast.Ident); ok && key.Name == "Predicate" && namesGate(node.Value) {
						buildsGateTriple = true
					}
					// The SOURCE test is what catches a rogue writer that takes its
					// predicate as a parameter (so it never names a gate fact): any
					// gate write must still stamp the sanctioned Source to be honoured
					// by the vocab census, and only the shared writer may do that.
					if key, ok := node.Key.(*ast.Ident); ok && key.Name == "Source" && stampsGateSource(node.Value) {
						buildsGateTriple = true
					}
				case *ast.AssignStmt:
					// t.Predicate = <gate>
					for i, lhs := range node.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if !ok || sel.Sel.Name != "Predicate" || i >= len(node.Rhs) {
							continue
						}
						if namesGate(node.Rhs[i]) {
							buildsGateTriple = true
						}
					}
				case *ast.CompositeLit:
					// Triple{subj, <gate>, ...} — a positional literal has no field names,
					// so any gate-naming element counts. Restricted to TRIPLE-typed
					// literals: a []string{Approved, Rejected} in a READER (decidedGateFact
					// checks which fact already landed) names both predicates and writes
					// nothing.
					if !isTripleLit(node) {
						return true
					}
					for _, elt := range node.Elts {
						if _, isKV := elt.(*ast.KeyValueExpr); isKV {
							continue
						}
						if namesGate(elt) {
							buildsGateTriple = true
						}
					}
				}
				return true
			})
			if buildsGateTriple {
				constructors = append(constructors, fn.Name.Name)
			}
			return true
		})
	}

	if len(constructors) == 0 {
		t.Fatal("found no gate-fact triple construction in internal/conversationchannel — the census would pass vacuously (did stampGateFact move or change shape?)")
	}
	for _, name := range constructors {
		if name != sharedWriter {
			t.Errorf("function %q constructs a run.change.* gate triple directly — the gate facts are ONE logical writer (G5/D11) and BOTH the exact-command fast-path and the apply consumer must route through %s(); a second construction site is how the two sites silently drift their Source apart", name, sharedWriter)
		}
	}
}
