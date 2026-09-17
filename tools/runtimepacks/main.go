// Command runtimepacks 把 Python / Node.js / Git 的上游绿色包重打成本应用的
// 「运行时包」，连同一份可直接配给 Bridge 的清单。
//
// 解决的问题：用户机器上不一定有 Python，装了的也未必够新——银河麒麟 V10 是 3.8
// 且**升不得**（dnf 与一堆系统工具绑在它上面），macOS 是 Xcode 命令行工具那份
// 3.9/3.10，而办公技能要 3.11+。与其让用户自己去装，不如平台发一份绿色包，应用
// 下到用户目录里解开，只在工具子进程的 PATH 前面加上它（客户端那半边见
// internal/desktop/runtimes.go）。
//
// 用法（仓库根目录执行）：
//
//	go run ./tools/runtimepacks --list                      # 只打印要从哪儿下什么，不动手
//	go run ./tools/runtimepacks --base-url https://obs.example.com/runtimes/
//	go run ./tools/runtimepacks --component python --platform linux/amd64
//	go run ./tools/runtimepacks --cache ~/Downloads/upstream # 内网：上游包自己下好放这儿
//
// 产物落在 --out（默认 dist/runtimepacks/）：每个包一个 .tar.gz，外加两种清单——
// manifest.json（全平台汇总，给人看、给运维核对）与 runtimes-<os>-<arch>.json
// （每平台一份，内容**就是**接口该返回的那个对象，服务端照平台键透传即可）。
// 把 .tar.gz 传到对象存储，清单配给发布服务。
//
// # 三件容易忽略的事
//
//  1. **可以在任意一个平台上打全部六个平台的包**。本工具只做归档格式转换，不编译
//     任何东西，所以不受 Wails 那条"不能交叉编译"的限制。前提是流式转换（见
//     archive.go）——落盘再压回去会在 Windows 上抹掉 Unix 可执行位。
//  2. **预装轮子那一步需要打包机上有一个 Python**（任意 3.x 均可，它只负责调用
//     pip 下载**目标平台**的轮子，自己不参与运行）。没有就加 --skip-wheels，出来的
//     是裸解释器包——那种包用户拿到手仍然装不上 python-docx，内网尤其如此，所以
//     只适合应急。
//  3. **上游版本号写死在 sources.go**，打包前先核对上游还有没有那几个 tag。404 时
//     本工具会把完整 URL 打出来，照着去上游对一眼即可。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// 三个组件的 id。与 protocol 里的常量是同一批值，取本地别名只是让 sources.go 读着短。
const (
	packPython = protocol.RuntimePackPython
	packNode   = protocol.RuntimePackNode
	packGit    = protocol.RuntimePackGit
)

// allPacks 是打包顺序，也是清单里的顺序。
var allPacks = []string{packPython, packNode, packGit}

func main() {
	var (
		out        = flag.String("out", filepath.Join("dist", "runtimepacks"), "产物目录")
		cache      = flag.String("cache", filepath.Join(os.TempDir(), "runcode-runtime-upstream"), "上游包的缓存目录；已存在的文件直接复用，内网可手工放进来")
		baseURL    = flag.String("base-url", "https://OBS_BASE_URL/runtimes/", "清单里包直链的前缀（对象存储地址）")
		component  = flag.String("component", "", "只打这一个组件（python|node|git），默认全部")
		platform   = flag.String("platform", "", "只打这一个平台（如 linux/amd64），默认全部")
		python     = flag.String("python", "", "打包机上用来调 pip 的 Python（默认自动找 python3/python）")
		skipWheels = flag.Bool("skip-wheels", false, "不预装第三方库，只出裸解释器包（应急用）")
		list       = flag.Bool("list", false, "只打印计划下载的上游地址，不实际打包")
	)
	flag.Parse()

	jobs, err := plan(*component, *platform)
	if err != nil {
		fail(err)
	}
	if *list {
		for _, j := range jobs {
			fmt.Printf("%-7s %-14s %s\n", j.id, j.target.platform, j.source.url)
		}
		return
	}
	if !strings.HasSuffix(*baseURL, "/") {
		*baseURL += "/"
	}

	pip := ""
	if !*skipWheels {
		if pip, err = findPython(*python); err != nil {
			fail(fmt.Errorf("%w\n提示：没有可用的 Python 时可加 --skip-wheels，但那样出来的包不含 python-docx 等库", err))
		}
	}

	// 一个包失败不中断其余的：整趟要下几百 MB，而这条路上最常见的失败是网络断一下。
	// 让第 5 个的超时废掉前 4 个的成果，只会逼人把同样的东西再下一遍。
	//
	// 但也**不能装作没事**：失败的那个包不会进清单，于是它在客户端表现为"本平台
	// 暂未提供"——一个安静的缺口。所以结尾要把失败清单重打一遍并以非零码退出。
	manifests := map[string]*protocol.RuntimeManifest{}
	var failures []string
	for _, j := range jobs {
		fmt.Printf("== %s %s\n", j.id, j.target.platform)
		pack, err := build(j, *cache, *out, *baseURL, pip)
		if err != nil {
			fmt.Fprintf(os.Stderr, "   !! 失败: %v\n", err)
			failures = append(failures, fmt.Sprintf("%s %s: %v", j.id, j.target.platform, err))
			continue
		}
		m := manifests[j.target.platform]
		if m == nil {
			m = &protocol.RuntimeManifest{Platform: j.target.platform}
			manifests[j.target.platform] = m
		}
		m.Packs = append(m.Packs, pack)
		fmt.Printf("   -> %s  %.1f MiB  %s...\n", filepath.Base(pack.URL), float64(pack.Size)/(1<<20), pack.SHA256[:16])
	}
	if err := writeManifest(*out, manifests); err != nil {
		fail(err)
	}
	fmt.Printf("\n清单：%s\n把 *.tar.gz 传到对象存储，清单配给发布服务即可。\n", filepath.Join(*out, "manifest.json"))
	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\n以下 %d 个包没打出来，清单里也就没有它们（客户端会显示\"本平台暂未提供\"）：\n", len(failures))
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "修好之后重跑本命令即可：上游包已在缓存里，不会再下一遍。\n")
		os.Exit(1)
	}
}

