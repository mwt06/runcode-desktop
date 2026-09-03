package desktop

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeZip 造一个内存里没有的 zip：键是条目名，值是内容。
func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.zip")
	f, err := os.Create(p) //nolint:gosec // 测试临时文件
	if err != nil {
		t.Fatalf("建 zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写条目 %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("写内容 %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关 zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关文件: %v", err)
	}
	return p
}

// TestUnzipSkillFlattensNestedRoot 盯住上游文档里那个「上传阶段验不出来、只在运行时
// 显形」的坑：包里 SKILL.md 套了一层同名目录。
//
// 照原样解开会变成 skills/<name>/<name>/SKILL.md，模型读不到——而安装、列表、界面
// 全都不报错。所以解压时认 SKILL.md 所在那一层为包根。
func TestUnzipSkillFlattensNestedRoot(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"cn-docx/SKILL.md":          "---\nname: cn-docx\n---\n正文",
		"cn-docx/references/tpl.md": "模板",
		"cn-docx/scripts/build.py":  "print(1)",
	})
	dir := t.TempDir()
	if err := unzipSkill(zipPath, 0, dir); err != nil {
		t.Fatalf("解压: %v", err)
	}
	if !hasSkillManifest(dir) {
		t.Fatal("SKILL.md 没有落在技能目录的根上——套了一层的包没被拍平")
	}
	if _, err := os.Stat(filepath.Join(dir, "references", "tpl.md")); err != nil {
		t.Fatalf("随附文件没跟着搬过来: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cn-docx")); err == nil {
		t.Fatal("多出了一层同名目录")
	}
}

// TestUnzipSkillAcceptsRootManifest 盯住另一种打包方式：SKILL.md 就在 zip 根上。
func TestUnzipSkillAcceptsRootManifest(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"SKILL.md":        "---\nname: x\n---\n正文",
		"references/a.md": "a",
	})
	dir := t.TempDir()
	if err := unzipSkill(zipPath, 0, dir); err != nil {
		t.Fatalf("解压: %v", err)
	}
	if !hasSkillManifest(dir) {
		t.Fatal("根目录的 SKILL.md 没解出来")
	}
}

// TestUnzipSkillRejectsPathTraversal 盯住 zip-slip：条目名里带 ..，解压能把文件写到
// 技能目录**外面**去。这些包是从网络来的，这道校验不能省。
func TestUnzipSkillRejectsPathTraversal(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"SKILL.md":      "---\nname: x\n---\n",
		"../escaped.md": "我不该出现在这里",
	})
	dir := t.TempDir()
	err := unzipSkill(zipPath, 0, dir)
	if err == nil {
		t.Fatal("带 .. 的条目应当被拒")
	}
	if !strings.Contains(err.Error(), "非法路径") {
		t.Fatalf("错误信息应指明非法路径，实际：%v", err)
	}
}

// TestUnzipSkillRejectsBundleWithoutManifest 盯住没有 SKILL.md 的包直接拒掉，
// 而不是解出一个不会被加载的空目录。
func TestUnzipSkillRejectsBundleWithoutManifest(t *testing.T) {
	zipPath := writeZip(t, map[string]string{"readme.txt": "hi"})
	if err := unzipSkill(zipPath, 0, t.TempDir()); err == nil {
		t.Fatal("没有 SKILL.md 的包应当被拒")
	}
}

