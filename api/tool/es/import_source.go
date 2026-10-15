// import_source.go 导入数据源：把「一个本地文件」变成一条条待导入的记录。
//
// 三种来源共用同一个出口（record），差异只在「怎么切出一条记录」：
//   - JSON / JSONL：一行的内容本身就是一个完整文档，直接填 Doc；
//   - 纯文本：填 Text，要不要提字段交给上层（切分 + grok）；
//   - 压缩包：见 import_zip.go —— 解压成一批普通文件后串成同一条流。
//
// 全程流式，不把整个文件读进内存：JSON 数组用 json.Decoder 逐个读元素，
// 文本按行扫描。值流模式靠「解析失败 = 还没读完」来累积多行内容，
// 于是 JSONL 与美化过的多行 JSON 用同一段逻辑就能都吃下。
package es

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 读取上限，都是防御性的：真超了说明这份文件不该走本功能。
const (
	sourceMaxLineBytes  = 4 << 20  // 单行 4MB（日志行通常几十 KB）
	sourceMaxAccumLines = 2000     // 值流模式最多累积多少行去找一个完整 JSON 值
	sourceMaxAccumBytes = 32 << 20 // 同上，按字节数兜底
	sniffBytes          = 512      // 嗅探只看开头这么多字节
)

// record 待导入的一条记录。
type record struct {
	Origin string         // 来源标识：文件名（压缩包内为条目相对路径）
	Line   int            // 行号 / 数组下标（从 1 起，排错用）
	Text   string         // 原始文本（文本来源；Doc 非空时忽略）
	Doc    map[string]any // 已结构化文档（JSON 来源）
}

// recordSource 逐条产出记录；Next 以 io.EOF 表示结束。
type recordSource interface {
	Next() (*record, error)
	Close() error
}

// fileStepper 逐个文件 / 压缩包条目的处理游标（进度抽屉逐项打勾用）：
// 返回当前正在读的序号（1-based）与总数；单文件来源不实现。
type fileStepper interface {
	FileCursor() (int, int)
}

// skipError 表示「这一条不合法，跳过即可」，不是致命错误。
// 上层据此计入失败条数并继续，而不是整份导入中断 —— 几万行里坏一行是常态。
type skipError struct{ reason string }

func (e *skipError) Error() string { return e.reason }

// ---------------- 按内容形态分派 ----------------

// 内容形态。
const (
	kindText      = iota
	kindJSONValue // 一行（或跨行累积）一个 JSON 值
	kindJSONArray // 顶层是数组，流式逐个读元素
)

// pickKind 决定按哪种形态读。
//
// 以内容为准而不是扩展名：同一个 .log 里既可能是纯文本日志、也可能整份是 JSON，
// 反过来把日志改名成 .json 也很常见，扩展名不可靠。
// 但单看首字符同样会误判 —— nginx 日志以 '[' 开头，而 '[' 也是 JSON 数组的开头，
// 所以还要确认 '[' 后面跟的是 '{' / '"' / ']' 之类（见 looksLikeJSON）。
func pickKind(head []byte) int {
	if !looksLikeJSON(head) {
		return kindText
	}
	return sniffJSONKind(head)
}

// looksLikeJSON 判断开头是否像 JSON：跳过 BOM 与空白后，首字符是 '{'；
// 或者是 '[' 且其后第一个非空白字符是 '{' / '"' / ']'（排除 "[2026-10-10 ..." 这类日志）。
func looksLikeJSON(head []byte) bool {
	s := bytes.TrimPrefix(head, []byte{0xEF, 0xBB, 0xBF})
	i := skipSpace(s, 0)
	if i >= len(s) {
		return false
	}
	switch s[i] {
	case '{':
		return true
	case '[':
		j := skipSpace(s, i+1)
		if j >= len(s) {
			return true // 只有 "[" 一个字符，交给解析器裁决
		}
		return s[j] == '{' || s[j] == '"' || s[j] == ']' || s[j] == '['
	}
	return false
}

// sniffJSONKind 已经确认像 JSON，再分「数组」还是「值流」。
func sniffJSONKind(head []byte) int {
	s := bytes.TrimPrefix(head, []byte{0xEF, 0xBB, 0xBF})
	if i := skipSpace(s, 0); i < len(s) && s[i] == '[' {
		return kindJSONArray
	}
	return kindJSONValue
}

