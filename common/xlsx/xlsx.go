// Package xlsx 流式写出 .xlsx（OOXML 表格）。
//
// 为什么自己写而不引第三方库：导出走的是「流式 + 自动分批」通道，一次可能落几百万行，
// 而常规表格库（excelize 等）以「整表在内存」为模型，与目标冲突。本实现按 OOXML 规定的
// entry 顺序边算边写 zip，字符串用 inlineStr（省掉 sharedStrings 表的全量驻留），
// 内存里只留当前一行。
//
// 已知限制：单工作表。Excel 单表上限 1,048,576 行，超出直接报错，不做多表拆分——
// [Content_Types].xml 必须是第一个 zip entry，而 sheet 数量要到写完才知道，
// 拆多表就得把整张表缓存下来或写两遍，与流式目标相悖。需要更大数据请用 SQL / CSV。
package xlsx

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Excel 单工作表上限（行含表头，列）。
const (
	MaxRows = 1048576
	MaxCols = 16384
)

// 样式索引，与 stylesXML 里 cellXfs 的顺序一一对应。
const (
	styleBody   = 0
	styleHeader = 1
)

// fixedTime zip 头里的时间取固定值：同一份数据导两次得到相同字节，便于比对与去重。
var fixedTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Writer 流式表格写出器。数据行按调用顺序进唯一的工作表，无需预知总行数。
type Writer struct {
	zw    *zip.Writer
	sheet io.Writer
	names []string // 列名（A、B … AA），避免每行每列重复换算
	line  []byte   // 当前行的 XML 缓冲，复用以免每行分配
	rows  int      // 已写数据行数（不含表头）
	done  bool
}

// NewWriter 写出表头并返回可继续追加数据行的写出器；调用方最终必须 Close。
func NewWriter(w io.Writer, sheetName string, header []string) (*Writer, error) {
	if len(header) == 0 {
		return nil, errors.New("xlsx: 至少要有一列")
	}
	if len(header) > MaxCols {
		return nil, fmt.Errorf("xlsx: 列数 %d 超过 Excel 单表上限 %d", len(header), MaxCols)
	}

	zw := zip.NewWriter(w)
	entry := func(name string) (io.Writer, error) {
		return zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: fixedTime})
	}
	put := func(name, body string) error {
		e, err := entry(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(e, body)
		return err
	}

	// [Content_Types].xml 必须是第一个 entry：Excel 打开时靠它定位工作簿各部分。
	if err := put("[Content_Types].xml", contentTypesXML); err != nil {
		return nil, err
	}
	if err := put("_rels/.rels", rootRelsXML); err != nil {
		return nil, err
	}
	if err := put("xl/workbook.xml", workbookXML(safeSheetName(sheetName))); err != nil {
		return nil, err
	}
	if err := put("xl/_rels/workbook.xml.rels", workbookRelsXML); err != nil {
		return nil, err
	}
	if err := put("xl/styles.xml", stylesXML); err != nil {
		return nil, err
	}

	sheet, err := entry("xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(sheet, sheetOpenXML(colWidths(header))); err != nil {
		return nil, err
	}

	x := &Writer{zw: zw, sheet: sheet, names: colNames(len(header))}
	hdr := make([]interface{}, len(header))
	for i, h := range header {
		hdr[i] = h
	}
	if err := x.writeRow(hdr, styleHeader, 1); err != nil {
		return nil, err
	}
	return x, nil
}

// WriteRow 追加一行数据。
//
// 值可以是 nil、string、[]byte、bool、各种整数/浮点（写真正的数字单元格，Excel 里可直接求和）
// 或 time.Time；其余类型按 fmt.Sprint 转文本。nil 写出真正的空单元格。
func (x *Writer) WriteRow(vals []interface{}) error {
	if x.done {
		return errors.New("xlsx: 写出器已关闭")
	}
	if x.rows+2 > MaxRows {
		return fmt.Errorf("xlsx: 数据超过 Excel 单表上限 %d 行，请改用 SQL 或 CSV 格式导出", MaxRows-1)
	}
	x.rows++
	return x.writeRow(vals, styleBody, x.rows+1)
}

