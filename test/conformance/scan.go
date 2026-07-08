package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// This file holds pure source-scan cores shared by the G1 (binary-parity) and
// G2 (lifecycle-caller) pins. Each takes source bytes so a red-first test can
// feed synthetic code and prove the scan actually fires.

// registrationCalls returns the qualified names (e.g. "boot.RegisterAll") of
// every call in src to a function whose name begins with "Register". It is how
// the binary-parity pin proves both mains register only through boot.RegisterAll
// and never call a component's Register directly.
func registrationCalls(src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := qualifiedFunc(call.Fun); ok {
			if base := name[strings.LastIndex(name, ".")+1:]; strings.HasPrefix(base, "Register") {
				out = append(out, name)
			}
		}
		return true
	})
	return out, nil
}

// markdownH2Anchors returns the set of level-2 heading texts ("## <text>") in a
// markdown document — the anchors an alignment-note reference must resolve to,
// and the row-key extraction the G10 docs pin reuses. Lines inside fenced code
// blocks are skipped, so a "## …" in an example fence is not a false anchor.
func markdownH2Anchors(src []byte) map[string]bool {
	out := make(map[string]bool)
	inFence := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if h, ok := strings.CutPrefix(trimmed, "## "); ok {
			out[strings.TrimSpace(h)] = true
		}
	}
	return out
}

// testFunctionNames returns the names of top-level `func Test*` declarations in a
// Go source file — the extraction the G6 regression manifest uses to prove a
// named pin has not silently disappeared.
func testFunctionNames(src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		out = append(out, fn.Name.Name)
	}
	return out, nil
}

// markdownTableRows returns the data rows (header and separator excluded) of the
// first markdown pipe-table under the given level-2 heading. Each row is its
// trimmed, backtick-stripped cells. It is how the G10 docs pin reads a doc table
// to compare against a code registry. Lines inside code fences are ignored.
func markdownTableRows(src []byte, heading string) [][]string {
	lines := strings.Split(string(src), "\n")
	inFence := false
	inSection := false
	var rows [][]string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if h, ok := strings.CutPrefix(trimmed, "## "); ok {
			inSection = strings.TrimSpace(h) == heading
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") {
			if inSection && rows != nil && trimmed == "" {
				break // table ended
			}
			continue
		}
		cells := splitTableRow(trimmed)
		if isSeparatorRow(cells) {
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return nil
	}
	return rows[1:] // drop the header row
}

func splitTableRow(line string) []string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.Trim(strings.TrimSpace(p), "`")
	}
	return out
}

func isSeparatorRow(cells []string) bool {
	for _, c := range cells {
		if c == "" || strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

// runCalls returns the qualified names (e.g. "boot.Run") of every call in src
// to a function named exactly "Run". It is how the binary-parity pin proves
// both mains bring up the runtime only through the shared boot.Run — never
// wiring NATS, the registries, or the ServiceManager independently.
func runCalls(src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := qualifiedFunc(call.Fun); ok {
			if base := name[strings.LastIndex(name, ".")+1:]; base == "Run" {
				out = append(out, name)
			}
		}
		return true
	})
	return out, nil
}

// fileImports returns the import paths of a Go source file.
func fileImports(src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		out = append(out, strings.Trim(imp.Path.Value, `"`))
	}
	return out, nil
}

// qualifiedFunc returns "pkg.Func" for a selector call like pkg.Func(...), or
// just "Func" for a bare call. ok is false for more complex call targets (method
// chains, etc.) that the registration pins do not need to reason about.
func qualifiedFunc(fun ast.Expr) (string, bool) {
	switch e := fun.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name, true
		}
		return e.Sel.Name, true
	default:
		return "", false
	}
}
