package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePNG(t *testing.T) []byte {
	t.Helper()
	var fx struct {
		Image struct {
			Base64 string `json:"base64"`
		} `json:"image"`
	}
	readImageGenerationFixture(t, "success-url.json", &fx)
	data, err := base64.StdEncoding.DecodeString(fx.Image.Base64)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// pngHeaderOnly：PNG 签名 + 声明 w×h 的合法 IHDR。
func pngHeaderOnly(w, h uint32) []byte {
	var ihdr bytes.Buffer
	_ = binary.Write(&ihdr, binary.BigEndian, w)
	_ = binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 6, 0, 0, 0})
	var out bytes.Buffer
	out.Write([]byte("\x89PNG\r\n\x1a\n"))
	_ = binary.Write(&out, binary.BigEndian, uint32(ihdr.Len()))
	chunk := append([]byte("IHDR"), ihdr.Bytes()...)
	out.Write(chunk)
	_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return out.Bytes()
}

// webpLossless：w×h 的最小 VP8L 头（不必解码像素）。
func webpLossless(w, h int) []byte {
	bits := uint32(w-1) | uint32(h-1)<<14
	payload := append([]byte{0x2f}, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	var chunk bytes.Buffer
	chunk.WriteString("VP8L")
	_ = binary.Write(&chunk, binary.LittleEndian, uint32(len(payload)))
	chunk.Write(payload)
	if len(payload)%2 == 1 {
		chunk.WriteByte(0)
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(4+chunk.Len()))
	out.WriteString("WEBP")
	out.Write(chunk.Bytes())
	return out.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func b64Item(data []byte) imageGenerationWireImage {
	return imageGenerationWireImage{B64JSON: base64.StdEncoding.EncodeToString(data)}
}

// allowLoopbackDownloads：让测试服 127.0.0.1 冒充公开图床。
func allowLoopbackDownloads(t *testing.T) {
	t.Helper()
	restore := imageDownloadAddrAllowed
	imageDownloadAddrAllowed = func(net.IP) bool { return true }
	t.Cleanup(func() { imageDownloadAddrAllowed = restore })
}

func TestImageGenerationArtifactSavesVerifiedFormats(t *testing.T) {
	cases := []struct {
		name          string
		data          []byte
		ext, mime     string
		width, height int
	}{
		{"png", fixturePNG(t), "png", "image/png", 1, 1},
		{"jpeg", jpegBytes(t), "jpg", "image/jpeg", 3, 2},
		{"webp", webpLossless(5, 4), "webp", "image/webp", 5, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			art, err := saveGeneratedImage(context.Background(), ws, "gen_"+tc.name, b64Item(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			wantPath := "生成图片/gen_" + tc.name + "/image_" + tc.name + "." + tc.ext
			if art.Path != wantPath || art.MIMEType != tc.mime || art.Width != tc.width || art.Height != tc.height || art.Bytes != int64(len(tc.data)) {
				t.Fatalf("artifact = %+v", art)
			}
			got, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(wantPath)))
			if err != nil || !bytes.Equal(got, tc.data) {
				t.Fatalf("saved bytes differ: %v", err)
			}
		})
	}
}

func TestImageGenerationArtifactSaveIgnoresCancelledTurnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ws := t.TempDir()
	if _, err := saveGeneratedImage(ctx, ws, "gen_cancelled_b64", b64Item(fixturePNG(t))); err != nil {
		t.Fatalf("cancelled turn lost already-received b64 image: %v", err)
	}

	allowLoopbackDownloads(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(fixturePNG(t))
	}))
	defer srv.Close()
	if _, err := saveGeneratedImage(ctx, ws, "gen_cancelled_url", imageGenerationWireImage{URL: srv.URL + "/image.png"}); err != nil {
		t.Fatalf("cancelled turn lost already-received url image: %v", err)
	}
}

func TestGeneratedImageFileName(t *testing.T) {
	if got := generatedImageFileName("gen_a4c50251ddeb890ef460e44f", "jpg"); got != "image_a4c50251ddeb890ef460e44f.jpg" {
		t.Fatalf("got %q", got)
	}
	if got := generatedImageFileName("customid", "png"); got != "image_customid.png" {
		t.Fatalf("got %q", got)
	}
}

