package officetool

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// extractDocx renders a .docx as line-numbered structured text: each body
// paragraph on its own line with its style, alignment and indent, its text
// segmented by run-level formatting (font/size/bold/italic/underline/color), and
// tables as pipe-delimited rows. Order is preserved so line numbers map to the
// document's actual reading order.
func extractDocx(path string) (*capBuf, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}
	defer func() { _ = zr.Close() }()

	styles := loadDocxStyles(zr) // styleId -> human name
	doc := openZipEntry(zr, "word/document.xml")
	if doc == nil {
		return nil, fmt.Errorf("not a valid docx: word/document.xml missing")
	}
	defer func() { _ = doc.Close() }()

	cb := newCapBuf()
	dec := xml.NewDecoder(doc)
	inBody := false
	para := 0
	table := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse document.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "body" {
			inBody = true
			continue
		}
		if !inBody {
			continue
		}
		switch se.Name.Local {
		case "p":
			var p docxPara
			if err := dec.DecodeElement(&p, &se); err != nil {
				return nil, fmt.Errorf("decode paragraph: %w", err)
			}
			para++
			cb.writeString(renderDocxPara(para, p, styles))
			cb.writeString("\n")
		case "tbl":
			var t docxTable
			if err := dec.DecodeElement(&t, &se); err != nil {
				return nil, fmt.Errorf("decode table: %w", err)
			}
			table++
			renderDocxTable(cb, table, t)
		default:
			_ = dec.Skip()
		}
		if cb.truncated() {
			break
		}
	}
	return cb, nil
}

// --- OOXML WordprocessingML subset (matched by local element name) ---

type docxToggle struct {
	Val string `xml:"val,attr"`
}