// job 是一次打包：某个组件 × 某个平台 × 它的上游来源。
type job struct {
	id     string
	target target
	source source
}

// plan 把过滤条件展开成打包任务表。
func plan(component, platform string) ([]job, error) {
	var jobs []job
	for _, id := range allPacks {
		if component != "" && component != id {
			continue
		}
		for _, t := range targets {
			if platform != "" && platform != t.platform {
				continue
			}
			src, ok := sourceFor(id, t)
			if !ok {
				// 这个平台不发这个组件（Git 只有 Windows），不是错误。
				continue
			}
			jobs = append(jobs, job{id: id, target: t, source: src})
		}
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("没有匹配的打包目标（component=%q platform=%q）", component, platform)
	}
	return jobs, nil
}

// build 走完一个包的全流程：取上游 → 校验 → 转写条目 → 预装轮子 → 打 tar.gz → 算摘要。
func build(j job, cacheDir, outDir, baseURL, pip string) (protocol.RuntimeManifestPack, error) {
	var zero protocol.RuntimeManifestPack
	src, err := fetchUpstream(j.source, cacheDir)
	if err != nil {
		return zero, err
	}
	entries, err := readSource(src, j.source.strip)
	if err != nil {
		return zero, err
	}
	if len(entries) == 0 {
		return zero, fmt.Errorf("剥掉 %q 之后包里什么都不剩——上游的目录结构多半变了", j.source.strip)
	}
	if j.id == packPython && pip != "" {
		wheels, err := installWheels(pip, j.target, j.source.pipPlatform)
		if err != nil {
			return zero, err
		}
		entries = append(entries, wheels...)
	}
	name := fmt.Sprintf("%s-%s-%s-%s.tar.gz", j.id, j.source.version, j.target.goos, j.target.goarch)
	dest := filepath.Join(outDir, name)
	if err := writeTarGz(dest, entries); err != nil {
		return zero, err
	}
	sum, size, err := digestOf(dest)
	if err != nil {
		return zero, err
	}
	return protocol.RuntimeManifestPack{
		ID:      j.id,
		Version: j.source.version,
		URL:     baseURL + name,
		SHA256:  sum,
		Size:    size,
		Notes:   notesFor(j.id, pip != ""),
	}, nil
}

// notesFor 是清单里那句给用户看的说明。
func notesFor(id string, withWheels bool) string {
	switch id {
	case packPython:
		if withWheels {
			return "Python " + pythonVersion + "，含 " + strings.Join(pythonWheels, " / ")
		}
		return "Python " + pythonVersion + "（未预装第三方库）"
	case packNode:
		return "Node.js " + nodeVersion + "，含 npm"
	case packGit:
		return "Git " + gitVersion + "（MinGit，免安装）"
	}
	return ""
}