func TestImageGenerationArtifactDownloadsWithoutCredentials(t *testing.T) {
	allowLoopbackDownloads(t)
	png := fixturePNG(t)
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()
	ws := t.TempDir()
	art, err := saveGeneratedImage(context.Background(), ws, "gen_url", imageGenerationWireImage{URL: srv.URL + "/signed.png?sig=secret"})
	if err != nil {
		t.Fatal(err)
	}
	if art.Path != "生成图片/gen_url/image_url.png" {
		t.Fatalf("artifact = %+v", art)
	}
	for _, h := range []string{"Authorization", "X-Internal-Service-Key", "Cookie"} {
		if headers.Get(h) != "" {
			t.Fatalf("download sent %s", h)
		}
	}
}

func TestImageGenerationArtifactRejectsUnsafeInput(t *testing.T) {
	restoreLimit := imageGenerationMaxImageBytes
	imageGenerationMaxImageBytes = 4096
	defer func() { imageGenerationMaxImageBytes = restoreLimit }()
	big := append(fixturePNG(t), bytes.Repeat([]byte{0}, 5000)...)
	cases := []struct {
		name string
		gen  string
		item imageGenerationWireImage
	}{
		{"html disguised", "gen_a", b64Item([]byte("<html><body>not an image</body></html>"))},
		{"gif not accepted", "gen_b", b64Item([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))},
		{"too many bytes", "gen_c", b64Item(big)},
		{"too many pixels", "gen_d", b64Item(pngHeaderOnly(5000, 5000))},
		{"bad base64", "gen_e", imageGenerationWireImage{B64JSON: "!!!not-base64"}},
		{"no result", "gen_f", imageGenerationWireImage{}},
		{"path traversal id", "../escape", b64Item(fixturePNG(t))},
		{"file scheme", "gen_g", imageGenerationWireImage{URL: "file:///C:/Windows/win.ini"}},
		{"loopback host", "gen_h", imageGenerationWireImage{URL: "http://127.0.0.1:1/x.png"}},
		{"private host", "gen_i", imageGenerationWireImage{URL: "http://10.0.0.5/x.png"}},
		{"link local metadata", "gen_j", imageGenerationWireImage{URL: "http://169.254.169.254/latest"}},
		{"userinfo url", "gen_k", imageGenerationWireImage{URL: "https://user:pass@images.example.invalid/x.png"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			if _, err := saveGeneratedImage(context.Background(), ws, tc.gen, tc.item); err == nil {
				t.Fatal("unsafe input saved")
			}
			entries, _ := os.ReadDir(filepath.Join(ws, "生成图片"))
			for _, e := range entries {
				inner, _ := os.ReadDir(filepath.Join(ws, "生成图片", e.Name()))
				if len(inner) != 0 {
					t.Fatalf("rejected input left %v", inner)
				}
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escape")); err == nil {
				t.Fatal("wrote outside the workspace")
			}
		})
	}
}

func TestImageGenerationArtifactRedirectAndOverwrite(t *testing.T) {
	allowLoopbackDownloads(t)
	png := fixturePNG(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(png) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/x.png", http.StatusFound)
	}))
	defer redirect.Close()
	ws := t.TempDir()
	if _, err := saveGeneratedImage(context.Background(), ws, "gen_r", imageGenerationWireImage{URL: redirect.URL}); err == nil {
		t.Fatal("redirect followed")
	}
	if _, err := saveGeneratedImage(context.Background(), ws, "gen_once", b64Item(png)); err != nil {
		t.Fatal(err)
	}
	if _, err := saveGeneratedImage(context.Background(), ws, "gen_once", b64Item(jpegBytes(t))); err == nil {
		t.Fatal("existing generation directory reused")
	}
	got, _ := os.ReadFile(filepath.Join(ws, "生成图片", "gen_once", "image_once.png"))
	if !bytes.Equal(got, png) {
		t.Fatal("existing image overwritten")
	}
}

func TestImageGenerationArtifactDiskFailure(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "生成图片"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveGeneratedImage(context.Background(), ws, "gen_disk", b64Item(fixturePNG(t))); err == nil || strings.Contains(err.Error(), "成功") {
		t.Fatalf("disk failure reported success: %v", err)
	}
}
