package desktop

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/protocol"
)

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		got, want string
		ok        bool
	}{
		{"3.12.11", "3.11", true},
		{"3.11.0", "3.11", true},
		// 麒麟 V10 的那一个：存在，但够不到技能要的 3.11。
		{"3.8.10", "3.11", false},
		{"3.9", "3.11", false},
		{"4.0", "3.11", true},
		// git 报的版本尾巴上还有非数字段，不该影响判断。
		{"2.50.0.windows.1", "2.0", true},
		{"1.9.5", "2.0", false},
		{"22.17.0", "18", true},
	}
	for _, c := range cases {
		if got := versionAtLeast(c.got, c.want); got != c.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", c.got, c.want, got, c.ok)
		}
	}
}

func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pack")
	bad := []string{
		"../evil",
		"a/../../evil",
		"/etc/passwd",
		".",
		"",
	}
	for _, name := range bad {
		if p, ok := safeJoin(root, name); ok {
			t.Errorf("safeJoin(%q) allowed %q, want rejected", name, p)
		}
	}
	good := map[string]string{
		"bin/python3":   filepath.Join(root, "bin", "python3"),
		"./lib/x.so":    filepath.Join(root, "lib", "x.so"),
		"a/b/../c/d.py": filepath.Join(root, "a", "c", "d.py"),
	}
	for name, want := range good {
		p, ok := safeJoin(root, name)
		if !ok || p != want {
			t.Errorf("safeJoin(%q) = %q,%v; want %q,true", name, p, ok, want)
		}
	}
}

