package desktop

// 安装链路的端到端：假清单 + 假对象存储 → 下载 → 校验 sha256 → 解压 → 就位 →
// PATH 与提示词生效。
//
// 单测那几个函数各自是对的，不等于**串起来**是对的：这条链路上真正会出事的地方
// 全在接缝处——清单的字段名对不上（服务端 camelCase、客户端 snake_case 这种）、
// 校验不过却仍然装上了、装好了但 PATH 没更新。所以这一份走真文件、真 HTTP。

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// runtimeFixture 是一套假平台：一个发清单与包的 HTTP 服务、一个临时安装根。
type runtimeFixture struct {
	app  *App
	dir  string
	pack protocol.RuntimeManifestPack
	// body 是包的字节，assetBody 之后算出 sha256/size 填进清单。
	body []byte
	// hits 记下清单被请求时带的查询参数，用来盯住 product/platform。
	query chan string
	// platform 是假服务端在清单里自报的平台；默认与本机一致，测"配错平台"时改它。
	platform string
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	f := &runtimeFixture{dir: t.TempDir(), query: make(chan string, 8), platform: runtimePlatform()}
	f.body = fakePackBytes(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/runtimes":
			select {
			case f.query <- r.URL.RawQuery:
			default:
			}
			_ = json.NewEncoder(w).Encode(protocol.RuntimeManifest{
				Platform: f.platform,
				Packs:    []protocol.RuntimeManifestPack{f.pack},
			})
		case strings.HasPrefix(r.URL.Path, "/obs/"):
			_, _ = w.Write(f.body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("RUNCODE_RUNTIME_BASE_URL", srv.URL)
	t.Setenv("RUNCODE_RUNTIME_PATH", "/runtimes")
	prevRoot := runtimeRoot
	runtimeRoot = func() (string, error) { return f.dir, nil }
	t.Cleanup(func() { runtimeRoot = prevRoot })

	sum := sha256.Sum256(f.body)
	f.pack = protocol.RuntimeManifestPack{
		ID:      protocol.RuntimePackPython,
		Version: "3.12.11",
		URL:     srv.URL + "/obs/python-3.12.11.tar.gz",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(f.body)),
		Notes:   "含 python-docx / openpyxl",
	}
	f.app = New(&recordingSink{})
	return f
}

// fakePackBytes 造一个最小的运行时包：一个"解释器"和一个 site-packages 文件。
func fakePackBytes(t *testing.T) []byte {
	t.Helper()
	var buf strings.Builder
	gz := gzip.NewWriter(&stringWriter{&buf})
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"bin/python3.12":                         "#!/bin/sh\necho 3.12.11\n",
		"lib/python3.12/site-packages/docx/x.py": "# python-docx\n",
		"python.exe":                             "MZ",
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(buf.String())
}

// stringWriter 让 strings.Builder 能当 io.Writer 用在 gzip 上（Builder 本来就是，
// 包一层只是为了避免取地址的写法把测试读乱）。
type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func TestRuntimeInstallEndToEnd(t *testing.T) {
	f := newRuntimeFixture(t)

	info, err := f.app.CheckRuntimes()
	if err != nil {
		t.Fatalf("CheckRuntimes: %v", err)
	}
	// product 与 platform 少一个都是发错包：把 linux/arm64 的 Python 发给 Windows
	// 用户，表现是下完了装上了、然后一句 %1 不是有效的 Win32 应用程序。
	q := <-f.query
	for _, want := range []string{"product=", "platform=" + strings.ReplaceAll(runtimePlatform(), "/", "%2F")} {
		if !strings.Contains(q, want) {
			t.Errorf("manifest query %q missing %q", q, want)
		}
	}
	if got := pythonPack(info).Available; got != "3.12.11" {
		t.Fatalf("available = %q, want 3.12.11", got)
	}

	if err := f.app.InstallRuntime(protocol.RuntimePackPython); err != nil {
		t.Fatalf("InstallRuntime: %v", err)
	}

	dest := filepath.Join(f.dir, protocol.RuntimePackPython, "3.12.11")
	for _, rel := range []string{"bin/python3.12", "lib/python3.12/site-packages/docx/x.py", packMarker} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s after install: %v", rel, err)
		}
	}

	p := pythonPack(f.app.RuntimeStatus())
	if p.Stage != protocol.RuntimeStageReady || p.Version != "3.12.11" || p.Active != "managed" {
		t.Fatalf("after install: %+v", p)
	}

	// 装完就该生效，不该要求用户新建一个对话：PATH 与提示词都立刻更新。
	env := f.app.rt.toolEnv()
	if !strings.Contains(env["PATH"], dest) {
		t.Errorf("pack dir not on the tool PATH: %q", env["PATH"])
	}
	if prompt := f.app.rt.promptAppend(); !strings.Contains(prompt, "3.12.11") || !strings.Contains(prompt, "python-docx") {
		t.Errorf("prompt does not describe the installed runtime:\n%s", prompt)
	}
	// 而且真的接进了会话配置。
	cfg := f.app.configForWorkspace(t.TempDir())
	applyRuntimeEnv(f.app.rt, &cfg)
	if !strings.Contains(cfg.ToolEnv["PATH"], dest) {
		t.Errorf("engine.Config.ToolEnv missing the pack dir: %q", cfg.ToolEnv["PATH"])
	}
	if !strings.Contains(cfg.SystemPromptAppend, "3.12.11") {
		t.Error("engine.Config.SystemPromptAppend does not mention the runtime")
	}
}