// TestWritePromptSkillIsLoadable 盯住纯提示词型技能生成出来的 SKILL.md 能被本地
// 加载器认出来——name 必须来自 frontmatter，正文必须留下。
func TestWritePromptSkillIsLoadable(t *testing.T) {
	dir := t.TempDir()
	err := writePromptSkill(dir, marketSkillWire{
		Name:         "cn-docx",
		Description:  "生成\n中文公文", // 多行描述要压成一行，否则 frontmatter 会断
		Version:      "1.2.0",
		AllowedTools: []string{"Read", "Write"},
		SystemPrompt: "# 用法\n\n照这个来。",
	})
	if err != nil {
		t.Fatalf("生成 SKILL.md: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // 测试临时目录
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	body := string(data)
	if got := frontmatterName(body); got != "cn-docx" {
		t.Fatalf("frontmatter 的 name 是 %q，应为 cn-docx", got)
	}
	if strings.Contains(strings.SplitN(body, "---", 3)[1], "生成\n中文公文") {
		t.Fatal("描述没有压成一行，frontmatter 会被它断掉")
	}
	if !strings.Contains(body, "照这个来。") {
		t.Fatal("正文丢了")
	}
	if !strings.Contains(body, "allowed-tools: Read, Write") {
		t.Fatal("allowed-tools 没写出来")
	}
}

// TestSkillBundleRootPrefersZipRoot 盯住根目录的 SKILL.md 优先于子目录里的那个：
// 一个包里两处都有时，根才是包根。
func TestSkillBundleRootPrefersZipRoot(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"SKILL.md":        "root",
		"nested/SKILL.md": "nested",
	})
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 zip: %v", err)
	}
	defer func() { _ = zr.Close() }()
	base, ok := skillBundleRoot(zr.File)
	if !ok || base != "" {
		t.Fatalf("包根应是 zip 根，实际 %q ok=%v", base, ok)
	}
}

// TestMarketRequestCarriesBothHeaders 把「两个头到底发出去了没有」钉死在测试里。
//
// 这不是自明的，而且**服务端答案分不出来**：实测网关对「没带 X-Selected-Tenant-ID」
// 和「带了但租户不认」返回的是同一句 403 selected tenant is invalid。既然报错不能
// 区分，就只能在客户端这一侧直接看请求。顺带把路径前缀也验了（网关只放行 /api/user，
// 转发时又把它剥掉，两边都写一遍容易漏）。
func TestMarketRequestCarriesBothHeaders(t *testing.T) {
	var gotAuth, gotTenant, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotTenant = r.Header.Get("X-Selected-Tenant-ID")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"skills":[],"total":0}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_SKILL_MARKET_BASE_URL", srv.URL)

	app, _, _ := newParallelApp(t)
	if _, err := app.marketRequest("tok-123", "t1", "/skills?size=200", 5*time.Second); err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("Authorization 头是 %q", gotAuth)
	}
	if gotTenant != "t1" {
		t.Fatalf("X-Selected-Tenant-ID 头是 %q，应为 t1", gotTenant)
	}
	if gotPath != "/api/user/skills" {
		t.Fatalf("路径是 %q，应为 /api/user/skills", gotPath)
	}
	if gotQuery != "size=200" {
		t.Fatalf("查询串是 %q", gotQuery)
	}
}

