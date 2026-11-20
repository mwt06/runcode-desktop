package desktop

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	appwire "github.com/wt68/runcode/internal/protocol"
)

// 与前端 image-edit-ops.ts 的 IMAGE_EDIT_MAX_* 对齐，防止两边漂移。
func TestImageEditLimitsMatchFrontendContract(t *testing.T) {
	if appwire.ImageEditMaxEdge != 4096 || appwire.ImageEditMaxBytes != 16<<20 {
		t.Fatalf("limits = %d %d", appwire.ImageEditMaxEdge, appwire.ImageEditMaxBytes)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeGenImage(t *testing.T, ws, genID string, data []byte) string {
	t.Helper()
	dir := filepath.Join(ws, imageGenerationDir, genID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := path.Join(imageGenerationDir, genID, "image.png")
	if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(rel)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestParseImageGenerationEditSource(t *testing.T) {
	gen, file, ok := parseImageGenerationEditSource(`生成图片/gen_1/image.png`)
	if !ok || gen != "gen_1" || file != "image.png" {
		t.Fatalf("ok path = %q %q %v", gen, file, ok)
	}
	gen, file, ok = parseImageGenerationEditSource(`生成图片/gen_1/edited-20260102.png`)
	if !ok || gen != "gen_1" || file != "edited-20260102.png" {
		t.Fatalf("edited path = %q %q %v", gen, file, ok)
	}
	for _, bad := range []string{
		`../生成图片/gen_1/image.png`,
		`生成图片/../etc/passwd`,
		`生成图片/bad id/image.png`,
		`other/gen_1/image.png`,
		`生成图片/gen_1/sub/image.png`,
		`C:/ws/生成图片/gen_1/image.png`,
	} {
		if _, _, ok := parseImageGenerationEditSource(bad); ok {
			t.Fatalf("accepted bad path %q", bad)
		}
	}
}

func TestSaveImageGenerationEditRejectsAndSucceeds(t *testing.T) {
	ws := t.TempDir()
	src := writeGenImage(t, ws, "gen_edit", pngBytes(t, 32, 32))
	orig, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(src)))
	if err != nil {
		t.Fatal(err)
	}

	okPNG := base64.StdEncoding.EncodeToString(pngBytes(t, 64, 64))

	// 非 PNG
	if _, err := saveImageGenerationEditAt(ws, src, base64.StdEncoding.EncodeToString([]byte("not-png"))); err == nil {
		t.Fatal("non-png accepted")
	}
	// 超像素边长
	if _, err := saveImageGenerationEditAt(ws, src, base64.StdEncoding.EncodeToString(pngBytes(t, appwire.ImageEditMaxEdge+1, 8))); err == nil {
		t.Fatal("over-edge accepted")
	}
	// 超字节：构造超过上限的声明（用假大 base64 前缀触发字节检查）
	huge := strings.Repeat("A", int(appwire.ImageEditMaxBytes)+100)
	if _, err := saveImageGenerationEditAt(ws, src, huge); err == nil {
		t.Fatal("over-bytes accepted")
	}
	// 越出 generation 目录
	if _, err := saveImageGenerationEditAt(ws, "生成图片/gen_edit/../gen_other/image.png", okPNG); err == nil {
		t.Fatal("path escape accepted")
	}
	// 源不存在
	if _, err := saveImageGenerationEditAt(ws, "生成图片/gen_edit/missing.png", okPNG); err == nil {
		t.Fatal("missing source accepted")
	}
	// 成功：原图不变，新文件在同目录
	art, err := saveImageGenerationEditAt(ws, src, okPNG)
	if err != nil {
		t.Fatal(err)
	}
	if art.MIMEType != "image/png" || art.Width != 64 || art.Height != 64 || art.Bytes <= 0 {
		t.Fatalf("artifact = %+v", art)
	}
	if !strings.HasPrefix(art.Path, "生成图片/gen_edit/edited-") || !strings.HasSuffix(art.Path, ".png") {
		t.Fatalf("path = %q", art.Path)
	}
	gotOrig, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(src)))
	if !bytes.Equal(gotOrig, orig) {
		t.Fatal("source bytes changed")
	}
	if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(art.Path))); err != nil {
		t.Fatal(err)
	}
	// 允许以 edited 为再编辑源
	again, err := saveImageGenerationEditAt(ws, art.Path, okPNG)
	if err != nil || !strings.Contains(again.Path, "edited-") {
		t.Fatalf("re-edit = %+v %v", again, err)
	}
	base := path.Base(art.Path)
	if !strings.HasPrefix(base, "edited-") || !strings.HasSuffix(base, ".png") || len(base) < len("edited-20060102-150405000.png") {
		t.Fatalf("unexpected edited name %q", base)
	}
}
