package officetool

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// extractWhole runs an extractor over the full document (no window) and renders
// it, the shape most tests want.
func extractWhole(t *testing.T, name string, extract func(*capBuf) error) string {
	t.Helper()
	cb := newCapBuf(0, 0)
	if err := extract(cb); err != nil {
		t.Fatalf("extract %s: %v", name, err)
	}
	cb.flush()
	return render(cb, name)
}

// writeZip writes a ZIP (docx/pptx are ZIPs) with the given entries to dir/name
// and returns its path.
func writeZip(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	zw := zip.NewWriter(f)
	for entry, content := range files {
		w, err := zw.Create(entry)
		if err != nil {
			t.Fatalf("zip create %s: %v", entry, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", entry, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file close: %v", err)
	}
	return p
}

const docxDoc = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/><w:jc w:val="center"/></w:pPr>
    <w:r><w:rPr><w:rFonts w:eastAsia="黑体"/><w:sz w:val="32"/><w:b/></w:rPr><w:t>标题内容</w:t></w:r>
  </w:p>
  <w:p><w:pPr><w:ind w:firstLineChars="200"/></w:pPr>
    <w:r><w:rPr><w:rFonts w:eastAsia="仿宋_GB2312"/><w:sz w:val="24"/></w:rPr><w:t>普通正文</w:t></w:r>
    <w:r><w:rPr><w:rFonts w:eastAsia="仿宋_GB2312"/><w:sz w:val="24"/><w:b/></w:rPr><w:t>加粗片段</w:t></w:r>
  </w:p>
  <w:tbl>
    <w:tr><w:tc><w:p><w:r><w:t>甲</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>乙</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body>
</w:document>`

const docxStyles = `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:styleId="Heading1"><w:name w:val="标题 1"/></w:style>
</w:styles>`

func TestExtractDocx(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeZip(t, dir, "test.docx", map[string]string{
		"word/document.xml": docxDoc,
		"word/styles.xml":   docxStyles,
	})
	out := extractWhole(t, "test.docx", func(cb *capBuf) error { return extractDocx(p, cb) })

	for _, want := range []string{
		"标题 1",   // style id resolved to human name
		"居中",     // alignment
		"黑体 32→", // font tag partial (see below check separately)
		"标题内容",
		"首行缩进2字符",                // firstLineChars=200 -> 2 chars
		"⟨仿宋_GB2312 12pt⟩普通正文",   // 24 half-points = 12pt, merged uniform run
		"⟨仿宋_GB2312 12pt B⟩加粗片段", // bold segment split out
		"▦ 表1 (1行×2列)",
		"| 甲 | 乙 |",
	} {
		if want == "黑体 32→" {
			continue // placeholder; real font-size check below
		}
		if !strings.Contains(out, want) {
			t.Errorf("docx output missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(out, "黑体 16pt B") {
		t.Errorf("heading font tag missing (want 黑体 16pt B)\n%s", out)
	}
	// The header names the window, then content lines carry global numbers.
	if !strings.HasPrefix(out, "[ReadOffice] test.docx · 第 1–") {
		t.Errorf("missing window header: %q", out[:min(60, len(out))])
	}
	if !strings.Contains(out, "\n1\t") {
		t.Errorf("output not line-numbered: %q", out[:min(80, len(out))])
	}
	if !strings.Contains(out, "已到末尾") {
		t.Errorf("a fully-read document must say so, else a page reads as the whole file:\n%s", out)
	}
}

func TestExtractXlsx(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "book.xlsx")
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "功能")
	f.SetCellValue("Sheet1", "B1", "说明")
	f.SetCellValue("Sheet1", "A2", "登录")
	f.SetCellValue("Sheet1", "B2", "用户登录")
	f.SetCellValue("Sheet1", "C2", 3.5)
	style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Family: "宋体", Size: 14}})
	if err != nil {
		t.Fatalf("NewStyle: %v", err)
	}
	if err := f.SetCellStyle("Sheet1", "A1", "A1", style); err != nil {
		t.Fatalf("SetCellStyle: %v", err)
	}
	if err := f.MergeCell("Sheet1", "A1", "B1"); err != nil {
		t.Fatalf("MergeCell: %v", err)
	}
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	_ = f.Close()

	out := extractWhole(t, "book.xlsx", func(cb *capBuf) error { return extractXlsx(p, cb) })
	for _, want := range []string{
		`工作表 "Sheet1"`,
		"合并单元格: A1:B1",
		"列: A | B | C",
		"功能",
		"用户登录",
		"3.5",
		"A1: 宋体 14pt B", // formatting note
	} {
		if !strings.Contains(out, want) {
			t.Errorf("xlsx output missing %q\n---\n%s", want, out)
		}
	}
}

const pptxPres = `<?xml version="1.0"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
 <p:sldSz cx="9144000" cy="6858000"/>
</p:presentation>`

const pptxSlide = `<?xml version="1.0"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
 <p:cSld><p:spTree>
  <p:sp>
   <p:nvSpPr><p:cNvPr name="Title 1"/></p:nvSpPr>
   <p:spPr><a:xfrm><a:off x="914400" y="1828800"/><a:ext cx="7315200" cy="1828800"/></a:xfrm></p:spPr>
   <p:txBody><a:p><a:r><a:rPr sz="4000" b="1"><a:latin typeface="微软雅黑"/></a:rPr><a:t>演示标题</a:t></a:r></a:p></p:txBody>
  </p:sp>
  <p:pic>
   <p:nvPicPr><p:cNvPr name="Picture 2"/></p:nvPicPr>
   <p:spPr><a:xfrm><a:off x="1080000" y="3600000"/><a:ext cx="1800000" cy="1800000"/></a:xfrm></p:spPr>
  </p:pic>
 </p:spTree></p:cSld>
</p:sld>`

func TestExtractPptx(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeZip(t, dir, "deck.pptx", map[string]string{
		"ppt/presentation.xml":    pptxPres,
		"ppt/slides/slide1.xml":   pptxSlide,
		"ppt/slides/_rels/x.rels": "<x/>", // non-slide part under slides/ must be ignored
	})
	out := extractWhole(t, "deck.pptx", func(cb *capBuf) error { return extractPptx(p, cb) })
	for _, want := range []string{
		"幻灯片 1 页",
		"页面 25.4×19.1cm",
		"### 幻灯片 1",
		`文本框 "Title 1" @(2.5, 5.1)cm 尺寸 20.3×5.1cm`,
		"⟨微软雅黑 40pt B⟩演示标题",
		`图片 "Picture 2" @(3.0, 10.0)cm`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pptx output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRunDispatchAndErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeZip(t, dir, "d.docx", map[string]string{"word/document.xml": docxDoc, "word/styles.xml": docxStyles})
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	ctx := &tool.Context{WorkingDirectory: dir}
	run := func(path string) (tool.Result, error) {
		raw, _ := json.Marshal(map[string]any{"path": path})
		return Tool{}.Run(context.Background(), raw, ctx, nil)
	}

	res, err := run("d.docx")
	if err != nil {
		t.Fatalf("run docx: %v", err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "标题内容") {
		t.Fatalf("docx run result unexpected: %#v", res)
	}
	if !strings.Contains(res.Content[0].Text, "\n1\t") {
		t.Errorf("run result not line-numbered")
	}

	if _, err := run("notes.txt"); err == nil {
		t.Error("unsupported extension should error")
	}
	if _, err := run("legacy.doc"); err == nil {
		t.Error("legacy .doc should error")
	}
	if _, err := run("../escape.docx"); err == nil {
		t.Error("path outside workspace should error")
	}
}

func TestRenderWindowAndContinuation(t *testing.T) {
	t.Parallel()

	// Whole document in one call: the header reports the true total and says the
	// document ends here, so the model cannot mistake a page for the whole file.
	cb := newCapBuf(0, 0)
	cb.writeString("alpha\nbeta\ngamma")
	cb.flush()
	if got, want := render(cb, "d.docx"),
		"[ReadOffice] d.docx · 第 1–3 行，全文共 3 行（已到末尾）\n1\talpha\n2\tbeta\n3\tgamma"; got != want {
		t.Fatalf("full read =\n%q\nwant\n%q", got, want)
	}

	// A middle page: only the requested line comes back, its number is global
	// (not 1), and the notice names the next offset.
	win := newCapBuf(2, 1)
	win.writeString("alpha\nbeta\ngamma\n")
	win.flush()
	out := render(win, "d.docx")
	if !strings.Contains(out, "\n2\tbeta") {
		t.Errorf("window lost global line numbering:\n%s", out)
	}
	if strings.Contains(out, "alpha") || strings.Contains(out, "gamma") {
		t.Errorf("window leaked lines outside it:\n%s", out)
	}
	if !strings.Contains(out, "offset=3") {
		t.Errorf("continuation offset missing:\n%s", out)
	}

	// The byte ceiling closes the window too, and a single over-long line still
	// returns its prefix rather than nothing at all.
	small := newCapBuf(0, 0)
	small.max = 10
	small.writeString("0123456789ABCDEF\nnext\n")
	small.flush()
	if !small.truncated() {
		t.Fatal("expected the byte ceiling to close the window")
	}
	if out := render(small, "d.docx"); !strings.Contains(out, "0123456789") || !strings.Contains(out, "offset=2") {
		t.Errorf("over-long first line mishandled:\n%s", out)
	}

	// An offset past the end reports the real length instead of an empty result
	// the model would read as "document is empty".
	past := newCapBuf(99, 0)
	past.writeString("alpha\nbeta\n")
	past.flush()
	if out := render(past, "d.docx"); !strings.Contains(out, "全文共 2 行") || !strings.Contains(out, "超出范围") {
		t.Errorf("out-of-range offset unclear:\n%s", out)
	}
}

// TestRunPaginatesLongSheet is the regression this window exists for: a sheet
// longer than the old fixed 300-row cap must now be reachable in full by walking
// the reported offsets, and the walk must terminate.
func TestRunPaginatesLongSheet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "big.xlsx")
	f := excelize.NewFile()
	for i := 1; i <= 500; i++ {
		if err := f.SetCellValue("Sheet1", fmt.Sprintf("A%d", i), fmt.Sprintf("行%d", i)); err != nil {
			t.Fatalf("SetCellValue: %v", err)
		}
	}
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	_ = f.Close()

	ctx := &tool.Context{WorkingDirectory: dir}
	readPage := func(offset int) string {
		raw, _ := json.Marshal(map[string]any{"path": "big.xlsx", "offset": offset, "limit": 120})
		res, err := Tool{}.Run(context.Background(), raw, ctx, nil)
		if err != nil {
			t.Fatalf("run offset=%d: %v", offset, err)
		}
		return res.Content[0].Text
	}

	var all strings.Builder
	offset := 1
	for page := 0; ; page++ {
		if page > 20 {
			t.Fatal("pagination did not terminate")
		}
		out := readPage(offset)
		all.WriteString(out)
		next, more := nextOffset(out)
		if !more {
			if !strings.Contains(out, "已到末尾") {
				t.Errorf("last page must state the document ended:\n%s", out)
			}
			break
		}
		if next <= offset {
			t.Fatalf("offset did not advance: %d -> %d", offset, next)
		}
		offset = next
	}

	joined := all.String()
	for _, want := range []string{"r1: 行1", "r300: 行300", "r301: 行301", "r500: 行500"} {
		if !strings.Contains(joined, want) {
			t.Errorf("paginated read never reached %q — a long sheet is still being cut off", want)
		}
	}
}

// nextOffset extracts the continuation offset from a truncated result.
func nextOffset(out string) (int, bool) {
	i := strings.LastIndex(out, "offset=")
	if i < 0 {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(out[i:], "offset=%d", &n); err != nil {
		return 0, false
	}
	return n, true
}
