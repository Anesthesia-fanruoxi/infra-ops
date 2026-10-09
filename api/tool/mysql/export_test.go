package mysql

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInsertWriterBatches 精确校验 INSERT 文件的产出形态：限定表名、反引号列名、
// 每批一条语句、批间空行、结尾统计注释。
func TestInsertWriterBatches(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	w := newInsertWriter(bw, "`db`.`t`", []string{"id", "name"}, []string{"INT", "VARCHAR"}, 2, 0)
	for _, r := range [][]interface{}{{1, "a"}, {2, "b"}, {3, "c"}, {4, nil}, {5, "e"}} {
		if err := w.WriteRow(r); err != nil {
			t.Fatalf("WriteRow: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if w.Batches() != 3 {
		t.Fatalf("5 行按每批 2 行应切成 3 批，实际 %d", w.Batches())
	}
	want := "INSERT INTO `db`.`t` (`id`, `name`) VALUES\n(1, 'a'),\n(2, 'b');\n\n" +
		"INSERT INTO `db`.`t` (`id`, `name`) VALUES\n(3, 'c'),\n(4, NULL);\n\n" +
		"INSERT INTO `db`.`t` (`id`, `name`) VALUES\n(5, 'e');\n\n" +
		"-- 导出完成，共 5 行\n"
	if got := buf.String(); got != want {
		t.Fatalf("INSERT 文件内容不符\n实际:\n%s\n期望:\n%s", got, want)
	}
}

// TestInsertWriterSplitsByBytes 字节阈值先于行数触发时也要切批：
// 只看行数的话，一行带几百 KB 的 TEXT 就能拼出超过 max_allowed_packet 的语句。
func TestInsertWriterSplitsByBytes(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	w := newInsertWriter(bw, "`t`", []string{"c"}, nil, 1000, 40)
	for i := 0; i < 5; i++ {
		if err := w.WriteRow([]interface{}{strings.Repeat("x", 20)}); err != nil {
			t.Fatalf("WriteRow: %v", err)
		}
	}
	_ = w.Close()
	_ = bw.Flush()
	if w.Batches() != 5 {
		t.Fatalf("每行 24 字节、阈值 40 字节时应一行一批，实际 %d 批", w.Batches())
	}
	if n := strings.Count(buf.String(), "INSERT INTO"); n != 5 {
		t.Fatalf("应写出 5 条 INSERT，实际 %d 条", n)
	}
}

func TestAppendSQLValue(t *testing.T) {
	at := time.Date(2026, 10, 8, 9, 8, 7, 0, time.Local)
	cases := []struct {
		name   string
		v      interface{}
		dbType string
		want   string
	}{
		{"空值", nil, "VARCHAR", "NULL"},
		{"字符串加引号", "abc", "VARCHAR", "'abc'"},
		{"撇号转义", "a'b", "TEXT", `'a\'b'`},
		{"反斜杠转义", `a\b`, "TEXT", `'a\\b'`},
		{"换行转义", "a\nb", "TEXT", `'a\nb'`},
		{"NUL 转义", "a\x00b", "TEXT", `'a\0b'`},
		{"SUB 转义", "a\x1ab", "TEXT", `'a\Zb'`},
		{"中文原样", "中文;分号", "VARCHAR", "'中文;分号'"},
		{"整数列裸写", []byte("42"), "BIGINT", "42"},
		{"无符号大整数", []byte("18446744073709551615"), "UNSIGNED BIGINT", "18446744073709551615"},
		{"小数裸写", []byte("3.14"), "DECIMAL", "3.14"},
		{"数值列为空串退化成引号", []byte(""), "INT", "''"},
		{"数值列但值不是数字", []byte("abc"), "INT", "'abc'"},
		{"几何类型走 hex", []byte{0x00, 0x1f}, "POINT", "X'001F'"},
		{"二进制走 hex", []byte{0xff, 0x00}, "BLOB", "X'FF00'"},
		{"布尔", true, "TINYINT", "1"},
		{"时间", at, "DATETIME", "'2026-10-08 09:08:07'"},
	}
	for _, c := range cases {
		if got := string(appendSQLValue(nil, c.v, c.dbType)); got != c.want {
			t.Errorf("%s：得到 %s，期望 %s", c.name, got, c.want)
		}
	}
}

// TestTypeClassification 类型判定必须精确：POINT 里含 "INT"，
// 用 Contains 匹配会把它当数值列，导出的几何数据就成了语法错误的裸文本。
func TestTypeClassification(t *testing.T) {
	for _, s := range []string{"INT", "int", "UNSIGNED BIGINT", "BIGINT UNSIGNED", "DECIMAL", "DOUBLE", "YEAR"} {
		if !isNumericType(s) {
			t.Errorf("%s 应判为数值类型", s)
		}
	}
	for _, s := range []string{"POINT", "VARCHAR", "TEXT", "DATETIME", "JSON", "GEOMETRY", "BLOB", "BIT"} {
		if isNumericType(s) {
			t.Errorf("%s 不应判为数值类型", s)
		}
	}
	for _, s := range []string{"BLOB", "TINYBLOB", "LONGBLOB", "VARBINARY", "POINT", "GEOMETRY", "BIT"} {
		if !isBinaryType(s) {
			t.Errorf("%s 应判为二进制类型", s)
		}
	}
	for _, s := range []string{"INT", "VARCHAR", "TEXT", "DATETIME"} {
		if isBinaryType(s) {
			t.Errorf("%s 不应判为二进制类型", s)
		}
	}
}

func TestIsPlainNumber(t *testing.T) {
	ok := []string{"1", "-1", "+1", "3.14", "-0.5", "1e10", "1E+10", "0"}
	for _, s := range ok {
		if !isPlainNumber(s) {
			t.Errorf("%s 应判为可裸写数字", s)
		}
	}
	bad := []string{"", "abc", "1a", "1.2.3", "--1", " ", "0x1f", "1,000", "9223372036854775808abc"}
	for _, s := range bad {
		if isPlainNumber(s) {
			t.Errorf("%s 不应判为可裸写数字", s)
		}
	}
}

// TestXLSXValue 超精度整数退回文本：Excel 用 double 存数字，
// 硬转会让人看到的主键跟库里对不上，且悄无声息。
func TestXLSXValue(t *testing.T) {
	if v := xlsxValue([]byte("42"), "BIGINT"); v != int64(42) {
		t.Errorf("小整数应为 int64(42)，得到 %#v", v)
	}
	if v := xlsxValue([]byte("18446744073709551615"), "BIGINT"); v != "18446744073709551615" {
		t.Errorf("超精度整数应退回字符串，得到 %#v", v)
	}
	if v := xlsxValue([]byte("3.14"), "DECIMAL"); v != 3.14 {
		t.Errorf("小数应为 float64，得到 %#v", v)
	}
	if v := xlsxValue([]byte("00138"), "VARCHAR"); v != "00138" {
		t.Errorf("文本列应保持字符串（前导零不能丢），得到 %#v", v)
	}
	if v := xlsxValue(nil, "INT"); v != nil {
		t.Errorf("空值应保持 nil，得到 %#v", v)
	}
}

func TestAppendCSVCell(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"a,b", `"a,b"`},
		{`a"b`, `"a""b"`},
		{"a\nb", "\"a\nb\""},
		{"中文", "中文"},
	}
	for _, c := range cases {
		if got := string(appendCSVCell(nil, c.in)); got != c.want {
			t.Errorf("appendCSVCell(%q) = %s，期望 %s", c.in, got, c.want)
		}
	}
}