// on reports whether a toggle property (b/i/…) is active: present with no value
// or a truthy value is on; an explicit 0/false/off is off.
func (t *docxToggle) on() bool {
	if t == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(t.Val)) {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

type docxRPr struct {
	Fonts *struct {
		ASCII    string `xml:"ascii,attr"`
		EastAsia string `xml:"eastAsia,attr"`
		HAnsi    string `xml:"hAnsi,attr"`
	} `xml:"rFonts"`
	Sz *struct {
		Val string `xml:"val,attr"`
	} `xml:"sz"`
	B *docxToggle `xml:"b"`
	I *docxToggle `xml:"i"`
	U *struct {
		Val string `xml:"val,attr"`
	} `xml:"u"`
	Color *struct {
		Val string `xml:"val,attr"`
	} `xml:"color"`
}

type docxRun struct {
	RPr    *docxRPr   `xml:"rPr"`
	Texts  []string   `xml:"t"`
	Tabs   []struct{} `xml:"tab"`
	Breaks []struct{} `xml:"br"`
}

func (r docxRun) text() string {
	s := strings.Join(r.Texts, "")
	if len(r.Tabs) > 0 {
		s = strings.Repeat("\t", len(r.Tabs)) + s
	}
	return s
}

type docxPPr struct {
	Style *struct {
		Val string `xml:"val,attr"`
	} `xml:"pStyle"`
	Jc *struct {
		Val string `xml:"val,attr"`
	} `xml:"jc"`
	Ind *struct {
		FirstLine      string `xml:"firstLine,attr"`
		FirstLineChars string `xml:"firstLineChars,attr"`
		Left           string `xml:"left,attr"`
		LeftChars      string `xml:"leftChars,attr"`
		Hanging        string `xml:"hanging,attr"`
	} `xml:"ind"`
}

type docxPara struct {
	PPr  *docxPPr  `xml:"pPr"`
	Runs []docxRun `xml:"r"`
}

type docxCell struct {
	Paras []docxPara `xml:"p"`
}

type docxRow struct {
	Cells []docxCell `xml:"tc"`
}

type docxTable struct {
	Rows []docxRow `xml:"tr"`
}

// runFmt is a run's resolved formatting, used to merge adjacent equally-formatted
// runs into one annotated segment.
type runFmt struct {
	font  string
	size  string // points, as a display string
	bold  bool
	ital  bool
	under bool
	color string
}

func fmtOf(rpr *docxRPr) runFmt {
	var f runFmt
	if rpr == nil {
		return f
	}
	if rpr.Fonts != nil {
		// East Asian font wins for CJK documents; fall back to ascii/hAnsi.
		switch {
		case rpr.Fonts.EastAsia != "":
			f.font = rpr.Fonts.EastAsia
		case rpr.Fonts.ASCII != "":
			f.font = rpr.Fonts.ASCII
		case rpr.Fonts.HAnsi != "":
			f.font = rpr.Fonts.HAnsi
		}
	}
	if rpr.Sz != nil {
		f.size = halfPointsToPt(rpr.Sz.Val)
	}
	f.bold = rpr.B.on()
	f.ital = rpr.I.on()
	if rpr.U != nil && strings.ToLower(rpr.U.Val) != "none" && rpr.U.Val != "" {
		f.under = true
	}
	if rpr.Color != nil && rpr.Color.Val != "" && strings.ToLower(rpr.Color.Val) != "auto" {
		f.color = "#" + rpr.Color.Val
	}
	return f
}

// tag renders a run's formatting compactly, e.g. "仿宋_GB2312 12pt B". Empty when
// nothing notable is set.
func (f runFmt) tag() string {
	var parts []string
	if f.font != "" {
		parts = append(parts, f.font)
	}
	if f.size != "" {
		parts = append(parts, f.size+"pt")
	}
	var flags string
	if f.bold {
		flags += "B"
	}
	if f.ital {
		flags += "I"
	}
	if f.under {
		flags += "U"
	}
	if flags != "" {
		parts = append(parts, flags)
	}
	if f.color != "" {
		parts = append(parts, f.color)
	}
	return strings.Join(parts, " ")
}

func renderDocxPara(idx int, p docxPara, styles map[string]string) string {
	var head []string
	if p.PPr != nil {
		if p.PPr.Style != nil && p.PPr.Style.Val != "" {
			head = append(head, styleName(styles, p.PPr.Style.Val))
		}
		if a := alignLabel(p.PPr.Jc); a != "" {
			head = append(head, a)
		}
		if ind := indentLabel(p.PPr); ind != "" {
			head = append(head, ind)
		}
	}
	prefix := fmt.Sprintf("¶%d", idx)
	if len(head) > 0 {
		prefix += " [" + strings.Join(head, " · ") + "]"
	}

	// Merge adjacent runs with identical formatting so a uniform paragraph is one
	// segment and only real formatting changes produce a new tag.
	var body strings.Builder
	var curFmt runFmt
	var curText strings.Builder
	haveSeg := false
	flush := func() {
		if !haveSeg {
			return
		}
		if tag := curFmt.tag(); tag != "" {
			fmt.Fprintf(&body, "⟨%s⟩%s", tag, curText.String())
		} else {
			body.WriteString(curText.String())
		}
		curText.Reset()
	}
	for _, r := range p.Runs {
		t := r.text()
		if t == "" {
			continue
		}
		f := fmtOf(r.RPr)
		if haveSeg && f == curFmt {
			curText.WriteString(t)
			continue
		}
		flush()
		curFmt = f
		curText.WriteString(t)
		haveSeg = true
	}
	flush()

	text := body.String()
	if text == "" {
		return prefix // empty paragraph — still numbered, keeps layout addressable
	}
	return prefix + " " + text
}

func renderDocxTable(cb *capBuf, idx int, t docxTable) {
	cols := 0
	for _, r := range t.Rows {
		if len(r.Cells) > cols {
			cols = len(r.Cells)
		}
	}
	cb.writef("▦ 表%d (%d行×%d列)\n", idx, len(t.Rows), cols)
	for _, row := range t.Rows {
		cells := make([]string, 0, len(row.Cells))
		for _, c := range row.Cells {
			cells = append(cells, cellText(c))
		}
		cb.writeString("| " + strings.Join(cells, " | ") + " |\n")
		if cb.truncated() {
			return
		}
	}
}

// cellText flattens a table cell to its text (paragraphs joined by ↵), dropping
// per-run formatting to keep tables compact.
func cellText(c docxCell) string {
	parts := make([]string, 0, len(c.Paras))
	for _, p := range c.Paras {
		var b strings.Builder
		for _, r := range p.Runs {
			b.WriteString(r.text())
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.ReplaceAll(strings.Join(parts, " ↵ "), "|", "¦")
}

// --- helpers ---

func halfPointsToPt(v string) string {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	pt := float64(n) / 2
	if pt == float64(int(pt)) {
		return strconv.Itoa(int(pt))
	}
	return strconv.FormatFloat(pt, 'f', 1, 64)
}

func alignLabel(jc *struct {
	Val string `xml:"val,attr"`
}) string {
	if jc == nil {
		return ""
	}
	switch strings.ToLower(jc.Val) {
	case "center":
		return "居中"
	case "right", "end":
		return "右对齐"
	case "both", "distribute":
		return "两端对齐"
	case "left", "start", "":
		return ""
	default:
		return jc.Val
	}
}

func indentLabel(ppr *docxPPr) string {
	if ppr == nil || ppr.Ind == nil {
		return ""
	}
	ind := ppr.Ind
	var parts []string
	if ind.FirstLineChars != "" {
		if n, err := strconv.Atoi(ind.FirstLineChars); err == nil {
			parts = append(parts, fmt.Sprintf("首行缩进%g字符", float64(n)/100))
		}
	} else if ind.FirstLine != "" {
		parts = append(parts, "首行缩进"+twipsToPt(ind.FirstLine)+"pt")
	}
	if ind.Hanging != "" {
		parts = append(parts, "悬挂"+twipsToPt(ind.Hanging)+"pt")
	}
	if ind.LeftChars != "" {
		if n, err := strconv.Atoi(ind.LeftChars); err == nil && n != 0 {
			parts = append(parts, fmt.Sprintf("左缩进%g字符", float64(n)/100))
		}
	} else if ind.Left != "" && ind.Left != "0" {
		parts = append(parts, "左缩进"+twipsToPt(ind.Left)+"pt")
	}
	return strings.Join(parts, " ")
}

func twipsToPt(v string) string {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return v
	}
	pt := float64(n) / 20
	if pt == float64(int(pt)) {
		return strconv.Itoa(int(pt))
	}
	return strconv.FormatFloat(pt, 'f', 1, 64)
}

func styleName(styles map[string]string, id string) string {
	if name, ok := styles[id]; ok && name != "" {
		return name
	}
	return id
}

// loadDocxStyles maps each styleId to its human-readable w:name, so paragraphs
// report "标题 1" rather than the opaque style id.
func loadDocxStyles(zr *zip.ReadCloser) map[string]string {
	out := map[string]string{}
	f := openZipEntry(zr, "word/styles.xml")
	if f == nil {
		return out
	}
	defer func() { _ = f.Close() }()
	var doc struct {
		Styles []struct {
			ID   string `xml:"styleId,attr"`
			Name struct {
				Val string `xml:"val,attr"`
			} `xml:"name"`
		} `xml:"style"`
	}
	if err := xml.NewDecoder(f).Decode(&doc); err != nil {
		return out
	}
	for _, s := range doc.Styles {
		if s.ID != "" && s.Name.Val != "" {
			out[s.ID] = s.Name.Val
		}
	}
	return out
}

// openZipEntry returns a reader for the named entry, or nil if absent. The caller
// closes it.
func openZipEntry(zr *zip.ReadCloser, name string) io.ReadCloser {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil
			}
			return rc
		}
	}
	return nil
}
