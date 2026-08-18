package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	deskproto "github.com/wt68/runcode/internal/protocol"
)

// eventPayloads is the explicit event→payload table: each Event* constant of
// either protocol package mapped to the protocol type name of its payload.
// Adding an event constant without adding it here fails generation, so the
// EventMap can never silently miss an event (and vice versa for stale rows).
var eventPayloads = map[string]string{
	"EventAssistantDelta":     "AssistantDelta",
	"EventAssistantThinking":  "AssistantDelta",
	"EventContextUsage":       "ContextUsage",
	"EventHarmAutoAllow":      "HarmAutoAllow",
	"EventPassportChanged":    "PassportStatus",
	"EventRecorderLevel":      "RecorderLevel",
	"EventRecorderState":      "RecorderState",
	"EventRecorderTranscript": "RecorderTranscript",
	"EventRetry":              "RetryNotice",
	"EventPermissionRequest":  "PermissionRequest",
	"EventPlanUpdated":        "PlanRun",
	"EventSessionRenamed":     "SessionRenamed",
	"EventToolEvent":          "ToolEvent",
	"EventTurnEnd":            "TurnEnd",
	"EventTurnQueued":         "TurnQueued",
	"EventTurnError":          "TurnError",
	"EventWarning":            "Warning",
}

// excludedMethods are App methods that are shell wiring, not wire commands.
var excludedMethods = map[string]bool{
	"SetCapturer": true, // installs the system audio capturer; called by the Wails shell, never the frontend
	"SetDialoger": true, // installs the native file-dialog provider; called by the Wails shell, never the frontend
	"Shutdown":    true, // once-per-run teardown; called by the Wails shell from OnShutdown, never the frontend
	"Startup":     true, // once-per-run background work; called by the Wails shell from OnStartup, never the frontend
}

// genericStructs declares protocol structs emitted as generic TS interfaces,
// with per-field (json name) type overrides referencing the type parameters.
var genericStructs = map[string]struct {
	typeParams string
	fields     map[string]string
}{
	// Envelope's `payload any` becomes the type parameter so EventMap payloads
	// flow through onEnvelope with full typing.
	"Envelope": {typeParams: "<P = unknown>", fields: map[string]string{"payload": "P"}},
}

// fieldTypeOverrides retypes struct fields (by json name) to a generated
// union instead of the raw Go type, wiring the discriminated unions in.
var fieldTypeOverrides = map[string]map[string]string{
	"ToolEvent": {"type": "ToolEventType"},
	"Error":     {"code": "ErrCode"},
	"PlanRun":   {"state": "PlanState", "stage": "PlanStage"},
}

// constGroupSpec describes one Go constant-prefix group emitted as a TS const
// object plus a literal-union type. open adds `| (string & {})` so unknown
// values from a newer host remain assignable (forward compatibility).
type constGroupSpec struct {
	goPrefix  string
	constName string
	unionName string
	open      bool
	doc       string
	unionDoc  string
}

// constGroupSpecs is the fixed emission order of the constant groups.
var constGroupSpecs = []constGroupSpec{
	{
		goPrefix: "Event", constName: "Events", unionName: "EventName", open: false,
		doc:      "Event names emitted by the host; payload types are mapped in events.ts.",
		unionDoc: "EventName is the union of known event names.",
	},
	{
		goPrefix: "ToolEvent", constName: "ToolEventTypes", unionName: "ToolEventType", open: true,
		doc:      "Tool event types, the discriminator values of ToolEvent.type.",
		unionDoc: "ToolEventType keeps unknown discriminators assignable so a newer host degrades gracefully.",
	},
	{
		goPrefix: "Decision", constName: "Decisions", unionName: "Decision", open: true,
		doc:      "Decision values a client passes to resolvePermission; the host fails closed on anything else.",
		unionDoc: "Decision keeps unknown values assignable; the host treats them as deny.",
	},
	{
		goPrefix: "ErrCode", constName: "ErrCodes", unionName: "ErrCode", open: true,
		doc:      "Error codes carried by protocol.Error; clients switch on code.",
		unionDoc: "ErrCode keeps unknown codes assignable so a newer host degrades gracefully.",
	},
	{
		goPrefix: "PlanStage", constName: "PlanStages", unionName: "PlanStage", open: false,
		doc:      "Stages of 计划模式's planning pipeline, in order; the values of plan_write's stage argument.",
		unionDoc: "PlanStage is the closed set of planning stages — the shell's own pipeline, so an unknown value is a bug, not a newer peer.",
	},
	{
		goPrefix: "PlanState", constName: "PlanStates", unionName: "PlanState", open: false,
		doc:      "Lifecycle states of a planning run, carried by PlanRun.state.",
		unionDoc: "PlanState is the closed set of planning-run states.",
	},
}