func TestQualifyTable(t *testing.T) {
	cases := []struct {
		schema, want, sql string
		qualified, plain  string
	}{
		{"db", "", "SELECT * FROM users", "`db`.`users`", "users"},
		{"", "", "SELECT * FROM `db`.`users`", "`db`.`users`", "users"},
		{"", "", "select id from db.users where id=1", "`db`.`users`", "users"},
		{"db", "other.t", "SELECT 1", "`other`.`t`", "t"},
		{"db", "`weird-name`", "SELECT 1", "`db`.`weird-name`", "weird-name"},
		{"", "", "SELECT 1", "", ""},
		{"", "", "SELECT a.id FROM a JOIN b ON a.id=b.id", "`a`", "a"},
		{"db", "", "-- 注释 FROM fake\nSELECT * FROM real_t", "`db`.`real_t`", "real_t"},
	}
	for _, c := range cases {
		q, p := qualifyTable(c.schema, c.want, c.sql)
		if q != c.qualified || p != c.plain {
			t.Errorf("qualifyTable(%q,%q,%q) = (%q,%q)，期望 (%q,%q)",
				c.schema, c.want, c.sql, q, p, c.qualified, c.plain)
		}
	}
}

func TestNormalizeExportTarget(t *testing.T) {
	dir := t.TempDir()

	if _, err := normalizeExportTarget("", "sql"); err == nil {
		t.Error("空路径应当报错")
	}
	if _, err := normalizeExportTarget("relative.sql", "sql"); err == nil {
		t.Error("相对路径应当报错")
	}
	if _, err := normalizeExportTarget(dir, "sql"); err == nil {
		t.Error("目录应当报错")
	}
	if _, err := normalizeExportTarget(filepath.Join(dir, "no-such-dir", "a.sql"), "sql"); err == nil {
		t.Error("父目录不存在应当报错")
	}

	// 扩展名与格式不符时补全，避免「选了 xlsx 却存成 .txt」这种打不开的结果
	got, err := normalizeExportTarget(filepath.Join(dir, "users"), "xlsx")
	if err != nil {
		t.Fatalf("补全扩展名失败: %v", err)
	}
	if filepath.Base(got) != "users.xlsx" {
		t.Fatalf("应补成 users.xlsx，实际 %s", filepath.Base(got))
	}
	if got, err = normalizeExportTarget(filepath.Join(dir, "a.sql"), "sql"); err != nil || filepath.Base(got) != "a.sql" {
		t.Fatalf("已带正确扩展名不应重复追加：%s / %v", got, err)
	}
}

func TestOneLine(t *testing.T) {
	// 换行若留在 `--` 注释或日志里，后面的内容会跑到注释之外
	if got := oneLine("SELECT\n  1\n-- x", 100); got != "SELECT 1 -- x" {
		t.Fatalf("应压成一行，得到 %q", got)
	}
	if got := oneLine(strings.Repeat("字", 10), 3); !strings.HasPrefix(got, "字字字") || !strings.HasSuffix(got, "…") {
		t.Fatalf("超长应截断并加省略号，得到 %q", got)
	}
}
