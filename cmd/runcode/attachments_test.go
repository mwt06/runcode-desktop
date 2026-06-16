package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseImageAttachments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), []byte{1, 2, 3, 4}, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	t.Run("loads and strips an image ref", func(t *testing.T) {
		text, images := parseImageAttachments("describe @shot.png please", dir)
		if len(images) != 1 || images[0].MediaType != "image/png" || len(images[0].Data) != 4 {
			t.Fatalf("images = %#v, want one png", images)
		}
		if text != "describe please" {
			t.Fatalf("text = %q, want the ref stripped", text)
		}
	})

	t.Run("non-image ref is left untouched", func(t *testing.T) {
		text, images := parseImageAttachments("see @notes.txt", dir)
		if len(images) != 0 || text != "see @notes.txt" {
			t.Fatalf("non-image ref changed: text=%q images=%v", text, images)
		}
	})

	t.Run("missing image ref is left untouched", func(t *testing.T) {
		text, images := parseImageAttachments("look @gone.png", dir)
		if len(images) != 0 || text != "look @gone.png" {
			t.Fatalf("missing ref changed: text=%q images=%v", text, images)
		}
	})

	t.Run("out-of-workspace ref is ignored", func(t *testing.T) {
		text, images := parseImageAttachments("peek @../escape.png", dir)
		if len(images) != 0 || text != "peek @../escape.png" {
			t.Fatalf("escape ref changed: text=%q images=%v", text, images)
		}
	})

	t.Run("plain text passes through", func(t *testing.T) {
		text, images := parseImageAttachments("no refs here", dir)
		if len(images) != 0 || text != "no refs here" {
			t.Fatalf("plain text changed: text=%q images=%v", text, images)
		}
	})
}