// Rows 已写出的数据行数（不含表头）。
func (x *Writer) Rows() int { return x.rows }

// Close 收尾工作表并关闭 zip；重复调用安全。
func (x *Writer) Close() error {
	if x.done {
		return nil
	}
	x.done = true
	if _, err := io.WriteString(x.sheet, "</sheetData></worksheet>"); err != nil {
		return err
	}
	return x.zw.Close()
}

func (x *Writer) writeRow(vals []interface{}, style, rowNo int) error {
	buf := append(x.line[:0], "<row r=\""...)
	buf = strconv.AppendInt(buf, int64(rowNo), 10)
	buf = append(buf, '"', '>')
	for i, name := range x.names {
		var v interface{}
		if i < len(vals) {
			v = vals[i]
		}
		buf = appendCell(buf, name, rowNo, v, style)
	}
	buf = append(buf, "</row>"...)
	x.line = buf
	_, err := x.sheet.Write(buf)
	return err
}

// appendCell 拼一个单元格。style 为 0 时省略 s 属性，让文件更小。
func appendCell(b []byte, colName string, rowNo int, v interface{}, style int) []byte {
	b = append(b, "<c r=\""...)
	b = append(b, colName...)
	b = strconv.AppendInt(b, int64(rowNo), 10)
	b = append(b, '"')
	if style != styleBody {
		b = append(b, " s=\""...)
		b = strconv.AppendInt(b, int64(style), 10)
		b = append(b, '"')
	}
	switch t := v.(type) {
	case nil:
		return append(b, "/>"...)
	case bool:
		b = append(b, " t=\"b\"><v>"...)
		if t {
			b = append(b, '1')
		} else {
			b = append(b, '0')
		}
		return append(b, "</v></c>"...)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		b = append(b, "><v>"...)
		b = append(b, fmt.Sprint(v)...)
		return append(b, "</v></c>"...)
	case time.Time:
		return appendText(b, t.Format("2006-01-02 15:04:05"))
	case []byte:
		return appendText(b, string(t))
	case string:
		return appendText(b, t)
	default:
		return appendText(b, fmt.Sprint(v))
	}
}

// appendText 内联字符串单元格。xml:space="preserve" 保证首尾空格不被吃掉。
func appendText(b []byte, s string) []byte {
	b = append(b, " t=\"inlineStr\"><is><t xml:space=\"preserve\">"...)
	b = append(b, escapeXML(escapeUnderscore(s))...)
	return append(b, "</t></is></c>"...)
}

// escapeUnderscore 把 `_x` 写成 `_x005F_x`：OOXML 里 `_xHHHH_` 是字符转义语法，
// 原文恰好含该模式时会被 Excel 当成别的字符（如 `_x0041_` 变成 `A`）。
func escapeUnderscore(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	return strings.ReplaceAll(s, "_x", "_x005F_x")
}

// escapeXML 转义 XML 特殊字符，并处理两类必须转义才不失真的字符：
//
//   - `\r`：XML 解析器会按规范把裸回车规范化成 `\n`（单独的 `\r` 直接变 `\n`，
//     `\r\n` 缩成 `\n`）。不转义的话，Windows 换行的文本导出来就被改掉了。
//   - XML 1.0 不允许的控制字符（如 0x00、0x07）：按 ECMA-376 §22.4.2.4 用
//     `_xHHHH_` 转义，而不是丢弃——导出工具不该悄悄改数据。
//
// `\t` 与 `\n` 在元素内容里无需转义，按原样写（xml:space="preserve" 已声明保留空白）。
func escapeXML(s string) string {
	if !strings.ContainsAny(s, "&<>\"'\r\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f"+
		"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&apos;")
		case r == '\r':
			b.WriteString("_x000D_")
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r >= 0x20 && r <= 0xD7FF, r >= 0xE000 && r <= 0xFFFD, r >= 0x10000 && r <= 0x10FFFF:
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "_x%04X_", r)
		}
	}
	return b.String()
}

