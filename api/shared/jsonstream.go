// jsonstream.go 流式 JSON 文本的字符串值增量扫描器。
//
// 用途：模型按约定「只输出 JSON」时，边生成边把其中的字符串值（字段、数组元素）
// 按路径提取成增量，供接口层实时推给前端做草稿展示。
//
// 它只服务展示，不是权威解析：对非法输入宽容跳过（不 panic、不发假事件），
// 最终结果仍以流结束后的完整 JSON 解析为准。转义序列（\n、\uXXXX、代理对）
// 可能被切在两个增量块之间，故解析状态全部跨 Feed 保留。
package shared

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// JSONStringEvent 一段字符串值增量。Path 是完整路径（sql / findings[0] / a[0].b）；
// Field/Index 是顶层字段与数组下标的便捷解析（findings[3] → findings,3），
// 嵌套路径（a[0].b）Index 记 -1，需要时由调用方读 Path。
type JSONStringEvent struct {
	Path  string
	Field string
	Index int
	Delta string
}

// 容器帧的解析模式：决定下一个有效字符怎么理解。
const (
	modeObjKey   = iota // 对象内：期待键（或 }）
	modeObjColon        // 对象内：键已读，期待 :
	modeObjValue        // 对象内：期待值
	modeObjAfter        // 对象内：值已完，期待 , 或 }
	modeArrValue        // 数组内：期待值（或 ]）
	modeArrAfter        // 数组内：值已完，期待 , 或 ]
)

// jsonFrame 一个容器的解析现场。
type jsonFrame struct {
	isArray bool
	mode    int
	label   string // 该容器在父级中的定位段："key" 或 "[i]"，用于拼路径
	idx     int    // 数组：下一个元素下标
	key     string // 对象：当前值的键（冒号后落定）
}

// jsonLiteralRe 合法 JSON 字面量（数字 / true / false / null）。
// 字面量收尾时校验用：模型输出前的 markdown 围栏、噪声文本不是合法字面量，
// 不能让它把结构推进掉。
var jsonLiteralRe = regexp.MustCompile(`^(true|false|null|-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?)$`)

// JSONStreamScanner 流式 JSON 扫描器；零值即可使用。
type JSONStreamScanner struct {
	stack    []jsonFrame
	rootDone bool // 顶层值已完成（此后的内容不再产生事件）

	// 字符串解析态
	inStr    bool
	strKey   bool   // 当前字符串是键还是值
	esc      bool   // 上一字符是反斜杠
	inU      bool   // 正在收集 \uXXXX 的十六进制
	uDigits  string // \u 后已收集的十六进制
	pendHigh rune   // 已解码的代理项高位，等待紧随的低位
	strPath  string // 当前值字符串的路径（值开始时算好）
	keyBuf   strings.Builder

	// 数字/字面量
	inLit  bool
	litBuf string

	// 增量攒批：同一路径在一个 Feed 内的字符合并为一段，避免逐字符推帧
	buf    strings.Builder
	path   string
	events []JSONStringEvent
}

// Feed 喂入一段增量文本；本段产生的字符串增量以事件回调返回（可能多条）。
func (s *JSONStreamScanner) Feed(chunk string, onEvent func(JSONStringEvent)) {
	for _, r := range chunk {
		s.step(r)
	}
	s.flushPending()
	if onEvent != nil {
		for _, ev := range s.events {
			onEvent(ev)
		}
	}
	s.events = s.events[:0]
}

// ---- 字符分发 ----

func (s *JSONStreamScanner) step(r rune) {
	if s.rootDone {
		return
	}
	if s.inStr {
		s.stepString(r)
		return
	}
	if s.inLit {
		if isLiteralRune(r) {
			s.litBuf += string(r)
			return
		}
		// 字面量收尾：合法才推进结构；围栏、噪声这类“伪字面量”只丢弃自己
		lit := s.litBuf
		s.litBuf = ""
		s.inLit = false
		if jsonLiteralRe.MatchString(lit) {
			s.completeValue()
		}
		s.step(r)
		return
	}
	switch s.mode() {
	case modeObjKey:
		s.stepObjKey(r)
	case modeObjColon:
		if r == ':' && s.frame() != nil {
			s.frame().key = s.keyBuf.String()
			s.frame().mode = modeObjValue
		}
	case modeObjAfter:
		switch r {
		case ',':
			s.frame().mode = modeObjKey
		case '}':
			s.popContainer()
		}
	case modeArrAfter:
		switch r {
		case ',':
			s.frame().mode = modeArrValue
		case ']':
			s.popContainer()
		}
	default: // 顶层 / 期待值
		s.stepValue(r)
	}
}

