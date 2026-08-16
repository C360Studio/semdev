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

// PresenceOfWork rejects an attempt that authored NONE of its declared target files
// — every declared target is absent from disk (a non-empty TargetFiles with an empty
// Files set). That is the SB5 "verified over zero executions" shape and the g4 hole
// this closes: the other five floors all pass VACUOUSLY on an empty file set (there is
// no authored source to reject), so without this floor an attempt that did nothing at
// all reads GREEN — exactly the theater that let both predecessors succeed against a
// scaffold.
//
// It deliberately does NOT reject a PARTIAL attempt (some declared targets present,
// some absent). The Attempts resolver includes every declared target that exists on
// disk regardless of whether THIS diff touched it, so len(Files) < len(TargetFiles)
// means "some declared target is absent from disk" — a legitimate intermediate or
// optional-target state, not proof of no work; rejecting it would false-park honest
// partial work. Only "authored nothing at all" (len(Files)==0 with declared targets)
// is an unambiguous no-work signal, matching the under-fire-not-false-reject posture
// of the other floors. Its verdict is deterministic from the Attempt shape alone (no
// parse), so it holds for any ecosystem, not just Go.
func PresenceOfWork(a Attempt) Finding {
	if len(a.TargetFiles) > 0 && len(a.Files) == 0 {
		return reject(FloorPresence, fmt.Sprintf(
			"the attempt authored none of its %d declared target file(s) — no work is present on disk to evaluate; a floor verdict over zero authored files would be theater (SB5)",
			len(a.TargetFiles)))
	}
	return pass(FloorPresence, "the attempt authored at least one of its declared target files")
}

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

// VacuousTest rejects a test function whose body can never meaningfully fail: no
// *testing.T failing call on computed behavior, no assertion-library call with a
// computed argument, and no *testing.T handed to an in-attempt helper that itself
// asserts. A test that only logs, asserts a constant tautology (if "ok" != "ok" {
// t.Fatal() }), or checks two literals, inflates the passing count without proving
// anything — a bare syntactic fail-call does NOT satisfy the floor (Codex P2). A
// test that Skips is exempt — that is the honest deferred form. Working on the AST
// means a renamed subtest receiver and an assertion inside a t.Run closure are
// recognized, and a commented-out or string-literal assertion is not.
//
// Accepted M0 limit (fail-OPEN, needs data-flow at M2): the tautology check is at
// the literal level — a constant laundered through a variable (x := "ok"; if x !=
// "ok" { t.Fatal() }) reads as a computed guard and escapes. AntiMock's
// target-reference check catches a no-target test when a double is declared; a
// bare no-target tautology with no double is the residual gap.
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

// funcAsserts reports whether fn contains a real assertion — one that can fail on
// COMPUTED behavior, not merely a syntactic fail-call. It credits: a fail-method
// call on a testing receiver whose guarding condition tests a computed value (so a
// constant tautology like `if "ok" != "ok" { t.Fatal() }` does NOT count), an
// assertion-library call (require./assert./is.) with at least one computed
// argument, or a call that hands a testing receiver to an in-attempt helper that
// itself asserts. visited guards against helper recursion cycles.
func funcAsserts(fn *ast.FuncDecl, idx funcIndex, visited map[string]bool) bool {
	if fn == nil || fn.Body == nil || visited[fn.Name.Name] {
		return false
	}
	visited[fn.Name.Name] = true

	vars := testingVarNames(fn)
	dead := deadFailCalls(fn.Body, vars) // fail-calls guarded only by a constant condition
	asserts := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isFailCall(call, vars) && !dead[call] {
			asserts = true
		}
		if isAssertionLibCall(call) && hasComputedArg(call, vars) {
			asserts = true
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

// isFailCall reports whether call is a fail-method call on a testing receiver.
func isFailCall(call *ast.CallExpr, vars map[string]bool) bool {
	method, ok := selectorOnTestingVar(call.Fun, vars)
	return ok && failMethods[method]
}

// isAssertionLibCall reports whether call is a require./assert./is. assertion.
func isAssertionLibCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && assertionPkgs[pkg.Name]
}

// deadFailCalls returns the fail-method calls that assert nothing about computed
// behavior: those guarded by an `if` whose condition is a constant expression
// (both operands literal). A real test compares a COMPUTED value against an
// expected one; a fail call reachable only under `if "ok" != "ok"` (or `if false`)
// is a tautology that inflates the passing count. Every fail call inside a
// constant-condition if — then-branch or else — is dead, including nested ones.
func deadFailCalls(body *ast.BlockStmt, vars map[string]bool) map[*ast.CallExpr]bool {
	dead := map[*ast.CallExpr]bool{}
	collect := func(n ast.Node) {
		ast.Inspect(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok && isFailCall(c, vars) {
				dead[c] = true
			}
			return true
		})
	}
	ast.Inspect(body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || !isConstantExpr(ifs.Cond) {
			return true
		}
		collect(ifs.Body)
		if ifs.Else != nil {
			collect(ifs.Else)
		}
		return true
	})
	return dead
}