func skipSpace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// newFileSource 打开一个本地文件并按内容分派读取器。
func newFileSource(path, origin string) (recordSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	name := origin
	if name == "" {
		name = filepath.Base(path)
	}
	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(sniffBytes) // 文件更短时 Peek 返回已有部分，err 忽略

	switch pickKind(head) {
	case kindJSONArray:
		dec := json.NewDecoder(br)
		dec.UseNumber()
		return &jsonArraySource{f: f, name: name, dec: dec}, nil
	case kindJSONValue:
		return &jsonValueSource{f: f, name: name, sc: newLineScanner(br)}, nil
	default:
		return &textLineSource{f: f, name: name, sc: newLineScanner(br)}, nil
	}
}

// newLineScanner 逐行扫描器：默认 64KB 上限太窄，日志行超了会直接 ErrTooLong。
func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), sourceMaxLineBytes)
	return sc
}

// ---------------- JSON 数组 ----------------

// jsonArraySource 流式读顶层数组：逐个读元素，不把整个数组读进内存。
type jsonArraySource struct {
	f      *os.File
	name   string
	dec    *json.Decoder
	idx    int
	opened bool
}

func (s *jsonArraySource) Next() (*record, error) {
	if !s.opened {
		tok, err := s.dec.Token()
		if err != nil {
			return nil, fmt.Errorf("读取 JSON 数组失败: %w", err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return nil, fmt.Errorf("文件开头不是 JSON 数组")
		}
		s.opened = true
	}
	if !s.dec.More() {
		return nil, io.EOF
	}
	var v any
	if err := s.dec.Decode(&v); err != nil {
		// 数组元素语法坏掉后 Decoder 无法定位下一个元素，只能终止
		return nil, fmt.Errorf("第 %d 个数组元素解析失败: %w", s.idx+1, err)
	}
	s.idx++
	doc, ok := v.(map[string]any)
	if !ok {
		return nil, &skipError{fmt.Sprintf("第 %d 个数组元素不是 JSON 对象", s.idx)}
	}
	return &record{Origin: s.name, Line: s.idx, Doc: doc}, nil
}

func (s *jsonArraySource) Close() error { return s.f.Close() }

// ---------------- JSON 值流（JSONL / 多行 JSON） ----------------

// jsonValueSource 逐行累积着解析 JSON 值。
//
// 每读一行就试解析累积缓冲：解析失败且原因是「输入提前结束」就说明这个值还没写完
// （美化过的多行 JSON），继续累积；解析成功就产出一条并清空。
// 因此 JSONL（一行一条）与多行 JSON 走的是同一条路径，不必先判断文件是哪种。
type jsonValueSource struct {
	f        *os.File
	name     string
	sc       *bufio.Scanner
	lineNo   int
	acc      strings.Builder
	accLines int
	accStart int
}

func (s *jsonValueSource) Next() (*record, error) {
	for s.sc.Scan() {
		s.lineNo++
		line := s.sc.Text()
		if s.acc.Len() == 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			s.accStart = s.lineNo
		}
		s.acc.WriteString(line)
		s.accLines++

		var v any
		dec := json.NewDecoder(strings.NewReader(s.acc.String()))
		dec.UseNumber() // 大整数不走 float64：时间戳、雪花 ID 这类值丢精度是静默的
		err := dec.Decode(&v)
		if err != nil {
			if isIncompleteJSON(err) && s.accLines < sourceMaxAccumLines && s.acc.Len() < sourceMaxAccumBytes {
				s.acc.WriteByte('\n') // 保留换行，后续报错里的行号才对得上
				continue
			}
			start := s.accStart
			raw := s.acc.String()
			s.reset()
			return nil, &skipError{fmt.Sprintf("第 %d 行起: %s%s", start, briefJSONErr(err), brief(raw))}
		}
		start := s.accStart
		s.reset()
		doc, ok := v.(map[string]any)
		if !ok {
			return nil, &skipError{fmt.Sprintf("第 %d 行: 不是 JSON 对象", start)}
		}
		return &record{Origin: s.name, Line: start, Doc: doc}, nil
	}
	if err := s.sc.Err(); err != nil {
		return nil, err
	}
	if s.acc.Len() > 0 { // 扫到文件末尾还是一块没闭合的内容
		start := s.accStart
		s.reset()
		return nil, &skipError{fmt.Sprintf("第 %d 行起: JSON 内容不完整", start)}
	}
	return nil, io.EOF
}

func (s *jsonValueSource) reset() {
	s.acc.Reset()
	s.accLines = 0
}

func (s *jsonValueSource) Close() error { return s.f.Close() }

