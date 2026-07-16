package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

func writeSkill(t *testing.T, root, dirName, content string) {
	t.Helper()
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefinitionFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestParseSkill(t *testing.T) {
	t.Parallel()
	p, err := parseSkill("---\nname: deploy\ndescription: \"Ship the service\"\n---\n\nStep 1\nStep 2\n")
	if err != nil {
		t.Fatalf("parseSkill: %v", err)
	}
	if p.Name != "deploy" || p.Description != "Ship the service" {
		t.Fatalf("parsed = %#v", p)
	}
	if p.Body != "Step 1\nStep 2\n" {
		t.Fatalf("body = %q", p.Body)
	}

	// CRLF line endings are tolerated.
	if _, err := parseSkill("---\r\nname: x\r\ndescription: y\r\n---\r\nbody\r\n"); err != nil {
		t.Fatalf("CRLF parse: %v", err)
	}

	// A leading UTF-8 BOM (common from Windows editors) is tolerated. The BOM is
	// built from bytes so the test source itself stays BOM-free.
	bom := string([]byte{0xEF, 0xBB, 0xBF})
	if _, err := parseSkill(bom + "---\nname: x\ndescription: y\n---\nbody"); err != nil {
		t.Fatalf("BOM parse: %v", err)
	}
}

func TestParseSkillErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no frontmatter":      "just a body with no frontmatter",
		"unterminated":        "---\nname: x\ndescription: y\n",
		"missing name":        "---\ndescription: y\n---\nbody",
		"missing description": "---\nname: x\n---\nbody",
		"invalid name":        "---\nname: bad name\ndescription: y\n---\nbody",
	}
	for label, content := range cases {
		if _, err := parseSkill(content); err == nil {
			t.Errorf("%s: expected an error", label)
		}
	}
}

func TestLoadDiscoversAndAttributes(t *testing.T) {
	t.Parallel()
	userRoot := t.TempDir()
	projectRoot := t.TempDir()
	writeSkill(t, userRoot, "alpha", "---\nname: alpha\ndescription: user alpha\n---\nuse alpha")
	writeSkill(t, projectRoot, "beta", "---\nname: beta\ndescription: project beta\n---\nuse beta")
	// A subdirectory without a SKILL.md is simply not a skill.
	if err := os.MkdirAll(filepath.Join(projectRoot, "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	set, problems := Load(LoadOptions{Roots: []Root{
		{Dir: userRoot, Source: SourceUser},
		{Dir: projectRoot, Source: SourceProject},
	}})
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if set.Len() != 2 {
		t.Fatalf("loaded %d skills, want 2 (%v)", set.Len(), set.Names())
	}
	alpha, ok := set.Get("alpha")
	if !ok || alpha.Source != SourceUser {
		t.Fatalf("alpha = %#v, ok=%v", alpha, ok)
	}
	beta, ok := set.Get("beta")
	if !ok || beta.Source != SourceProject {
		t.Fatalf("beta = %#v, ok=%v", beta, ok)
	}
}

func TestLoadUserSkillShadowsProject(t *testing.T) {
	t.Parallel()
	userRoot := t.TempDir()
	projectRoot := t.TempDir()
	writeSkill(t, userRoot, "dup", "---\nname: dup\ndescription: user wins\n---\nuser body")
	writeSkill(t, projectRoot, "dup", "---\nname: dup\ndescription: project loses\n---\nproject body")

	set, _ := Load(LoadOptions{Roots: []Root{
		{Dir: userRoot, Source: SourceUser},
		{Dir: projectRoot, Source: SourceProject},
	}})
	if set.Len() != 1 {
		t.Fatalf("loaded %d, want 1 deduplicated", set.Len())
	}
	dup, _ := set.Get("dup")
	if dup.Source != SourceUser || dup.Body != "user body" {
		t.Fatalf("dup = %#v, want the user skill to win", dup)
	}
}

func TestLoadSkipsMalformedWithProblem(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "good", "---\nname: good\ndescription: fine\n---\nok")
	writeSkill(t, root, "bad", "no frontmatter here")

	set, problems := Load(LoadOptions{Roots: []Root{{Dir: root, Source: SourceUser}}})
	if set.Len() != 1 || set.Names()[0] != "good" {
		t.Fatalf("loaded = %v, want only good", set.Names())
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Dir, "bad") {
		t.Fatalf("problems = %v, want one for bad", problems)
	}
}

func TestLoadTruncatesOversizedBody(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := strings.Repeat("x", 200)
	writeSkill(t, root, "big", "---\nname: big\ndescription: large\n---\n"+body)

	set, _ := Load(LoadOptions{Roots: []Root{{Dir: root, Source: SourceUser}}, MaxBodyBytes: 64})
	big, ok := set.Get("big")
	if !ok || !big.Truncated {
		t.Fatalf("big = %#v, want truncated", big)
	}
}

