// grok.go 一个不依赖集群的 grok 解析引擎（RE2 方言）。
//
// 语义要点：
//   - 整行匹配优先：pattern 编译为 ^(?:...)$，只有覆盖整行才算命中。
//     这与 Logstash / ES grok processor 的用法一致（避免把半行日志「解析成功」）。
//   - 整行不中但局部命中时返回「部分命中」（Partial=true）：这是排错最有用的信号，
//     用户据此知道该在 pattern 末尾补 GREEDYDATA 之类的兜底，而不是一头雾水。
//   - 多条 pattern 按顺序尝试，第一条整行命中的胜出。
//   - 字段名重复出现时后者覆盖前者，位置保持首次出现的位置（与 Logstash 一致）；
//     内部用唯一组名承载，规避 Go regexp 不允许同名捕获组的限制。
//   - %{NAME:field:int} / %{NAME:field:float} 做数值规范化（失败则原样返回，不谎称类型）。
package grok

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxDepth 递归展开的最大层级（防自定义 pattern 互相引用导致栈溢出）。
const maxDepth = 24

// refRe 匹配 %{NAME} / %{NAME:field} / %{NAME:field:type}。
var refRe = regexp.MustCompile(`%\{([A-Za-z_][A-Za-z0-9_]*)(?::([A-Za-z_][A-Za-z0-9_.\-]*))?(?::([A-Za-z]+))?\}`)

// namedGroupRe 匹配手写在 pattern 里的命名捕获组（(?P<name> 与 (?<name> 两种写法）。
var namedGroupRe = regexp.MustCompile(`\(\?P?<([A-Za-z_][A-Za-z0-9_]*)>`)

// internalPrefix 内部组名前缀：与用户字段名隔离，保证组名唯一。
const internalPrefix = "_grok"

// Field 解析出的一条字段。
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"` // int / float（无类型后缀或转换失败时为空）
}

// Match 一次解析结果；Partial 表示只有局部命中（字段值可能只是整行的一个片段）。
type Match struct {
	Fields     []Field
	PatternIdx int
	Pattern    string
	Partial    bool
}

// Matcher 编译好的模式集合。
type Matcher struct {
	patterns []*compiledPattern
}

type fieldSpec struct {
	name string
	typ  string
}

type compiledPattern struct {
	raw        string
	full       *regexp.Regexp // ^(?:...)$
	loose      *regexp.Regexp // 无锚，用于诊断「部分命中」
	order      []string       // 内部组名，按首次出现顺序
	specs      map[string]fieldSpec
	groupIndex map[string]int
}

