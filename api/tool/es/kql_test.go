package es

import (
	"testing"
	"time"
)

// TestKQLPrecedence NOT > AND > OR：`a or b and c` 应解析为 a or (b and c)。
func TestKQLPrecedence(t *testing.T) {
	n, err := parseKQL("a or b and c")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeOr {
		t.Fatalf("顶层应为 OR，得到 kind=%d", n.kind)
	}
	and := n.right
	if and.kind != nodeAnd {
		t.Fatalf("OR 右侧应为 AND，得到 kind=%d", and.kind)
	}
	if and.left.value != "b" || and.right.value != "c" {
		t.Fatalf("AND 子节点错误: %+v", and)
	}
}

// TestKQLNotPrecedence NOT 高于 AND：`not a and b` 应解析为 (not a) and b。
func TestKQLNotPrecedence(t *testing.T) {
	n, err := parseKQL("not a and b")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeAnd {
		t.Fatalf("顶层应为 AND，得到 kind=%d", n.kind)
	}
	left := n.left
	if left.kind != nodeNot || left.left.value != "a" {
		t.Fatalf("AND 左侧应为 (not a): %+v", left)
	}
}

// TestKQLParen 括号覆盖优先级：`(a or b) and c`。
func TestKQLParen(t *testing.T) {
	n, err := parseKQL("(a or b) and c")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeAnd {
		t.Fatalf("顶层应为 AND，得到 kind=%d", n.kind)
	}
	or := n.left
	if or.kind != nodeOr {
		t.Fatalf("AND 左侧应为 OR 分组，得到 kind=%d", or.kind)
	}
}

// TestKQLExplicitLogic && || ! 符号亦被识别。
func TestKQLSymbolLogic(t *testing.T) {
	n, err := parseKQL("a || b and !c")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeOr {
		t.Fatalf("顶层应为 OR，得到 kind=%d", n.kind)
	}
	and := n.right
	if and.kind != nodeAnd || and.right.kind != nodeNot {
		t.Fatalf("应解析为 a or (b and (not c)): %+v", and)
	}
}

// TestKQLImplicitAnd 空格 = 隐式 AND。
func TestKQLImplicitAnd(t *testing.T) {
	n, err := parseKQL("a b c")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// a and b and c
	if n.kind != nodeAnd {
		t.Fatalf("应为左结合 AND，得到 %d", n.kind)
	}
	if n.right.value != "c" || n.left.kind != nodeAnd {
		t.Fatalf("隐式 AND 结构错误: %+v", n)
	}
}

// TestKQLFieldOpValue 字段:算子:值。
func TestKQLFieldOpValue(t *testing.T) {
	n, err := parseKQL("status:>=500")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeTerm || n.field != "status" || n.op != ">=" || n.value != "500" {
		t.Fatalf("字段算子值解析错误: %+v", n)
	}
}

// TestKQLQuoted 引号内容不参与逻辑词识别（缺陷 1 相关）。
func TestKQLQuotedLogic(t *testing.T) {
	n, err := parseKQL(`msg:"AND or NOT"`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeTerm || n.quoted != true || n.value != "AND or NOT" {
		t.Fatalf("引号值应保持原样且不触发逻辑词: %+v", n)
	}
}

// TestKQLEscape 转义：\* 字面星号不触发通配；\\ 单个反斜杠。
func TestKQLEscape(t *testing.T) {
	n, err := parseKQL(`path:C\:\*`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.value != "C:*" {
		t.Fatalf("转义 \\* 应得到字面星号，得到 %q", n.value)
	}
	if n.wildcard {
		t.Fatalf("\\* 是字面星号，不应标记通配")
	}
	// \\ 反斜杠
	n2, err := parseKQL(`a\\b`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n2.value != `a\b` {
		t.Fatalf("\\\\ 应解为单个反斜杠，得到 %q", n2.value)
	}
}

// TestKQLWildcard 未转义 * 触发通配标记。
func TestKQLWildcard(t *testing.T) {
	n, err := parseKQL(`host:nginx*`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !n.wildcard {
		t.Fatalf("nginx* 应标记通配")
	}
}

// TestKQLInvalidEscape 未知转义报 4001。
func TestKQLInvalidEscape(t *testing.T) {
	_, err := parseKQL(`a\b`) // 原始字符串，反斜杠原样传入
	if err == nil {
		t.Fatalf("未知转义应报错")
	}
}

// TestKQLErrors 各类语法错误。
func TestKQLErrors(t *testing.T) {
	cases := []string{
		`"unclosed`,
		``,
		`a and`,
		`status:`,
		`(a or b`,
		`a or or b`,
	}
	for _, c := range cases {
		if _, err := parseKQL(c); err == nil {
			t.Errorf("输入 %q 应报错", c)
		}
	}
}

// TestKQLStar 独立 * 匹配所有。
func TestKQLStar(t *testing.T) {
	n, err := parseKQL("*")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeTerm {
		t.Fatalf("裸 * 应是 term，得到 %d", n.kind)
	}
}

// TestKQLFieldExists 字段:* → star，DSL 层映射 exists。
func TestKQLFieldStar(t *testing.T) {
	n, err := parseKQL("env:*")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeTerm || n.field != "env" || n.op != ":" || n.value != "*" || !n.wildcard {
		t.Fatalf("env:* 解析错误: %+v", n)
	}
}

// TestKQLNoProgressRegress 回归：保留字符不得造成词法不前进的死循环（曾致 OOM）。
func TestKQLNoProgressRegress(t *testing.T) {
	// 普通字段算子值组合必须正常解析
	n, err := parseKQL("status:>=500 and env:prod")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if n.kind != nodeAnd {
		t.Fatalf("应为 AND: %+v", n)
	}
	// 保留字符直接出现在值外 → 必须报 4001 错误而非死循环
	for _, c := range []string{",", "a,,b", "a:,:b"} {
		done := make(chan struct{})
		go func(s string) {
			defer close(done)
			if _, e := parseKQL(s); e == nil {
				t.Errorf("输入 %q 应报错", s)
			}
		}(c)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("输入 %q 疑似死循环", c)
		}
	}
}
