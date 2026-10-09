package mysql

import (
	"io"
	"strings"
	"testing"
)

func scanAll(t *testing.T, in string) []string {
	t.Helper()
	sc := newSQLScanner(strings.NewReader(in), 0)
	out := []string{}
	for {
		stmt, err := sc.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("扫描 %q 出错: %v", in, err)
		}
		out = append(out, stmt)
	}
}

// TestScannerMatchesSplitStatements 是这套流式切分器的核心保障：
// 文件导入与编辑器执行必须切出完全一样的语句，否则同一份 SQL 在两处行为不同。
//
// 曾经踩过的坑都在样例里：引号里的分号、注释里的分号、`--` 后不跟空白的情况、
// 连写引号转义、未闭合引号与未闭合块注释。
func TestScannerMatchesSplitStatements(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1; SELECT 2;",
		"SELECT 1;;SELECT 2;",
		";;  ; SELECT 1 ;;",
		"SELECT ';' AS a; SELECT 2",
		"SELECT 'a;b;c'; SELECT 2",
		"SELECT \"a;b\"; SELECT 2",
		"SELECT `a;b` FROM t;",
		"SELECT 1 -- 注释里有;分号\n; SELECT 2",
		"SELECT /* 块注释 ; 分号 */ 1; SELECT 2",
		"SELECT 1;-- 尾部注释",
		"SELECT 1;# hash 注释里有;\nSELECT 2",
		"# 整行注释\nSELECT 1;SELECT 2",
		"SELECT 'it\\'s ok'; SELECT 2",
		"SELECT 'it''s ok'; SELECT 2",
		"SELECT \"say \"\"hi\"\"\"; SELECT 2",
		"INSERT INTO t VALUES (1,'a'),(2,'b');\nINSERT INTO t VALUES (3,'c');",
		"a--b; SELECT 1",
		"SELECT 1 UNION SELECT 2 -- 尾注释无换行",
		"/* 整段注释 */SELECT 1;",
		"-- 只有注释\n-- 第二行\n",
		"SELECT '\u4e2d\u6587;\u5206\u53f7'; SELECT 2",
		"SELECT '未闭合引号",
		"/* 未闭合块注释 SELECT 1",
		"SELECT 1;\n\n\nSELECT 2;\n",
		"CREATE TABLE t (\n id INT,\n name VARCHAR(20)\n);\nINSERT INTO t VALUES (1,'a');",
	}
	for _, in := range cases {
		want := splitStatements(in)
		got := scanAll(t, in)
		if len(want) != len(got) {
			t.Errorf("输入 %q：切出 %d 条，编辑器切出 %d 条\n文件端 %q\n编辑器 %q",
				in, len(got), len(want), got, want)
			continue
		}
		for i := range want {
			if want[i] != got[i] {
				t.Errorf("输入 %q 第 %d 条不一致：\n文件端 %q\n编辑器 %q", in, i+1, got[i], want[i])
			}
		}
	}
}

// TestScannerStreamsLargeInput 校验流式读取：语句条数与内容都对，且不依赖一次性读入。
func TestScannerStreamsLargeInput(t *testing.T) {
	var sb strings.Builder
	const n = 20000
	for i := 0; i < n; i++ {
		sb.WriteString("INSERT INTO t VALUES (")
		sb.WriteString(strings.Repeat("9", 20))
		sb.WriteString(",'v');\n")
	}
	got := scanAll(t, sb.String())
	if len(got) != n {
		t.Fatalf("应切出 %d 条，实际 %d 条", n, len(got))
	}
	if !strings.HasPrefix(got[0], "INSERT INTO t VALUES (") || !strings.HasSuffix(got[n-1], "'v')") {
		t.Fatalf("首尾语句内容异常: %q … %q", got[0], got[n-1])
	}
}

// TestScannerStatementLimit 单条语句超限要报错并指出是第几条，而不是把内存吃光。
func TestScannerStatementLimit(t *testing.T) {
	sc := newSQLScanner(strings.NewReader("SELECT 1; SELECT "+strings.Repeat("x", 200)+";"), 64)
	first, err := sc.Next()
	if err != nil || first != "SELECT 1" {
		t.Fatalf("第一条应当正常切出，得到 %q / %v", first, err)
	}
	if _, err := sc.Next(); err == nil {
		t.Fatal("第二条超过上限应当报错")
	} else if !strings.Contains(err.Error(), "第 2 条") {
		t.Fatalf("报错应指明第几条语句，实际: %v", err)
	}
}

// TestScannerLineCount 语句计数用于错误定位，不应把空语句算进去。
func TestScannerLineCount(t *testing.T) {
	sc := newSQLScanner(strings.NewReader("SELECT 1;;\nSELECT 2;"), 0)
	_, _ = sc.Next()
	if sc.Lines() != 1 {
		t.Fatalf("切出第一条后计数应为 1，实际 %d", sc.Lines())
	}
	_, _ = sc.Next()
	if sc.Lines() != 2 {
		t.Fatalf("切出第二条后计数应为 2，实际 %d", sc.Lines())
	}
}
