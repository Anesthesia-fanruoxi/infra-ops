// import_types.go 字段类型推断，以及写入前按声明类型转换值。
//
// 为什么需要这一步：ES 的 dynamic mapping 是「首次出现的类型定终身」。
// 日志里 status 一条是 200、一条是 "-"，后到的那条会被整条拒掉，
// 报错还很不直观（failed to parse field [status] of type [long]）。
// 所以预览阶段把推断结果摊开给用户确认/修改，写入前再按最终类型转换值，
// 让「用户看到的类型」与「ES 里建的 mapping」严格一致。
package es

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// 可选的字段类型（与 ES mapping 对应）。
const (
	typeLong    = "long"
	typeDouble  = "double"
	typeBoolean = "boolean"
	typeKeyword = "keyword" // 不分词：能聚合、能排序、能精确匹配，日志字段的默认选择
	typeText    = "text"    // 分词：用于全文检索，代价是不能聚合
	typeDate    = "date"
)

// typeOptions 供界面展示的可选类型（顺序即展示顺序）。
var typeOptions = []string{typeKeyword, typeLong, typeDouble, typeBoolean, typeText, typeDate}

// inferTypes 从采样文档推断每个字段的类型。
//
// 「宽者胜」：数字与非数字字符串混在一个字段里（status 有 200 也有 "-"），
// 只能判成字符串，否则那串文字无处安放。这也是要给用户看、让用户改的原因 ——
// 他一眼就知道 status 该是 keyword，而推断只能保守。
func inferTypes(samples []map[string]any) map[string]string {
	acc := map[string]*typeAcc{}
	for _, doc := range samples {
		for k, v := range doc {
			a := acc[k]
			if a == nil {
				a = &typeAcc{}
				acc[k] = a
			}
			a.observe(v)
		}
	}
	out := make(map[string]string, len(acc))
	for k, a := range acc {
		out[k] = a.result()
	}
	return out
}

type typeAcc struct {
	total    int // 参与判定的值个数（null 不计）
	numbers  int // 能当数字看的
	fraction int // 其中带小数部分的
	bools    int
	dates    int // 能当时间戳看的字符串
}

func (a *typeAcc) observe(v any) {
	// 毫秒时间戳优先判 date —— 日志里 epoch_ms 是常见的时间字段，判成 long 就没法按时间查/聚合。
	// 不是毫秒时间戳的数再走下面的常规判定（数字仍按 long/double 走）。
	if _, ok := epochMillis(v); ok {
		a.total++
		a.dates++
		return
	}
	switch t := v.(type) {
	case nil:
		return // 缺失/null 不参与判定
	case bool:
		a.total++
		a.bools++
	case json.Number:
		a.total++
		a.numbers++
		if f, err := t.Float64(); err == nil && f != math.Trunc(f) {
			a.fraction++
		}
	case float64:
		a.total++
		a.numbers++
		if t != math.Trunc(t) {
			a.fraction++
		}
	case float32:
		a.total++
		a.numbers++
		if float64(t) != math.Trunc(float64(t)) {
			a.fraction++
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		a.total++
		a.numbers++
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			a.total++ // 空串是「有值但空」，按字符串算
			return
		}
		switch {
		case numericString(s):
			a.total++
			a.numbers++
			if isFractionalString(s) {
				a.fraction++
			}
		case s == "true" || s == "false":
			a.total++
			a.bools++
		case looksLikeDate(s):
			a.total++
			a.dates++
		default:
			a.total++
		}
	default:
		a.total++
	}
}

func (a *typeAcc) result() string {
	if a.total == 0 {
		return typeKeyword
	}
	switch {
	case a.bools == a.total:
		return typeBoolean
	case a.numbers == a.total && a.fraction > 0:
		return typeDouble
	case a.numbers == a.total:
		return typeLong
	// 全部值都像时间戳才判 date：时间戳字段自动建 date 映射，ES 里才能按时间范围查、按时间聚合。
	// 混了非日期值就退回 keyword —— 判错类型的代价（整条文档被拒）比判宽大得多。
	case a.dates == a.total && a.dates > 0:
		return typeDate
	}
	return typeKeyword
}

// dateMappingFormat date 字段建映射时携带的宽松 format。
// ES 默认只认 ISO8601（strict_date_optional_time），而日志时间戳五花八门：空格分隔、
// 逗号小数（logback）、斜杠、nginx 的 02/Oct/2026:00:00:27 +0800 —— 不带这串 format 的话，
// 这些值写入时会整条文档被拒。
const dateMappingFormat = "strict_date_optional_time||epoch_millis||yyyy-MM-dd HH:mm:ss||yyyy-MM-dd HH:mm:ss,SSS||yyyy/MM/dd HH:mm:ss||dd/MMM/yyyy:HH:mm:ss Z"

// dateLayouts 自动判 date 的时间戳布局，与 dateMappingFormat 覆盖同一批形式。
// 注意都是「能明确断定为时间」的形状：月名、冒号、偏移量等特征齐备，
// 不会把 10/14/2026 这类纯数字斜杠串误判成日期。
var dateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
	"2006-01-02",
	"02/Jan/2006:15:04:05 -0700", // nginx $time_local
}