// constExemptTypes names constant types that are transport metadata rather than
// wire values a client switches on, so their constants generate no TS and don't
// trip the ungrouped-constant check. CommandKind classifies App methods
// server-side; the per-command kinds already reach TS through the commands table
// (which has its own missing/stale sync check).
var constExemptTypes = map[string]bool{
	"CommandKind": true,
}

// constTypeName returns the declared (named) type of a constant, "" for untyped
// or basic-typed constants.
func constTypeName(c *types.Const) string {
	if named, ok := types.Unalias(c.Type()).(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

type model struct {
	version    int64  // protocol.Version
	versionDoc string // its Go doc synopsis
	groups     []constGroup
	structs    []structDef // sorted by name
	events     []eventDef  // sorted by wire name
	commands   []commandDef
}

type constGroup struct {
	spec  constGroupSpec
	items []constItem // sorted by key
}

type constItem struct{ key, value string }

type structDef struct {
	name       string
	doc        string
	typeParams string
	fields     []fieldDef // Go declaration order
}

type fieldDef struct {
	name     string
	optional bool
	tsType   string
}

type eventDef struct {
	wire    string // event name on the wire, e.g. "assistant:delta"
	payload string // TS interface name
}

type commandDef struct {
	goName string
	tsName string
	kind   string
	doc    string
	params []paramDef
	result string // TS type inside Promise<...>; "void" for none
}

type paramDef struct {
	name   string
	tsType string
}

// extract builds the emit model from the wire-contract packages plus the App.
// The protocol packages are read as one contract: their declarations are merged
// and re-sorted by name, so which of the two a type lives in never shows up in
// the generated TypeScript. A name declared in both is a conflict, not a merge.
func extract(protoPkgs []*packages.Package, deskPkg *packages.Package) (*model, error) {
	m := &model{}
	docs := map[string]string{}
	mapper := &typeMapper{protocolPaths: map[string]bool{}}
	for _, p := range protoPkgs {
		mapper.protocolPaths[p.PkgPath] = true
		for name, doc := range collectDocs(p) {
			docs[name] = doc
		}
	}
	if err := checkNoDuplicateNames(protoPkgs); err != nil {
		return nil, err
	}

	structNames := map[string]bool{}
	consts := map[string]*types.Const{}
	var constNames []string
	for _, p := range protoPkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			switch obj := scope.Lookup(name).(type) {
			case *types.TypeName:
				if !obj.Exported() || obj.IsAlias() {
					continue
				}
				st, ok := obj.Type().Underlying().(*types.Struct)
				if !ok {
					continue // non-struct named types (e.g. CommandKind) have no wire shape
				}
				def, err := extractStruct(name, st, docs[name], mapper)
				if err != nil {
					return nil, err
				}
				m.structs = append(m.structs, def)
				structNames[name] = true
			case *types.Const:
				if !obj.Exported() {
					continue
				}
				consts[name] = obj
				constNames = append(constNames, name)
			}
		}
	}
	sort.Slice(m.structs, func(i, j int) bool { return m.structs[i].name < m.structs[j].name })
	sort.Strings(constNames)

	// Constants → const groups (+ protocol.Version).
	var eventConsts []string
	var ungrouped []string
	for _, name := range constNames {
		c := consts[name]
		if name == "Version" {
			v, ok := constant.Int64Val(c.Val())
			if !ok {
				return nil, fmt.Errorf("protocol.Version is not an integer constant")
			}
			m.version = v
			m.versionDoc = docs[name]
			continue
		}
		if c.Val().Kind() != constant.String {
			continue // e.g. future non-string constants; nothing to mirror
		}
		grp := matchGroup(name)
		if grp == nil {
			// No silent drops: a string constant that matches no group prefix is
			// either transport metadata (its type is exempted below) or a mistake —
			// someone added a constant group and forgot to register it, and the TS
			// side would just silently miss it. Force the decision.
			if !constExemptTypes[constTypeName(c)] {
				ungrouped = append(ungrouped, name)
			}
			continue
		}
		item := constItem{key: strings.TrimPrefix(name, grp.goPrefix), value: constant.StringVal(c.Val())}
		for i := range m.groups {
			if m.groups[i].spec.goPrefix == grp.goPrefix {
				m.groups[i].items = append(m.groups[i].items, item)
				grp = nil
				break
			}
		}
		if grp != nil {
			m.groups = append(m.groups, constGroup{spec: *grp, items: []constItem{item}})
		}
		if strings.HasPrefix(name, "Event") {
			eventConsts = append(eventConsts, name)
		}
	}
	if len(ungrouped) > 0 {
		sort.Strings(ungrouped)
		return nil, fmt.Errorf("exported string constants in protocol match no const group prefix and no exempt type — register a constGroupSpec (they become a TS union) or add their type to constExemptTypes (transport metadata):\n  %s",
			strings.Join(ungrouped, ", "))
	}
	// Fixed group order, sorted items.
	sort.Slice(m.groups, func(i, j int) bool {
		return groupOrder(m.groups[i].spec.goPrefix) < groupOrder(m.groups[j].spec.goPrefix)
	})
	for i := range m.groups {
		sort.Slice(m.groups[i].items, func(a, b int) bool {
			return m.groups[i].items[a].key < m.groups[i].items[b].key
		})
	}

	// Events: the explicit table must exactly cover the Event* constants.
	events, err := extractEvents(consts, eventConsts, structNames)
	if err != nil {
		return nil, err
	}
	m.events = events

	// Commands from desktop.App.
	cmds, err := extractCommands(deskPkg, mapper)
	if err != nil {
		return nil, err
	}
	m.commands = cmds
	return m, nil
}