func (s *JSONStreamScanner) stepObjKey(r rune) {
	switch {
	case r == '"':
		s.startString(true)
	case r == '}':
		s.popContainer()
	}
}

func (s *JSONStreamScanner) stepValue(r rune) {
	switch {
	case r == '"':
		s.startString(false)
	case r == '{':
		s.pushContainer(false)
	case r == '[':
		s.pushContainer(true)
	case r == ']' || r == '}':
		if r == ']' && s.topIsArray() { // 空数组收尾
			s.popContainer()
		}
		// 期待值位置的错误闭合：垃圾，忽略
	case r == ',' || isJSONSpace(r):
		// 垃圾分隔：忽略
	default:
		if isLiteralStart(r) {
			s.inLit = true
			s.litBuf = string(r)
		}
		// 其余字符（markdown 围栏、说明文字）忽略
	}
}

// ---- 字符串解析 ----

func (s *JSONStreamScanner) startString(isKey bool) {
	s.inStr = true
	s.strKey = isKey
	s.esc = false
	s.inU = false
	s.uDigits = ""
	if isKey {
		s.keyBuf.Reset()
	} else {
		s.strPath = s.buildPath()
	}
}

func (s *JSONStreamScanner) stepString(r rune) {
	if s.inU {
		if isHexRune(r) {
			s.uDigits += string(r)
			if len(s.uDigits) == 4 {
				s.inU = false
				s.decodeUnicode()
			}
			return
		}
		// 非法十六进制：把序列按字面还原，当前字符另按普通字符处理
		s.inU = false
		s.emitText(`\u` + s.uDigits)
		s.uDigits = ""
		s.stepStringPlain(r)
		return
	}
	if s.esc {
		s.esc = false
		switch r {
		case 'u':
			s.inU = true
			s.uDigits = ""
		case 'n':
			s.emitRune('\n')
		case 't':
			s.emitRune('\t')
		case 'r':
			s.emitRune('\r')
		case 'b':
			s.emitRune('\b')
		case 'f':
			s.emitRune('\f')
		default: // \" \\ \/ 与未知转义：原样输出
			s.emitRune(r)
		}
		return
	}
	s.stepStringPlain(r)
}

func (s *JSONStreamScanner) stepStringPlain(r rune) {
	switch r {
	case '\\':
		s.esc = true
	case '"':
		s.endString()
	default:
		s.emitRune(r)
	}
}

// decodeUnicode 解出一个 \uXXXX；代理对需要等下一个 \uXXXX 来合并。
func (s *JSONStreamScanner) decodeUnicode() {
	v, err := strconv.ParseUint(s.uDigits, 16, 32)
	s.uDigits = ""
	if err != nil {
		return
	}
	r := rune(v)
	if s.pendHigh != 0 && r >= 0xDC00 && r <= 0xDFFF {
		high := s.pendHigh
		s.pendHigh = 0
		s.emitRune(utf16.DecodeRune(high, r))
		return
	}
	if r >= 0xD800 && r <= 0xDBFF {
		if s.pendHigh != 0 { // 上一个高位没等到低位：作废
			s.pendHigh = 0
			s.emitRune('\uFFFD')
		}
		s.pendHigh = r
		return
	}
	if r >= 0xDC00 && r <= 0xDFFF { // 孤立低位
		r = '\uFFFD'
	}
	s.emitRune(r)
}

func (s *JSONStreamScanner) endString() {
	s.inStr = false
	if s.pendHigh != 0 { // 字符串里悬着代理项高位：补替换字符
		s.pendHigh = 0
		s.emitRune('\uFFFD')
	}
	if s.strKey {
		if s.frame() != nil {
			s.frame().mode = modeObjColon
		}
		return
	}
	// 字符串值完成：先冲掉攒批（跨字符串的增量不能混段），再推进容器
	s.flushPending()
	s.completeValue()
}

