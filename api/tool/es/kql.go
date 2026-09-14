// KQL 词法与递归下降语法分析。本文件只做解析，不接触字段类型；
// 字段类型感知与 DSL 生成在 dsl.go（B7）完成。
// 语法 §8.2：优先级 NOT > AND > OR，空格=隐式 AND，括号可覆盖。
package es

import (
	"fmt"
	"strings"
)

// kqlErr KQL 编译期错误（§14.9 code 4001 语法 / 4002 语义）。
// 语义错误（字段不存在、算子与类型不匹配）实际由 dsl.go 在拿到 Schema 后判，
// 故此处只产生 4001；Pos/Len 用于前端定位。
type kqlErr struct {
	msg string
	pos int
	len int
}

func (e *kqlErr) Error() string {
	return fmt.Sprintf("%s（位置 %d）", e.msg, e.pos)
}

func syntaxErr(pos int, format string, args ...interface{}) *kqlErr {
	return &kqlErr{msg: fmt.Sprintf(format, args...), pos: pos, len: 1}
}

// ---------- 词法 ----------

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokQuoted // 引号包裹的值（内容不参与逻辑词识别）
	tokOp
	tokLogic
	tokLParen
	tokRParen
)

// token 词法单元，携带位置与解转义后的值。
type token struct {
	kind     tokKind
	pos      int
	text     string // ident: 字段/值文字；quoted: 剥壳后内容；op/logic: 运算符/逻辑词
	wildcard bool   // ident 内是否含未转义 * 或 ?（通配符）
	quoted   bool   // 原始是否为引号包裹
}

// lexer 单遍扫描器；保留字符集见 §8.3。
type lexer struct {
	src  string
	pos  int
	toks []token
	perr *kqlErr
}

const reservedChars = "*?=!<>()\"',\\: "

func newLexer(src string) *lexer {
	return &lexer{src: src}
}

// scan 完成整段词法分析。返回 token 序列（以 tokEOF 结尾）；遇错误停在首个错误。
func (l *lexer) scan() ([]token, *kqlErr) {
	for l.perr == nil {
		l.skipSpace()
		if l.pos >= len(l.src) {
			l.toks = append(l.toks, token{kind: tokEOF, pos: l.pos})
			return l.toks, nil
		}
		l.next()
	}
	return l.toks, l.perr
}

func (l *lexer) skipSpace() {
	for l.pos < len(l.src) && l.src[l.pos] == ' ' {
		l.pos++
	}
}

func (l *lexer) next() {
	start := l.pos
	ch := l.src[l.pos]

	switch {
	case ch == '(':
		l.toks = append(l.toks, token{kind: tokLParen, pos: start})
		l.pos++
	case ch == ')':
		l.toks = append(l.toks, token{kind: tokRParen, pos: start})
		l.pos++
	case ch == '"' || ch == '\'':
		l.scanQuoted(ch)
	case ch == ':':
		l.scanOp()
	case ch == '!':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
			l.scanOp()
		} else {
			// 单独 ! 是逻辑 NOT
			l.toks = append(l.toks, token{kind: tokLogic, pos: start, text: "not"})
			l.pos++
		}
	case strings.ContainsRune("=<>", rune(ch)):
		l.scanOp()
	case strings.ContainsRune("&|", rune(ch)):
		l.scanLogicSymbol()
	default:
		l.scanIdent()
	}
}

// scanQuoted 扫描引号包裹的值；剥壳前校验长度，未知转义报 4001。
func (l *lexer) scanQuoted(quote byte) {
	start := l.pos
	l.pos++ // 跳过开引号
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == quote:
			l.pos++
			val := sb.String()
			if val == "" {
				l.perr = syntaxErr(start, "空的值")
				return
			}
			l.toks = append(l.toks, token{kind: tokQuoted, pos: start, text: val, quoted: true})
			return
		case c == '\\':
			if !l.consumeEscape(&sb, start) {
				return
			}
		default:
			sb.WriteByte(c)
			l.pos++
		}
	}
	l.perr = syntaxErr(start, "引号未闭合")
}

