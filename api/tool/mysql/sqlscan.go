package mysql

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// 单条语句的字节上限：超过这个体积的多半不是正常 DML，而是没换行的一整块导出，
// 直接报错让人自己拆开，好过把内存吃光。
const maxStatementBytes = 64 << 20

// sqlScanner 流式切分 .sql 文件，逐条产出完整语句，内存里只留当前这一条。
//
// 为什么不复用 splitStatements：那个函数吃的是编辑器里的整段文本（前端本就有大小上限），
// 而导入读的可能是几百 MB 的导出文件。两者的切分规则必须完全一致——注释、引号里的
// 分号、`--` 后必须跟空白这些细节一个都不能差，靠 TestScannerMatchesSplitStatements
// 做等价性校验锁住，避免两份规则各自漂移。
type sqlScanner struct {
	rr       *runeReader
	buf      []byte
	limit    int
	overflow bool // 单条语句超过上限
	lines    int  // 已产出的语句数
}

func newSQLScanner(r io.Reader, limit int) *sqlScanner {
	if limit <= 0 {
		limit = maxStatementBytes
	}
	return &sqlScanner{rr: newRuneReader(r), buf: make([]byte, 0, 4096), limit: limit}
}

// Lines 已产出的语句条数。
func (s *sqlScanner) Lines() int { return s.lines }

// Next 返回下一条语句；读到文件尾返回 io.EOF。注释被替换成一个空格，空语句跳过。
func (s *sqlScanner) Next() (string, error) {
	s.buf = s.buf[:0]
	s.overflow = false
	for {
		c, ok := s.rr.next()
		if !ok {
			if stmt := strings.TrimSpace(string(s.buf)); stmt != "" {
				s.lines++
				return stmt, nil
			}
			return "", io.EOF
		}

		switch {
		case c == '#':
			s.skipLine()
			s.push(' ')
		case c == '-':
			if s.isDashComment() {
				s.skipLine()
				s.push(' ')
				continue
			}
			s.push(c)
		case c == '/':
			if s.isBlockComment() {
				s.skipBlockComment()
				s.push(' ')
				continue
			}
			s.push(c)
		case c == '\'' || c == '"' || c == '`':
			s.readQuoted(c)
		case c == ';':
			if stmt := strings.TrimSpace(string(s.buf)); stmt != "" {
				s.lines++
				return stmt, nil
			}
			s.buf = s.buf[:0]
		default:
			s.push(c)
		}

		if s.overflow {
			return "", fmt.Errorf("第 %d 条语句超过 %dMB 上限（多半是缺换行的整块导出），请先拆分文件",
				s.lines+1, s.limit>>20)
		}
	}
}

// push 追加一个字符，并在累积超过上限时打标（不立刻返回，
// 否则调用方还得处理半截语句，标志位更容易做对）。
func (s *sqlScanner) push(c rune) {
	if s.overflow {
		return
	}
	s.buf = utf8.AppendRune(s.buf, c)
	if len(s.buf) > s.limit {
		s.overflow = true
	}
}

// isDashComment 判断刚读到的 '-' 是否开启行注释：需再跟一个 '-'，且其后是空白或行尾。
// 不是注释时把预读的字符退回去，让它们按普通字符继续走。
func (s *sqlScanner) isDashComment() bool {
	d, ok := s.rr.next()
	if !ok {
		return false
	}
	if d != '-' {
		s.rr.back(d)
		return false
	}
	e, ok := s.rr.next()
	if !ok {
		return true // 文件在 `--` 处结束，仍算注释（对齐 splitStatements 的 i+2 >= n）
	}
	s.rr.back(e)
	if e == ' ' || e == '\t' || e == '\r' || e == '\n' {
		return true
	}
	s.rr.back(d)
	return false
}

// isBlockComment 判断刚读到的 '/' 是否开启块注释（已消费掉后面的 '*'）。
func (s *sqlScanner) isBlockComment() bool {
	d, ok := s.rr.next()
	if !ok {
		return false
	}
	if d != '*' {
		s.rr.back(d)
		return false
	}
	return true
}

// skipLine 跳过行注释内容，停在换行符**之前**：
// 换行本身是普通字符，吞掉它会让「语句中间夹注释」的还原结果与 splitStatements 不一致。
func (s *sqlScanner) skipLine() {
	for {
		c, ok := s.rr.next()
		if !ok {
			return
		}
		if c == '\n' {
			s.rr.back(c)
			return
		}
	}
}

// skipBlockComment 跳到 */ 之后；未闭合则一路吃到文件尾。
func (s *sqlScanner) skipBlockComment() {
	prev := rune(0)
	for {
		c, ok := s.rr.next()
		if !ok {
			return
		}
		if prev == '*' && c == '/' {
			return
		}
		prev = c
	}
}

// readQuoted 原样保留一整段引号内容（含两端引号），内部的分号不当分隔符。
// 反引号标识符不认反斜杠转义；单双引号里连写两个引号表示一个引号。
func (s *sqlScanner) readQuoted(quote rune) {
	s.push(quote)
	for {
		if s.overflow {
			return
		}
		c, ok := s.rr.next()
		if !ok {
			return // 引号未闭合，吃到文件尾
		}
		if c == '\\' && quote != '`' {
			s.push(c)
			if d, ok := s.rr.next(); ok {
				s.push(d)
			}
			continue
		}
		if c == quote {
			if quote != '`' {
				d, ok := s.rr.next()
				if ok && d == quote {
					s.push(c)
					s.push(d)
					continue
				}
				if ok {
					s.rr.back(d)
				}
			}
			s.push(c)
			return
		}
		s.push(c)
	}
}

// runeReader 支持少量回退的 rune 读取器（前瞻判断 '--' 与 '/*' 要用）。
type runeReader struct {
	r      *bufio.Reader
	pushed []rune
}

func newRuneReader(r io.Reader) *runeReader {
	if br, ok := r.(*bufio.Reader); ok {
		return &runeReader{r: br}
	}
	return &runeReader{r: bufio.NewReaderSize(r, 64*1024)}
}

func (r *runeReader) next() (rune, bool) {
	if n := len(r.pushed); n > 0 {
		c := r.pushed[n-1]
		r.pushed = r.pushed[:n-1]
		return c, true
	}
	c, _, err := r.r.ReadRune()
	if err != nil {
		return 0, false
	}
	return c, true
}

func (r *runeReader) back(c rune) { r.pushed = append(r.pushed, c) }
