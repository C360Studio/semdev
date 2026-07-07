package floors

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// The Go-source floors work on the parsed AST, never on raw bytes. An earlier
// text-scanning draft fell open in the classic ways a structural check must not:
// a brace inside a string literal corrupted a brace-counting body scan (a vacuous
// test escaped; an honest brace-string test was false-rejected), a commented-out
// assertion counted as real, and a target symbol named in a t.Run("...") label or
// a doc comment satisfied the anti-mock reference check. Parsing makes every floor
// blind to string and comment contents for free — which is exactly the property a
// backstop that "cannot be skipped" needs.

// failMethods are the *testing.T calls that can actually fail a test. A test body
// that makes none of these (and no framework assertion, and passes t to no helper)
// is vacuous.
var failMethods = map[string]bool{
	"Fatal": true, "Fatalf": true, "Error": true, "Errorf": true, "Fail": true, "FailNow": true,
}

// skipMethods mark an honest deferral (the Go analogue of semspec's @Disabled): a
// test that Skips is explicitly not-run, not vacuous.
var skipMethods = map[string]bool{"Skip": true, "Skipf": true, "SkipNow": true}

// assertionPkgs are assertion-library selector roots whose calls fail a test
// independently of the *testing.T receiver name (testify, matryer/is).
var assertionPkgs = map[string]bool{"require": true, "assert": true, "is": true}

// notImplementedPhrases are the not-implemented markers a stub panic or sentinel
// error carries. Matched case-insensitively against the string literal's content.
var notImplementedPhrases = []string{"not implemented", "unimplemented", "not yet implemented", "todo"}

func isGoFile(path string) bool   { return strings.HasSuffix(path, ".go") }
func isTestFile(path string) bool { return strings.HasSuffix(path, "_test.go") }

// parseFile parses one authored Go file. ok is false when it does not parse —
// SourceBuild owns that failure, so the other floors skip an unparseable file
// rather than guess at its bytes.
func parseFile(path, content string) (*token.FileSet, *ast.File, bool) {
	fset := token.NewFileSet()
	// Mode 0 (comments not parsed): every floor works on code nodes, so a comment
	// can never be mistaken for a code identifier or a stub marker.
	file, err := parser.ParseFile(fset, path, content, 0)
	if err != nil {
		return nil, nil, false
	}
	return fset, file, true
}

// TestsMustExist rejects an attempt that authored production Go source but no test
// function — the floor no fabrication can talk past, because the absence of a test
// is unambiguous. An attempt that touched only tests, or only non-Go files, does
// not trip it.
func TestsMustExist(a Attempt) Finding {
	authoredSource := false
	for _, f := range a.Files {
		if isGoFile(f.Path) && !isTestFile(f.Path) {
			authoredSource = true
			break
		}
	}
	if !authoredSource {
		return pass(FloorTestsMustExist, "no production Go source authored — floor does not apply")
	}
	for _, f := range a.Files {
		if !isTestFile(f.Path) {
			continue
		}
		_, file, ok := parseFile(f.Path, f.Content)
		if !ok {
			continue
		}
		for _, decl := range file.Decls {
			if fn, isFn := decl.(*ast.FuncDecl); isFn && isTestFunc(fn) {
				return pass(FloorTestsMustExist, "attempt authored at least one test function")
			}
		}
	}
	return reject(FloorTestsMustExist,
		"attempt authored production Go source but no *_test.go test function — a change without a test proves nothing")
}

// VacuousTest rejects a test function whose body can never fail: no *testing.T
// failing call, no assertion-library call, and no *testing.T handed to an
// in-attempt helper that itself asserts. A test that only logs, or asserts a
// constant tautology, inflates the passing count without proving anything. A test
// that Skips is exempt — that is the honest deferred form. Working on the AST means
// a renamed subtest receiver and an assertion inside a t.Run closure are
// recognized, and a commented-out or string-literal assertion is not.
//
// Helper delegation is credited only when the callee resolves to an authored
// function whose OWN body asserts (transitively) — passing t to a non-asserting
// setup helper, or logging t, does not count, so a "setup-plus-discarded-result"
// test is still flagged. Two accepted M0 fail-SAFE limits (they park honest work
// toward the human, never pass a fabrication): a test that delegates its only
// assertion to a helper defined outside the attempt (unresolvable) is flagged, as
// is one that aliases its receiver by assignment (tt := t; tt.Fatal()).
func VacuousTest(a Attempt) Finding {
	idx := buildFuncIndex(a)
	var offenders []string
	for _, f := range a.Files {
		if !isTestFile(f.Path) {
			continue
		}
		_, file, ok := parseFile(f.Path, f.Content)
		if !ok {
			continue // SourceBuild owns the parse failure
		}
		for _, decl := range file.Decls {
			fn, isFn := decl.(*ast.FuncDecl)
			if !isFn || !isTestFunc(fn) || fn.Body == nil {
				continue
			}
			if isSkipped(fn) || funcAsserts(fn, idx, map[string]bool{}) {
				continue
			}
			offenders = append(offenders, fmt.Sprintf("%s: %s", f.Path, fn.Name.Name))
		}
	}
	if len(offenders) == 0 {
		return pass(FloorVacuousTest, "no test function lacks a real assertion")
	}
	sort.Strings(offenders)
	return reject(FloorVacuousTest,
		"test function(s) with no assertion that can fail (only logging / tautology): "+strings.Join(offenders, "; ")+
			" — assert on computed behavior, or Skip with a reason if deferred")
}