// checkNoDuplicateNames rejects an exported name declared by more than one
// protocol package. The two are emitted into a single TypeScript module, so a
// collision would silently drop one declaration; it also means a type was moved
// between the packages without deleting the original.
func checkNoDuplicateNames(protoPkgs []*packages.Package) error {
	owner := map[string]string{}
	var dups []string
	for _, p := range protoPkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			if obj := scope.Lookup(name); obj == nil || !obj.Exported() {
				continue
			}
			if first, seen := owner[name]; seen {
				dups = append(dups, fmt.Sprintf("%s (in %s and %s)", name, first, p.PkgPath))
				continue
			}
			owner[name] = p.PkgPath
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		return fmt.Errorf("the protocol packages declare the same exported name twice; they are emitted as one TypeScript module, so each name may only be defined once:\n  %s",
			strings.Join(dups, "\n  "))
	}
	return nil
}

func groupOrder(prefix string) int {
	for i, g := range constGroupSpecs {
		if g.goPrefix == prefix {
			return i
		}
	}
	return len(constGroupSpecs)
}

// matchGroup finds the const group for a constant name, longest prefix first
// so ToolEvent* is never mistaken for Event*.
func matchGroup(name string) *constGroupSpec {
	var best *constGroupSpec
	for i := range constGroupSpecs {
		g := &constGroupSpecs[i]
		if strings.HasPrefix(name, g.goPrefix) && (best == nil || len(g.goPrefix) > len(best.goPrefix)) {
			best = g
		}
	}
	return best
}

func extractStruct(name string, st *types.Struct, doc string, mapper *typeMapper) (structDef, error) {
	def := structDef{name: name, doc: doc}
	if g, ok := genericStructs[name]; ok {
		def.typeParams = g.typeParams
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Exported() {
			continue // unexported fields never serialize
		}
		if f.Embedded() {
			return def, fmt.Errorf("protocol.%s embeds %s: embedded fields are not supported by protogen", name, f.Name())
		}
		jsonName, optional, stringEncoded, skip := parseJSONTag(reflect.StructTag(st.Tag(i)).Get("json"), f.Name())
		if skip {
			continue
		}
		tsType, err := fieldTSType(name, jsonName, f.Type(), optional, mapper)
		if err != nil {
			return def, fmt.Errorf("protocol.%s.%s: %w", name, f.Name(), err)
		}
		if stringEncoded && isStringEncodable(f.Type()) {
			// `,string` wraps the value in a JSON string on the wire (typically to
			// keep 64-bit integers exact past JS float precision) — the Go type no
			// longer reflects the wire type.
			tsType = "string"
		}
		def.fields = append(def.fields, fieldDef{name: jsonName, optional: optional, tsType: tsType})
	}
	return def, nil
}