// writeTestPack 造一个小 tar.gz：一个可执行文件、一个指向它的符号链接、一个越界条目。
func writeTestPack(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(h *tar.Header, body string) {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(&tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	write(&tar.Header{Name: "bin/python3.12", Typeflag: tar.TypeReg, Mode: 0o755, Size: 5}, "hello")
	write(&tar.Header{Name: "bin/python3", Typeflag: tar.TypeSymlink, Linkname: "python3.12"}, "")
	// 越界的两条：解压器必须跳过，而不是写到解压根外面去。
	write(&tar.Header{Name: "../escaped.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}, "bad")
	write(&tar.Header{Name: "bin/out", Typeflag: tar.TypeSymlink, Linkname: "../../../etc/passwd"}, "")
	for _, c := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := c(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUntargz(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "pack.tar.gz")
	writeTestPack(t, src)
	dest := filepath.Join(dir, "out")
	if err := untargz(src, dest, nil); err != nil {
		t.Fatalf("untargz: %v", err)
	}

	realFile := filepath.Join(dest, "bin", "python3.12")
	info, err := os.Stat(realFile)
	if err != nil {
		t.Fatalf("real file missing: %v", err)
	}
	// 可执行位必须还原：少了它，Linux 与 macOS 上 python3 就是一句
	// "Permission denied"，而打包机上完全看不出异常。
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("exec bit lost: mode = %v", info.Mode().Perm())
	}
	// 符号链接：类 Unix 上是链接，Windows 上退化成复制（见 writeSymlink），
	// 两种都必须读得出内容。
	if b, err := os.ReadFile(filepath.Join(dest, "bin", "python3")); err != nil || string(b) != "hello" {
		t.Errorf("symlink unusable: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); !os.IsNotExist(err) {
		t.Error("zip-slip entry escaped the extraction root")
	}
	if _, err := os.Lstat(filepath.Join(dest, "bin", "out")); err == nil {
		t.Error("symlink pointing outside the pack was created")
	}
}

func TestValidateRuntimePack(t *testing.T) {
	ok := protocol.RuntimeManifestPack{
		ID: protocol.RuntimePackPython, Version: "3.12.11",
		URL: "https://obs.example.com/x.tar.gz", SHA256: strings.Repeat("a", 64),
	}
	if err := validateRuntimePack(ok); err != nil {
		t.Fatalf("valid pack rejected: %v", err)
	}
	bad := map[string]protocol.RuntimeManifestPack{
		// sha256 缺失必须拒绝：包的直链可能是明文 http，没有校验就等于允许别人
		// 替换掉用户即将执行的解释器。
		"no sha":      {Version: "1", URL: "https://x/y", SHA256: ""},
		"short sha":   {Version: "1", URL: "https://x/y", SHA256: "abc"},
		"no version":  {URL: "https://x/y", SHA256: strings.Repeat("a", 64)},
		"bad url":     {Version: "1", URL: "not-a-url", SHA256: strings.Repeat("a", 64)},
		"path in ver": {Version: "../3.12", URL: "https://x/y", SHA256: strings.Repeat("a", 64)},
		"dot version": {Version: ".hidden", URL: "https://x/y", SHA256: strings.Repeat("a", 64)},
	}
	for name, p := range bad {
		if err := validateRuntimePack(p); err == nil {
			t.Errorf("%s: accepted, want rejected", name)
		}
	}
}

func TestFindInstalledNeedsMarker(t *testing.T) {
	root := t.TempDir()
	m := &runtimeManager{root: root, packs: map[string]*packState{}}
	id := protocol.RuntimePackPython
	mk := func(version string, marked bool) {
		dir := filepath.Join(root, id, version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if marked {
			if err := os.WriteFile(filepath.Join(dir, packMarker), []byte("sum"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 解压到一半留下的残目录没有标记，不能被当成装好了。
	mk("3.12.11", false)
	if _, v := m.findInstalled(id); v != "" {
		t.Fatalf("unmarked dir treated as installed: %q", v)
	}
	mk("3.12.11", true)
	mk("3.11.0", true)
	if _, v := m.findInstalled(id); v != "3.12.11" {
		t.Fatalf("findInstalled = %q, want the newest marked version", v)
	}
}

// newTestManager 造一台不发事件、不落盘的管理器。
func newTestManager() *runtimeManager {
	m := &runtimeManager{packs: map[string]*packState{}}
	for _, s := range packSpecs {
		m.packs[s.id] = &packState{spec: s, stage: protocol.RuntimeStageAbsent}
	}
	return m
}

func TestBuildEnvManagedPack(t *testing.T) {
	m := newTestManager()
	dir := filepath.Join(t.TempDir(), "python", "3.12.11")
	st := m.packs[protocol.RuntimePackPython]
	st.stage, st.version, st.dir = protocol.RuntimeStageReady, "3.12.11", dir
	st.avail = protocol.RuntimeManifestPack{Version: "3.12.11", Notes: "python-docx / openpyxl"}

	e := m.buildEnv()
	if len(e.dirs) == 0 || !strings.HasPrefix(e.dirs[0], dir) {
		t.Fatalf("pack dirs not on PATH: %v", e.dirs)
	}
	// 提示词必须说出"自带、可用"，否则模型仍会叫用户自己去装 Python。
	if !strings.Contains(e.prompt, "3.12.11") || !strings.Contains(e.prompt, "bundled") {
		t.Errorf("prompt does not announce the bundled runtime:\n%s", e.prompt)
	}
	if !strings.Contains(e.prompt, "python-docx") {
		t.Errorf("prompt omits the preinstalled libraries:\n%s", e.prompt)
	}
	env := m.toolEnv()
	// PATH 必须是整条：ToolEnv 是覆盖语义，只给前缀的话子进程连系统命令都找不到。
	if !strings.HasPrefix(env["PATH"], e.dirs[0]) || !strings.Contains(env["PATH"], basePath()) {
		t.Errorf("toolEnv PATH is not prefix+inherited: %q", env["PATH"])
	}
}

func TestBuildEnvTooOldSystemPython(t *testing.T) {
	m := newTestManager()
	st := m.packs[protocol.RuntimePackPython]
	// 麒麟 V10 的处境：系统里有 Python，但够不到技能要的版本。
	st.sysPath, st.sysVersion, st.sysUsable = "/usr/bin/python3", "3.8.10", false

	e := m.buildEnv()
	if len(e.dirs) != 0 {
		t.Errorf("nothing is installed, yet PATH was modified: %v", e.dirs)
	}
	for _, want := range []string{"NOT available", "3.8.10", "too old", "3.11"} {
		if !strings.Contains(e.prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, e.prompt)
		}
	}
	// 绝不能让模型叫用户自己去装 Python：在麒麟上照做会把系统 python3 换掉，
	// 连带 dnf/apt 一起坏。这条危害是 Python 独有的，只该出现在 Python 那一行。
	if !strings.Contains(e.prompt, "do not tell the user to download and install it themselves") {
		t.Errorf("prompt does not discourage the self-install advice:\n%s", e.prompt)
	}
	if !strings.Contains(e.prompt, "breaks the OS package manager") {
		t.Errorf("prompt omits why self-installing Python is dangerous:\n%s", e.prompt)
	}
}

func TestSnapshotSeparatesSystemFromManaged(t *testing.T) {
	m := newTestManager()
	st := m.packs[protocol.RuntimePackPython]
	st.sysPath, st.sysVersion, st.sysUsable = "/usr/bin/python3", "3.8.10", false
	st.avail = protocol.RuntimeManifestPack{Version: "3.12.11", Size: 1 << 20}

	info := m.snapshot()
	var p protocol.RuntimePack
	for _, row := range info.Packs {
		if row.ID == protocol.RuntimePackPython {
			p = row
		}
	}
	// "系统里有 3.8" 与 "托管包没装" 同时成立，两者必须分开表达——揉进一个枚举
	// 就只能二选一地撒谎，而用户会看着一个绿勾却跑不起技能。
	if p.Stage != protocol.RuntimeStageAbsent {
		t.Errorf("stage = %q, want absent", p.Stage)
	}
	if p.SystemVersion != "3.8.10" || p.SystemUsable {
		t.Errorf("system fields wrong: %+v", p)
	}
	if p.Active != "" {
		t.Errorf("Active = %q, want empty (nothing usable)", p.Active)
	}
	if p.Available != "3.12.11" {
		t.Errorf("Available = %q, want the manifest version", p.Available)
	}
}

func TestPackPublished(t *testing.T) {
	// Git 只在 Windows 发包：另外两个平台没有官方绿色包，用系统的。
	if packPublished(protocol.RuntimePackGit, "linux") || packPublished(protocol.RuntimePackGit, "darwin") {
		t.Error("git should not be published for non-Windows platforms")
	}
	if !packPublished(protocol.RuntimePackGit, "windows") {
		t.Error("git should be published for Windows")
	}
	for _, id := range []string{protocol.RuntimePackPython, protocol.RuntimePackNode} {
		for _, goos := range []string{"windows", "linux", "darwin"} {
			if !packPublished(id, goos) {
				t.Errorf("%s should be published for %s", id, goos)
			}
		}
	}
}

func TestIsStoreStub(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 专属：应用商店占位 exe")
	}
	// 装了 Windows 但没装 Python 的机器上，这个路径下有个一运行就弹商店的壳。
	if !isStoreStub(`C:\Users\x\AppData\Local\Microsoft\WindowsApps\python.exe`) {
		t.Error("store stub not detected")
	}
	if isStoreStub(`C:\Python312\python.exe`) {
		t.Error("real interpreter misdetected as a store stub")
	}
}

func TestRuntimeManifestURLRoutes(t *testing.T) {
	plat := runtimePlatform()
	dashed := strings.ReplaceAll(plat, "/", "-")

	t.Run("bridge 默认带查询参数", func(t *testing.T) {
		t.Setenv("RUNCODE_RUNTIME_BASE_URL", "https://bridge.example.com/")
		t.Setenv("RUNCODE_RUNTIME_PATH", "")
		got := runtimeManifestURL()
		if !strings.HasPrefix(got, "https://bridge.example.com/api/app/runtimes?") {
			t.Fatalf("url = %q", got)
		}
		// product 与 platform 少一个都是发错包，所以两个都得在。
		for _, want := range []string{"product=", "platform="} {
			if !strings.Contains(got, want) {
				t.Errorf("url %q missing %q", got, want)
			}
		}
	})

	t.Run("静态清单替换占位符且不带查询参数", func(t *testing.T) {
		t.Setenv("RUNCODE_RUNTIME_BASE_URL", "https://obs.example.com/zhikai/runtimepacks")
		t.Setenv("RUNCODE_RUNTIME_PATH", "/runtimes-{platform}.json")
		got := runtimeManifestURL()
		want := "https://obs.example.com/zhikai/runtimepacks/runtimes-" + dashed + ".json"
		if got != want {
			t.Fatalf("url = %q, want %q", got, want)
		}
		// 查询串要是跟上去，某些对象存储会拒签或另起缓存键。
		if strings.Contains(got, "?") {
			t.Errorf("static manifest url carries a query string: %q", got)
		}
	})

	t.Run("编译期注入在没有环境变量时生效", func(t *testing.T) {
		t.Setenv("RUNCODE_RUNTIME_BASE_URL", "")
		t.Setenv("RUNCODE_RUNTIME_PATH", "")
		prev := runtimeManifestDefault
		runtimeManifestDefault = "https://obs.example.com/p/runtimes-{platform}.json"
		t.Cleanup(func() { runtimeManifestDefault = prev })
		if got, want := runtimeManifestURL(), "https://obs.example.com/p/runtimes-"+dashed+".json"; got != want {
			t.Fatalf("url = %q, want %q", got, want)
		}
	})

	t.Run("环境变量压过编译期注入", func(t *testing.T) {
		prev := runtimeManifestDefault
		runtimeManifestDefault = "https://obs.example.com/p/runtimes-{platform}.json"
		t.Cleanup(func() { runtimeManifestDefault = prev })
		t.Setenv("RUNCODE_RUNTIME_BASE_URL", "https://dev.example.com")
		t.Setenv("RUNCODE_RUNTIME_PATH", "")
		// 开发机上要能把地址临时指走，否则调试只能靠改代码重编。
		if got := runtimeManifestURL(); !strings.HasPrefix(got, "https://dev.example.com/api/app/runtimes?") {
			t.Fatalf("env override ignored: %q", got)
		}
	})
}

func TestLibsFromNotes(t *testing.T) {
	cases := []struct{ notes, want string }{
		// 打包工具当前产出的形状：整句里前半截与我们自己已经打印的版本号重复。
		{"Python 3.12.11，含 python-docx / python-pptx / openpyxl / Pillow", "python-docx / python-pptx / openpyxl / Pillow"},
		{"Python 3.12.11（未预装第三方库）", "（未预装第三方库）"},
		// 服务端换了措辞就原样用，不做花哨解析。
		{"含 lxml", "lxml"},
		{"anything else", "anything else"},
		{"", ""},
	}
	for _, c := range cases {
		if got := libsFromNotes(c.notes, "Python", "3.12.11"); got != c.want {
			t.Errorf("libsFromNotes(%q) = %q, want %q", c.notes, got, c.want)
		}
	}
}

func TestMissingLineMatchesWhatTheAppCanActuallyDo(t *testing.T) {
	m := newTestManager()
	prompt := m.buildEnv().prompt

	// 本平台发了包却没装：唯一正确的出口是设置页。
	if !strings.Contains(prompt, "设置 → 运行时环境") {
		t.Errorf("prompt should point at the in-app installer for published packs: %q", prompt)
	}

	var gitLine string
	for _, l := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(l, "- Git") {
			gitLine = l
		}
	}
	if gitLine == "" {
		t.Fatalf("no Git line in prompt: %q", prompt)
	}
	if packPublished(protocol.RuntimePackGit, runtime.GOOS) {
		return // Windows 发 Git 包，指向设置页是对的
	}
	// Linux/macOS **不发** Git 包。这时候再把用户指到设置页就是把人带进死胡同：
	// 那里只会显示"本平台暂未提供安装包"，点什么都没有。
	if strings.Contains(gitLine, "设置 → 运行时环境") {
		t.Errorf("Git is not published on %s, yet the prompt sends the user to the in-app installer: %q", runtime.GOOS, gitLine)
	}
	if !strings.Contains(gitLine, "system package manager") {
		t.Errorf("Git line should point at the system package manager instead: %q", gitLine)
	}
	// Python 专属的那条危害不该套到 Git 头上——驴唇不对马嘴的理由会让整段可信度下降。
	if strings.Contains(gitLine, "system Python") {
		t.Errorf("Git line carries Python-specific reasoning: %q", gitLine)
	}
}