// colNames 生成 A、B … Z、AA、AB … 的列名表。
func colNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = colName(i)
	}
	return out
}

func colName(i int) string {
	var out []byte
	for {
		out = append([]byte{byte('A' + i%26)}, out...)
		i = i/26 - 1
		if i < 0 {
			return string(out)
		}
	}
}

// colWidths 按表头显示宽度估列宽（CJK 按 2 个字宽算），夹在 10~60 之间。
// 不扫数据行求真实宽度：那要写两遍或缓存全表，与流式目标冲突。
func colWidths(header []string) []int {
	out := make([]int, len(header))
	for i, h := range header {
		w := displayWidth(h) + 2
		if w < 10 {
			w = 10
		}
		if w > 60 {
			w = 60
		}
		out[i] = w
	}
	return out
}

// displayWidth 估算字符串在 Excel 里的显示宽度：东亚宽字符算 2。
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && r <= 0x115F, // 谚文字母
			r == 0x2329 || r == 0x232A,
			r >= 0x2E80 && r <= 0xA4CF && r != 0x303F, // CJK 部首 ~ 彝文（排除 0x303F）
			r >= 0xAC00 && r <= 0xD7A3, // 谚文音节
			r >= 0xF900 && r <= 0xFAFF, // CJK 兼容表意
			r >= 0xFE30 && r <= 0xFE6F, // CJK 兼容形式
			r >= 0xFF00 && r <= 0xFF60, // 全角
			r >= 0xFFE0 && r <= 0xFFE6:
			n += 2
		default:
			n++
		}
	}
	return n
}

// safeSheetName 清理工作表名：去掉 Excel 不允许的字符并限长 31。
func safeSheetName(s string) string {
	s = strings.TrimSpace(s)
	if s != "" {
		s = strings.Map(func(r rune) rune {
			switch r {
			case '[', ']', ':', '*', '?', '/', '\\':
				return -1
			}
			return r
		}, s)
	}
	if s == "" {
		return "Sheet1"
	}
	if r := []rune(s); len(r) > 31 {
		return string(r[:31])
	}
	return s
}

const nsMain = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
const nsRel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
	`</Types>`

const rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="` + nsRel + `/worksheet" Target="worksheets/sheet1.xml"/>` +
	`<Relationship Id="rId2" Type="` + nsRel + `/styles" Target="styles.xml"/>` +
	`</Relationships>`

// stylesXML 只定义两种样式：0 普通、1 表头加粗。
const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<styleSheet xmlns="` + nsMain + `">` +
	`<fonts count="2">` +
	`<font><sz val="11"/><name val="Calibri"/></font>` +
	`<font><b/><sz val="11"/><name val="Calibri"/></font>` +
	`</fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="2">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
	`</cellXfs>` +
	`</styleSheet>`

func workbookXML(sheetName string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<workbook xmlns="` + nsMain + `" xmlns:r="` + nsRel + `">` +
		`<sheets><sheet name="` + escapeXML(sheetName) + `" sheetId="1" r:id="rId1"/></sheets>` +
		`</workbook>`
}

// sheetOpenXML 拼工作表开头：冻结首行（滚动时表头常驻）+ 列宽 + 打开 sheetData。
func sheetOpenXML(widths []int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="` + nsMain + `">`)
	b.WriteString(`<sheetViews><sheetView workbookViewId="0">` +
		`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
		`</sheetView></sheetViews>`)
	b.WriteString(`<sheetFormatPr defaultRowHeight="15"/>`)
	b.WriteString(`<cols>`)
	for i, w := range widths {
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, w)
	}
	b.WriteString(`</cols>`)
	b.WriteString(`<sheetData>`)
	return b.String()
}
