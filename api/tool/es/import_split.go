// import_split.go 规则层：把一条原始记录变成要写进索引的文档。
//
// 一件事：提字段 —— 用 grok pattern 从文本里取出结构化字段；pattern 为空时把整段
// 文本存进 message 字段（兜底路径，原文不丢）。
//
// pattern 怎么来的（给样例让 AI 生成）不在这一层：导入分析（/es/:id/import/analyze）
// 生成后随请求传进来，避免两套 AI 逻辑各跑各的。
package es

import (
	"fmt"
	"strconv"

	"infra-ops/common/grok"
)

const (
	importDefaultField = "message" // 不提取字段时，整段文本落到这个字段
	importMaxPatterns  = grokMaxPatterns
)

// ---------------- 提字段 ----------------

// recordBuilder 把原始记录加工成待写入的文档。
type recordBuilder struct {
	matcher *grok.Matcher // nil = 不提取字段，整段文本存 message 字段
}

// newRecordBuilder 编译 pattern；patterns 为空表示「整段存文本」。
func newRecordBuilder(patterns []string, defs map[string]string) (*recordBuilder, error) {
	list := normalizePatterns(patterns, "")
	if len(list) == 0 {
		return &recordBuilder{}, nil
	}
	if len(list) > importMaxPatterns {
		return nil, fmt.Errorf("pattern 条数最多 %d 条", importMaxPatterns)
	}
	m, err := grok.Cached(list, defs) // 走缓存：每条记录都重新编译一处都不划算
	if err != nil {
		return nil, err
	}
	return &recordBuilder{matcher: m}, nil
}

// build 产出一条文档。第二个返回值表示「只是局部命中」（字段可能不完整，但仍要导入）。
// 返回 *skipError 表示这条不导入（原因会汇总给用户）。
func (b *recordBuilder) build(rec *record) (map[string]any, bool, error) {
	doc, _, partial, err := b.buildRow(rec)
	return doc, partial, err
}

// buildRow 同 build，额外返回按 pattern 顺序的 grok 字段。
// 预览要逐行展示「字段名 → 值」，map 没有顺序，会按 pattern 顺序拆出来的字段
// （ts、client_ip、method…）必须原样保序，所以这里把有序的 Fields 一并带回。
// JSON 来源与「整段存文本」模式没有 grok 字段，fields 为空，由调用方按 doc 展示。
func (b *recordBuilder) buildRow(rec *record) (map[string]any, []grok.Field, bool, error) {
	if rec.Doc != nil {
		return rec.Doc, nil, false, nil // JSON 来源本就是结构化文档，不再过 pattern
	}
	if b.matcher == nil {
		return map[string]any{importDefaultField: rec.Text}, nil, false, nil
	}
	m := b.matcher.Parse(rec.Text)
	if m == nil {
		return nil, nil, false, &skipError{fmt.Sprintf("第 %d 行未匹配 pattern", rec.Line)}
	}
	if len(m.Fields) == 0 {
		return nil, nil, false, &skipError{fmt.Sprintf("第 %d 行 pattern 未产出字段", rec.Line)}
	}
	return fieldsToDoc(m.Fields), m.Fields, m.Partial, nil
}

// fieldsToDoc 把 grok 取出的字段转成文档。
//
// 带 :int / :float 后缀的字段转成真正的数字（grok 已确认能转）；其余一律留字符串 ——
// 「看着像数字的字符串到底该不该当数字」交给类型推断那一步，那里有采样与人工确认，
// 在这里猜会让用户没法控制。
func fieldsToDoc(fields []grok.Field) map[string]any {
	doc := make(map[string]any, len(fields))
	for _, f := range fields {
		switch f.Type {
		case "int":
			if n, err := strconv.ParseInt(f.Value, 10, 64); err == nil {
				doc[f.Name] = n
				continue
			}
		case "float":
			if v, err := strconv.ParseFloat(f.Value, 64); err == nil {
				doc[f.Name] = v
				continue
			}
		}
		doc[f.Name] = f.Value
	}
	return doc
}
