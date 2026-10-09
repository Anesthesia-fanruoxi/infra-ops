package xlsx

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// build 走完整流程生成一份表格，返回原始字节。
func build(t *testing.T, sheet string, header []string, rows [][]interface{}) ([]byte, *Writer) {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, sheet, header)
	if err != nil {
		t.Fatalf("NewWriter 失败: %v", err)
	}
	for i, r := range rows {
		if err := w.WriteRow(r); err != nil {
			t.Fatalf("第 %d 行写入失败: %v", i+2, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	return buf.Bytes(), w
}

func readParts(t *testing.T, raw []byte) (map[string]string, []string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("生成的 zip 无法读回: %v", err)
	}
	parts := map[string]string{}
	order := []string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", f.Name, err)
		}
		parts[f.Name] = string(b)
		order = append(order, f.Name)
	}
	return parts, order
}

// TestWriterStructure 校验 OOXML 必需的部件齐全、顺序正确，以及单元格写法。
func TestWriterStructure(t *testing.T) {
	raw, w := build(t, "查询结果", []string{"id", "名称", "备注"}, [][]interface{}{
		{12, "张三", nil},
		{7.5, "含 <标签> & 引号\"", "a"},
		{true, []byte("bytes"), time.Date(2026, 10, 8, 13, 5, 9, 0, time.Local)},
	})
	if w.Rows() != 3 {
		t.Fatalf("行数应为 3，实际 %d", w.Rows())
	}

	parts, order := readParts(t, raw)
	for _, name := range []string{
		"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml",
		"xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml",
	} {
		if _, ok := parts[name]; !ok {
			t.Fatalf("缺少部件 %s", name)
		}
	}
	// [Content_Types].xml 必须在最前：Excel 打开时先读它定位工作簿
	if order[0] != "[Content_Types].xml" {
		t.Fatalf("[Content_Types].xml 必须是第一个部件，实际是 %s", order[0])
	}
	if !strings.Contains(parts["xl/workbook.xml"], `name="查询结果"`) {
		t.Fatalf("工作表名未写入 workbook.xml: %s", parts["xl/workbook.xml"])
	}

	sheet := parts["xl/worksheets/sheet1.xml"]
	want := []string{
		// 表头加粗（s="1"）
		`<row r="1"><c r="A1" s="1" t="inlineStr"><is><t xml:space="preserve">id</t></is></c>`,
		// 数字写真正的数字单元格（无 t 属性），Excel 里可直接求和
		`<c r="A2"><v>12</v></c>`,
		// 空值写真正的空格子
		`<c r="C2"/>`,
		`<c r="A3"><v>7.5</v></c>`,
		// 特殊字符转义
		`含 &lt;标签&gt; &amp; 引号&quot;`,
		`<c r="A4" t="b"><v>1</v></c>`,
		`<c r="C4" t="inlineStr"><is><t xml:space="preserve">2026-10-08 13:05:09</t></is></c>`,
		`</sheetData></worksheet>`,
	}
	for _, s := range want {
		if !strings.Contains(sheet, s) {
			t.Fatalf("工作表 XML 缺少片段 %q\n实际:\n%s", s, sheet)
		}
	}
	if !strings.Contains(sheet, `state="frozen"`) {
		t.Fatal("应冻结首行，便于滚动时表头常驻")
	}
}

// TestWriterEscaping 校验 OOXML 转义——这些字节若原样写进 <t>，
// Excel 要么直接报「文件已损坏」，要么静默把文字改掉。
func TestWriterEscaping(t *testing.T) {
	raw, _ := build(t, "s", []string{"c"}, [][]interface{}{
		{"_x0041_ 字面量"},
		{"含控制字符\x00\x07\x1f结尾"},
		{"保留\t制表与\n换行"},
		{"回车\r与\r\n换行"},
	})
	parts, _ := readParts(t, raw)
	sheet := parts["xl/worksheets/sheet1.xml"]

	// `_xHHHH_` 是 OOXML 的字符转义语法：原文出现该模式时必须再转义一层，
	// 否则 Excel 读出来是 "A" 而不是 "_x0041_"
	if !strings.Contains(sheet, `_x005F_x0041_ 字面量`) {
		t.Fatalf("下划线转义未生效:\n%s", sheet)
	}
	// 非法控制字符按 ECMA-376 §22.4.2.4 转义而非丢弃，保证数据一字不差
	if strings.ContainsAny(sheet, "\x00\x07\x1f") {
		t.Fatalf("非法控制字符未被转义:\n%q", sheet)
	}
	if !strings.Contains(sheet, "含控制字符_x0000__x0007__x001F_结尾") {
		t.Fatalf("控制字符应按 _xHHHH_ 转义:\n%s", sheet)
	}
	if !strings.Contains(sheet, "保留\t制表与\n换行") {
		t.Fatal("制表符与换行应原样保留（元素内容里无需转义）")
	}
	// 裸回车会被 XML 解析器规范化成换行，必须转义才不失真
	if !strings.Contains(sheet, "回车_x000D_与_x000D_\n换行") {
		t.Fatalf("回车应转义成 _x000D_:\n%s", sheet)
	}
	if strings.Contains(sheet, "\r") {
		t.Fatalf("工作表里不应出现裸回车:\n%q", sheet)
	}
}

// TestWriterLimits 边界：空表头、超列、超行、关闭后写入。
func TestWriterLimits(t *testing.T) {
	if _, err := NewWriter(io.Discard, "s", nil); err == nil {
		t.Fatal("空表头应当报错")
	}
	if _, err := NewWriter(io.Discard, "s", make([]string, MaxCols+1)); err == nil {
		t.Fatal("超列数应当报错")
	}

	var buf bytes.Buffer
	w, err := NewWriter(&buf, "s", []string{"a"})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	// 直接把计数器顶到上限，避免真写 100 万行
	w.rows = MaxRows - 2
	if err := w.WriteRow([]interface{}{1}); err != nil {
		t.Fatalf("到达上限前的最后一行应当能写: %v", err)
	}
	if err := w.WriteRow([]interface{}{2}); err == nil {
		t.Fatal("超过 Excel 单表行数上限应当报错")
	} else if !strings.Contains(err.Error(), "SQL 或 CSV") {
		t.Fatalf("超限提示应给出替代格式建议，实际: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("重复 Close 应当安全: %v", err)
	}
	if err := w.WriteRow([]interface{}{3}); err == nil {
		t.Fatal("关闭后写入应当报错")
	}
}

// TestSafeSheetName 工作表名清理：非法字符、空名、超长。
func TestSafeSheetName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "Sheet1"},
		{"   ", "Sheet1"},
		{"a[b]c:d*e?f/g\\h", "abcdefgh"},
		{strings.Repeat("长", 40), strings.Repeat("长", 31)},
		{"正常名", "正常名"},
	}
	for _, c := range cases {
		if got := safeSheetName(c.in); got != c.want {
			t.Fatalf("safeSheetName(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestColNames 列名换算：A…Z、AA…AZ、BA…
func TestColNames(t *testing.T) {
	names := colNames(28)
	if names[0] != "A" || names[25] != "Z" || names[26] != "AA" || names[27] != "AB" {
		t.Fatalf("列名换算错误: %v", names[:28])
	}
	if got := colName(701); got != "ZZ" {
		t.Fatalf("colName(701) = %s，期望 ZZ", got)
	}
	if got := colName(702); got != "AAA" {
		t.Fatalf("colName(702) = %s，期望 AAA", got)
	}
}