// TestTokenHasScope 盯住授权范围的识别：两种 scope 形状都要认，解不开的一律放行。
//
// 放行是刻意的：这不是安全判断（验签在网关那边），只是想在发请求前认出"这份令牌
// 肯定不行"。看不懂就交给服务端说话，不能因为自己解不开令牌就把功能挡死。
func TestTokenHasScope(t *testing.T) {
	// payload: {"scope":["openid","manageapi"]}
	arr := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"scope":["openid","manageapi"]}`)) + ".y"
	// payload: {"scope":"openid passportapi"}
	str := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"scope":"openid passportapi"}`)) + ".y"
	none := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"a"}`)) + ".y"

	if !tokenHasScope(arr, "manageapi") {
		t.Fatal("数组形状的 scope 没认出来")
	}
	if tokenHasScope(arr, "nope") {
		t.Fatal("不存在的 scope 不该说有")
	}
	if !tokenHasScope(str, "passportapi") {
		t.Fatal("空格分隔的 scope 没认出来")
	}
	if tokenHasScope(str, "manageapi") {
		t.Fatal("空格分隔里没有的 scope 不该说有")
	}
	if !tokenHasScope(none, "manageapi") {
		t.Fatal("没有 scope 这一栏时应当放行，交给服务端判")
	}
	if !tokenHasScope("不是jwt", "manageapi") {
		t.Fatal("解不开的令牌应当放行")
	}
}

// writeSkill 在 dir 下造一个最小可加载的技能。
func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	body := "---\nname: " + name + "\ndescription: 测试用\n---\n正文\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("写 SKILL.md: %v", err)
	}
}

// TestMarketInstalledTracksBothScopes 盯住「已装」是**按作用域**分开算的。
//
// 装到哪由用户选，卸载也得删对地方，所以一个布尔说不清：用户级与项目级可以同时
// 装着同名技能。这里还压住一个只在磁盘上看得见的情形——被用户级遮住的项目级技能。
// 如果按 ListSkills（模型看到的视图）来判，它根本不在清单里，卡片会说"没装"，
// 于是那份确实躺在 .runcode/skills 下的目录连卸载入口都没有。
func TestMarketInstalledTracksBothScopes(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("APPDATA", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)

	app, _, _ := newParallelApp(t)
	ws := t.TempDir()
	app.mu.Lock()
	app.workspace = ws
	app.mu.Unlock()
	projectSkills := filepath.Join(ws, ".runcode", "skills")

	writeSkill(t, userResourceDir(kindSkills), "mine")
	writeSkill(t, projectSkills, "theirs")
	// both 两边都装着：用户级会在 ListSkills 里遮掉项目级那一份。
	writeSkill(t, userResourceDir(kindSkills), "both")
	writeSkill(t, projectSkills, "both")

	page := app.skillMarketPage([]marketSkillWire{
		{ID: 1, Name: "mine", Category: "甲"},
		{ID: 2, Name: "theirs", Category: "甲"},
		{ID: 3, Name: "both", Category: "甲"},
		{ID: 4, Name: "neither", Category: "乙"},
	}, time.Now())

	got := map[string][2]bool{}
	for _, s := range page.Skills {
		got[s.Name] = [2]bool{s.InstalledUser, s.InstalledProject}
	}
	want := map[string][2]bool{
		"mine":    {true, false},
		"theirs":  {false, true},
		"both":    {true, true},
		"neither": {false, false},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: 已装标记 = 用户 %v / 项目 %v，期望 用户 %v / 项目 %v",
				name, got[name][0], got[name][1], w[0], w[1])
		}
	}
	if len(page.Categories) != 2 {
		t.Errorf("分类应当去重成 2 个，实际 %v", page.Categories)
	}
}

// TestFetchToFileReportsProgress 盯住下载进度是**真的**：字节数来自响应体，
// 总长来自 Content-Length，而不是按定时器推出来的。
//
// 同时压住收尾那一下：节流会吞掉最后一小段，不补报的话进度条永远停在
// 97% 这种地方——看起来就像卡死了。
func TestFetchToFileReportsProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 300*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	f, err := os.CreateTemp(t.TempDir(), "bundle-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var got [][2]int64
	sum, n, err := fetchToFile(t.Context(), srv.URL, "技能包", f, skillBundleMaxBytes, func(received, total int64) {
		got = append(got, [2]int64{received, total})
	})
	if err != nil {
		t.Fatalf("fetchToFile: %v", err)
	}
	if n != int64(len(payload)) {
		t.Fatalf("字节数 = %d，期望 %d", n, len(payload))
	}
	want := sha256.Sum256(payload)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("sha256 对不上")
	}
	if len(got) == 0 {
		t.Fatal("一次进度都没报")
	}
	last := got[len(got)-1]
	if last[0] != int64(len(payload)) {
		t.Errorf("最后一次报的是 %d 字节，应当是全部 %d——节流吞掉了收尾", last[0], len(payload))
	}
	if last[1] != int64(len(payload)) {
		t.Errorf("总长 = %d，应当来自 Content-Length %d", last[1], len(payload))
	}
}

// TestFetchToFileUnknownLengthReportsZeroTotal 盯住分块传输（没有 Content-Length）时
// 总长归零。
//
// Go 在这种情况下给的 ContentLength 是 **-1**，原样传给前端会被拿去算百分比，
// 算出一个负数。统一用 0 表示"不知道"，前端据此画成不确定态。
func TestFetchToFileUnknownLengthReportsZeroTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		_, _ = w.Write(bytes.Repeat([]byte("y"), 4096))
	}))
	defer srv.Close()

	f, err := os.CreateTemp(t.TempDir(), "bundle-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var lastTotal int64 = -7
	if _, _, err := fetchToFile(t.Context(), srv.URL, "技能包", f, skillBundleMaxBytes, func(_, total int64) { lastTotal = total }); err != nil {
		t.Fatalf("fetchToFile: %v", err)
	}
	if lastTotal != 0 {
		t.Errorf("总长未知时应当报 0，实际 %d", lastTotal)
	}
}