// fieldTSType maps one struct field's Go type to TS, applying the generic and
// union overrides and the omitempty-pointer rule (`f?: T` rather than `f?: T | null`,
// since a nil pointer with omitempty is omitted, never null).
func fieldTSType(structName, jsonName string, t types.Type, optional bool, mapper *typeMapper) (string, error) {
	if g, ok := genericStructs[structName]; ok {
		if over, ok := g.fields[jsonName]; ok {
			return over, nil
		}
	}
	if over, ok := fieldTypeOverrides[structName][jsonName]; ok {
		return over, nil
	}
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok && optional {
		return mapper.ts(ptr.Elem(), true)
	}
	return mapper.ts(t, true)
}

// parseJSONTag returns the wire name, whether omitempty is set, whether the
// value is string-encoded on the wire (`,string`), and whether the field is
// excluded from JSON entirely. `json:"-"` skips; `json:"-,"` is encoding/json's
// escape for a field literally named "-".
func parseJSONTag(tag, goName string) (name string, optional, stringEncoded, skip bool) {
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "-" && len(parts) == 1 {
		return "", false, false, true
	}
	if name == "" {
		name = goName
	}
	for _, opt := range parts[1:] {
		switch opt {
		case "omitempty":
			optional = true
		case "string":
			stringEncoded = true
		}
	}
	return name, optional, stringEncoded, false
}

