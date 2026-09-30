package desktop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// This parses the Darwin source even on non-Mac CI. Adjacent Go prose comments
// join the import C preamble and are emitted as raw C, so gofmt alone isn't a gate.
func TestDarwinAskpassPreambleExcludesGoProse(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "askpass_darwin.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.IMPORT {
			continue
		}
		for _, spec := range group.Specs {
			imp := spec.(*ast.ImportSpec)
			if imp.Path.Value != `"C"` {
				continue
			}
			doc := imp.Doc
			if doc == nil {
				doc = group.Doc
			}
			if doc == nil || len(doc.List) != 1 || !strings.HasPrefix(doc.List[0].Text, "/*\n#cgo") {
				t.Fatal("import C must have only its C block, separated from Go prose by a blank line")
			}
			if !strings.Contains(doc.Text(), "#include <libproc.h>") {
				t.Fatal("missing libproc declarations")
			}
			return
		}
	}
	t.Fatal("Darwin askpass source no longer has its expected C import")
}
