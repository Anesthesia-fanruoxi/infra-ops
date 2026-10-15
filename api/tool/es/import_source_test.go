package es

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// numEq 比较 JSON 数字：读取层用 UseNumber 保住大整数精度，数字是 json.Number 而非 float64。
func numEq(v any, want string) bool {
	n, ok := v.(json.Number)
	return ok && n.String() == want
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写临时文件失败: %v", err)
	}
	return p
}

// openFileSource 打开普通文件来源（测试里省掉逐处处理 error）。
func openFileSource(t *testing.T, path string) recordSource {
	t.Helper()
	src, err := newFileSource(path, "")
	if err != nil {
		t.Fatalf("打开来源失败: %v", err)
	}
	return src
}

// drainSource 读完整个来源，返回记录与被跳过的条目。
func drainSource(t *testing.T, src recordSource) (recs []*record, skips []string) {
	t.Helper()
	defer src.Close()
	for {
		r, e := src.Next()
		if e == io.EOF {
			return recs, skips
		}
		if e != nil {
			var se *skipError
			if errors.As(e, &se) {
				skips = append(skips, se.Error())
				continue
			}
			t.Fatalf("意外错误: %v", e)
		}
		recs = append(recs, r)
	}
}

// TestPickKindNginxLogIsText 这是最要紧的一条判断。
// nginx 日志以 '[' 开头，而 '[' 同样是 JSON 数组的开头；只看首字符会把整份日志
// 当成数组去解析，结果是「一条都读不出来」且错误信息莫名其妙。
func TestPickKindNginxLogIsText(t *testing.T) {
	cases := []struct {
		name string
		head string
		want int
	}{
		{"nginx 访问日志", `[2026-10-10 14:20:31,123] [INFO] GET /a 200`, kindText},
		{"带 BOM 的 nginx 日志", "\xEF\xBB\xBF[2026-10-10 14:20:31,123] x", kindText},
		{"JSON 数组（对象元素）", `[{"a":1},{"a":2}]`, kindJSONArray},
		{"JSON 数组（字符串元素）", `["a","b"]`, kindJSONArray},
		{"空数组", `[]`, kindJSONArray},
		{"单对象", `{"a":1}`, kindJSONValue},
		{"JSONL", "{\"a\":1}\n{\"a\":2}\n", kindJSONValue},
		{"缩进对象", "  \n\t{\"a\":1}", kindJSONValue},
		{"纯文本", "hello world", kindText},
		{"空内容", "", kindText},
	}
	for _, c := range cases {
		if got := pickKind([]byte(c.head)); got != c.want {
			t.Errorf("%s: pickKind = %d，期望 %d", c.name, got, c.want)
		}
	}
}

func TestJSONLSource(t *testing.T) {
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n\n{\"a\":2}\n{\"b\":\"x\"}\n")
	recs, skips := drainSource(t, openFileSource(t, p))
	if len(recs) != 3 || len(skips) != 0 {
		t.Fatalf("应读到 3 条、0 跳过，实际 %d / %v", len(recs), skips)
	}
	if !numEq(recs[0].Doc["a"], "1") || recs[2].Doc["b"] != "x" {
		t.Fatalf("解析结果不对: %+v", recs)
	}
	// 行号要能对上原文件（空行不算记录，但不该让后面记录的编号错位）
	if recs[1].Line != 3 {
		t.Fatalf("第二条的原始行号应为 3，实际 %d", recs[1].Line)
	}
}

// TestJSONMultiLineValue 美化过的多行 JSON 必须能解析 ——
// 靠「解析失败但原因是不完整就继续累积」来判定，不需要文件里带分隔符。
func TestJSONMultiLineValue(t *testing.T) {
	src := "{\n  \"a\": 1,\n  \"b\": [1,2]\n}\n{\n  \"a\": 2\n}\n"
	recs, skips := drainSource(t, openFileSource(t, writeTemp(t, "m.json", src)))
	if len(recs) != 2 || len(skips) != 0 {
		t.Fatalf("应读到 2 条、0 跳过，实际 %d / %v", len(recs), skips)
	}
	if !numEq(recs[0].Doc["a"], "1") || !numEq(recs[1].Doc["a"], "2") {
		t.Fatalf("多行 JSON 解析结果不对: %+v", recs)
	}
}