// Compile 编译一组 pattern；defs 为用户自定义定义（优先于内置同名项）。
func Compile(patterns []string, defs map[string]string) (*Matcher, error) {
	var list []string
	for _, p := range patterns {
		if s := strings.TrimSpace(p); s != "" {
			list = append(list, s)
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("请至少填写一条 grok pattern")
	}
	m := &Matcher{}
	for _, p := range list {
		cp, err := compileOne(p, defs)
		if err != nil {
			return nil, err
		}
		m.patterns = append(m.patterns, cp)
	}
	return m, nil
}

// Names 内置 pattern 名（排序后），供提示词与界面展示。
func Names() []string {
	out := make([]string, 0, len(Builtin))
	for k := range Builtin {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Parse 逐条尝试 pattern；整行命中即返回，全不中时返回第一个「部分命中」，都无则返回 nil。
func (m *Matcher) Parse(text string) *Match {
	var partial *Match
	for i, cp := range m.patterns {
		if cp.full.MatchString(text) {
			return &Match{
				Fields:     cp.extract(cp.full.FindStringSubmatch(text)),
				PatternIdx: i, Pattern: cp.raw,
			}
		}
		if partial == nil && cp.loose.MatchString(text) {
			partial = &Match{
				Fields:     cp.extract(cp.loose.FindStringSubmatch(text)),
				PatternIdx: i, Pattern: cp.raw, Partial: true,
			}
		}
	}
	return partial
}

// extract 按内部组顺序取字段；同名字段后者覆盖、位置不变。
func (cp *compiledPattern) extract(sub []string) []Field {
	pos := map[string]int{}
	out := make([]Field, 0, len(cp.order))
	for _, g := range cp.order {
		i, ok := cp.groupIndex[g]
		if !ok || i >= len(sub) {
			continue
		}
		spec := cp.specs[g]
		v := sub[i]
		if v == "" { // 可选组未参与匹配：不产出空字段
			continue
		}
		val, typ := normalize(v, spec.typ)
		f := Field{Name: spec.name, Value: val, Type: typ}
		if at, dup := pos[f.Name]; dup {
			out[at] = f
		} else {
			pos[f.Name] = len(out)
			out = append(out, f)
		}
	}
	return out
}

// normalize 按类型后缀做规范化；转换失败时原样返回并不标注类型。
func normalize(v, typ string) (string, string) {
	s := strings.TrimSpace(v)
	switch typ {
	case "int":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return strconv.FormatInt(n, 10), "int"
		}
	case "float":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return strconv.FormatFloat(f, 'f', -1, 64), "float"
		}
	}
	return v, ""
}

// ---------------- 编译 ----------------

type compiler struct {
	defs  map[string]string
	seq   int
	order []string
	specs map[string]fieldSpec
}

func compileOne(raw string, defs map[string]string) (*compiledPattern, error) {
	c := &compiler{defs: merged(defs), specs: map[string]fieldSpec{}}
	body, err := c.expand(raw, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", truncate(raw, 120), err)
	}
	body = c.uniqueNamedGroups(body)
	full, err := regexp.Compile("^(?:" + body + ")$")
	if err != nil {
		return nil, fmt.Errorf("%s: 正则编译失败: %v（Go 用 RE2 方言：不支持后顾 (?<=)、原子组 (?>)、反向引用 \\1）", truncate(raw, 120), err)
	}
	loose, err := regexp.Compile("(?:" + body + ")")
	if err != nil {
		return nil, fmt.Errorf("%s: 正则编译失败: %v", truncate(raw, 120), err)
	}
	idx := map[string]int{}
	for i, g := range full.SubexpNames() {
		if i > 0 && g != "" {
			idx[g] = i
		}
	}
	return &compiledPattern{
		raw: raw, full: full, loose: loose,
		order: c.order, specs: c.specs, groupIndex: idx,
	}, nil
}

// merged 合并内置与用户定义：用户优先，便于按现场口味覆盖内置语义。
func merged(defs map[string]string) map[string]string {
	out := make(map[string]string, len(Builtin)+len(defs))
	for k, v := range Builtin {
		out[k] = v
	}
	for k, v := range defs {
		k = strings.TrimSpace(k)
		if k != "" && strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	return out
}

// expand 展开 %{...} 引用；path 记录当前引用链用于环检测。
func (c *compiler) expand(src string, depth int, path []string) (string, error) {
	if depth > maxDepth {
		return "", fmt.Errorf("pattern 引用层级超过 %d 层，请检查自定义 pattern 是否互相引用", maxDepth)
	}
	locs := refRe.FindAllStringSubmatchIndex(src, -1)
	if len(locs) == 0 {
		return src, nil
	}
	var b strings.Builder
	last := 0
	for _, m := range locs {
		b.WriteString(src[last:m[0]])
		name := src[m[2]:m[3]]
		field, typ := "", ""
		if m[4] >= 0 {
			field = src[m[4]:m[5]]
		}
		if m[6] >= 0 {
			typ = src[m[6]:m[7]]
		}
		for _, prev := range path {
			if prev == name {
				return "", fmt.Errorf("%%{%s} 存在循环引用（%s）", name, strings.Join(append(path, name), " → "))
			}
		}
		def, ok := c.defs[name]
		if !ok {
			return "", fmt.Errorf("未定义的 pattern %%{%s}（检查拼写，或在「自定义 pattern」里补上）", name)
		}
		next := append(append([]string{}, path...), name)
		inner, err := c.expand(def, depth+1, next)
		if err != nil {
			return "", err
		}
		if field != "" {
			b.WriteString("(?P<" + c.newGroup(field, typ) + ">" + inner + ")")
		} else {
			b.WriteString("(?:" + inner + ")")
		}
		last = m[1]
	}
	b.WriteString(src[last:])
	return b.String(), nil
}

// newGroup 分配唯一内部组名（Go regexp 不允许同名捕获组）。
func (c *compiler) newGroup(name, typ string) string {
	g := internalPrefix + strconv.Itoa(c.seq)
	c.seq++
	if _, ok := c.specs[g]; !ok {
		c.order = append(c.order, g)
	}
	c.specs[g] = fieldSpec{name: name, typ: typ}
	return g
}

// uniqueNamedGroups 把 pattern 里手写的命名组改写成唯一内部名，避免与展开出的组重名。
func (c *compiler) uniqueNamedGroups(body string) string {
	return namedGroupRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := namedGroupRe.FindStringSubmatch(m)
		if strings.HasPrefix(sub[1], internalPrefix) {
			return m
		}
		return "(?P<" + c.newGroup(sub[1], "") + ">"
	})
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