// isStringEncodable mirrors the types encoding/json honors `,string` for —
// string, bool, integer, and floating-point (plus pointers to them); the option
// is ignored elsewhere, so the TS override must not fire there either.
func isStringEncodable(t types.Type) bool {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

func extractEvents(consts map[string]*types.Const, eventConsts []string, structNames map[string]bool) ([]eventDef, error) {
	constSet := map[string]bool{}
	for _, n := range eventConsts {
		constSet[n] = true
	}
	var missing, stale []string
	for _, n := range eventConsts {
		if _, ok := eventPayloads[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range eventPayloads {
		if !constSet[n] {
			stale = append(stale, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		var b strings.Builder
		b.WriteString("event table (eventPayloads in tools/protogen) is out of sync with the protocol package's Event* constants:")
		if len(missing) > 0 {
			fmt.Fprintf(&b, "\n  missing table rows for: %s", strings.Join(missing, ", "))
		}
		if len(stale) > 0 {
			fmt.Fprintf(&b, "\n  stale table rows (no such constant): %s", strings.Join(stale, ", "))
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	events := make([]eventDef, 0, len(eventConsts))
	for _, n := range eventConsts {
		payload := eventPayloads[n]
		if !structNames[payload] {
			return nil, fmt.Errorf("event table maps %s to unknown protocol type %s", n, payload)
		}
		events = append(events, eventDef{wire: constant.StringVal(consts[n].Val()), payload: payload})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].wire < events[j].wire })
	return events, nil
}

func extractCommands(deskPkg *packages.Package, mapper *typeMapper) ([]commandDef, error) {
	obj := deskPkg.Types.Scope().Lookup("App")
	if obj == nil {
		return nil, fmt.Errorf("type App not found in %s", deskPkg.PkgPath)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s.App is not a named type", deskPkg.PkgPath)
	}
	methodDocs := collectMethodDocs(deskPkg, "App")

	var cmds []commandDef
	var violations []string
	methodSet := map[string]bool{}
	for i := 0; i < named.NumMethods(); i++ {
		fn := named.Method(i)
		if !fn.Exported() || excludedMethods[fn.Name()] {
			continue
		}
		methodSet[fn.Name()] = true
		cmd, errs := extractCommand(fn, methodDocs[fn.Name()], mapper)
		if len(errs) > 0 {
			violations = append(violations, errs...)
			continue
		}
		cmds = append(cmds, cmd)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		return nil, fmt.Errorf("app methods with signatures that cannot cross the wire (only protocol and basic types may appear):\n  %s",
			strings.Join(violations, "\n  "))
	}

	// Cross-check against the shell's own internal/protocol.CommandKinds (imported directly, so it can
	// never skew from the sources protogen was built against).
	var unclassified, orphaned []string
	for name := range methodSet {
		if _, ok := deskproto.CommandKinds[name]; !ok {
			unclassified = append(unclassified, name)
		}
	}
	for name := range deskproto.CommandKinds {
		if !methodSet[name] {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(orphaned)
	if len(unclassified) > 0 || len(orphaned) > 0 {
		var b strings.Builder
		b.WriteString("internal/protocol.CommandKinds is out of sync with desktop.App's exported methods:")
		if len(unclassified) > 0 {
			fmt.Fprintf(&b, "\n  methods missing a CommandKinds entry: %s", strings.Join(unclassified, ", "))
		}
		if len(orphaned) > 0 {
			fmt.Fprintf(&b, "\n  CommandKinds entries with no App method: %s", strings.Join(orphaned, ", "))
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	for i := range cmds {
		cmds[i].kind = string(deskproto.CommandKinds[cmds[i].goName])
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].tsName < cmds[j].tsName })
	return cmds, nil
}

// extractCommand maps one App method to a command definition. It returns the
// violations (never a partial success) when the signature leaks non-wire types.
func extractCommand(fn *types.Func, doc string, mapper *typeMapper) (commandDef, []string) {
	sig := fn.Type().(*types.Signature)
	cmd := commandDef{goName: fn.Name(), tsName: lowerCamel(fn.Name()), doc: doc}
	var violations []string

	if sig.Variadic() {
		violations = append(violations, fmt.Sprintf("%s: variadic parameters are not supported", fn.Name()))
	}
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		name := p.Name()
		if name == "" || name == "_" {
			name = fmt.Sprintf("arg%d", i)
		}
		// Parameters flow client→host: the caller always passes a value, so
		// slices/maps are non-nullable here (unlike results, where a Go nil
		// serializes to null).
		tsType, err := mapper.ts(p.Type(), false)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: parameter %s: %v", fn.Name(), name, err))
			continue
		}
		cmd.params = append(cmd.params, paramDef{name: sanitizeIdent(name), tsType: tsType})
	}

	results := sig.Results()
	switch results.Len() {
	case 0:
		cmd.result = "void"
	case 1:
		if isErrorType(results.At(0).Type()) {
			cmd.result = "void"
		} else {
			ts, err := mapper.ts(results.At(0).Type(), true)
			if err != nil {
				violations = append(violations, fmt.Sprintf("%s: result: %v", fn.Name(), err))
			}
			cmd.result = ts
		}
	case 2:
		if !isErrorType(results.At(1).Type()) {
			violations = append(violations, fmt.Sprintf("%s: second result must be error, got %s", fn.Name(), results.At(1).Type()))
			break
		}
		ts, err := mapper.ts(results.At(0).Type(), true)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: result: %v", fn.Name(), err))
		}
		cmd.result = ts
	default:
		violations = append(violations, fmt.Sprintf("%s: %d results — multi-value returns beyond (T, error) need a protogen decision", fn.Name(), results.Len()))
	}
	return cmd, violations
}

func isErrorType(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() == nil && named.Obj().Name() == "error"
}

// typeMapper converts Go wire types to TypeScript. nullable controls whether
// slices/maps/pointers carry `| null` (true for host→client values, where Go
// nil serializes as JSON null).
type typeMapper struct {
	// protocolPaths is the set of packages whose types are wire types. Anything
	// outside it on a wire signature is a leak and fails generation.
	protocolPaths map[string]bool
}

func (m *typeMapper) ts(t types.Type, nullable bool) (string, error) {
	switch tt := types.Unalias(t).(type) {
	case *types.Named:
		obj := tt.Obj()
		if obj.Pkg() == nil {
			return "", fmt.Errorf("universe type %s is not a wire type", obj.Name())
		}
		if m.protocolPaths[obj.Pkg().Path()] {
			if _, ok := tt.Underlying().(*types.Struct); ok {
				return obj.Name(), nil
			}
			// Named non-struct protocol types (string enums etc.) map by shape.
			return m.ts(tt.Underlying(), nullable)
		}
		if obj.Pkg().Path() == "encoding/json" && obj.Name() == "RawMessage" {
			return "unknown", nil // opaque JSON value
		}
		return "", fmt.Errorf("non-protocol type %s", types.TypeString(tt, nil))
	case *types.Basic:
		switch {
		case tt.Info()&types.IsBoolean != 0:
			return "boolean", nil
		case tt.Info()&types.IsString != 0:
			return "string", nil
		case tt.Info()&types.IsInteger != 0, tt.Info()&types.IsFloat != 0:
			return "number", nil
		default:
			return "", fmt.Errorf("unsupported basic type %s", tt)
		}
	case *types.Slice:
		if elem, ok := types.Unalias(tt.Elem()).(*types.Basic); ok && elem.Kind() == types.Uint8 {
			return "string", nil // []byte serializes as base64
		}
		inner, err := m.ts(tt.Elem(), nullable)
		if err != nil {
			return "", err
		}
		if strings.ContainsAny(inner, " |") {
			inner = "(" + inner + ")"
		}
		if nullable {
			return inner + "[] | null", nil
		}
		return inner + "[]", nil
	case *types.Map:
		if key, ok := types.Unalias(tt.Key()).(*types.Basic); !ok || key.Info()&types.IsString == 0 {
			return "", fmt.Errorf("map key %s is not a string", tt.Key())
		}
		val, err := m.ts(tt.Elem(), nullable)
		if err != nil {
			return "", err
		}
		if nullable {
			return "Record<string, " + val + "> | null", nil
		}
		return "Record<string, " + val + ">", nil
	case *types.Pointer:
		inner, err := m.ts(tt.Elem(), nullable)
		if err != nil {
			return "", err
		}
		if nullable {
			return inner + " | null", nil
		}
		return inner, nil
	case *types.Interface:
		if tt.Empty() {
			return "unknown", nil
		}
		return "", fmt.Errorf("non-empty interface %s is not a wire type", tt)
	default:
		return "", fmt.Errorf("unsupported type %s", types.TypeString(t, nil))
	}
}

// collectDocs maps every type/const name declared in pkg to its doc synopsis.
func collectDocs(pkg *packages.Package) map[string]string {
	docs := map[string]string{}
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					d := s.Doc
					if d == nil {
						d = gd.Doc
					}
					docs[s.Name.Name] = synopsis(d.Text())
				case *ast.ValueSpec:
					d := s.Doc
					if d == nil && len(gd.Specs) == 1 {
						d = gd.Doc // unparenthesized decl: the doc sits on the GenDecl
					}
					if d == nil {
						continue
					}
					for _, n := range s.Names {
						docs[n.Name] = synopsis(d.Text())
					}
				}
			}
		}
	}
	return docs
}

