package repl

import (
	"reflect"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tools"
)

func TestToolSpecs(t *testing.T) {
	t.Parallel()

	builtins := tools.Builtins()
	specs := ToolSpecs(builtins)
	if len(specs) != len(builtins) {
		t.Fatalf("expected %d specs, got %d", len(builtins), len(specs))
	}
	for i, spec := range specs {
		builtin := builtins[i]
		if spec.Name != builtin.Name() {
			t.Fatalf("spec name = %q, want %q", spec.Name, builtin.Name())
		}
		if spec.Description != builtin.Description() {
			t.Fatalf("spec description = %q, want %q", spec.Description, builtin.Description())
		}
		if !reflect.DeepEqual(spec.InputSchema, builtin.InputSchema()) {
			t.Fatalf("spec input schema = %#v, want %#v", spec.InputSchema, builtin.InputSchema())
		}
	}
}

func TestToolSpecsNil(t *testing.T) {
	t.Parallel()

	if specs := ToolSpecs(nil); specs != nil {
		t.Fatalf("expected nil specs, got %#v", specs)
	}
}
