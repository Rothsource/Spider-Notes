// Package xlsx writes simple Excel workbooks (Office Open XML) with no dependencies.
//
// It supports text, numbers and date-times, a bold header row, column widths,
// a frozen header and an auto-filter: enough for data exports. Text is always
// written as an inline string, never as a formula, so exported values cannot
// execute in a spreadsheet.
package xlsx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Cell styles (indexes into the cellXfs of styles.xml).
const (
	Plain    = 0
	Bold     = 1
	DateTime = 2 // yyyy-mm-dd hh:mm
	Money2   = 3 // #,##0.00
	Money0   = 4 // #,##0
	BoldM2   = 5
	BoldM0   = 6
	Date     = 7 // yyyy-mm-dd
)

// Cell is one value. V may be string, int, int64, float64 or time.Time
// (written as the wall-clock time it carries). A nil V leaves the cell empty.
type Cell struct {
	V     any
	Style int
}

// Sheet is one worksheet.
type Sheet struct {
	Name         string
	Widths       []float64 // column widths in characters; 0 = default
	Rows         [][]Cell
	FreezeHeader bool // keep the first row visible when scrolling
	AutoFilter   bool // filter buttons on the first row
}

// Write writes the workbook to w.
func Write(w io.Writer, sheets []Sheet) error {
	if len(sheets) == 0 {
		return fmt.Errorf("xlsx: no sheets")
	}
	z := zip.NewWriter(w)
	add := func(name, body string) error {
		f, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, body)
		return err
	}
	var ct, rels, wb strings.Builder
	ct.WriteString(xml.Header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	rels.WriteString(xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	wb.WriteString(xml.Header + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	var defined strings.Builder
	used := map[string]bool{}
	for i, sh := range sheets {
		n := i + 1
		name := sheetName(sh.Name, n, used)
		ct.WriteString(fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, n))
		rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n))
		wb.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, esc(name), n, n))
		if sh.AutoFilter && len(sh.Rows) > 0 {
			defined.WriteString(fmt.Sprintf(`<definedName name="_xlnm._FilterDatabase" localSheetId="%d" hidden="1">'%s'!%s</definedName>`,
				i, esc(strings.ReplaceAll(name, "'", "''")), absRange(filterRef(sh))))
		}
		if err := add(fmt.Sprintf("xl/worksheets/sheet%d.xml", n), sheetXML(sh)); err != nil {
			return err
		}
	}
	ct.WriteString(`</Types>`)
	rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, len(sheets)+1))
	wb.WriteString(`</sheets>`)
	if defined.Len() > 0 {
		wb.WriteString(`<definedNames>` + defined.String() + `</definedNames>`)
	}
	wb.WriteString(`</workbook>`)

	for _, f := range []struct{ name, body string }{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", wb.String()},
		{"xl/_rels/workbook.xml.rels", rels.String()},
		{"xl/styles.xml", stylesXML},
	} {
		if err := add(f.name, f.body); err != nil {
			return err
		}
	}
	return z.Close()
}

const stylesXML = xml.Header + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<numFmts count="2"><numFmt numFmtId="164" formatCode="yyyy-mm-dd hh:mm"/><numFmt numFmtId="165" formatCode="yyyy-mm-dd"/></numFmts>` +
	`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>` +
	`<fill><patternFill patternType="solid"><fgColor rgb="FFE6F4F1"/><bgColor indexed="64"/></patternFill></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="8">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/>` +
	`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="4" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="3" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="4" fontId="1" fillId="2" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1" applyFill="1"/>` +
	`<xf numFmtId="3" fontId="1" fillId="2" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1" applyFill="1"/>` +
	`<xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`</cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`

func sheetXML(sh Sheet) string {
	var b strings.Builder
	b.WriteString(xml.Header + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	if sh.FreezeHeader && len(sh.Rows) > 1 {
		b.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
			`<selection pane="bottomLeft" activeCell="A2" sqref="A2"/></sheetView></sheetViews>`)
	}
	if len(sh.Widths) > 0 {
		b.WriteString(`<cols>`)
		for i, wd := range sh.Widths {
			if wd > 0 {
				b.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%s" customWidth="1"/>`, i+1, i+1, strconv.FormatFloat(wd, 'f', -1, 64)))
			}
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	for r, row := range sh.Rows {
		b.WriteString(fmt.Sprintf(`<row r="%d">`, r+1))
		for c, cell := range row {
			if cell.V == nil {
				continue
			}
			ref := ColName(c) + strconv.Itoa(r+1)
			style := ""
			if cell.Style != Plain {
				style = fmt.Sprintf(` s="%d"`, cell.Style)
			}
			switch v := cell.V.(type) {
			case string:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, esc(v)))
			case int:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%d</v></c>`, ref, style, v))
			case int64:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%d</v></c>`, ref, style, v))
			case float64:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`, ref, style, strconv.FormatFloat(v, 'f', -1, 64)))
			case time.Time:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`, ref, style, strconv.FormatFloat(Serial(v), 'f', -1, 64)))
			default:
				b.WriteString(fmt.Sprintf(`<c r="%s"%s t="inlineStr"><is><t>%s</t></is></c>`, ref, style, esc(fmt.Sprint(v))))
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData>`)
	if sh.AutoFilter && len(sh.Rows) > 0 {
		b.WriteString(`<autoFilter ref="` + filterRef(sh) + `"/>`)
	}
	b.WriteString(`<pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/></worksheet>`)
	return b.String()
}

func filterRef(sh Sheet) string {
	cols := len(sh.Rows[0])
	return "A1:" + ColName(max(cols-1, 0)) + strconv.Itoa(len(sh.Rows))
}

// absRange turns "A1:H20" into "$A$1:$H$20".
func absRange(ref string) string {
	parts := strings.Split(ref, ":")
	for i, p := range parts {
		j := strings.IndexFunc(p, func(r rune) bool { return r >= '0' && r <= '9' })
		parts[i] = "$" + p[:j] + "$" + p[j:]
	}
	return strings.Join(parts, ":")
}

// Serial converts a wall-clock time to an Excel date serial (days since 1899-12-30).
func Serial(t time.Time) float64 {
	wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	return wall.Sub(time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)).Hours() / 24
}

// ColName returns the column letters for a zero-based index (0 → A, 26 → AA).
func ColName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

// esc escapes text for XML and drops characters XML 1.0 cannot carry.
func esc(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF && r != utf8.RuneError) {
			return r
		}
		return -1
	}, s)
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// sheetName returns a valid, unique sheet name (max 31 chars, no []:*?/\).
func sheetName(name string, n int, used map[string]bool) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	if r := []rune(name); len(r) > 31 {
		name = string(r[:31])
	}
	if name == "" || used[strings.ToLower(name)] {
		name = "Sheet" + strconv.Itoa(n)
	}
	used[strings.ToLower(name)] = true
	return name
}
