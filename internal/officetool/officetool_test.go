package officetool

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

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
	cb, err := extractDocx(p)
	if err != nil {
		t.Fatalf("extractDocx: %v", err)
	}
	out := render(cb)

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
	// Line numbering: first content line is prefixed "1\t".
	if !strings.HasPrefix(out, "1\t") {
		t.Errorf("output not line-numbered: %q", out[:min(40, len(out))])
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

	cb, err := extractXlsx(p)
	if err != nil {
		t.Fatalf("extractXlsx: %v", err)
	}
	out := render(cb)
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
	cb, err := extractPptx(p)
	if err != nil {
		t.Fatalf("extractPptx: %v", err)
	}
	out := render(cb)
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
		raw, _ := json.Marshal(map[string]string{"path": path})
		return Tool{}.Run(context.Background(), raw, ctx, nil)
	}

	res, err := run("d.docx")
	if err != nil {
		t.Fatalf("run docx: %v", err)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "标题内容") {
		t.Fatalf("docx run result unexpected: %#v", res)
	}
	if !strings.HasPrefix(res.Content[0].Text, "1\t") {
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

func TestRenderLineNumberingAndTruncation(t *testing.T) {
	t.Parallel()
	cb := newCapBuf()
	cb.writeString("alpha\nbeta\ngamma")
	out := render(cb)
	if out != "1\talpha\n2\tbeta\n3\tgamma" {
		t.Fatalf("numbering = %q", out)
	}

	small := &capBuf{max: 10}
	small.writeString("0123456789ABCDEF")
	if !small.truncated() {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(render(small), truncationMarker) {
		t.Fatal("truncation marker missing")
	}
}