// collectMethodDocs maps recvType's method names to their doc synopses.
func collectMethodDocs(pkg *packages.Package, recvType string) map[string]string {
	docs := map[string]string{}
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || fd.Doc == nil {
				continue
			}
			t := fd.Recv.List[0].Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			if id, ok := t.(*ast.Ident); ok && id.Name == recvType {
				docs[fd.Name.Name] = synopsis(fd.Doc.Text())
			}
		}
	}
	return docs
}

// synopsis returns the doc text's first sentence, collapsed to one line.
func synopsis(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i+1 == len(s) || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

func lowerCamel(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// tsReserved are the TypeScript/JavaScript reserved words that cannot be
// parameter names; sanitizeIdent appends an underscore to escape them.
var tsReserved = map[string]bool{
	"await": true, "break": true, "case": true, "catch": true, "class": true,
	"const": true, "continue": true, "debugger": true, "default": true,
	"delete": true, "do": true, "else": true, "enum": true, "export": true,
	"extends": true, "false": true, "finally": true, "for": true,
	"function": true, "if": true, "implements": true, "import": true,
	"in": true, "instanceof": true, "interface": true, "let": true,
	"new": true, "null": true, "package": true, "private": true,
	"protected": true, "public": true, "return": true, "static": true,
	"super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "var": true, "void": true, "while": true,
	"with": true, "yield": true,
}

func sanitizeIdent(name string) string {
	if tsReserved[name] {
		return name + "_"
	}
	return name
}