func TestLoadMissingRootIsNotAProblem(t *testing.T) {
	t.Parallel()
	set, problems := Load(LoadOptions{Roots: []Root{{Dir: filepath.Join(t.TempDir(), "does-not-exist"), Source: SourceUser}}})
	if set.Len() != 0 || len(problems) != 0 {
		t.Fatalf("set=%d problems=%v, want empty and no problem", set.Len(), problems)
	}
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	set := NewSet([]Skill{
		{Name: "alpha", Description: "does alpha", Source: SourceUser},
		{Name: "beta", Description: "does beta", Source: SourceProject},
	})
	cat := Catalog(set)
	if !strings.Contains(cat, "Skill: alpha") || !strings.Contains(cat, "does alpha") {
		t.Fatalf("catalog missing alpha:\n%s", cat)
	}
	if !strings.Contains(cat, "Skill: beta [project]") {
		t.Fatalf("catalog missing project tag for beta:\n%s", cat)
	}
	// alpha must precede beta (name order).
	if strings.Index(cat, "alpha") > strings.Index(cat, "beta") {
		t.Fatalf("catalog not name-ordered:\n%s", cat)
	}
	if Catalog(NewSet(nil)) != "" {
		t.Fatal("empty set catalog must be empty")
	}
}

func TestToolRun(t *testing.T) {
	t.Parallel()
	set := NewSet([]Skill{{Name: "deploy", Description: "ship", Body: "do the deploy"}})
	tl := NewTool(set)
	if tl.Name() != "Skill" || !tl.IsConcurrencySafe() {
		t.Fatalf("tool name/concurrency = %q/%v", tl.Name(), tl.IsConcurrencySafe())
	}
	if tl.InputSchema().Type != tool.SchemaTypeObject {
		t.Fatalf("schema type = %q", tl.InputSchema().Type)
	}

	res, err := tl.Run(context.Background(), json.RawMessage(`{"name":"deploy"}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError || len(res.Content) != 1 || res.Content[0].Text != "do the deploy" {
		t.Fatalf("result = %#v, want the body", res)
	}
}

func TestToolRunErrors(t *testing.T) {
	t.Parallel()
	tl := NewTool(NewSet([]Skill{{Name: "known", Description: "d", Body: "b"}}))
	cases := []json.RawMessage{
		json.RawMessage(`{"name":"missing"}`),
		json.RawMessage(`{"name":"  "}`),
		json.RawMessage(`not json`),
	}
	for _, in := range cases {
		res, err := tl.Run(context.Background(), in, nil, nil)
		if err != nil {
			t.Fatalf("Run(%s) returned Go error: %v", in, err)
		}
		if !res.IsError {
			t.Fatalf("Run(%s) = %#v, want is_error", in, res)
		}
	}
}

func TestToolRunTruncatedNote(t *testing.T) {
	t.Parallel()
	tl := NewTool(NewSet([]Skill{{Name: "big", Description: "d", Body: "partial", Truncated: true}}))
	res, _ := tl.Run(context.Background(), json.RawMessage(`{"name":"big"}`), nil, nil)
	if !strings.Contains(res.Content[0].Text, "truncated") {
		t.Fatalf("truncated body missing note: %q", res.Content[0].Text)
	}
}

// A skill's body points at its bundled files by a path relative to the skill's own
// directory, so loading it must name that directory — otherwise the model has to
// search the filesystem for files whose location is already known.
func TestToolRunDisclosesSkillDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("srv", "skills", "dataviz")
	tl := NewTool(NewSet([]Skill{{
		Name: "dataviz", Description: "charts",
		Body: "see references/palette.md",
		Path: filepath.Join(dir, DefinitionFileName),
	}}))
	res, err := tl.Run(context.Background(), json.RawMessage(`{"name":"dataviz"}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %#v, want success", res)
	}
	if !strings.Contains(res.Content[0].Text, dir) {
		t.Fatalf("result missing skill directory %q: %q", dir, res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "see references/palette.md") {
		t.Fatalf("result dropped the body: %q", res.Content[0].Text)
	}
}

// Loaded skills always carry a Path; a hand-built one has no location to disclose,
// and must still return its instructions unchanged.
func TestToolRunWithoutPathReturnsBareBody(t *testing.T) {
	t.Parallel()
	tl := NewTool(NewSet([]Skill{{Name: "deploy", Description: "ship", Body: "do the deploy"}}))
	res, _ := tl.Run(context.Background(), json.RawMessage(`{"name":"deploy"}`), nil, nil)
	if res.Content[0].Text != "do the deploy" {
		t.Fatalf("result = %q, want the bare body", res.Content[0].Text)
	}
}
