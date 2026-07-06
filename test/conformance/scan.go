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
// and the row-key extraction the G10 docs pin reuses.
func markdownH2Anchors(src []byte) map[string]bool {
	out := make(map[string]bool)
	for _, line := range strings.Split(string(src), "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok {
			out[strings.TrimSpace(h)] = true
		}
	}
	return out
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