// fetchUpstream 取上游包到缓存目录并校验；已经在缓存里的直接用。
//
// 缓存不只是省流量：内网机器多半下不动 GitHub 与 nodejs.org，把手工下好的文件按
// 同样的文件名丢进 --cache 目录，这里就会直接用它——本工具于是在完全离线的机器上
// 也能跑完。
func fetchUpstream(s source, cacheDir string) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(s.url)
	dest := filepath.Join(cacheDir, name)
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("   缓存命中 %s\n", name)
	} else {
		fmt.Printf("   下载 %s\n", s.url)
		if err := download(s.url, dest); err != nil {
			return "", err
		}
	}
	if s.sumURL == "" {
		// 上游没提供校验和文件（MinGit 就是）。不因此中断：这一步校验的是"上游
		// 到打包机"这一跳，而发给用户的那个 sha256 是重打包之后另算的，安全性不
		// 依赖这里。只是把话说清楚，好让人决定要不要手工核对。
		fmt.Printf("   （上游未提供校验和文件，跳过源校验）\n")
		return dest, nil
	}
	want, err := upstreamSum(s, name)
	if err != nil {
		return "", err
	}
	got, _, err := digestOf(dest)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, want) {
		// 就地删掉：校验不过的缓存文件没有任何价值，留着只会让下一趟"缓存命中"
		// 然后以同样的方式失败——那种循环看起来像是工具坏了。代理把大文件截断
		// 是最常见的成因，重跑一次通常就好。
		_ = os.Remove(dest)
		return "", fmt.Errorf("上游包校验失败（已删除缓存，重跑本命令即可）\n  期望 %s\n  实得 %s\n  持续不一致才说明源头已经不是上游那份了", want, got)
	}
	return dest, nil
}

// upstreamSum 取上游声明的摘要。两种格式：整个文件就是一个哈希，或每行
// "<hash>  <filename>" 的清单。
func upstreamSum(s source, name string) (string, error) {
	body, err := get(s.sumURL)
	if err != nil {
		return "", err
	}
	text := string(body)
	if s.sumKind == "bare" {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			return "", fmt.Errorf("上游校验和文件是空的: %s", s.sumURL)
		}
		return fields[0], nil
	}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("上游校验和清单里没有 %s", name)
}

// downloadAttempts 是一个上游包最多试几趟。
//
// 需要重试不是"以防万一"：这些包 20–40 MB 起步，而打包机大概率坐在企业代理后面，
// 一次连接撑不到底是常态而不是异常。断在半路的下载会留下 .part，下一趟带 Range
// 从断点接着下——重头再来在这种网络上可能永远下不完。
const downloadAttempts = 5

func download(url, dest string) error {
	tmp := dest + ".part"
	var last error
	for i := 1; i <= downloadAttempts; i++ {
		done, err := downloadChunk(url, tmp)
		if done {
			return os.Rename(tmp, dest)
		}
		last = err
		off := partSize(tmp)
		fmt.Printf("   第 %d 趟中断（已取 %.1f MiB）：%v\n", i, float64(off)/(1<<20), err)
		time.Sleep(time.Duration(i) * time.Second)
	}
	return fmt.Errorf("下载失败（试了 %d 趟）: %w", downloadAttempts, last)
}

