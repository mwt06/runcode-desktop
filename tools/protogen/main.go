// Command protogen generates the desktop frontend's TypeScript protocol layer
// from the Go single sources of truth. The wire contract is split across two Go
// packages by ownership — agentloop's protocol package (what the engine's host
// produces or consumes during a turn, shared with cmd/runcode-server) and
// internal/protocol (what this shell invents for its own features) — and they
// are read here as one contract, sorted by name, so moving a type between them
// leaves the generated output unchanged:
//
//   - both protocol packages → types.ts (interfaces for every wire struct, plus
//     the constant groups as const objects and literal-union types)
//   - both packages' Event* constants → events.ts (the EventMap and typed
//     subscription helpers over the Wails runtime event bus)
//   - internal/desktop's App exported methods → commands.ts (typed wrappers
//     for the Wails bindings, annotated with each command's CommandKind)
//
// Run it directly with `go run ./tools/protogen`. With --check it compares the
// would-be output against the files on disk and exits non-zero on drift (the
// CI gate); the default mode writes the files.
//
// Generation fails loudly when the sources drift from their metadata: an App
// method absent from protocol.CommandKinds (or a stale map entry), an Event*
// constant missing from the event→payload table, an App signature that leaks a
// non-protocol type onto the wire, or the same exported name declared by both
// protocol packages.
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

// The wire contract is defined by two packages, generated as one: the engine's
// protocol package owns what the host produces or consumes while a turn runs
// (and is shared with cmd/runcode-server), and internal/protocol owns what the
// desktop shell invents for its own features. Order matters only for error
// messages; the emitted TypeScript is sorted by name, so a type moving between
// the two packages does not move in the output.
var protocolPkgPaths = []string{
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol",
	"github.com/wt68/runcode/internal/protocol",
}

const desktopPkgPath = "github.com/wt68/runcode/internal/desktop"

// outRelDir is where the generated TypeScript lands, relative to the module root.
// 前端按分层重组后,生成物随 bridge 一起归入 core/ —— 改前端目录时务必同步这里,
// 否则 --check 门禁会对着一个不存在的旧路径报"漂移"。
var outRelDir = filepath.Join("cmd", "runcode-desktop", "frontend", "src", "core", "protocol")

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
	pkgs, err := packages.Load(cfg, append(append([]string{}, protocolPkgPaths...), desktopPkgPath)...)
	if err != nil {
		return fmt.Errorf("load packages: %w", err)
	}
	byPath := map[string]*packages.Package{}
	var loadErrs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
		byPath[p.PkgPath] = p
	}
	if len(loadErrs) > 0 {
		return fmt.Errorf("packages failed to load:\n  %s", strings.Join(loadErrs, "\n  "))
	}
	protoPkgs := make([]*packages.Package, 0, len(protocolPkgPaths))
	for _, path := range protocolPkgPaths {
		p := byPath[path]
		if p == nil {
			return fmt.Errorf("load result is missing %s", path)
		}
		protoPkgs = append(protoPkgs, p)
	}
	deskPkg := byPath[desktopPkgPath]
	if deskPkg == nil {
		return fmt.Errorf("load result is missing %s", desktopPkgPath)
	}
	// The output anchors on the desktop package's module (the repo root, where
	// the frontend lives) — NOT the protocol package's module, which is the
	// external agentloop module.
	if deskPkg.Module == nil || deskPkg.Module.Dir == "" {
		return fmt.Errorf("no module information for %s (protogen must run inside the runcode repo)", desktopPkgPath)
	}

	m, err := extract(protoPkgs, deskPkg)
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
			return fmt.Errorf("generated files are out of date (run `go run ./tools/protogen`):\n  %s",
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
