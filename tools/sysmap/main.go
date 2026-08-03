// Command sysmap scans the runcode shell + agentloop engine with go/types and
// dumps a machine-readable system map: package graph, exported API surface,
// interface -> implementation edges, and cross-package reference counts.
//
// It regenerates the numbers in docs/system-overview.md; run it from the repo
// root with go.work active so ../agentloop resolves to the local checkout.
// Unlike tools/protogen it is not part of the build contract — nothing in CI
// depends on its output, and it excludes itself from the scan (see inWorkspace).
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// NeedDeps is deliberately absent: with it, go/packages type-checks every
// transitive dependency from source (modernc.org/libc alone is ~1M lines).
// Without it, the initial packages are checked from source and their deps come
// from the go command's cached export data — the same tradeoff gopls makes.
// One single Load call keeps *types.Package identity shared, which
// types.Implements below depends on.
const loadMode = packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
	packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports |
	packages.NeedModule

type pkgInfo struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Module     string   `json:"module"`
	Layer      string   `json:"layer"`
	Files      int      `json:"files"`
	Lines      int      `json:"lines"`
	Imports    []string `json:"imports"` // workspace-internal only
	ExpFuncs   int      `json:"expFuncs"`
	ExpTypes   int      `json:"expTypes"`
	Interfaces int      `json:"interfaces"`
	Doc        string   `json:"doc"`
}

type methodInfo struct {
	Name   string `json:"name"`
	Sig    string `json:"sig"`
	Line   int    `json:"line"`
	File   string `json:"file"`
	Refs   int    `json:"refs"`
	RefPkg int    `json:"refPkgs"`
}

type ifaceInfo struct {
	Pkg      string       `json:"pkg"`
	Name     string       `json:"name"`
	File     string       `json:"file"`
	Line     int          `json:"line"`
	Doc      string       `json:"doc"`
	Methods  []methodInfo `json:"methods"`
	Impls    []string     `json:"impls"`
	Refs     int          `json:"refs"`
	RefPkgs  []string     `json:"refPkgs"`
	Exported bool         `json:"exported"`
}

type typeInfo struct {
	Pkg     string       `json:"pkg"`
	Name    string       `json:"name"`
	Kind    string       `json:"kind"`
	File    string       `json:"file"`
	Line    int          `json:"line"`
	Doc     string       `json:"doc"`
	Methods []methodInfo `json:"methods"`
	Refs    int          `json:"refs"`
}