// downloadChunk 下一趟：有 .part 就带 Range 续传，返回 done=true 表示整个文件到齐了。
func downloadChunk(url, tmp string) (bool, error) {
	off := partSize(tmp)
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // 打包工具，整趟时长不设上限
	if err != nil {
		return false, err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		// 续上。
	case http.StatusOK:
		// 服务端不认 Range（或这是第一趟），从头来：把已有的截断掉，否则新数据会
		// 接在旧数据后面，拼出一个 sha256 永远对不上、还看不出为什么的文件。
		off = 0
		if err := os.Truncate(tmp, 0); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// 已经下完了（.part 恰好是完整长度），上一趟只是没来得及改名。
		return true, nil
	default:
		return false, fmt.Errorf("HTTP %d — %s\n（上游版本号写在 tools/runtimepacks/sources.go，404 多半是该升了）", resp.StatusCode, url)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // 打包工具的临时文件
	if err != nil {
		return false, err
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		_ = f.Close()
		return false, err
	}
	_, copyErr := io.Copy(f, resp.Body)
	if err := f.Close(); err != nil {
		return false, err
	}
	if copyErr != nil {
		return false, copyErr
	}
	// 响应体读完即认为这一趟到底了。真的少了字节也无妨：调用方紧接着要比对
	// sha256，缺一个字节都过不去。
	return true, nil
}

// partSize 返回半成品的字节数（不存在就是 0）。
func partSize(tmp string) int64 {
	st, err := os.Stat(tmp)
	if err != nil {
		return 0
	}
	return st.Size()
}

// get 取一个小文件（校验和清单）。
//
// 一样要重试：它虽然只有几十字节，走的却是同一条会断的路——而且它断在**包已经下好
// 之后**，于是整次打包白费。上游的校验和文件不带 Range 语义，重试就是重来一趟。
func get(url string) ([]byte, error) {
	var last error
	for i := 1; i <= downloadAttempts; i++ {
		body, err := getOnce(url)
		if err == nil {
			return body, nil
		}
		last = err
		var status statusError
		if errors.As(err, &status) {
			// 404 这类答复重试多少次都是同一个答案，早点失败好过让人等五趟。
			return nil, err
		}
		time.Sleep(time.Duration(i) * time.Second)
	}
	return nil, last
}

// statusError 是"服务端明确答复了一个非 200"，与"连接断了"区别开：前者重试无意义。
type statusError struct {
	code int
	url  string
}

func (e statusError) Error() string { return fmt.Sprintf("HTTP %d — %s", e.code, e.url) }

func getOnce(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec,noctx // 同上
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError{code: resp.StatusCode, url: url}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// digestOf 算文件的 sha256 与字节数。
func digestOf(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// findPython 找打包机上的 Python（只用来调 pip 下目标平台的轮子，自己不进包）。
func findPython(explicit string) (string, error) {
	for _, c := range []string{explicit, "python3", "python"} {
		if c == "" {
			continue
		}
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		if err := exec.Command(p, "-m", "pip", "--version").Run(); err != nil { //nolint:gosec // 打包工具
			continue
		}
		return p, nil
	}
	return "", fmt.Errorf("找不到带 pip 的 Python（试过 python3 / python，可用 --python 指定）")
}

// installWheels 把 pythonWheels 下成**目标平台**的轮子并摊进包的 site-packages。
//
// 关键是那几个 --platform/--python-version/--abi/--only-binary 参数：没有它们，pip
// 装的是**打包机**的轮子，出来的包在目标平台上会在 import lxml 时炸——而且是装机
// 之后才炸。--only-binary 保证不会有源码包蒙混过关（源码包要在目标平台上编译，
// 那在打包机上根本做不到，pip 却会"成功"地装一个不可用的东西）。
func installWheels(pip string, t target, pipPlatform string) ([]entry, error) {
	if pipPlatform == "" {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "runcode-wheels-")
	if err != nil {
		return nil, err
	}
	args := []string{
		"-m", "pip", "install",
		"--target", dir,
		"--platform", pipPlatform,
		"--python-version", majorMinor(pythonVersion),
		"--implementation", "cp",
		"--abi", pythonABI,
		"--only-binary", ":all:",
		"--no-compile", // .pyc 按解释器版本与路径生成，跨机器带过去没意义，只是体积
		"--upgrade",
	}
	args = append(args, pythonWheels...)
	cmd := exec.Command(pip, args...) //nolint:gosec // 打包工具，参数来自本文件里的版本表
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fmt.Printf("   pip -> %s\n", pipPlatform)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("预装第三方库失败: %w", err)
	}
	return dirEntries(dir, sitePackages(t))
}

// manifestDoc 是产出的清单文件：一个文件装下所有平台。
//
// 不按平台分成六份：发布服务按平台挑一段答给客户端，而运维手上只有一份文件要传、
// 要对——分成六份的唯一结果是某次只更新了其中五份。
type manifestDoc struct {
	GeneratedAt string                               `json:"generatedAt"`
	Platforms   map[string]*protocol.RuntimeManifest `json:"platforms"`
}

func writeManifest(outDir string, m map[string]*protocol.RuntimeManifest) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(manifestDoc{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Platforms:   m,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), append(b, '\n'), 0o644); err != nil { //nolint:gosec // 清单是公开内容
		return err
	}
	// 再按平台各写一份，内容**就是**接口该返回的那个对象。服务端于是不必理解上面
	// 那份聚合文件的结构，照着平台键透传即可；改动清单时也不会出现"聚合文件改了、
	// 接口返回的还是旧的"这种只在客户端才看得出来的偏差。
	for platform, man := range m {
		one, err := json.MarshalIndent(man, "", "  ")
		if err != nil {
			return err
		}
		name := "runtimes-" + strings.ReplaceAll(platform, "/", "-") + ".json"
		if err := os.WriteFile(filepath.Join(outDir, name), append(one, '\n'), 0o644); err != nil { //nolint:gosec // 同上
			return err
		}
	}
	return nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "runtimepacks: %v\n", err)
	os.Exit(1)
}
