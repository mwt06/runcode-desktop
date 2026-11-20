package desktop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineSessionAssertionsStayCentralized(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]string{
		"app.go":                         "newWithBuild",
		"image_generation_invocation.go": "unwrapEngineSession",
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range src.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.TypeAssertExpr:
					if isEngineSessionPointer(x.Type) && allowed[file] != fn.Name.Name {
						t.Errorf("%s:%d: direct engine session assertion outside %s; use unwrapEngineSession", file, x.Pos(), allowed[file])
					}
				case *ast.CaseClause:
					for _, expr := range x.List {
						if isEngineSessionPointer(expr) && allowed[file] != fn.Name.Name {
							t.Errorf("%s:%d: direct engine session type-switch outside %s; use unwrapEngineSession", file, x.Pos(), allowed[file])
						}
					}
				}
				return true
			})
		}
	}
}

func isEngineSessionPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Session" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "engine"
}
