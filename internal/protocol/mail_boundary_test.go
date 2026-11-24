package protocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDesktopProtocolDoesNotOwnSessionStatus 禁止在桌面协议手工复制引擎 SessionStatus。
// SessionInfo / session:status 仍由引擎协议产生；桌面只引用 hostproto。
func TestDesktopProtocolDoesNotOwnSessionStatus(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				switch ts.Name.Name {
				case "SessionStatus", "SessionInfo":
					t.Errorf("%s defines %s; keep engine-owned session status types out of desktop protocol", name, ts.Name.Name)
				}
			}
		}
	}
}

// TestMailWireTypesDoNotCloneAutomationNames 邮件 DTO 不得与自动化任务同名冲突。
func TestMailWireTypesDoNotCloneAutomationNames(t *testing.T) {
	forbidden := map[string]bool{
		"AutomationTask": true, "AutomationRun": true, "AutomationDefaults": true,
		"AutomationContext": true, "ExitSummary": true,
	}
	src, err := os.ReadFile("mail.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "mail.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts := spec.(*ast.TypeSpec)
			if forbidden[ts.Name.Name] {
				t.Errorf("mail.go must not define automation-named type %s", ts.Name.Name)
			}
		}
	}
}
