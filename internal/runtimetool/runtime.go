// Package runtimetool implements the install_runtime tool: the model installs one of
// the app's own runtime packs (Python / Node.js / Git) when a task needs it, instead
// of stopping to ask the user to go to Settings and click 安装.
//
// The tool is a thin door onto the desktop's existing installer — the same one the
// settings page drives: the pack comes from the app's own manifest, is verified
// against its sha256, and is unpacked into the user's own directory without admin
// rights. Nothing about *what* gets installed is up to the model; it only names which
// of the three. That is what makes it safe to hand to the model at all: the obvious
// alternative it reaches for on its own (`curl … | sh`, uv, pyenv) runs whatever the
// internet serves that day.
//
// Every call still needs the user's approval: the desktop classifies this tool as
// mutating, which the permission pipeline treats like an external call — always
// prompted, never waved through by the harm judge, refused in plan and safe mode.
//
// It is registered only in the desktop (via engine.Options.ExtraTools), never the
// CLI: the CLI has no runtime manager to install into.
package runtimetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Name is the wire name the model calls.
const Name = "install_runtime"

// ids is every runtime the tool accepts, in the order the schema lists them.
var ids = []string{protocol.RuntimePackPython, protocol.RuntimePackNode, protocol.RuntimePackGit}

// Installer is the desktop side of the tool: the runtime manager.
type Installer interface {
	// EnsureRuntime makes runtime id usable — installs it if missing, re-requests the
	// security-center authorization if it is installed but not yet trusted, waits if
	// an install is already running — and returns what the model gets back: where it
	// is and how to run it. ctx is the turn's: stopping the turn cancels the download.
	//
	// The error goes to the model as an is_error result, so it must say what to do
	// instead (use the system package manager, ask the user, do not retry).
	EnsureRuntime(ctx context.Context, id string) (string, error)
}

type input struct {
	Runtime string `json:"runtime"`
}

// Tool is the install_runtime tool.
type Tool struct{ inst Installer }

// New returns the install_runtime tool backed by inst.
func New(inst Installer) tool.Tool { return Tool{inst: inst} }

// Name returns "install_runtime".
func (Tool) Name() string { return Name }

// Description says when to call it (only when the task needs the runtime and the
// system prompt's runtime section says it is missing) and what the user will see —
// an approval prompt, and on 银河麒麟 a password dialog — so the model can tell the
// user what is about to happen instead of silently waiting on them.
func (Tool) Description() string {
	return `Install one of the app's own runtimes — Python, Node.js or Git — when the task needs it and the "Runtime environment" section says it is NOT available (or that the security center has not trusted it yet).

The app downloads its own verified copy (sha256-checked) into the user's own directory; no admin rights are involved. The user must approve the call. On 银河麒麟 the user is then asked for their system password once in the app's own dialog, so the security center lets the new programs run; you never see the password.

When it succeeds, the runtime is on PATH for Bash right away, in this conversation — ignore the earlier note that said it was missing. The result says how to run it.

This is the only supported way to get these runtimes. Do not install interpreters any other way (uv, pyenv, conda, ` + "`curl … | sh`" + `, downloading one), and do not tell the user to download one. Do not call it for a runtime that is already available.`
}

// InputSchema declares the one argument: which runtime.
func (Tool) InputSchema() tool.Schema {
	enum := make([]any, len(ids))
	for i, id := range ids {
		enum[i] = id
	}
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"runtime": {
				Type:        tool.SchemaTypeString,
				Description: `Which runtime to install: "python", "node" or "git".`,
				Enum:        enum,
			},
		},
		Required:             []string{"runtime"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe is false: installs share one runtime directory and one password
// dialog, and two at once would put two approval-then-password sequences in front
// of the user with no way to tell which is which.
func (Tool) IsConcurrencySafe() bool { return false }

// Run validates the runtime id and hands it to the installer.
func (t Tool) Run(ctx context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	id, err := parse(raw)
	if err != nil {
		return tool.Result{}, err
	}
	if t.inst == nil {
		return tool.Result{}, errors.New("installing runtimes is unavailable in this session; ask the user to install it from 设置 → 运行时环境")
	}
	out, err := t.inst.EnsureRuntime(ctx, id)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: out}}}, nil
}

// RuntimeID extracts the runtime a call names, or "" when the input does not name a
// known one. The desktop's permission resolver uses it to tell the user which
// runtime (and how big a download) they are approving — the approval request itself
// carries no tool arguments.
func RuntimeID(raw json.RawMessage) string {
	id, err := parse(raw)
	if err != nil {
		return ""
	}
	return id
}

func parse(raw json.RawMessage) (string, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parse %s input: %w", Name, err)
	}
	id := strings.ToLower(strings.TrimSpace(in.Runtime))
	// Common spellings the model reaches for; the schema's enum is advisory to it.
	switch id {
	case "python3", "py":
		id = protocol.RuntimePackPython
	case "nodejs", "node.js":
		id = protocol.RuntimePackNode
	}
	if slices.Contains(ids, id) {
		return id, nil
	}
	if id == "" {
		return "", errors.New(`runtime is required: one of "python", "node", "git"`)
	}
	return "", fmt.Errorf(`unknown runtime %q (want "python", "node" or "git")`, in.Runtime)
}
