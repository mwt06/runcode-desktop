package officetool

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// extractPptx renders a .pptx as line-numbered structured text: slides in order,
// each shape with its kind, name, on-slide position and size (the layout), and
// its text segmented by paragraph with font info where present. Positions come
// from the slide's own a:xfrm; shapes that inherit geometry from the layout are
// marked as such.
func extractPptx(path string, cb *capBuf) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open pptx: %w", err)
	}
	defer func() { _ = zr.Close() }()

	sw, sh := pptSlideSize(zr)
	slides := pptSlideNames(zr)
	cb.writef("[pptx] 幻灯片 %d 页", len(slides))
	if sw != "" {
		cb.writef("，页面 %s×%scm", sw, sh)
	}
	cb.writeString("\n")

	for i, name := range slides {
		if cb.full() {
			break
		}
		cb.writef("\n### 幻灯片 %d\n", i+1)
		if err := renderSlide(cb, zr, name); err != nil {
			cb.writef("（解析失败：%v）\n", err)
		}
	}
	return nil
}

func renderSlide(cb *capBuf, zr *zip.ReadCloser, entry string) error {
	f := openZipEntry(zr, entry)
	if f == nil {
		return fmt.Errorf("%s missing", entry)
	}
	defer func() { _ = f.Close() }()

	dec := xml.NewDecoder(f)
	inTree := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "spTree" {
			inTree = true
			continue
		}
		if !inTree {
			continue
		}
		switch se.Name.Local {
		case "sp":
			var s pptShape
			if err := dec.DecodeElement(&s, &se); err != nil {
				return err
			}
			renderShape(cb, "文本框", s.NvSpPr.CNvPr.Name, s.SpPr.Xfrm, s.txPara())
		case "pic":
			var p pptPic
			if err := dec.DecodeElement(&p, &se); err != nil {
				return err
			}
			renderShape(cb, "图片", p.NvPicPr.CNvPr.Name, p.SpPr.Xfrm, nil)
		case "graphicFrame":
			var g pptGraphicFrame
			if err := dec.DecodeElement(&g, &se); err != nil {
				return err
			}
			renderShape(cb, "图表/表格", g.NvGraphicFramePr.CNvPr.Name, g.Xfrm, nil)
		case "grpSp":
			// Grouped shapes: report the group's presence without descending, so a
			// deck built entirely of groups still lists something meaningful.
			cb.writeString("- 组合形状（未展开其中内容）\n")
			_ = dec.Skip()
		default:
			_ = dec.Skip()
		}
		if cb.full() {
			return nil
		}
	}
	return nil
}

func renderShape(cb *capBuf, kind, name string, xfrm *pptXfrm, paras []pptRenderedPara) {
	label := kind
	if name != "" {
		label += fmt.Sprintf(" %q", name)
	}
	cb.writef("- %s %s\n", label, pptGeom(xfrm))
	for _, p := range paras {
		cb.writeString("    " + p.text + "\n")
		if cb.full() {
			return
		}
	}
}

// --- OOXML PresentationML / DrawingML subset ---

type pptXfrm struct {
	Off *struct {
		X string `xml:"x,attr"`
		Y string `xml:"y,attr"`
	} `xml:"off"`
	Ext *struct {
		CX string `xml:"cx,attr"`
		CY string `xml:"cy,attr"`
	} `xml:"ext"`
}

type pptRun struct {
	RPr *struct {
		Sz    string `xml:"sz,attr"`
		B     string `xml:"b,attr"`
		I     string `xml:"i,attr"`
		Latin *struct {
			Typeface string `xml:"typeface,attr"`
		} `xml:"latin"`
	} `xml:"rPr"`
	T string `xml:"t"`
}

type pptPara struct {
	PPr *struct {
		Lvl string `xml:"lvl,attr"`
	} `xml:"pPr"`
	Runs []pptRun `xml:"r"`
}

type pptShape struct {
	NvSpPr struct {
		CNvPr struct {
			Name string `xml:"name,attr"`
		} `xml:"cNvPr"`
	} `xml:"nvSpPr"`
	SpPr struct {
		Xfrm *pptXfrm `xml:"xfrm"`
	} `xml:"spPr"`
	TxBody *struct {
		Paras []pptPara `xml:"p"`
	} `xml:"txBody"`
}

type pptPic struct {
	NvPicPr struct {
		CNvPr struct {
			Name string `xml:"name,attr"`
		} `xml:"cNvPr"`
	} `xml:"nvPicPr"`
	SpPr struct {
		Xfrm *pptXfrm `xml:"xfrm"`
	} `xml:"spPr"`
}

