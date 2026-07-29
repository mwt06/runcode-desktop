package officetool

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	maxXlsxRows     = 300 // per sheet
	maxXlsxCols     = 60  // per sheet
	maxXlsxFmtNotes = 200 // per sheet
)

// extractXlsx renders a .xlsx as line-numbered structured text: per sheet, the
// used range and merged ranges, a value grid addressed by Excel row number and
// column letter, and a formatting section listing cells whose font/fill/number
// format is non-default. Values are the formatted (display) values, so dates and
// number formats read as they appear in Excel.
func extractXlsx(path string) (*capBuf, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	cb := newCapBuf()
	sheets := f.GetSheetList()
	cb.writef("[xlsx] 工作表 %d 个：%s\n", len(sheets), strings.Join(sheets, "、"))

	styleCache := map[int]string{}
	for _, sheet := range sheets {
		if cb.truncated() {
			break
		}
		renderSheet(cb, f, sheet, styleCache)
	}
	return cb, nil
}

func renderSheet(cb *capBuf, f *excelize.File, sheet string, styleCache map[int]string) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		cb.writef("\n### 工作表 %q — 读取失败：%v\n", sheet, err)
		return
	}
	dim, _ := f.GetSheetDimension(sheet)
	maxCols := 0
	for _, r := range rows {
		if len(r) > maxCols {
			maxCols = len(r)
		}
	}
	if maxCols > maxXlsxCols {
		maxCols = maxXlsxCols
	}

	cb.writef("\n### 工作表 %q — %d行×%d列", sheet, len(rows), maxCols)
	if dim != "" {
		cb.writef("  区域 %s", dim)
	}
	cb.writeString("\n")

	if merged, err := f.GetMergeCells(sheet); err == nil && len(merged) > 0 {
		ranges := make([]string, 0, len(merged))
		for _, m := range merged {
			ranges = append(ranges, m.GetStartAxis()+":"+m.GetEndAxis())
		}
		cb.writef("合并单元格: %s\n", strings.Join(ranges, ", "))
	}

	// Column-letter header, then each Excel row prefixed with its 1-based row
	// number so the model can name an exact cell (e.g. C2) for later edits.
	if maxCols > 0 {
		header := make([]string, maxCols)
		for c := 0; c < maxCols; c++ {
			header[c], _ = excelize.ColumnNumberToName(c + 1)
		}
		cb.writeString("列: " + strings.Join(header, " | ") + "\n")
	}
	for ri, row := range rows {
		if ri >= maxXlsxRows {
			cb.writef("…（省略 %d 行）\n", len(rows)-maxXlsxRows)
			break
		}
		cells := make([]string, maxCols)
		for c := 0; c < maxCols; c++ {
			if c < len(row) {
				cells[c] = strings.ReplaceAll(row[c], "|", "¦")
			}
		}
		cb.writef("r%d: %s\n", ri+1, strings.Join(cells, " | "))
		if cb.truncated() {
			return
		}
	}

	renderSheetFormats(cb, f, sheet, rows, maxCols, styleCache)
}

// renderSheetFormats lists non-empty cells whose style carries notable formatting
// (font, fill, bold/italic, alignment, or a custom number format), capped so a
// heavily-styled sheet cannot flood the output.
func renderSheetFormats(cb *capBuf, f *excelize.File, sheet string, rows [][]string, maxCols int, styleCache map[int]string) {
	var notes []string
	count := 0
	for ri := 0; ri < len(rows) && ri < maxXlsxRows; ri++ {
		for ci := 0; ci < len(rows[ri]) && ci < maxCols; ci++ {
			if strings.TrimSpace(rows[ri][ci]) == "" {
				continue
			}
			addr, err := excelize.CoordinatesToCellName(ci+1, ri+1)
			if err != nil {
				continue
			}
			styleID, err := f.GetCellStyle(sheet, addr)
			if err != nil || styleID == 0 {
				continue
			}
			summary, ok := styleCache[styleID]
			if !ok {
				summary = styleSummary(f, styleID)
				styleCache[styleID] = summary
			}
			if summary == "" {
				continue
			}
			if count < maxXlsxFmtNotes {
				notes = append(notes, addr+": "+summary)
			}
			count++
		}
	}
	if len(notes) == 0 {
		return
	}
	cb.writeString("格式:\n")
	for _, n := range notes {
		cb.writeString("  " + n + "\n")
		if cb.truncated() {
			return
		}
	}
	if count > len(notes) {
		cb.writef("  …（另有 %d 个带格式单元格）\n", count-len(notes))
	}
}

// styleSummary renders a cell style's notable attributes, or "" when it is plain.
func styleSummary(f *excelize.File, styleID int) string {
	st, err := f.GetStyle(styleID)
	if err != nil || st == nil {
		return ""
	}
	var parts []string
	if st.Font != nil {
		fn := st.Font
		if fn.Family != "" {
			parts = append(parts, fn.Family)
		}
		if fn.Size != 0 {
			parts = append(parts, fmt.Sprintf("%gpt", fn.Size))
		}
		var flags string
		if fn.Bold {
			flags += "B"
		}
		if fn.Italic {
			flags += "I"
		}
		if fn.Underline != "" && fn.Underline != "none" {
			flags += "U"
		}
		if flags != "" {
			parts = append(parts, flags)
		}
		if fn.Color != "" {
			parts = append(parts, "字色"+hexColor(fn.Color))
		}
	}
	if st.Fill.Type == "pattern" && st.Fill.Pattern > 0 && len(st.Fill.Color) > 0 && st.Fill.Color[0] != "" {
		parts = append(parts, "填充"+hexColor(st.Fill.Color[0]))
	}
	if st.Alignment != nil {
		if a := xlsxAlign(st.Alignment.Horizontal); a != "" {
			parts = append(parts, a)
		}
	}
	if st.CustomNumFmt != nil && *st.CustomNumFmt != "" {
		parts = append(parts, "数字格式 "+*st.CustomNumFmt)
	} else if st.NumFmt != 0 {
		parts = append(parts, fmt.Sprintf("数字格式#%d", st.NumFmt))
	}
	return strings.Join(parts, " ")
}

// hexColor normalizes an excelize color to "#RRGGBB", dropping a leading alpha
// byte from 8-digit ARGB values (excelize reports fills as e.g. "004472C4").
func hexColor(c string) string {
	c = strings.TrimPrefix(c, "#")
	if len(c) == 8 {
		c = c[2:]
	}
	return "#" + c
}

func xlsxAlign(h string) string {
	switch h {
	case "center":
		return "居中"
	case "right":
		return "右对齐"
	case "left":
		return ""
	default:
		return h
	}
}
