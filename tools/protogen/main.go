// Command protogen generates the desktop frontend's TypeScript protocol layer
// from the Go single sources of truth:
//
//   - engine/protocol → types.ts (interfaces for every wire struct, plus the
//     constant groups as const objects and literal-union types)
//   - engine/protocol's Event* constants → events.ts (the EventMap and typed
//     subscription helpers over the Wails runtime event bus)
//   - internal/desktop's App exported methods → commands.ts (typed wrappers
//     for the Wails bindings, annotated with each command's CommandKind)
//
// It is wired to `go generate ./engine/protocol`. With --check it compares the
// would-be output against the files on disk and exits non-zero on drift (the
// CI gate); the default mode writes the files.
//
// Generation fails loudly when the sources drift from their metadata: an App
// method absent from protocol.CommandKinds (or a stale map entry), an Event*
// constant missing from the event→payload table, or an App signature that
// leaks a non-protocol type onto the wire.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	protocolPkgPath = "github.com/wt68/runcode/engine/protocol"
	desktopPkgPath  = "github.com/wt68/runcode/internal/desktop"
)

// outRelDir is where the generated TypeScript lands, relative to the module root.
var outRelDir = filepath.Join("cmd", "runcode-desktop", "frontend", "src", "protocol")

func main() {
	check := flag.Bool("check", false, "compare generated output with the files on disk; exit 1 on drift instead of writing")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "protogen: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo |
			packages.NeedModule,
	}
	pkgs, err := packages.Load(cfg, protocolPkgPath, desktopPkgPath)
	if err != nil {
		return fmt.Errorf("load packages: %w", err)
	}
	var protoPkg, deskPkg *packages.Package
	var loadErrs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
		switch p.PkgPath {
		case protocolPkgPath:
			protoPkg = p
		case desktopPkgPath:
			deskPkg = p
		}
	}
	if len(loadErrs) > 0 {
		return fmt.Errorf("packages failed to load:\n  %s", strings.Join(loadErrs, "\n  "))
	}
	if protoPkg == nil || deskPkg == nil {
		return fmt.Errorf("load result is missing %s or %s", protocolPkgPath, desktopPkgPath)
	}
	// The output anchors on the desktop package's module (the repo root, where
	// the frontend lives) — NOT the protocol package's module, which is the
	// nested engine module since the protocol's promotion.
	if deskPkg.Module == nil || deskPkg.Module.Dir == "" {
		return fmt.Errorf("no module information for %s (protogen must run inside the runcode repo)", desktopPkgPath)
	}

	m, err := extract(protoPkg, deskPkg)
	if err != nil {
		return err
	}

	files := map[string][]byte{
		"types.ts":    emitTypes(m),
		"events.ts":   emitEvents(m),
		"commands.ts": emitCommands(m),
	}
	outDir := filepath.Join(deskPkg.Module.Dir, outRelDir)

	if check {
		var stale []string
		for _, name := range sortedKeys(files) {
			existing, err := os.ReadFile(filepath.Join(outDir, name))
			if err != nil || !bytes.Equal(normalizeEOL(existing), normalizeEOL(files[name])) {
				stale = append(stale, filepath.Join(outDir, name))
			}
		}
		if len(stale) > 0 {
			return fmt.Errorf("generated files are out of date (run `go generate ./engine/protocol`):\n  %s",
				strings.Join(stale, "\n  "))
		}
		return nil
	}

	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	for _, name := range sortedKeys(files) {
		path := filepath.Join(outDir, name)
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, files[name]) {
			continue // already up to date; keep the run idempotent (zero diff, no mtime churn)
		}
		if err := os.WriteFile(path, files[name], 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Println("protogen: wrote", path)
	}
	return nil
}

// normalizeEOL strips carriage returns so --check tolerates a CRLF checkout
// (git autocrlf) of content that is otherwise identical.
func normalizeEOL(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