func (l *lexer) scanOp() {
	start := l.pos
	two := ""
	if l.pos+1 < len(l.src) {
		two = l.src[l.pos : l.pos+2]
	}
	switch two {
	case "!=", ">=", "<=":
		l.toks = append(l.toks, token{kind: tokOp, pos: start, text: two})
		l.pos += 2
		return
	}
	one := l.src[l.pos : l.pos+1]
	switch one {
	case ":", "=", ">", "<", "!":
		l.toks = append(l.toks, token{kind: tokOp, pos: start, text: one})
		l.pos++
		return
	}
	l.perr = syntaxErr(start, "无法识别的算子 %q", one)
}

func (l *lexer) scanLogicSymbol() {
	start := l.pos
	switch {
	case l.pos+1 < len(l.src) && l.src[l.pos:l.pos+2] == "&&":
		l.toks = append(l.toks, token{kind: tokLogic, pos: start, text: "and"})
		l.pos += 2
	case l.pos+1 < len(l.src) && l.src[l.pos:l.pos+2] == "||":
		l.toks = append(l.toks, token{kind: tokLogic, pos: start, text: "or"})
		l.pos += 2
	default:
		l.perr = syntaxErr(start, "无法识别的逻辑符号 %q", l.src[l.pos:])
	}
}

// scanIdent 扫描标识符 / 裸值。内部允许未转义 * ? 作为通配符。
func (l *lexer) scanIdent() {
	start := l.pos
	var sb strings.Builder
	wildcard := false
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\\':
			if !l.consumeEscape(&sb, start) {
				return
			}
		case c == '*' || c == '?':
			wildcard = true
			sb.WriteByte(c)
			l.pos++
		case strings.ContainsRune(reservedChars, rune(c)):
			goto done
		default:
			sb.WriteByte(c)
			l.pos++
		}
	}
done:
	text := sb.String()
	// 保留字符（如 `,`）落到此处时一个字符都未消费，pos 不前进会导致死循环 —— 必须报错。
	if text == "" && !wildcard {
		l.perr = syntaxErr(start, "无法识别的字符 %q", string(l.src[start]))
		return
	}
	// 识别逻辑词（大小写不敏感）；独立词才作逻辑。
	if low := strings.ToLower(text); low == "and" || low == "or" || low == "not" {
		if !wildcard {
			l.toks = append(l.toks, token{kind: tokLogic, pos: start, text: low})
			return
		}
	}
	l.toks = append(l.toks, token{kind: tokIdent, pos: start, text: text, wildcard: wildcard})
}

// consumeEscape 处理 "\x"：保留字符→字面量；"\\"→单个反斜杠；其余未知转义报 4001。
func (l *lexer) consumeEscape(sb *strings.Builder, tokStart int) bool {
	l.pos++ // 跳过反斜杠
	if l.pos >= len(l.src) {
		l.perr = syntaxErr(tokStart, "转义符后缺少字符")
		return false
	}
	c := l.src[l.pos]
	if c == '\\' {
		sb.WriteByte('\\')
		l.pos++
		return true
	}
	if !strings.ContainsRune(reservedChars, rune(c)) {
		l.perr = syntaxErr(l.pos-1, "无效转义 \\%c，合法转义集：* ? : = ! < > ( ) \\\" ' , 与空格", c)
		return false
	}
	sb.WriteByte(c)
	l.pos++
	return true
}

// ---------- 语法 ----------

type nodeKind int

const (
	nodeTerm nodeKind = iota
	nodeNot
	nodeAnd
	nodeOr
)

// astNode 语法树节点。
type astNode struct {
	kind  nodeKind
	left  *astNode
	right *astNode

	// nodeTerm 载荷
	field    string // 空串 = 裸值
	op       string
	value    string
	wildcard bool
	quoted   bool
	star     bool // 单独 * ：匹配所有
	pos      int
}