// isTestFunc reports whether fn is a `func TestXxx(t *testing.T)` — top-level,
// name-prefixed Test, with a *testing.T parameter.
func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	return len(testingParamNames(fn.Type)) > 0
}

// funcIndex maps an authored top-level function name to its declaration, so a
// delegated assertion can be resolved to the helper that makes it.
type funcIndex map[string]*ast.FuncDecl

// buildFuncIndex indexes every authored top-level function across the attempt's
// parsed Go files (production and test), so VacuousTest can follow a test into the
// helpers it calls. Unparseable files are skipped — SourceBuild owns them.
func buildFuncIndex(a Attempt) funcIndex {
	idx := funcIndex{}
	for _, f := range a.Files {
		if !isGoFile(f.Path) {
			continue
		}
		_, file, ok := parseFile(f.Path, f.Content)
		if !ok {
			continue
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				idx[fn.Name.Name] = fn
			}
		}
	}
	return idx
}

// isSkipped reports whether fn calls a Skip on one of its testing receivers — the
// honest deferral that exempts a test from the vacuity check.
func isSkipped(fn *ast.FuncDecl) bool {
	vars := testingVarNames(fn)
	skipped := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if sel, ok := selectorOnTestingVar(n, vars); ok && skipMethods[sel] {
			skipped = true
		}
		return true
	})
	return skipped
}

// funcAsserts reports whether fn contains a real (failing) assertion: a fail-method
// call on one of its testing receivers, an assertion-library call
// (require./assert./is.), or a call that hands a testing receiver to an in-attempt
// helper that itself asserts. visited guards against helper recursion cycles.
func funcAsserts(fn *ast.FuncDecl, idx funcIndex, visited map[string]bool) bool {
	if fn == nil || fn.Body == nil || visited[fn.Name.Name] {
		return false
	}
	visited[fn.Name.Name] = true

	vars := testingVarNames(fn)
	asserts := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := selectorOnTestingVar(call.Fun, vars); ok && failMethods[sel] {
			asserts = true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && assertionPkgs[pkg.Name] {
				asserts = true
			}
		}
		// Delegated assertion: a testing receiver passed to an authored helper that
		// itself asserts. A non-asserting or unresolvable callee does not count.
		if callee := resolveBareCallee(call, idx); callee != nil && callPassesTestingVar(call, vars) {
			if funcAsserts(callee, idx, visited) {
				asserts = true
			}
		}
		return true
	})
	return asserts
}

// selectorOnTestingVar returns the method name when n is a selector call target
// `<recv>.Method` whose receiver recv is one of the testing vars.
func selectorOnTestingVar(n ast.Node, vars map[string]bool) (method string, ok bool) {
	sel, isSel := n.(*ast.SelectorExpr)
	if !isSel {
		return "", false
	}
	x, isID := sel.X.(*ast.Ident)
	if !isID || !vars[x.Name] {
		return "", false
	}
	return sel.Sel.Name, true
}

// resolveBareCallee returns the authored helper a bare-identifier call names, or
// nil for a method/package-qualified/unresolvable call.
func resolveBareCallee(call *ast.CallExpr, idx funcIndex) *ast.FuncDecl {
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	return idx[id.Name]
}

// callPassesTestingVar reports whether any argument of call is one of the testing
// receivers (delegating an assertion to the callee).
func callPassesTestingVar(call *ast.CallExpr, vars map[string]bool) bool {
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && vars[id.Name] {
			return true
		}
	}
	return false
}

// testingVarNames collects every *testing.{T,B,F} parameter name declared anywhere
// in n — the outer receiver and any nested (subtest) closure receiver, whatever
// they are named.
func testingVarNames(n ast.Node) map[string]bool {
	vars := map[string]bool{}
	ast.Inspect(n, func(node ast.Node) bool {
		if ft, ok := node.(*ast.FuncType); ok {
			for _, name := range testingParamNames(ft) {
				vars[name] = true
			}
		}
		return true
	})
	return vars
}

