package es

import (
	"encoding/json"
	"testing"
)

func TestInferTypes(t *testing.T) {
	samples := []map[string]any{
		{"code": json.Number("200"), "rt": json.Number("0.25"), "ok": true, "path": "/a"},
		{"code": json.Number("404"), "rt": json.Number("1.5"), "ok": false, "path": "/b"},
		{"code": json.Number("500"), "rt": json.Number("0.031"), "ok": true, "path": "/c"},
	}
	got := inferTypes(samples)
	want := map[string]string{
		"code": typeLong, "rt": typeDouble, "ok": typeBoolean, "path": typeKeyword,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("字段 %s 的类型应为 %s，实际 %s", k, v, got[k])
		}
	}
}

// TestInferTypesNumericString 日志里 grok 取出的值都是字符串（没写 :int 后缀），
// 但 "200" 该被认成数字，否则 status 会建成 keyword，范围查询与聚合都用不了。
func TestInferTypesNumericString(t *testing.T) {
	got := inferTypes([]map[string]any{{"status": "200"}, {"status": "404"}})
	if got["status"] != typeLong {
		t.Fatalf("纯数字字符串应推断为 long，实际 %s", got["status"])
	}
}

// TestInferTypesMixedFallsBackToKeyword status 一条是 200、一条是 "-"：
// 只能判成字符串，否则那串文字无处安放。这正是要摊给用户看、允许改的原因。
func TestInferTypesMixedFallsBackToKeyword(t *testing.T) {
	got := inferTypes([]map[string]any{
		{"status": "200"},
		{"status": "-"},
		{"status": "500"},
	})
	if got["status"] != typeKeyword {
		t.Fatalf("数字与文本混用时最宽类型是 keyword，实际 %s", got["status"])
	}
}

// TestInferTypesLeadingZero 带前导零的编号（007）不该当数字：转成 long 会把 0 丢掉。
func TestInferTypesLeadingZero(t *testing.T) {
	got := inferTypes([]map[string]any{{"id": "007"}, {"id": "042"}})
	if got["id"] != typeKeyword {
		t.Fatalf("前导零编号应保持字符串，实际 %s", got["id"])
	}
}

func TestInferTypesNullIgnored(t *testing.T) {
	got := inferTypes([]map[string]any{{"a": nil}, {"a": json.Number("7")}})
	if got["a"] != typeLong {
		t.Fatalf("null 不该参与判定，实际 %s", got["a"])
	}
}

func TestCoerceDoc(t *testing.T) {
	types := map[string]string{
		"status": typeLong, "rt": typeDouble, "ok": typeBoolean, "path": typeKeyword,
	}
	doc := map[string]any{
		"status": "200", "rt": "0.25", "ok": "true", "path": "/a",
	}
	if dropped := coerceDoc(doc, types); len(dropped) != 0 {
		t.Fatalf("都能转换，不该有字段被丢弃: %v", dropped)
	}
	if doc["status"] != int64(200) {
		t.Fatalf("status 应转成 int64(200)，实际 %T %v", doc["status"], doc["status"])
	}
	if doc["rt"] != 0.25 {
		t.Fatalf("rt 应为 0.25，实际 %T %v", doc["rt"], doc["rt"])
	}
	if doc["ok"] != true {
		t.Fatalf("ok 应为 true，实际 %T %v", doc["ok"], doc["ok"])
	}
	if doc["path"] != "/a" {
		t.Fatalf("path 应保持字符串，实际 %T %v", doc["path"], doc["path"])
	}
}

// TestCoerceDocDropsUnconvertible 声明 long 但值是 "-"：丢掉这个字段而不是保留原值 ——
// 保留会让 ES 拒掉整条文档，为一条脏值赔上整条记录不划算；但丢掉了什么必须回报。
func TestCoerceDocDropsUnconvertible(t *testing.T) {
	doc := map[string]any{"status": "-", "msg": "kept"}
	dropped := coerceDoc(doc, map[string]string{"status": typeLong})
	if len(dropped) != 1 || dropped[0] != "status" {
		t.Fatalf("转不掉的字段要被丢弃并回报，实际 %v", dropped)
	}
	if _, ok := doc["status"]; ok {
		t.Fatal("转不掉的字段不该留在文档里")
	}
	if doc["msg"] != "kept" {
		t.Fatal("未声明类型的字段不该被动到")
	}
}

