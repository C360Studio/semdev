package conformance

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// The G2 denylist: firing a lifecycle transition from Go means calling one of
// these phase-mutating methods on a *lifecycle.Manager, or the agentrun.Mint
// wrapper (which calls Manager.Create). Rules fire transitions declaratively via
// the JSON `lifecycle_transition` action through a narrowed interface that omits
// Transition — so the legitimate path never appears as one of these calls.
const (
	lifecyclePkg = "github.com/c360studio/semstreams/pkg/lifecycle"
	agentrunPkg  = "github.com/c360studio/semstreams/agentic/agentrun"
)

var managerTransitionMethods = map[string]bool{
	"Transition":     true,
	"TransitionWith": true,
	"Complete":       true,
	"Fail":           true,
	"Create":         true,
}

// lifecycleTransitionExceptions is the ADR-linked exception table for product Go
// that fires a transition directly. Target size: 0 (constitution G2). An entry
// keys "file:line: symbol" to the ADR that sanctions it.
var lifecycleTransitionExceptions = map[string]string{}

// isNamedType reports whether t (deref'd through one pointer) is the named type
// pkgPath.name.
func isNamedType(t types.Type, pkgPath, name string) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == pkgPath && obj.Name() == name
}

// methodReceiverIsManager reports whether obj is a method whose declared receiver
// is *lifecycle.Manager. Keying on the method's own receiver (not the receiver
// expression's type) catches a call through an embedded *lifecycle.Manager,
// where the promoted method still declares the Manager receiver.
func methodReceiverIsManager(obj types.Object) bool {
	fn, ok := obj.(*types.Func)
	if !ok {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	return isNamedType(sig.Recv().Type(), lifecyclePkg, "Manager")
}

// lifecycleTransitionCallers type-checks the given package patterns and returns
// "file:line: symbol" for every call that fires a lifecycle transition: a
// managerTransitionMethods call whose method declares a *lifecycle.Manager
// receiver (direct or embedded), or agentrun.Mint. It resolves types via
// go/types, so it does not false-match a same-named method (Create/Complete) on
// an unrelated type.
//
// Known blind spots (inherent to a method-call census, documented so a future
// author does not assume total coverage): a transition fired through an
// interface value whose dynamic type is *lifecycle.Manager; a method value
// (`f := m.Complete; f(...)`); and a phase triple written directly through the
// graph write path, bypassing Manager entirely. Closing those needs a
// state-ownership pin (predicate-level), which lands with a later change.
func lifecycleTransitionCallers(t *testing.T, patterns ...string) []string {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: repoRoot(t),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatalf("load packages %v: %v", patterns, err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("packages.Load(%v) matched no packages; the G2 census would pass vacuously", patterns)
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, fmt.Sprintf("%s: %s", p.PkgPath, e))
		}
	})
	if len(loadErrs) > 0 {
		t.Fatalf("package load/type errors (the census cannot run on uncompilable code):\n%v", loadErrs)
	}

	var callers []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pos := pkg.Fset.Position(sel.Pos())
				if selection := pkg.TypesInfo.Selections[sel]; selection != nil {
					if selection.Kind() == types.MethodVal &&
						managerTransitionMethods[sel.Sel.Name] &&
						methodReceiverIsManager(selection.Obj()) {
						callers = append(callers, fmt.Sprintf("%s: (*lifecycle.Manager).%s", pos, sel.Sel.Name))
					}
					return true
				}
				if obj := pkg.TypesInfo.Uses[sel.Sel]; obj != nil {
					if fn, ok := obj.(*types.Func); ok && fn.Pkg() != nil &&
						fn.Pkg().Path() == agentrunPkg && fn.Name() == "Mint" {
						callers = append(callers, fmt.Sprintf("%s: agentrun.Mint", pos))
					}
				}
				return true
			})
		}
	}
	return callers
}

// G2 — rules own all lifecycle transitions. Product Go fires none. semspec ended
// with 5/6 lifecycle edges in Go; that is the disease this pin prevents. A
// direct Manager.Transition/Complete/Fail/Create or agentrun.Mint from product
// Go fails the build unless it is in the ADR-linked exception table (target 0).
func TestNoLifecycleTransitionCallersInProductGo(t *testing.T) {
	callers := lifecycleTransitionCallers(t, "./cmd/...", "./internal/...")
	for _, c := range callers {
		if _, excepted := lifecycleTransitionExceptions[c]; excepted {
			continue
		}
		t.Errorf("product Go fires a lifecycle transition (G2, B3): %s — rules own transitions; if a transition genuinely cannot be expressed, file an upstream semstreams ask and park toward the human, never a Go reconciler", c)
	}
}

// The exception table's target size is zero (constitution G2). It exists as a
// documented escape hatch, but at M0 it must be empty.
func TestLifecycleExceptionTableIsEmpty(t *testing.T) {
	if n := len(lifecycleTransitionExceptions); n != 0 {
		t.Errorf("lifecycle-transition exception table has %d entries; G2 target size is 0", n)
	}
}

// Red-first: the census must flag every transition-firing call. Runs the same
// type-aware scan over the testdata violation fixture and expects it to catch
// all five denylisted Manager methods (direct), the embedded-Manager call, and
// agentrun.Mint the fixture plants.
func TestG2CensusCatchesTransitionCallers(t *testing.T) {
	callers := lifecycleTransitionCallers(t, "./test/conformance/testdata/g2fixture")

	wantMethods := map[string]int{"Complete": 0, "Fail": 0, "Transition": 0, "Create": 0, "TransitionWith": 0}
	sawMint := false
	for _, c := range callers {
		for m := range wantMethods {
			// The caller string ends with the method name, so HasSuffix cleanly
			// distinguishes Transition from TransitionWith.
			if strings.HasSuffix(c, "(*lifecycle.Manager)."+m) {
				wantMethods[m]++
			}
		}
		if strings.Contains(c, "agentrun.Mint") {
			sawMint = true
		}
	}
	for m, n := range wantMethods {
		if n == 0 {
			t.Errorf("census missed the planted Manager.%s call: %v", m, callers)
		}
	}
	// The embedded-Manager call means Complete must appear at least twice
	// (one direct, one promoted) — this is the embedding-coverage red-first.
	if wantMethods["Complete"] < 2 {
		t.Errorf("census caught %d Complete calls; the embedded-Manager call was not caught: %v", wantMethods["Complete"], callers)
	}
	if !sawMint {
		t.Errorf("census missed the planted agentrun.Mint call: %v", callers)
	}
}