// isIncompleteJSON 判断解析失败是否只是「还没读完」。
// 两种 API 的文案不同：json.Unmarshal 说 unexpected end of JSON input，
// json.Decoder 说 unexpected EOF，都要认。
func isIncompleteJSON(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unexpected end of JSON input") ||
		strings.Contains(msg, "unexpected EOF")
}

// briefJSONErr 把 encoding/json 的错误文案压短，避免整屏英文。
func briefJSONErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unexpected end of JSON input"), strings.Contains(msg, "unexpected EOF"):
		return "JSON 内容不完整"
	case strings.Contains(msg, "invalid character"):
		if i := strings.Index(msg, "looking for"); i > 0 {
			return "JSON 语法错误（" + strings.TrimSpace(msg[:i]) + "）"
		}
		return "JSON 语法错误"
	case strings.Contains(msg, "cannot unmarshal"):
		return "JSON 类型错误"
	}
	return msg
}

// brief 截取片段供排错；太长反而把错误信息挤没了。
func brief(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= 60 {
		if s == "" {
			return ""
		}
		return ": " + s
	}
	return ": " + string(r[:60]) + "…"
}

// ---------------- 纯文本按行 ----------------

// textLineSource 按行产出文本记录（「一行一条」的默认口径）。
type textLineSource struct {
	f      *os.File
	name   string
	sc     *bufio.Scanner
	lineNo int
}

func (s *textLineSource) Next() (*record, error) {
	for s.sc.Scan() {
		s.lineNo++
		line := s.sc.Text()
		if strings.TrimSpace(line) == "" {
			continue // 空行不是一条记录
		}
		return &record{Origin: s.name, Line: s.lineNo, Text: line}, nil
	}
	if err := s.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *textLineSource) Close() error { return s.f.Close() }

// ---------------- AI 生成取样 ----------------

// grokSampleScanCap 为 AI 生成取样例时最多扫描多少条记录再抽样。
// 取样要「跨越开头 / 中段」才有代表性——像 '-' 占位行、少数派格式未必在最前面；
// 但也不能为了取样把几个 G 的文件整个读一遍，扫够这么多条就停，在其中等距抽。
const grokSampleScanCap = 2000

// sampleFromSource 复用导入的读取逻辑，从文件里抽取最多 want 条真实文本行，作为 AI 生成 grok 的样例。
//
// 与「只取开头 N 行」不同：先扫至多 grokSampleScanCap 条，再在其中等距抽样，让占位行 / 少数派格式
// 也有机会进样例——服务端校验因此才真正有意义（样例若清一色带 IP，模型就会生成只认 IP 的过严 pattern）。
// 只取纯文本行：rec.Doc != nil 的结构化 JSON 不参与 grok 提字段，读不出来的行（skipError）也跳过。
func sampleFromSource(ctx context.Context, source, filter string, want int) ([]string, error) {
	if want <= 0 {
		return nil, nil
	}
	src, err := openSource(source, filter)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return sampleRecords(ctx, src, want)
}

// sampleFromSources 同 sampleFromSource，一次选择多份文件时从合并流里抽样
// （按选择顺序读到扫描上限为止，再整体等距抽）。filter 只作用于压缩包内的条目。
func sampleFromSources(ctx context.Context, sources []string, filter string, want int) ([]string, error) {
	if want <= 0 {
		return nil, nil
	}
	if len(sources) == 1 {
		return sampleFromSource(ctx, sources[0], filter, want)
	}
	src, err := openSources(sources, filter)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return sampleRecords(ctx, src, want)
}

// sampleRecords 从已打开的记录流里抽 want 条真实文本行（先扫到上限、再等距抽）。
func sampleRecords(ctx context.Context, src recordSource, want int) ([]string, error) {
	var lines []string
	for len(lines) < grokSampleScanCap {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rec, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var se *skipError
			if errors.As(err, &se) {
				continue
			}
			return nil, err
		}
		if rec.Doc != nil {
			continue // 结构化 JSON：grok 提字段不适用
		}
		if strings.TrimSpace(rec.Text) != "" {
			lines = append(lines, rec.Text)
		}
	}
	if len(lines) <= want {
		return lines, nil
	}
	// 等距抽样：在扫到的 lines 里均匀挑 want 条，覆盖从开头到中段
	out := make([]string, 0, want)
	stride := float64(len(lines)) / float64(want)
	for i := 0; i < want; i++ {
		out = append(out, lines[int(float64(i)*stride)])
	}
	return out, nil
}