type pptGraphicFrame struct {
	NvGraphicFramePr struct {
		CNvPr struct {
			Name string `xml:"name,attr"`
		} `xml:"cNvPr"`
	} `xml:"nvGraphicFramePr"`
	Xfrm *pptXfrm `xml:"xfrm"`
}

type pptRenderedPara struct {
	text string
}

// txPara renders a shape's text paragraphs: each paragraph is one line, indented
// by its outline level, carrying the (first run's) font tag and the joined text.
func (s pptShape) txPara() []pptRenderedPara {
	if s.TxBody == nil {
		return nil
	}
	var out []pptRenderedPara
	for _, p := range s.TxBody.Paras {
		var text strings.Builder
		for _, r := range p.Runs {
			text.WriteString(r.T)
		}
		body := text.String()
		if strings.TrimSpace(body) == "" {
			continue
		}
		var line strings.Builder
		if p.PPr != nil {
			if lvl, err := strconv.Atoi(p.PPr.Lvl); err == nil && lvl > 0 {
				line.WriteString(strings.Repeat("  ", lvl))
			}
		}
		line.WriteString("• ")
		if tag := pptRunTag(p.Runs); tag != "" {
			fmt.Fprintf(&line, "⟨%s⟩", tag)
		}
		line.WriteString(body)
		out = append(out, pptRenderedPara{text: line.String()})
	}
	return out
}

// pptRunTag summarizes the first run's font (name/size/bold/italic) so text
// styling is visible without annotating every run.
func pptRunTag(runs []pptRun) string {
	for _, r := range runs {
		if r.RPr == nil {
			continue
		}
		var parts []string
		if r.RPr.Latin != nil && r.RPr.Latin.Typeface != "" {
			parts = append(parts, r.RPr.Latin.Typeface)
		}
		if r.RPr.Sz != "" {
			if n, err := strconv.Atoi(r.RPr.Sz); err == nil {
				parts = append(parts, fmt.Sprintf("%gpt", float64(n)/100))
			}
		}
		var flags string
		if r.RPr.B == "1" {
			flags += "B"
		}
		if r.RPr.I == "1" {
			flags += "I"
		}
		if flags != "" {
			parts = append(parts, flags)
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
}

// pptGeom renders a shape's position and size in centimetres, or notes that it
// inherits geometry from the slide layout.
func pptGeom(x *pptXfrm) string {
	if x == nil || x.Off == nil || x.Ext == nil {
		return "@(继承布局)"
	}
	return fmt.Sprintf("@(%s, %s)cm 尺寸 %s×%scm",
		emuToCm(x.Off.X), emuToCm(x.Off.Y), emuToCm(x.Ext.CX), emuToCm(x.Ext.CY))
}

// emuToCm converts English Metric Units (914400 per inch) to centimetres.
func emuToCm(v string) string {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return "?"
	}
	cm := float64(n) / 360000
	return strconv.FormatFloat(cm, 'f', 1, 64)
}

// pptSlideSize reads the presentation's slide dimensions (EMU → cm).
func pptSlideSize(zr *zip.ReadCloser) (w, h string) {
	f := openZipEntry(zr, "ppt/presentation.xml")
	if f == nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	var doc struct {
		SldSz struct {
			CX string `xml:"cx,attr"`
			CY string `xml:"cy,attr"`
		} `xml:"sldSz"`
	}
	if err := xml.NewDecoder(f).Decode(&doc); err != nil {
		return "", ""
	}
	if doc.SldSz.CX == "" {
		return "", ""
	}
	return emuToCm(doc.SldSz.CX), emuToCm(doc.SldSz.CY)
}

// pptSlideNames returns the slide part names in slide order (slide1, slide2, …),
// sorted by the numeric suffix rather than lexically so slide10 follows slide9.
func pptSlideNames(zr *zip.ReadCloser) []string {
	type sl struct {
		name string
		n    int
	}
	var slides []sl
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "ppt/slides/slide") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		base := strings.TrimSuffix(strings.TrimPrefix(f.Name, "ppt/slides/slide"), ".xml")
		n, err := strconv.Atoi(base)
		if err != nil {
			continue // skip non-slide parts under ppt/slides (e.g. rels)
		}
		slides = append(slides, sl{name: f.Name, n: n})
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].n < slides[j].n })
	names := make([]string, len(slides))
	for i, s := range slides {
		names[i] = s.name
	}
	return names
}