// testingParamNames returns the parameter names of ft whose type is *testing.T
// (or *testing.B/*testing.F — the same failing-call surface).
func testingParamNames(ft *ast.FuncType) []string {
	var out []string
	if ft.Params == nil {
		return out
	}
	for _, field := range ft.Params.List {
		if !isTestingPtr(field.Type) {
			continue
		}
		for _, name := range field.Names {
			out = append(out, name.Name)
		}
	}
	return out
}

// isTestingPtr reports whether expr is *testing.{T,B,F}.
func isTestingPtr(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "testing" {
		return false
	}
	switch sel.Sel.Name {
	case "T", "B", "F":
		return true
	}
	return false
}

// StubArtifact rejects a production source file that ships an admitted hole: a
// panic or a sentinel error (errors.New / fmt.Errorf) whose message announces the
// code is not implemented. It keys on these explicit code markers, NOT on a bare
// // TODO comment — a future-work TODO on complete, tested code is not a stub, and
// flagging it parked honest work (the check now reads the AST, so a TODO in a
// comment is invisible to it by construction). This is the Go profile of semspec's
// stub-artifact detector, which caught 55-byte MANIFEST-only JARs: the shape
// differs, the intent is the same — a green build must not ship a declared stub.
//
// Accepted M0 limit (fail-SAFE — it under-fires, never passes a fabrication a
// harness would then bless): a bare return of a not-implemented sentinel defined
// OUTSIDE the attempt (return foo.ErrNotImplemented, no local marker string) is
// not caught; resolving sentinel identifiers needs go/types, deferred to M2. A
// same-file sentinel is caught, because its errors.New("not implemented") literal
// is scanned at the declaration.
func StubArtifact(a Attempt) Finding {
	var offenders []string
	for _, f := range a.Files {
		if !isGoFile(f.Path) || isTestFile(f.Path) {
			continue
		}
		fset, file, ok := parseFile(f.Path, f.Content)
		if !ok {
			continue // SourceBuild owns the parse failure
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isNotImplementedCall(call) {
				return true
			}
			offenders = append(offenders, fmt.Sprintf("%s:%d", f.Path, fset.Position(call.Pos()).Line))
			return true
		})
	}
	if len(offenders) == 0 {
		return pass(FloorStub, "no unimplemented-stub markers in production source")
	}
	sort.Strings(offenders)
	return reject(FloorStub,
		"production source declares an unimplemented stub (panic / sentinel not-implemented error): "+strings.Join(offenders, ", "))
}

// isNotImplementedCall reports whether call is a panic, errors.New, or fmt.Errorf
// whose message names an unimplemented marker. It inspects every string literal in
// the call's arguments, so a wrapped form — panic(fmt.Sprintf("not implemented:
// %v", err)) — is caught, not only a bare literal first argument.
func isNotImplementedCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if fn.Name != "panic" {
			return false
		}
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		if !ok {
			return false
		}
		if !((pkg.Name == "errors" && fn.Sel.Name == "New") || (pkg.Name == "fmt" && fn.Sel.Name == "Errorf")) {
			return false
		}
	default:
		return false
	}
	found := false
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			if isNotImplementedLit(n) {
				found = true
			}
			return true
		})
	}
	return found
}

// isNotImplementedLit reports whether n is a string literal whose content names an
// unimplemented marker as a whole word — so "not implemented" matches but "todos
// remaining" (a domain string that merely contains "todo") does not false-reject.
func isNotImplementedLit(n ast.Node) bool {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	s = strings.ToLower(s)
	for _, phrase := range notImplementedPhrases {
		if containsWord(s, phrase) {
			return true
		}
	}
	return false
}

// containsWord reports whether s contains marker bounded by non-word characters on
// both sides (a word is [a-z0-9_]; s is already lower-cased). Multi-word markers
// are bounded at their outer edges.
func containsWord(s, marker string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], marker)
		if j < 0 {
			return false
		}
		j += i
		beforeOK := j == 0 || !isWordByte(s[j-1])
		end := j + len(marker)
		afterOK := end >= len(s) || !isWordByte(s[end])
		if beforeOK && afterOK {
			return true
		}
		i = j + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}

