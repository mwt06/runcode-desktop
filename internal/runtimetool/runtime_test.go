package runtimetool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/protocol"
)

type fakeInstaller struct {
	got   []string
	reply string
	err   error
}

func (f *fakeInstaller) EnsureRuntime(_ context.Context, id string) (string, error) {
	f.got = append(f.got, id)
	return f.reply, f.err
}

func run(t *testing.T, inst Installer, input string) (string, error) {
	t.Helper()
	res, err := New(inst).Run(context.Background(), json.RawMessage(input), nil, nil)
	if err != nil {
		return "", err
	}
	if len(res.Content) != 1 {
		t.Fatalf("result content = %+v, want one text block", res.Content)
	}
	return res.Content[0].Text, nil
}

func TestRunPassesCanonicalID(t *testing.T) {
	cases := map[string]string{
		`{"runtime":"python"}`:  protocol.RuntimePackPython,
		`{"runtime":"Python3"}`: protocol.RuntimePackPython,
		`{"runtime":" node "}`:  protocol.RuntimePackNode,
		`{"runtime":"nodejs"}`:  protocol.RuntimePackNode,
		`{"runtime":"git"}`:     protocol.RuntimePackGit,
	}
	for input, want := range cases {
		inst := &fakeInstaller{reply: "ok"}
		out, err := run(t, inst, input)
		if err != nil {
			t.Errorf("%s: %v", input, err)
			continue
		}
		if len(inst.got) != 1 || inst.got[0] != want {
			t.Errorf("%s: installer got %v, want [%s]", input, inst.got, want)
		}
		if out != "ok" {
			t.Errorf("%s: result = %q, want the installer's report", input, out)
		}
	}
}

func TestRunRejectsUnknownRuntimeWithoutInstalling(t *testing.T) {
	for _, input := range []string{`{"runtime":"ruby"}`, `{"runtime":""}`, `{}`, `not json`} {
		inst := &fakeInstaller{}
		if _, err := run(t, inst, input); err == nil {
			t.Errorf("%s: accepted", input)
		}
		if len(inst.got) != 0 {
			t.Errorf("%s: installer was called with %v", input, inst.got)
		}
	}
}

func TestRunSurfacesInstallerError(t *testing.T) {
	inst := &fakeInstaller{err: errors.New("installing Python was cancelled by the user")}
	if _, err := run(t, inst, `{"runtime":"python"}`); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want the installer's error passed through to the model", err)
	}
}

func TestRunWithoutInstallerPointsAtSettings(t *testing.T) {
	_, err := run(t, nil, `{"runtime":"python"}`)
	if err == nil || !strings.Contains(err.Error(), "设置 → 运行时环境") {
		t.Fatalf("err = %v, want a pointer to the settings page", err)
	}
}

func TestRuntimeID(t *testing.T) {
	if got := RuntimeID(json.RawMessage(`{"runtime":"node"}`)); got != protocol.RuntimePackNode {
		t.Errorf("RuntimeID = %q, want node", got)
	}
	if got := RuntimeID(json.RawMessage(`{"runtime":"ruby"}`)); got != "" {
		t.Errorf("RuntimeID(unknown) = %q, want empty", got)
	}
}

func TestSchemaAndDescription(t *testing.T) {
	tl := New(nil)
	s := tl.InputSchema()
	enum := s.Properties["runtime"].Enum
	if len(enum) != len(ids) {
		t.Fatalf("enum = %v, want %v", enum, ids)
	}
	for i, id := range ids {
		if enum[i] != id {
			t.Errorf("enum[%d] = %v, want %s", i, enum[i], id)
		}
	}
	// 描述里最要紧的一句：别换别的法子装。这句一丢，模型在工具失败后就会去 curl | sh。
	if d := tl.Description(); !strings.Contains(d, "Do not install interpreters any other way") {
		t.Errorf("description no longer forbids other installers:\n%s", d)
	}
}