// parser 递归下降，回溯式（kql 量小，简单可靠）。
type parser struct {
	toks []token
	idx  int
}

func parseKQL(input string) (*astNode, *kqlErr) {
	lx := newLexer(input)
	toks, err := lx.scan()
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	node, perr := p.parseQuery()
	if perr != nil {
		return nil, perr
	}
	if p.peek().kind != tokEOF {
		t := p.peek()
		return nil, syntaxErr(t.pos, "多余的内容 %q", t.text)
	}
	return node, nil
}

func (p *parser) peek() token { return p.toks[p.idx] }

func (p *parser) advance() token {
	t := p.toks[p.idx]
	if t.kind != tokEOF {
		p.idx++
	}
	return t
}

// parseQuery := orExpr
func (p *parser) parseQuery() (*astNode, *kqlErr) {
	return p.parseOr()
}

func (p *parser) parseOr() (*astNode, *kqlErr) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tokLogic || t.text != "or" {
			break
		}
		p.advance()
		right, rerr := p.parseAnd()
		if rerr != nil {
			return nil, rerr
		}
		left = &astNode{kind: nodeOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (*astNode, *kqlErr) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind == tokEOF || t.kind == tokRParen {
			break
		}
		if t.kind == tokLogic {
			if t.text == "or" {
				break // 交给上层 parseOr
			}
			if t.text == "and" {
				p.advance()
			} else { // not，但已由 parseUnary 处理；理论不可达
				p.advance()
			}
		}
		right, rerr := p.parseUnary()
		if rerr != nil {
			return nil, rerr
		}
		left = &astNode{kind: nodeAnd, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (*astNode, *kqlErr) {
	t := p.peek()
	if t.kind == tokLogic && t.text == "not" {
		p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &astNode{kind: nodeNot, left: operand}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*astNode, *kqlErr) {
	t := p.peek()
	switch t.kind {
	case tokLParen:
		p.advance()
		inner, err := p.parseQuery()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			return nil, syntaxErr(p.peek().pos, "缺少右括号")
		}
		p.advance()
		return inner, nil
	case tokIdent:
		return p.parseTerm()
	case tokQuoted:
		p.advance()
		return &astNode{kind: nodeTerm, quoted: true, value: t.text, pos: t.pos}, nil
	default:
		return nil, syntaxErr(t.pos, "意外的语法单元 %q", t.text)
	}
}

// parseTerm 解析 field op value | value | field:* 。
// `field:>=N` 这类链式比较（§9 表格）在 `:` 之后还允许跟一个比较算子。
func (p *parser) parseTerm() (*astNode, *kqlErr) {
	first := p.advance() // tokIdent
	// 紧跟算子 → field:op:value
	if p.peek().kind == tokOp {
		op := p.advance()
		eff := op.text
		if op.text == ":" && p.peek().kind == tokOp && p.peek().text != ":" {
			comp := p.advance()
			eff = comp.text // field:>=500 / field:!=x → 比较算子生效
		}
		v, verr := p.parseValue(first.pos)
		if verr != nil {
			return nil, verr
		}
		return &astNode{
			kind: nodeTerm, field: first.text, op: eff,
			value: v.value, wildcard: v.wildcard, quoted: v.quoted, pos: first.pos,
		}, nil
	}
	return &astNode{
		kind: nodeTerm, value: first.text, wildcard: first.wildcard, quoted: first.quoted, pos: first.pos,
	}, nil
}

type parsedValue struct {
	value    string
	wildcard bool
	quoted   bool
}

// parseValue 读取 field 右侧的值（ident / quoted）。
func (p *parser) parseValue(tokStart int) (parsedValue, *kqlErr) {
	t := p.peek()
	switch t.kind {
	case tokIdent:
		p.advance()
		return parsedValue{value: t.text, wildcard: t.wildcard}, nil
	case tokQuoted:
		p.advance()
		return parsedValue{value: t.text, quoted: true}, nil
	default:
		return parsedValue{}, syntaxErr(t.pos, "算子 %q 后缺少值", t.text)
	}
}