func TestJSONArraySource(t *testing.T) {
	recs, skips := drainSource(t, openFileSource(t, writeTemp(t, "arr.json", "[\n {\"a\":1},\n {\"a\":2}\n]\n")))
	if len(recs) != 2 || len(skips) != 0 {
		t.Fatalf("应读到 2 条、0 跳过，实际 %d / %v", len(recs), skips)
	}
	if !numEq(recs[1].Doc["a"], "2") {
		t.Fatalf("数组元素解析结果不对: %+v", recs[1].Doc)
	}
}

// TestJSONArraySkipsNonObject 数组里混进标量：跳过它，但不该拖垮后面的元素。
func TestJSONArraySkipsNonObject(t *testing.T) {
	recs, skips := drainSource(t, openFileSource(t, writeTemp(t, "arr2.json", `[{"a":1}, 42, {"a":3}]`)))
	if len(recs) != 2 {
		t.Fatalf("应读到 2 条对象，实际 %d", len(recs))
	}
	if len(skips) != 1 || !strings.Contains(skips[0], "不是 JSON 对象") {
		t.Fatalf("标量元素应被跳过并说明原因: %v", skips)
	}
}

// TestJSONLBadLineKeepsGoing 一行语法坏掉不能终止整份导入 ——
// 几万行的日志里坏一行是常态，用户要的是「导进去绝大部分」而不是「全白等」。
func TestJSONLBadLineKeepsGoing(t *testing.T) {
	src := "{\"a\":1}\n{bad json here\n{\"a\":3}\n"
	recs, skips := drainSource(t, openFileSource(t, writeTemp(t, "bad.jsonl", src)))
	if len(recs) != 2 || !numEq(recs[0].Doc["a"], "1") || !numEq(recs[1].Doc["a"], "3") {
		t.Fatalf("坏行前后的记录都要保留，实际 %+v", recs)
	}
	if len(skips) != 1 || !strings.Contains(skips[0], "第 2 行") {
		t.Fatalf("坏行要报告行号: %v", skips)
	}
}

func TestTextLineSource(t *testing.T) {
	p := writeTemp(t, "a.log", "[2026-10-10 14:20:31] GET /a\n\n[2026-10-10 14:20:32] GET /b\n")
	recs, skips := drainSource(t, openFileSource(t, p))
	if len(recs) != 2 || len(skips) != 0 {
		t.Fatalf("应读到 2 行（空行不算），实际 %d / %v", len(recs), skips)
	}
	if !strings.Contains(recs[0].Text, "GET /a") || recs[0].Line != 1 {
		t.Fatalf("文本行内容或行号不对: %+v", recs[0])
	}
	if recs[1].Line != 3 {
		t.Fatalf("第二条的原始行号应为 3，实际 %d", recs[1].Line)
	}
}

// TestLongLineNotTruncated 日志行常见几十 KB（长堆栈、埋点串），
// bufio.Scanner 默认 64KB 上限会直接报 ErrTooLong 并终止扫描。
func TestLongLineNotTruncated(t *testing.T) {
	long := strings.Repeat("x", 200<<10) // 200KB
	recs, _ := drainSource(t, openFileSource(t, writeTemp(t, "long.log", long+"\n")))
	if len(recs) != 1 || len(recs[0].Text) != len(long) {
		t.Fatalf("超长行不能被截断或丢弃，实际长度 %d", len(recs[0].Text))
	}
}

func TestSafeJoinRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeJoin(root, "../../evil.txt"); ok {
		t.Fatal("路径越界的条目名必须拒绝（zip slip）")
	}
	if _, ok := safeJoin(root, "/abs/evil.txt"); ok {
		t.Fatal("绝对路径条目名必须拒绝")
	}
	if p, ok := safeJoin(root, "sub/dir/ok.txt"); !ok || !strings.HasSuffix(p, filepath.Join("sub", "dir", "ok.txt")) {
		t.Fatalf("正常子目录应放行，实际 %q ok=%v", p, ok)
	}
}

func TestLooksBinary(t *testing.T) {
	if !looksBinary([]byte("PNG\x00\x01\x02")) {
		t.Fatal("含 NUL 应判为二进制")
	}
	if looksBinary([]byte("plain text line")) {
		t.Fatal("纯文本不该判为二进制")
	}
}
