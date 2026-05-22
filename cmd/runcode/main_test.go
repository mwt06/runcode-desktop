package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmd := versionCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	text := out.String()
	for _, want := range []string{"runcode 0.1.0-alpha", "commit:", "built:", "go:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("version output missing %q: %q", want, text)
		}
	}
}

func TestChatCommandIsPlaceholder(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmd := chatCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute chat: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "奔跑的代码") {
		t.Fatalf("chat output missing banner: %q", text)
	}
	if !strings.Contains(text, "chat is not implemented yet") {
		t.Fatalf("chat output missing placeholder message: %q", text)
	}
}
