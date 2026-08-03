package desktop

// Windows 侧的实现:用 COM 驱动本机的 Office 套件另存为 PDF。
//
// 走 PowerShell 而不是在 Go 里绑 COM(go-ole):预览本来就要一两秒,多花 ~300ms 起一个
// 进程无所谓,换来的是根模块不为"桌面预览"多背一个依赖——CLI 也在这个模块里。
//
// 脚本用 -EncodedCommand 传:参数里全是用户的真实路径,可能带空格、单引号、中文、
// 反引号,一路 shell 转义迟早出事;base64(UTF-16LE) 把整段脚本原样送进去,不经过任何
// 命令行解析。

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

// officeAutomation 描述一类文档怎么被驱动:先试哪些 ProgID、集合叫什么、另存为的
// 格式号是多少。ProgID 按"先微软后金山"排——两家都注册了 Office 兼容的名字,装了
// 谁就落到谁身上;WPS 同时注册 PowerPoint.Application,所以这张表在只装 WPS 的机器上
// 也走得通。
type officeAutomation struct {
	progIDs    []string
	collection string // Presentations / Documents / Workbooks
	saveAs     string // 另存为方法名
	format     int    // 目标格式号(PDF)
}

// officeConvertTimeout 兜住一次转换。COM 调用偶尔会卡在无人应答的对话框上,超时后
// 连进程一起收掉,不能让预览这条路把界面挂死。
const officeConvertTimeout = 90 * time.Second

var officeAutomations = map[string]officeAutomation{
	// ppSaveAsPDF = 32。第三个参数是 msoTriState(嵌入字体),-2 = 用文档自己的设置。
	"ppt": {progIDs: []string{"PowerPoint.Application", "Kwpp.Application"}, collection: "Presentations", saveAs: "SaveAs", format: 32},
	// wdFormatPDF = 17。
	"doc": {progIDs: []string{"Word.Application", "Kwps.Application"}, collection: "Documents", saveAs: "SaveAs", format: 17},
	// 表格没有"另存为 PDF"的格式号,导出走 ExportAsFixedFormat(xlTypePDF = 0)。
	"xls": {progIDs: []string{"Excel.Application", "Ket.Application"}, collection: "Workbooks", saveAs: "ExportAsFixedFormat", format: 0},
}

// convertOfficeToPDF 把 src 转成 dst 处的 PDF。没有可用的 Office 组件时返回
// errOfficeUnavailable,调用方据此让前端退回内嵌渲染器。
func convertOfficeToPDF(src, dst string) error {
	auto, ok := officeAutomations[officeKind(src)]
	if !ok {
		return errOfficeUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), officeConvertTimeout)
	defer cancel()

	script := officeConvertScript(auto, src, dst)
	// 程序名是常量,唯一的变量是 -EncodedCommand 的载荷:一段脚本的 base64,里面的
	// 路径全部经 psQuote 包成单引号字面量(单引号翻倍)。它不经过命令行解析,也就没有
	// 可注入的缝——路径里有分号、&、反引号都只是字符串内容。
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(script)) //nolint:gosec // 见上：命令固定，载荷是引用安全的编码脚本
	// 没有这行,每次预览都会闪一个控制台窗口——桌面应用里那是明显的瑕疵。
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("转换超时（%s）", officeConvertTimeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// 脚本自己判定"一个 ProgID 都创建不出来"时退 2:这台机器没有 Office,
			// 属于能力缺失而不是故障。
			if exitErr.ExitCode() == 2 {
				return errOfficeUnavailable
			}
		}
		return fmt.Errorf("调用本机 Office 转换失败: %s", officeErrorLine(string(out)))
	}
	if _, statErr := os.Stat(dst); statErr != nil {
		return errOfficeUnavailable
	}
	return nil
}

// officeConvertScript 生成转换脚本。逐个试 ProgID,创建成功就用它;全都创建不出来
// 退 2(= 没装 Office)。转换过程本身的失败退 1,并把原因打到 stderr 供上层记录。
//
// 另存为一律经 InvokeMember 调用:PowerShell 的后期绑定会把带参数的 COM 方法误当成
// 属性赋值("Cannot convert the value ... to type Object"),InvokeMember 绕开这层。
func officeConvertScript(auto officeAutomation, src, dst string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	fmt.Fprintf(&b, "$src = %s\n$dst = %s\n", psQuote(src), psQuote(dst))
	b.WriteString("$app = $null\n")
	fmt.Fprintf(&b, "foreach ($id in @(%s)) {\n", strings.Join(psQuoteAll(auto.progIDs), ","))
	b.WriteString("  try { $app = New-Object -ComObject $id; break } catch { }\n}\n")
	b.WriteString("if (-not $app) { exit 2 }\n")
	b.WriteString("$doc = $null\ntry {\n")
	// DisplayAlerts 挡住"字体缺失""要不要覆盖"这类模态框——无人应答的对话框会把
	// 转换永远挂住,这正是上面那个超时存在的原因,但能不触发就别触发。
	b.WriteString("  try { $app.DisplayAlerts = 0 } catch { }\n")
	fmt.Fprintf(&b, "  $doc = $app.%s.Open($src)\n", auto.collection)
	if auto.saveAs == "ExportAsFixedFormat" {
		fmt.Fprintf(&b, "  [void]$doc.GetType().InvokeMember('%s','InvokeMethod',$null,$doc,@(%d,$dst))\n", auto.saveAs, auto.format)
	} else {
		fmt.Fprintf(&b, "  [void]$doc.GetType().InvokeMember('%s','InvokeMethod',$null,$doc,@($dst,%d,-2))\n", auto.saveAs, auto.format)
	}
	b.WriteString("} catch {\n  [Console]::Error.WriteLine($_.Exception.Message)\n  $script:failed = $true\n} finally {\n")
	// 关文档时明确说"不保存":转换不该回头改用户的原稿。应用退出失败无所谓,
	// 下次调用会复用同一个已在运行的实例。
	b.WriteString("  if ($doc) { try { $doc.Close(0) } catch { try { $doc.Close() } catch { } } }\n")
	b.WriteString("  if ($app) { try { $app.Quit() } catch { } }\n}\n")
	b.WriteString("if ($script:failed) { exit 1 }\n")
	return b.String()
}

// psQuote 把一个字符串包成 PowerShell 单引号字面量(内部单引号翻倍)。单引号字面量
// 不做任何展开,所以 $、反引号、中文路径都原样送达。
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func psQuoteAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = psQuote(v)
	}
	return out
}

// encodePowerShell 按 -EncodedCommand 的要求编码:UTF-16LE 再 base64。
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// officeErrorLine 从转换器输出里挑一行能读的原因,并且**不把整段输出带上**——那里面
// 会有用户的完整路径,而这条消息要进界面。
func officeErrorLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return firstRunes(line, 160)
		}
	}
	return "未返回具体原因"
}

func firstRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
