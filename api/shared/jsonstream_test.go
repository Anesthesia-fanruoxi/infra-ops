package shared

import (
	"strings"
	"testing"
)

// collect 把按给定切分喂入的 JSON 文本转成 路径→拼接文本 的映射，并返回全部事件。
func collect(chunks ...string) (map[string]string, []JSONStringEvent) {
	sc := &JSONStreamScanner{}
	joined := map[string]string{}
	var all []JSONStringEvent
	for _, c := range chunks {
		sc.Feed(c, func(ev JSONStringEvent) {
			all = append(all, ev)
			joined[ev.Path] += ev.Delta
		})
	}
	return joined, all
}

// runeChunks 按单个字符切分：模拟 SSE 逐字到达（转义、\uXXXX、代理对都会跨块）。
func runeChunks(s string) []string {
	runes := []rune(s)
	out := make([]string, 0, len(runes))
	for _, r := range runes {
		out = append(out, string(r))
	}
	return out
}

func assertJoined(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for path, text := range want {
		if got[path] != text {
			t.Errorf("路径 %q：得到 %q，期望 %q", path, got[path], text)
		}
	}
	for path, text := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("多出未预期路径 %q = %q", path, text)
		}
	}
}

func TestJSONScannerBasic(t *testing.T) {
	joined, all := collect(`{"sql":"SELECT 1","explain":"查询一","tables":["a","b"]}`)
	assertJoined(t, joined, map[string]string{
		"sql":       "SELECT 1",
		"explain":   "查询一",
		"tables[0]": "a",
		"tables[1]": "b",
	})
	// 事件里的便捷解析：顶层字段与数组下标
	for _, ev := range all {
		switch ev.Path {
		case "sql", "explain":
			if ev.Field != ev.Path || ev.Index != -1 {
				t.Errorf("事件 %q 解析错误: field=%q index=%d", ev.Path, ev.Field, ev.Index)
			}
		case "tables[0]":
			if ev.Field != "tables" || ev.Index != 0 {
				t.Errorf("事件 tables[0] 解析错误: field=%q index=%d", ev.Field, ev.Index)
			}
		case "tables[1]":
			if ev.Field != "tables" || ev.Index != 1 {
				t.Errorf("事件 tables[1] 解析错误: field=%q index=%d", ev.Field, ev.Index)
			}
		}
	}
}

func TestJSONScannerEscapes(t *testing.T) {
	doc := `{"sql":"a\nb\tc\"d\\e\u4f60\u597d\ud83d\ude00"}`
	want := "a\nb\tc\"d\\e你好😀"

	joined, _ := collect(doc)
	if joined["sql"] != want {
		t.Fatalf("整段喂入：得到 %q，期望 %q", joined["sql"], want)
	}

	// 逐字符喂入：\u、代理对、普通转义全部跨块
	joined2, _ := collect(runeChunks(doc)...)
	if joined2["sql"] != want {
		t.Fatalf("逐字喂入：得到 %q，期望 %q", joined2["sql"], want)
	}
}

func TestJSONScannerSplitAtEveryBoundary(t *testing.T) {
	doc := `{"sql":"SELECT * FROM t WHERE name = 'x' AND n > 3","findings":["第一条","第二段说明"]}`
	want, _ := collect(doc)

	runes := []rune(doc)
	for i := 1; i < len(runes); i++ {
		got, _ := collect(string(runes[:i]), string(runes[i:]))
		for path, text := range want {
			if got[path] != text {
				t.Fatalf("在第 %d 个字符处切分，路径 %q 得到 %q，期望 %q", i, path, got[path], text)
			}
		}
	}
}

func TestJSONScannerFencePrefix(t *testing.T) {
	doc := "```json\n{\"sql\":\"SELECT 1\"}\n```"
	joined, _ := collect(doc)
	assertJoined(t, joined, map[string]string{"sql": "SELECT 1"})

	// 无换行的围栏与前置噪声
	joined2, _ := collect("以下是结果 ```json{\"summary\":\"好的\"}")
	assertJoined(t, joined2, map[string]string{"summary": "好的"})
}

func TestJSONScannerNestedPaths(t *testing.T) {
	joined, all := collect(`{"findings":[{"t":1},"x"],"a":[["y"]],"b":{},"c":[]}`)
	assertJoined(t, joined, map[string]string{
		"findings[1]": "x",
		"a[0][0]":     "y",
	})
	for _, ev := range all {
		if ev.Path == "findings[1]" && (ev.Field != "findings" || ev.Index != 1) {
			t.Errorf("findings[1] 解析错误: field=%q index=%d", ev.Field, ev.Index)
		}
		if ev.Path == "a[0][0]" && ev.Index != -1 {
			t.Errorf("嵌套路径不应给出顶层下标: %d", ev.Index)
		}
	}
}

func TestJSONScannerRootDoneIgnoresRest(t *testing.T) {
	// 完整 JSON 之后的解释文字/第二个 JSON 不再产生事件
	joined, _ := collect(`{"sql":"SELECT 1"} 后续说明 {"sql":"SELECT 2"}`)
	assertJoined(t, joined, map[string]string{"sql": "SELECT 1"})
}

func TestJSONScannerGarbageTolerated(t *testing.T) {
	// 前缀说明里出现 t 开头的单词（伪字面量），不能吞掉后面的结构
	joined, _ := collect("text 前缀\n", `{"sql":"ok"}`)
	assertJoined(t, joined, map[string]string{"sql": "ok"})

	// 截断的字符串照常吐出已收到的部分（草稿展示要的正是这个）
	joined2, _ := collect(`{"sql":"SELECT * FROM `)
	if joined2["sql"] != "SELECT * FROM " {
		t.Fatalf("截断输入：得到 %q", joined2["sql"])
	}
}

func TestJSONScannerEmptyBuffers(t *testing.T) {
	joined, _ := collect(`{"a":[],"b":{},"c":{"d":[]},"e":"尾"}`)
	assertJoined(t, joined, map[string]string{"e": "尾"})
}

func TestParseEventPath(t *testing.T) {
	cases := []struct {
		path  string
		field string
		index int
	}{
		{"sql", "sql", -1},
		{"findings[3]", "findings", 3},
		{"a.b", "a", -1},
		{"a[0].b", "a", -1},
		{"[0]", "", -1},
	}
	for _, c := range cases {
		field, index := parseEventPath(c.path)
		if field != c.field || index != c.index {
			t.Errorf("parseEventPath(%q) = (%q,%d)，期望 (%q,%d)", c.path, field, index, c.field, c.index)
		}
	}
}

func TestJSONScannerLargeChunkSplit(t *testing.T) {
	// 长中文串按固定宽度切块：拼接结果不丢不重
	content := strings.Repeat("甲乙丙丁戊己庚辛壬癸", 200)
	doc := `{"summary":"` + content + `"}`
	want, _ := collect(doc)
	runes := []rune(doc)
	const width = 7
	var chunks []string
	for i := 0; i < len(runes); i += width {
		end := i + width
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	got, _ := collect(chunks...)
	if got["summary"] != want["summary"] {
		t.Fatalf("按宽度切块结果不一致：len=%d want=%d", len([]rune(got["summary"])), len([]rune(want["summary"])))
	}
}
