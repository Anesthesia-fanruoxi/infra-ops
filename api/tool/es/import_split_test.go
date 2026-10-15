package es

import (
	"errors"
	"strings"
	"testing"
)

func TestRecordBuilderNoPatternKeepsWholeText(t *testing.T) {
	b, err := newRecordBuilder(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc, partial, err := b.build(&record{Line: 7, Text: "a raw log line"})
	if err != nil || partial {
		t.Fatalf("不提字段时不该报错或标记部分命中: %v %v", err, partial)
	}
	if doc[importDefaultField] != "a raw log line" {
		t.Fatalf("整段文本应落到 %s 字段: %+v", importDefaultField, doc)
	}
}

func TestRecordBuilderExtractsFields(t *testing.T) {
	b, err := newRecordBuilder([]string{`%{WORD:method} %{NUMBER:status:int} %{NUMBER:rt:float}`}, nil)
	if err != nil {
		t.Fatalf("编译 pattern 失败: %v", err)
	}
	doc, partial, err := b.build(&record{Line: 1, Text: "GET 200 0.25"})
	if err != nil || partial {
		t.Fatalf("整行命中不该报错: %v partial=%v", err, partial)
	}
	// :int / :float 后缀的字段要变成真数字，而不是字符串 —— 否则 ES 里会建成 text
	if doc["method"] != "GET" {
		t.Fatalf("字符串字段不对: %+v", doc)
	}
	if doc["status"] != int64(200) {
		t.Fatalf(":int 字段应为 int64(200)，实际 %T %v", doc["status"], doc["status"])
	}
	if doc["rt"] != 0.25 {
		t.Fatalf(":float 字段应为 0.25，实际 %T %v", doc["rt"], doc["rt"])
	}
}

// TestRecordBuilderMissIsSkipped 整行未命中：跳过这条并把原因回传（上层汇总给用户），
// 不能让一条不匹配的记录变成一篇空文档写进索引。
func TestRecordBuilderMissIsSkipped(t *testing.T) {
	b, _ := newRecordBuilder([]string{`%{WORD:method} %{NUMBER:status:int}`}, nil)
	_, _, err := b.build(&record{Line: 42, Text: "完全不匹配的内容"})
	var se *skipError
	if !errors.As(err, &se) {
		t.Fatalf("未命中应返回 skipError，实际 %v", err)
	}
	if !strings.Contains(se.Error(), "42") {
		t.Fatalf("跳过原因要带行号: %s", se.Error())
	}
}

// TestRecordBuilderPartialStillImports 局部命中字段可能不全，但强过整条丢掉 ——
// 这是 Grok 解析页签已有的口径（Partial 是有用信号，不是失败）。
func TestRecordBuilderPartialStillImports(t *testing.T) {
	b, _ := newRecordBuilder([]string{`%{WORD:method} %{NUMBER:status:int}`}, nil)
	doc, partial, err := b.build(&record{Line: 1, Text: "GET 200 extra trailing stuff"})
	if err != nil {
		t.Fatalf("局部命中不该报错: %v", err)
	}
	if !partial || doc["method"] != "GET" {
		t.Fatalf("局部命中应标记 partial 且尽量给字段: partial=%v doc=%+v", partial, doc)
	}
}

// TestRecordBuilderSkipsPatternForJSONSource JSON 来源本就是结构化文档，不需要再过 pattern。
func TestRecordBuilderSkipsPatternForJSONSource(t *testing.T) {
	b, _ := newRecordBuilder([]string{`%{WORD:method}`}, nil)
	doc, _, err := b.build(&record{Line: 1, Doc: map[string]any{"already": "structured"}})
	if err != nil || doc["already"] != "structured" {
		t.Fatalf("JSON 来源应原样返回: %v %+v", err, doc)
	}
}

func TestRecordBuilderBadPattern(t *testing.T) {
	if _, err := newRecordBuilder([]string{`%{NOPE:x}`}, nil); err == nil {
		t.Fatal("引用不存在的 pattern 应编译失败")
	}
}