type funcInfo struct {
	Pkg     string   `json:"pkg"`
	Name    string   `json:"name"`
	Sig     string   `json:"sig"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Refs    int      `json:"refs"`
	RefPkgs []string `json:"refPkgs"`
}

type report struct {
	Packages   []pkgInfo         `json:"packages"`
	Interfaces []ifaceInfo       `json:"interfaces"`
	Types      []typeInfo        `json:"types"`
	Funcs      []funcInfo        `json:"funcs"`
	Edges      map[string]int    `json:"edges"` // "a -> b": symbol refs crossing that edge
	Totals     map[string]int    `json:"totals"`
	Errors     []string          `json:"errors"`
	ModuleOf   map[string]string `json:"moduleOf"`
}

func main() {
	root, _ := os.Getwd()
	var all []*packages.Package
	var errs []string

	load := func(dir string, patterns ...string) {
		cfg := &packages.Config{Mode: loadMode, Dir: dir, Tests: false}
		pkgs, err := packages.Load(cfg, patterns...)
		if err != nil {
			errs = append(errs, fmt.Sprintf("load %s: %v", dir, err))
			return
		}
		for _, p := range pkgs {
			if strings.Contains(p.PkgPath, "node_modules") {
				continue
			}
			for _, e := range p.Errors {
				errs = append(errs, fmt.Sprintf("%s: %s", p.PkgPath, e.Msg))
			}
			all = append(all, p)
		}
	}

	// One Load, every module of the go.work workspace.
	load(root,
		"./...",
		"github.com/wt68/runcode/cmd/runcode-desktop",
		"github.com/wt68/runcode/cmd/runcode-server",
		"gitlab.ouc-online.com.cn/aibase/agentloop/...",
	)

	// Deduplicate by path, keeping the richest instance.
	byPath := map[string]*packages.Package{}
	for _, p := range all {
		if p.Types == nil {
			continue
		}
		if old, ok := byPath[p.PkgPath]; !ok || len(p.Syntax) > len(old.Syntax) {
			byPath[p.PkgPath] = p
		}
	}
	ws := map[string]bool{}
	for path := range byPath {
		if inWorkspace(path) {
			ws[path] = true
		}
	}

	out := report{
		Edges:    map[string]int{},
		Totals:   map[string]int{},
		ModuleOf: map[string]string{},
	}

	// ---- reference counting: obj -> total uses, and set of referencing pkgs.
	type refKey struct{ pkg, sym string }
	refCount := map[refKey]int{}
	refPkgs := map[refKey]map[string]bool{}
	objKey := func(obj types.Object) (refKey, bool) {
		if obj == nil || obj.Pkg() == nil {
			return refKey{}, false
		}
		p := obj.Pkg().Path()
		if !ws[p] {
			return refKey{}, false
		}
		name := obj.Name()
		if fn, ok := obj.(*types.Func); ok {
			if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
				name = recvName(sig.Recv().Type()) + "." + name
			}
		}
		return refKey{p, name}, true
	}

	for path, p := range byPath {
		if !ws[path] {
			continue
		}
		for _, use := range p.TypesInfo.Uses {
			k, ok := objKey(use)
			if !ok {
				continue
			}
			refCount[k]++
			if refPkgs[k] == nil {
				refPkgs[k] = map[string]bool{}
			}
			refPkgs[k][path] = true
			if k.pkg != path {
				out.Edges[path+" -> "+k.pkg]++
			}
		}
	}

	docOf := func(p *packages.Package) string {
		for _, f := range p.Syntax {
			if f.Doc != nil {
				return firstSentence(f.Doc.Text())
			}
		}
		return ""
	}

	// ---- per-package facts
	for path, p := range byPath {
		if !ws[path] {
			continue
		}
		mod := "?"
		if p.Module != nil {
			mod = p.Module.Path
		}
		out.ModuleOf[path] = mod
		pi := pkgInfo{
			Path: path, Name: p.Name, Module: mod, Layer: layerOf(path),
			Files: len(p.GoFiles), Lines: countLines(p.GoFiles), Doc: docOf(p),
		}
		for _, imp := range p.Imports {
			if ws[imp.PkgPath] {
				pi.Imports = append(pi.Imports, imp.PkgPath)
			}
		}
		sort.Strings(pi.Imports)

		scope := p.Types.Scope()
		for _, nm := range scope.Names() {
			obj := scope.Lookup(nm)
			switch o := obj.(type) {
			case *types.Func:
				if o.Exported() {
					pi.ExpFuncs++
				}
			case *types.TypeName:
				if o.Exported() {
					pi.ExpTypes++
				}
				if _, ok := o.Type().Underlying().(*types.Interface); ok {
					pi.Interfaces++
				}
			}
		}
		out.Packages = append(out.Packages, pi)
	}

	// ---- collect every named type in the workspace for implements-checks
	type named struct {
		pkg  string
		name string
		t    *types.Named
	}
	var allNamed []named
	for path, p := range byPath {
		if !ws[path] {
			continue
		}
		s := p.Types.Scope()
		for _, nm := range s.Names() {
			if tn, ok := s.Lookup(nm).(*types.TypeName); ok {
				if n, ok := tn.Type().(*types.Named); ok {
					allNamed = append(allNamed, named{path, nm, n})
				}
			}
		}
	}

	posOf := func(p *packages.Package, pos token.Pos) (string, int) {
		f := p.Fset.Position(pos)
		return shortPath(f.Filename), f.Line
	}
	declDoc := func(p *packages.Package, name string) string {
		for _, file := range p.Syntax {
			for _, d := range file.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, sp := range gd.Specs {
					ts, ok := sp.(*ast.TypeSpec)
					if !ok || ts.Name.Name != name {
						continue
					}
					if ts.Doc != nil {
						return firstSentence(ts.Doc.Text())
					}
					if gd.Doc != nil {
						return firstSentence(gd.Doc.Text())
					}
				}
			}
		}
		return ""
	}

	for path, p := range byPath {
		if !ws[path] {
			continue
		}
		s := p.Types.Scope()
		for _, nm := range s.Names() {
			tn, ok := s.Lookup(nm).(*types.TypeName)
			if !ok {
				continue
			}
			file, line := posOf(p, tn.Pos())
			k := refKey{path, nm}
			if iface, ok := tn.Type().Underlying().(*types.Interface); ok && iface.NumMethods() > 0 {
				ii := ifaceInfo{
					Pkg: path, Name: nm, File: file, Line: line, Doc: declDoc(p, nm),
					Refs: refCount[k], Exported: tn.Exported(),
				}
				for i := 0; i < iface.NumMethods(); i++ {
					m := iface.Method(i)
					ii.Methods = append(ii.Methods, methodInfo{
						Name: m.Name(),
						Sig:  types.TypeString(m.Type(), relTo(path)),
					})
				}
				for _, cand := range allNamed {
					if cand.pkg == path && cand.name == nm {
						continue
					}
					if _, isIface := cand.t.Underlying().(*types.Interface); isIface {
						if types.Implements(cand.t, iface) {
							ii.Impls = append(ii.Impls, cand.pkg+"."+cand.name+" (iface)")
						}
						continue
					}
					if types.Implements(cand.t, iface) {
						ii.Impls = append(ii.Impls, cand.pkg+"."+cand.name)
					} else if types.Implements(types.NewPointer(cand.t), iface) {
						ii.Impls = append(ii.Impls, cand.pkg+".*"+cand.name)
					}
				}
				sort.Strings(ii.Impls)
				for rp := range refPkgs[k] {
					ii.RefPkgs = append(ii.RefPkgs, rp)
				}
				sort.Strings(ii.RefPkgs)
				out.Interfaces = append(out.Interfaces, ii)
				continue
			}

			// concrete named type: record kind + method set
			kind := "type"
			switch tn.Type().Underlying().(type) {
			case *types.Struct:
				kind = "struct"
			case *types.Interface:
				kind = "interface(empty)"
			case *types.Signature:
				kind = "func"
			case *types.Basic:
				kind = "basic"
			case *types.Slice, *types.Map:
				kind = "collection"
			}
			ti := typeInfo{Pkg: path, Name: nm, Kind: kind, File: file, Line: line,
				Doc: declDoc(p, nm), Refs: refCount[k]}
			if n, ok := tn.Type().(*types.Named); ok {
				for i := 0; i < n.NumMethods(); i++ {
					m := n.Method(i)
					if !m.Exported() {
						continue
					}
					mf, ml := posOf(p, m.Pos())
					mk := refKey{path, nm + "." + m.Name()}
					ti.Methods = append(ti.Methods, methodInfo{
						Name: m.Name(), Sig: types.TypeString(m.Type(), relTo(path)),
						File: mf, Line: ml, Refs: refCount[mk], RefPkg: len(refPkgs[mk]),
					})
				}
				sort.Slice(ti.Methods, func(i, j int) bool { return ti.Methods[i].Name < ti.Methods[j].Name })
			}
			out.Types = append(out.Types, ti)
		}

		for _, nm := range s.Names() {
			fn, ok := s.Lookup(nm).(*types.Func)
			if !ok || !fn.Exported() {
				continue
			}
			file, line := posOf(p, fn.Pos())
			k := refKey{path, nm}
			fi := funcInfo{Pkg: path, Name: nm, Sig: types.TypeString(fn.Type(), relTo(path)),
				File: file, Line: line, Refs: refCount[k]}
			for rp := range refPkgs[k] {
				fi.RefPkgs = append(fi.RefPkgs, rp)
			}
			sort.Strings(fi.RefPkgs)
			out.Funcs = append(out.Funcs, fi)
		}
	}

	sort.Slice(out.Packages, func(i, j int) bool { return out.Packages[i].Path < out.Packages[j].Path })
	sort.Slice(out.Interfaces, func(i, j int) bool {
		if out.Interfaces[i].Refs != out.Interfaces[j].Refs {
			return out.Interfaces[i].Refs > out.Interfaces[j].Refs
		}
		return out.Interfaces[i].Pkg+out.Interfaces[i].Name < out.Interfaces[j].Pkg+out.Interfaces[j].Name
	})
	sort.Slice(out.Types, func(i, j int) bool {
		if len(out.Types[i].Methods) != len(out.Types[j].Methods) {
			return len(out.Types[i].Methods) > len(out.Types[j].Methods)
		}
		return out.Types[i].Pkg+out.Types[i].Name < out.Types[j].Pkg+out.Types[j].Name
	})
	sort.Slice(out.Funcs, func(i, j int) bool { return out.Funcs[i].Refs > out.Funcs[j].Refs })

	out.Totals["packages"] = len(out.Packages)
	out.Totals["interfaces"] = len(out.Interfaces)
	out.Totals["types"] = len(out.Types)
	out.Totals["exportedFuncs"] = len(out.Funcs)
	lines, methods := 0, 0
	for _, p := range out.Packages {
		lines += p.Lines
	}
	for _, t := range out.Types {
		methods += len(t.Methods)
	}
	out.Totals["goLines"] = lines
	out.Totals["exportedMethods"] = methods
	out.Errors = errs

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "pkgs=%d ifaces=%d types=%d funcs=%d lines=%d errs=%d\n",
		len(out.Packages), len(out.Interfaces), len(out.Types), len(out.Funcs), lines, len(errs))
}

func relTo(pkgPath string) types.Qualifier {
	return func(p *types.Package) string {
		if p.Path() == pkgPath {
			return ""
		}
		return p.Name()
	}
}

func recvName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return types.TypeString(t, nil)
}

func inWorkspace(path string) bool {
	// The map does not include the mapper: sysmap is a one-off analysis tool, not
	// part of the system being described, and counting itself would make every
	// total drift whenever this file is edited. protogen stays in — it is part of
	// the build contract (CI runs it with --check).
	if path == "github.com/wt68/runcode/tools/sysmap" {
		return false
	}
	return strings.HasPrefix(path, "github.com/wt68/runcode") ||
		strings.HasPrefix(path, "gitlab.ouc-online.com.cn/aibase/agentloop")
}

func layerOf(path string) string {
	switch {
	case strings.HasPrefix(path, "github.com/wt68/runcode/cmd/runcode-desktop"):
		return "shell/desktop-main"
	case strings.HasPrefix(path, "github.com/wt68/runcode/cmd/runcode-server"):
		return "shell/server"
	case strings.HasPrefix(path, "github.com/wt68/runcode/cmd/runcode"):
		return "shell/cli"
	case strings.HasPrefix(path, "github.com/wt68/runcode/internal/ui"),
		strings.HasPrefix(path, "github.com/wt68/runcode/internal/command"):
		return "shell/tui"
	case strings.HasPrefix(path, "github.com/wt68/runcode/internal/desktop"),
		strings.HasPrefix(path, "github.com/wt68/runcode/internal/protocol"):
		return "shell/desktop-core"
	case strings.HasPrefix(path, "github.com/wt68/runcode/internal/"):
		return "shell/host-tools"
	case strings.HasPrefix(path, "github.com/wt68/runcode/tools/"):
		return "shell/codegen"
	case strings.Contains(path, "agentloop/internal/"):
		return "engine/internal"
	case strings.Contains(path, "agentloop/tools/"):
		return "engine/tools"
	case strings.Contains(path, "agentloop/llm"):
		return "engine/llm"
	case path == "gitlab.ouc-online.com.cn/aibase/agentloop":
		return "engine/core"
	default:
		return "engine/support"
	}
}

func countLines(files []string) int {
	n := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		n += strings.Count(string(b), "\n") + 1
	}
	return n
}

func shortPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for _, marker := range []string{"/runcode_desktop/", "/agentloop/"} {
		if i := strings.LastIndex(p, marker); i >= 0 {
			return strings.TrimPrefix(marker, "/") + p[i+len(marker):]
		}
	}
	return p
}

func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	// Truncate on runes, not bytes: these doc comments are Chinese, and a byte
	// slice would cut a multi-byte rune in half (the JSON encoder then emits
	// U+FFFD, which downstream consumers reject).
	if r := []rune(s); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
}
