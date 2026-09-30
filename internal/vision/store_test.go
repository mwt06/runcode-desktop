package vision

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func observation(q, ref string) imageinput.Record {
	r := imageinput.Record{QuestionID: q, RouteKey: "private-route-hash", Refs: []string{ref}, Question: "describe", Answer: imageinput.Answer{Text: "chart 42", Thinking: "uncertain scale", Model: "vision"}}
	r.Key = imageinput.CacheKey(r.RouteKey, r.Refs, r.Question)
	return r
}
func TestStorePersistenceCancellationCorruptionAndFork(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir()
	src := llm.ImageSource{Data: []byte("original"), MediaType: "image/png"}
	ref := imageinput.Ref("source", src)
	s := NewStore(ws, "source")
	r := observation("q1", ref)
	if err := s.Put(ctx, r); err != nil {
		t.Fatal(err)
	}
	r2 := r
	r2.Question = "future task"
	r2.QuestionID = "q2"
	r2.Key = imageinput.CacheKey(r2.RouteKey, r2.Refs, r2.Question)
	if err := s.Put(ctx, r2); err != nil {
		t.Fatal(err)
	}
	prefix := []llm.Message{{ID: "q1", Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeImage, Source: &src}}}}
	if err := Fork(ctx, ws, "source", "target", prefix); err != nil {
		t.Fatal(err)
	}
	copied, err := NewStore(ws, "target").List(ctx)
	if err != nil || len(copied) != 1 || copied[0].Refs[0] != imageinput.Ref("target", src) {
		t.Fatalf("fork=%+v err=%v", copied, err)
	}
	b, _ := json.Marshal(copied)
	if strings.Contains(string(b), "original") || strings.Contains(string(b), "future task") {
		t.Fatal("leaked raw image or future task")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Put(cancelled, r); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	if err := os.WriteFile(filepath.Join(ws, s.name), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("corrupt accepted")
	}
	if err := s.Put(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatal("delete failed")
	}
}
func TestStoreRejectsEscapingLinks(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, ".runcode")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := NewStore(ws, "session").Put(context.Background(), observation("q", "image_x")); err == nil {
		t.Fatal("wrote outside workspace")
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("outside mutated")
	}
}