// TestCoerceDocToStringAvoidsFloatNoise 从 JSON 来的数字是 float64，
// 转字符串时不能写成 "200.000000"（那是值层面的污染，比类型错更难发现）。
func TestCoerceDocToStringAvoidsFloatNoise(t *testing.T) {
	doc := map[string]any{"code": float64(200), "rt": float64(0.25)}
	coerceDoc(doc, map[string]string{"code": typeKeyword, "rt": typeKeyword})
	if doc["code"] != "200" {
		t.Fatalf("整数应转成 \"200\"，实际 %v", doc["code"])
	}
	if doc["rt"] != "0.25" {
		t.Fatalf("小数应转成 \"0.25\"，实际 %v", doc["rt"])
	}
}

func TestCoerceDocNoTypesIsNoop(t *testing.T) {
	doc := map[string]any{"a": "200"}
	if dropped := coerceDoc(doc, nil); dropped != nil {
		t.Fatalf("没有声明类型时不该动任何字段: %v", dropped)
	}
	if doc["a"] != "200" {
		t.Fatalf("没有声明类型时值应原样保留，实际 %T %v", doc["a"], doc["a"])
	}
}

// TestInferTypesTimestamp 时间戳字符串应自动判为 date —— 建了 date 映射才能在 ES 里
// 按时间范围查、按时间聚合。ISO8601（带偏移）、空格分隔、logback 逗号小数、nginx 串都要认。
func TestInferTypesTimestamp(t *testing.T) {
	cases := [][]string{
		{"2026-10-14T00:00:27+08:00", "2026-10-14T01:00:00+08:00"},   // RFC3339
		{"2026-10-14 00:00:27", "2026-10-14 01:00:00"},               // 空格分隔
		{"2026-10-14 00:00:27,123", "2026-10-14 01:00:00,456"},       // logback 逗号小数
		{"02/Oct/2026:00:00:27 +0800", "02/Oct/2026:01:00:00 +0800"}, // nginx
		{"2026-10-14", "2026-10-15"},                                 // 纯日期
	}
	for _, vals := range cases {
		got := inferTypes([]map[string]any{{"ts": vals[0]}, {"ts": vals[1]}})
		if got["ts"] != typeDate {
			t.Errorf("%q 应推断为 date，实际 %s", vals[0], got["ts"])
		}
	}
}

// TestInferTypesTimestampMixedFallsBack 时间戳混了占位符/非时间值 → keyword：判错类型
// （整条文档被拒）的代价远大于判宽。
func TestInferTypesTimestampMixedFallsBack(t *testing.T) {
	got := inferTypes([]map[string]any{{"ts": "2026-10-14T00:00:27+08:00"}, {"ts": "-"}})
	if got["ts"] != typeKeyword {
		t.Fatalf("时间戳混占位符应退回 keyword，实际 %s", got["ts"])
	}
}

// TestInferTypesEpochMillisIsDate 毫秒时间戳（13 位整数）优先判 date：日志里 epoch_ms 是
// 常见的时间字段，判成 long 就没法按时间范围查、按时间聚合；字符串与 JSON 原生数字都要认。
// 不是毫秒时间戳的数仍走常规判定（普通整数 → long）。
func TestInferTypesEpochMillisIsDate(t *testing.T) {
	got := inferTypes([]map[string]any{{"epoch_ms": "1791907227528"}, {"epoch_ms": "1791907228000"}})
	if got["epoch_ms"] != typeDate {
		t.Fatalf("毫秒时间戳字符串应判 date，实际 %s", got["epoch_ms"])
	}
	got2 := inferTypes([]map[string]any{
		{"ts": json.Number("1791907227528")}, {"ts": json.Number("1791907228000")},
	})
	if got2["ts"] != typeDate {
		t.Fatalf("毫秒时间戳（JSON 数字）应判 date，实际 %s", got2["ts"])
	}
	got3 := inferTypes([]map[string]any{{"n": "42"}, {"n": "43"}})
	if got3["n"] != typeLong {
		t.Fatalf("普通整数应保持 long，实际 %s", got3["n"])
	}
}

// TestMappingDateCarriesFormat date 字段必须带宽松 format：日志时间戳形式五花八门，
// ES 默认只认 ISO8601，不带 format 会让空格分隔 / nginx 这些值写入时整条被拒。
func TestMappingDateCarriesFormat(t *testing.T) {
	props := buildMappingProps(map[string]string{"ts": typeDate, "n": typeLong})
	ts := props["ts"].(map[string]any)
	if ts["type"] != typeDate || ts["format"] != dateMappingFormat {
		t.Fatalf("date 字段应带宽松 format: %v", ts)
	}
	n := props["n"].(map[string]any)
	if _, ok := n["format"]; ok {
		t.Fatalf("非 date 字段不该带 format: %v", n)
	}
}