// looksLikeDate 判断字符串是否像时间戳。logback 用逗号做小数分隔，先归一化成点再解析
// （Go 的时间解析在秒后容忍小数，但只认小数点）。
func looksLikeDate(s string) bool {
	if len(s) < 8 || len(s) > 40 {
		return false
	}
	if i := strings.IndexByte(s, ','); i > 0 {
		s = s[:i] + "." + s[i+1:]
	}
	for _, layout := range dateLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// epochMillis 毫秒时间戳判定：整数且落在 [1e12, 1e14) —— 约 2001 年至 5138 年，
// 13 位毫秒值都在此区间。字符串与 JSON 数字都认（日志里 grok 取的是字符串，
// JSONL 里则是原生数字，两者都该判成 date，date 映射自带 epoch_millis 格式）。
func epochMillis(v any) (int64, bool) {
	const lo, hi = int64(1e12), int64(1e14)
	hit := func(n int64) bool { return n >= lo && n < hi }
	switch t := v.(type) {
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, hit(n)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, hit(n)
		}
	case float64:
		if t == math.Trunc(t) {
			n := int64(t)
			return n, hit(n)
		}
	case float32:
		if f := float64(t); f == math.Trunc(f) {
			n := int64(f)
			return n, hit(n)
		}
	case int64:
		return t, hit(t)
	case int:
		return int64(t), hit(int64(t))
	}
	return 0, false
}

// numericString 判断字符串是不是数字。
// 「007」这类带前导零的不算 —— 那通常是编号而不是数值，转成数字会把 0 丢掉。
func numericString(s string) bool {
	if len(s) > 1 && s[0] == '0' && s[1] != '.' {
		return false
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

func isFractionalString(s string) bool {
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && f != math.Trunc(f)
}

// applyFieldPlan 校验字段映射（预览第二列）并折算成「最终字段名 → 类型」。
//
// 映射语义：把第一列（系统生成字段）替换为第二列（目标名）；目标为空 = 不改。
// 冲突（两个字段映射到同名、或目标名与其它保留字段重名）直接拒绝 —— 写进文档会互相覆盖。
func applyFieldPlan(types, renames map[string]string) (map[string]string, map[string]string, error) {
	clean := map[string]string{}
	for src, dst := range renames {
		src, dst = strings.TrimSpace(src), strings.TrimSpace(dst)
		if src == "" || dst == "" || src == dst {
			continue
		}
		clean[src] = dst
	}
	if len(clean) == 0 {
		return types, nil, nil
	}
	final := make(map[string]string, len(types))
	for k, v := range types {
		if d, ok := clean[k]; ok {
			final[d] = v
		} else {
			final[k] = v
		}
	}
	if len(final) != len(types) {
		return nil, nil, fmt.Errorf("字段映射冲突：目标名与其它字段重名（写入会互相覆盖），请换一个名字")
	}
	return final, clean, nil
}

// applyFieldRenames 按映射表改写文档字段名（第二列有值时替换第一列）。
func applyFieldRenames(doc map[string]any, renames map[string]string) {
	for src, dst := range renames {
		v, ok := doc[src]
		if !ok {
			continue
		}
		doc[dst] = v
		delete(doc, src)
	}
}

// coerceDoc 原地按声明类型转换字段值，返回被丢掉的字段名。
//
// 转不掉的字段直接丢：例如声明 status 是 long 但某行给的是 "-"，
// 保留原值会让 ES 拒掉**整条文档**，为一条脏值赔上整条记录不划算。
// 丢掉哪些字段要回报给用户，不能静默。
func coerceDoc(doc map[string]any, types map[string]string) []string {
	if len(types) == 0 {
		return nil
	}
	var dropped []string
	for k, want := range types {
		v, ok := doc[k]
		if !ok || v == nil {
			continue
		}
		if nv, ok := coerceValue(v, want); ok {
			doc[k] = nv
			continue
		}
		delete(doc, k)
		dropped = append(dropped, k)
	}
	return dropped
}

func coerceValue(v any, want string) (any, bool) {
	switch want {
	case typeLong:
		return toInt64(v)
	case typeDouble:
		return toFloat64(v)
	case typeBoolean:
		return toBool(v)
	case typeKeyword, typeText:
		return toStr(v), true
	case typeDate:
		s := strings.TrimSpace(toStr(v))
		return s, s != ""
	}
	return v, true // 未知类型：不动
}

func toInt64(v any) (any, bool) {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
		if f, err := t.Float64(); err == nil && f == math.Trunc(f) {
			return int64(f), true
		}
		return nil, false
	case float64:
		if t == math.Trunc(t) {
			return int64(t), true
		}
		return nil, false
	case float32:
		if f := float64(t); f == math.Trunc(f) {
			return int64(f), true
		}
		return nil, false
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		s := strings.TrimSpace(t)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && f == math.Trunc(f) {
			return int64(f), true
		}
	}
	return nil, false
}

func toFloat64(v any) (any, bool) {
	switch t := v.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
		return nil, false
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f, true
		}
	}
	return nil, false
}

func toBool(v any) (any, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		if b, err := strconv.ParseBool(strings.TrimSpace(t)); err == nil {
			return b, true
		}
	case json.Number:
		if f, err := t.Float64(); err == nil && (f == 0 || f == 1) {
			return f == 1, true
		}
	case float64:
		if t == 0 || t == 1 {
			return t == 1, true
		}
	}
	return nil, false
}

// toStr 转字符串；数字要避免 200 变成 "200.000000" 这种写法。
func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return toStr(float64(t))
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}