func TestRuntimeInstallRejectsBadChecksum(t *testing.T) {
	f := newRuntimeFixture(t)
	if _, err := f.app.CheckRuntimes(); err != nil {
		t.Fatalf("CheckRuntimes: %v", err)
	}
	// 服务端说的和实际下到的对不上——中间有人把包换了，或者代理把它截断了。
	f.app.rt.mu.Lock()
	f.app.rt.packs[protocol.RuntimePackPython].avail.SHA256 = strings.Repeat("b", 64)
	f.app.rt.mu.Unlock()

	if err := f.app.InstallRuntime(protocol.RuntimePackPython); err == nil {
		t.Fatal("install accepted a pack whose checksum did not match")
	}
	p := pythonPack(f.app.RuntimeStatus())
	if p.Stage != protocol.RuntimeStageFailed {
		t.Errorf("stage = %q, want failed", p.Stage)
	}
	// 关键：一个字节都不该留下。半装的运行时比没装糟得多——它会让探测说"有"，
	// 然后每条命令都莫名其妙地失败。
	if _, err := os.Stat(filepath.Join(f.dir, protocol.RuntimePackPython, "3.12.11")); !os.IsNotExist(err) {
		t.Error("a pack that failed verification was left on disk")
	}
	if dirs := f.app.rt.envSnapshot().dirs; len(dirs) != 0 {
		t.Errorf("failed install still changed PATH: %v", dirs)
	}
}

func TestRuntimeManifestMissingChecksumRefused(t *testing.T) {
	f := newRuntimeFixture(t)
	f.pack.SHA256 = ""
	if _, err := f.app.CheckRuntimes(); err != nil {
		t.Fatalf("CheckRuntimes: %v", err)
	}
	// 没有校验和就没有"这是发布方那个包"的任何依据，而包里装的是即将被执行的
	// 解释器。宁可装不上。
	if err := f.app.InstallRuntime(protocol.RuntimePackPython); err == nil {
		t.Fatal("install accepted a manifest entry with no sha256")
	}
}

// pythonPack 从快照里挑出 Python 那一行——这套用例盯的都是它（三个包走的是同一条
// 代码路径，重复验另外两个只是把用例变长）。
func pythonPack(info protocol.RuntimeInfo) protocol.RuntimePack {
	for _, p := range info.Packs {
		if p.ID == protocol.RuntimePackPython {
			return p
		}
	}
	return protocol.RuntimePack{}
}

func TestRuntimeManifestPlatformMismatchRefused(t *testing.T) {
	f := newRuntimeFixture(t)
	// 发布方把别的平台的清单配到了这个平台上（静态清单那条路尤其容易——文件名是
	// 人传上去的）。sha256 拦不住这种错：它只证明"这是发布方给的那个文件"。
	f.pack.URL = strings.Replace(f.pack.URL, "python-3.12.11", "python-3.12.11-wrongplat", 1)
	srvPlatform := "solaris/sparc"
	prev := f.platform
	f.platform = srvPlatform
	t.Cleanup(func() { f.platform = prev })

	_, err := f.app.CheckRuntimes()
	if err == nil {
		t.Fatal("manifest for another platform was accepted")
	}
	if !strings.Contains(err.Error(), srvPlatform) || !strings.Contains(err.Error(), runtimePlatform()) {
		t.Errorf("error should name both platforms, got: %v", err)
	}
	// 拦下之后不能留下任何"可安装"的假象。
	if p := pythonPack(f.app.RuntimeStatus()); p.Available != "" {
		t.Errorf("a refused manifest still offered %q", p.Available)
	}
}

// TestRuntimeManifestStaticRouteSendsNoToken 盯住：已登录的用户去取静态清单，**不能**带令牌。
//
// 麒麟真机上实际发生过：对象存储把 Authorization 头当成它自己的签名来解析，Bearer 令牌它
// 不认识，直接回 400——只要登录了通行证，运行时清单就永远取不到；令牌还被发给了第三方
// 存储、被回显在它的错误信息里。这里的假服务端照搬对象存储的这条行为。
func TestRuntimeManifestStaticRouteSendsNoToken(t *testing.T) {
	isolateConfigDir(t)
	auths := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths <- r.Header.Get("Authorization")
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<Error><Code>InvalidArgument</Code><Message>Unsupported Authorization Type</Message></Error>"))
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.RuntimeManifest{Platform: runtimePlatform()})
	}))
	defer srv.Close()

	app := New(&recordingSink{})
	app.tokens = newTokenManager("http://unused", "c", http.DefaultClient, nil)
	app.tokens.setInMemory(tokenSet{AccessToken: "secret-token", RefreshToken: "RT", Expiry: time.Now().Add(time.Hour)})

	// 静态清单：不带令牌，取得到。
	t.Setenv("RUNCODE_RUNTIME_BASE_URL", srv.URL)
	t.Setenv("RUNCODE_RUNTIME_PATH", "/runtimes-{platform}.json")
	if _, err := app.fetchRuntimeManifest(context.Background()); err != nil {
		t.Fatalf("static manifest failed for a logged-in user: %v", err)
	}
	if got := <-auths; got != "" {
		t.Errorf("static manifest request carried Authorization %q — the user's token leaked to object storage", got)
	}

	// Bridge：照常带令牌（那是我们自己的服务，令牌是它认人的凭据）。
	t.Setenv("RUNCODE_RUNTIME_PATH", "/api/app/runtimes")
	_, _ = app.fetchRuntimeManifest(context.Background())
	if got := <-auths; got != "Bearer secret-token" {
		t.Errorf("Bridge manifest request Authorization = %q, want the bearer token", got)
	}
}