// SourceBuild rejects an authored .go file that does not parse — source that
// cannot parse cannot build, and a warm sandbox must not wave through a syntax
// error a cold build would reject. This is the pure, offline, PARSE-level slice of
// build integrity: it is intentionally narrower than the name's promise —
// go/parser accepts source that go build rejects (unused imports, undefined
// symbols), and cold dependency resolution is clean-room verify's job (group 8),
// where the cache-masked-fabrication class lives. A SourceBuild pass therefore
// means "parses", not "compiles".
func SourceBuild(a Attempt) Finding {
	var offenders []string
	for _, f := range a.Files {
		if !isGoFile(f.Path) {
			continue
		}
		if _, _, ok := parseFile(f.Path, f.Content); !ok {
			// Re-parse once for the concrete error text (parseFile discards it).
			fset := token.NewFileSet()
			_, err := parser.ParseFile(fset, f.Path, f.Content, parser.AllErrors)
			offenders = append(offenders, fmt.Sprintf("%s: %v", f.Path, err))
		}
	}
	if len(offenders) == 0 {
		return pass(FloorSourceBuild, "every authored .go file parses")
	}
	sort.Strings(offenders)
	return reject(FloorSourceBuild,
		"authored Go source does not parse (cannot build): "+strings.Join(offenders, "; "))
}

// AntiMock rejects a test suite that declares a mock/fake/stub double yet
// references none of the exported symbols of the task's target files in CODE — a
// "test" that exercises only its own double proves nothing about the code under
// test. It is the conservative M0 Go profile and fires only when both signals
// hold. Three known M0 limits, all accepted here: (1) a type whose name merely
// contains mock/fake/stub is treated as a double, so an unlucky legitimate name
// (Stubborn) could read as a mock — harmless unless the suite also touches no
// target symbol; (2) the mock-declared and target-referenced signals are OR-folded
// across all test files, so a fabricated mock-only file is masked when a sibling
// file honestly exercises the target; (3) reference matching is by identifier name
// without type resolution, so a same-named but unrelated identifier (a cross-
// package http.Handler when a target type is Handler, or a mock method colliding
// with a target top-level func name) counts as a reference — an over-accept that
// needs go/types to close. The richer integration-real-vs-mock discipline
// (semspec's testcontainers floor, the OSH hard fixtures) and type-resolved
// references land with the JVM profiles at M2.
func AntiMock(a Attempt) Finding {
	targetSymbols := exportedTargetSymbols(a)
	if len(targetSymbols) == 0 {
		return pass(FloorAntiMock, "no exported target symbols to check against — floor does not apply")
	}

	declaresMock := false
	referencesTarget := false
	for _, f := range a.Files {
		if !isTestFile(f.Path) {
			continue
		}
		_, file, ok := parseFile(f.Path, f.Content)
		if !ok {
			continue
		}
		if declaresMockType(file) {
			declaresMock = true
		}
		if referencesTargetSymbol(file, targetSymbols) {
			referencesTarget = true
		}
	}

	if declaresMock && !referencesTarget {
		return reject(FloorAntiMock,
			"test suite declares a mock/fake/stub but references none of the target-file symbols "+
				strings.Join(sortedKeys(targetSymbols), ", ")+" in code — it tests only its own double, not the code under test")
	}
	return pass(FloorAntiMock, "test suite exercises the code under test (or declares no double)")
}

// declaresMockType reports whether file declares a type whose name contains
// mock/fake/stub (case-insensitive) — catching hand-written unexported doubles
// (fakeStore, mockClock) as well as generated MockX.
func declaresMockType(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		lower := strings.ToLower(ts.Name.Name)
		if strings.Contains(lower, "mock") || strings.Contains(lower, "fake") || strings.Contains(lower, "stub") {
			found = true
		}
		return true
	})
	return found
}

// referencesTargetSymbol reports whether file names any target symbol as a code
// identifier. Walking the AST (not raw text) means a symbol named only in a string
// literal — a t.Run("FetchUser", …) subtest label — or in a comment does NOT count
// as exercising it.
func referencesTargetSymbol(file *ast.File, symbols map[string]struct{}) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if _, hit := symbols[id.Name]; hit {
				found = true
			}
		}
		return true
	})
	return found
}

// exportedTargetSymbols returns the set of exported top-level identifiers
// (functions, types, vars, consts) declared in the attempt's target files.
// Unparseable target files are skipped — SourceBuild owns that failure.
func exportedTargetSymbols(a Attempt) map[string]struct{} {
	targets := map[string]struct{}{}
	for _, p := range a.TargetFiles {
		targets[p] = struct{}{}
	}
	symbols := map[string]struct{}{}
	for _, f := range a.Files {
		if _, ok := targets[f.Path]; !ok || isTestFile(f.Path) {
			continue
		}
		_, file, ok := parseFile(f.Path, f.Content)
		if !ok {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					symbols[d.Name.Name] = struct{}{}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							symbols[s.Name.Name] = struct{}{}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								symbols[n.Name] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
	return symbols
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