// isConstantExpr reports whether e is a compile-time constant expression — a
// literal, a predeclared const identifier (true/false/nil/iota), or an operator
// tree over such. An expression that names a variable or calls a function is NOT
// constant: it computes a value, and comparing against it exercises behavior.
func isConstantExpr(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return x.Name == "true" || x.Name == "false" || x.Name == "nil" || x.Name == "iota"
	case *ast.ParenExpr:
		return isConstantExpr(x.X)
	case *ast.UnaryExpr:
		return isConstantExpr(x.X)
	case *ast.BinaryExpr:
		return isConstantExpr(x.X) && isConstantExpr(x.Y)
	default:
		return false
	}
}

// hasComputedArg reports whether call has an argument that is a computed value —
// non-constant and not the testing receiver itself. require.Equal(t, "ok", "ok")
// is a tautology (no computed arg); require.Equal(t, "ok", got) asserts behavior.
func hasComputedArg(call *ast.CallExpr, vars map[string]bool) bool {
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && vars[id.Name] {
			continue // the testing receiver, not the value under assertion
		}
		if !isConstantExpr(arg) {
			return true
		}
	}
	return false
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
// references none of the task's target-file symbols in CODE — a "test" that
// exercises only its own double proves nothing about the code under test. The
// target index includes UNEXPORTED top-level declarations and methods, not just
// exported ones, so a task changing an unexported helper or method in an internal
// package is covered (Codex P2) — otherwise an empty symbol set let a mock-only
// test pass. It is the conservative M0 Go profile and fires only when both signals
// hold. Three known M0 limits, all accepted here: (1) a type whose name merely
// contains mock/fake/stub is treated as a double, so an unlucky legitimate name
// (Stubborn) could read as a mock — harmless unless the suite also touches no
// target symbol; (2) the mock-declared and target-referenced signals are OR-folded
// across all test files, so a fabricated mock-only file is masked when a sibling
// file honestly exercises the target; (3) reference matching is by identifier name
// without type resolution, so a same-named but unrelated identifier (a cross-
// package http.Handler when a target type is Handler, or a mock method colliding
// with a target symbol name) counts as a reference — an over-accept that needs
// go/types to close. The richer integration-real-vs-mock discipline (semspec's
// testcontainers floor, the OSH hard fixtures) and type-resolved references land
// with the JVM profiles at M2.
func AntiMock(a Attempt) Finding {
	targetSymbols := targetSymbolIndex(a)
	if len(targetSymbols) == 0 {
		return pass(FloorAntiMock, "no target symbols to check against — floor does not apply")
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

// targetSymbolIndex returns the set of top-level identifiers — functions AND
// methods (by name), types, vars, consts, EXPORTED and unexported — declared in
// the attempt's target files. Unexported symbols are included so a same-package
// test of internal code is covered: a task changing `parseThing` has a non-empty
// index, so a mock-only test that never names it is caught. Unparseable target
// files are skipped — SourceBuild owns that failure.
func targetSymbolIndex(a Attempt) map[string]struct{} {
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
				// Both plain funcs and methods — a same-package test calls either by
				// name (parseThing() or store.parseThing()).
				symbols[d.Name.Name] = struct{}{}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						symbols[s.Name.Name] = struct{}{}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							symbols[n.Name] = struct{}{}
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

// CleanTree rejects an attempt whose working tree diverged from the committed attempt at
// resolution time (a non-empty `git status --porcelain`, captured into DirtyPaths by the
// resolver). Because floors evaluate the working-tree file contents but the cold verify
// clones the COMMITTED tree, a dirty tree means the two would prove DIFFERENT bytes — the
// floors' green would not describe what verify proves. A dirty tree after measurement is
// also evidence tampering (the container mutated the artifact post-commit). Either way it
// fails closed (G4/G7): the divergence must be committed (a fresh attempt) or is a park.
// Its verdict is deterministic from the Attempt shape alone (no parse), so it holds for
// any ecosystem, not just Go.
//
// KNOWN CONSTRAINT (system-level, not this pure function): DirtyPaths is `git status
// --porcelain` over the warm checkout AFTER measure_task ran its test_command in-container
// over that same bind-mounted tree. So the signal assumes the operator repo gitignores its
// build/test output — the current Go fixture is clean because go's caches live under
// GOCACHE/$HOME, never the module dir. A test that writes a NON-gitignored artifact into
// the working tree (golden-file regen, an in-tree lockfile update) would false-reject a
// legitimately-passing attempt. Acceptable for M0-Go; revisit for tree-writing ecosystems.
//
// The false-ACCEPT direction is CLOSED (security-forge-containment): a rejected attempt's
// residue can no longer become the mechanism by which a later attempt reads clean — the
// patcher commits only the enumerated diff targets and resets the tree to the committed
// snapshot before each apply, so "clean" always means "the commit contains exactly the
// authored change", never "the commit absorbed the divergence"
// (TestPatcherLaunderingClosedAcrossAttempts pins the shape).
func CleanTree(a Attempt) Finding {
	if len(a.DirtyPaths) == 0 {
		return pass(FloorCleanTree, "the working tree matches the committed attempt (git status clean) — floors and cold verify evaluate the same bytes")
	}
	return reject(FloorCleanTree, fmt.Sprintf(
		"the working tree diverged from the committed attempt in %d path(s) (e.g. %s) — floors evaluate the working tree but cold verify clones the commit, so their bytes differ; commit the change as a fresh attempt or park (evidence tampering, G7)",
		len(a.DirtyPaths), a.DirtyPaths[0]))
}