// emitRune/emitText 输出的是「值字符串」内容；键字符串只进键缓冲。
func (s *JSONStreamScanner) emitRune(r rune) { s.emitText(string(r)) }

func (s *JSONStreamScanner) emitText(text string) {
	if s.strKey {
		s.keyBuf.WriteString(text)
		return
	}
	if s.buf.Len() > 0 && s.path != s.strPath {
		s.flushPending() // 路径切换：把上一段先落成事件，避免混段
	}
	s.path = s.strPath
	s.buf.WriteString(text)
}

// flushPending 把攒批的增量落成事件。
func (s *JSONStreamScanner) flushPending() {
	if s.buf.Len() == 0 {
		return
	}
	field, index := parseEventPath(s.path)
	s.events = append(s.events, JSONStringEvent{Path: s.path, Field: field, Index: index, Delta: s.buf.String()})
	s.buf.Reset()
}

// ---- 结构推进 ----

func (s *JSONStreamScanner) pushContainer(isArray bool) {
	label := ""
	if top := s.frame(); top != nil {
		if top.isArray {
			label = "[" + strconv.Itoa(top.idx) + "]"
		} else {
			label = top.key
		}
	}
	f := jsonFrame{isArray: isArray, label: label}
	if isArray {
		f.mode = modeArrValue
	} else {
		f.mode = modeObjKey
	}
	s.stack = append(s.stack, f)
}

func (s *JSONStreamScanner) popContainer() {
	if len(s.stack) == 0 {
		return
	}
	s.stack = s.stack[:len(s.stack)-1]
	s.completeValue()
}

// completeValue 一个值（标量或子容器）完成：所在容器推进到下一位。
func (s *JSONStreamScanner) completeValue() {
	top := s.frame()
	if top == nil {
		s.rootDone = true
		return
	}
	if top.isArray {
		top.idx++
		top.mode = modeArrAfter
	} else {
		top.mode = modeObjAfter
	}
}

func (s *JSONStreamScanner) frame() *jsonFrame {
	if len(s.stack) == 0 {
		return nil
	}
	return &s.stack[len(s.stack)-1]
}

func (s *JSONStreamScanner) topIsArray() bool {
	f := s.frame()
	return f != nil && f.isArray
}

func (s *JSONStreamScanner) mode() int {
	if f := s.frame(); f != nil {
		return f.mode
	}
	return modeObjValue // 顶层视作「期待值」；rootDone 由 step 前置判断
}

// ---- 路径 ----

// buildPath 当前值字符串的完整路径：容器链的定位段 + 所在容器的键/下标。
func (s *JSONStreamScanner) buildPath() string {
	var b strings.Builder
	for _, f := range s.stack {
		b.WriteString(f.label)
	}
	if top := s.frame(); top != nil {
		if top.isArray {
			b.WriteString("[" + strconv.Itoa(top.idx) + "]")
		} else if top.key != "" {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(top.key)
		}
	}
	return b.String()
}

// parseEventPath 解析事件路径的顶层字段与数组下标。
//
//	sql            → sql, -1
//	findings[3]    → findings, 3
//	a[0].b / a.b   → a, -1（嵌套路径按 Path 原样交给调用方）
func parseEventPath(path string) (string, int) {
	if open := strings.IndexByte(path, '['); open > 0 && !strings.Contains(path, ".") && strings.HasSuffix(path, "]") {
		if idx, err := strconv.Atoi(path[open+1 : len(path)-1]); err == nil {
			return path[:open], idx
		}
	}
	field := path
	if i := strings.IndexAny(path, "[."); i >= 0 {
		field = path[:i]
	}
	return field, -1
}

// ---- 字符判定 ----

func isJSONSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// isLiteralStart 合法 JSON 字面量的首字符：数字、符号、true/false/null 的首字母。
// 只有这些字符才进入字面量态——否则 markdown 围栏、说明文字会误吞结构。
func isLiteralStart(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case r == '-' || r == '+' || r == '.':
		return true
	case r == 't' || r == 'f' || r == 'n':
		return true
	}
	return false
}

// isLiteralRune 字面量内部可出现的字符。
func isLiteralRune(r rune) bool {
	switch {
	case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	case r == '.' || r == '+' || r == '-':
		return true
	}
	return false
}
