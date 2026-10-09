package mysql

import (
	"bufio"
	"fmt"
	"io"

	"infra-ops/common/xlsx"
)

// tableWriter 三种导出格式的统一接口：逐行喂进来，收尾时 Close。
type tableWriter interface {
	WriteRow(vals []interface{}) error
	Close() error
}

// preambleWriter 可选接口：在数据之前写一段说明文字（INSERT 语句文件用）。
type preambleWriter interface {
	WritePreamble(text string) error
}

const (
	defaultBatchRows = 200
	maxBatchRows     = 5000
	// 单条 INSERT 的字节上限：只按行数切批不够——一行带几百 KB 的 TEXT 时，
	// 200 行拼出的语句会超过 MySQL 默认 max_allowed_packet(4MB) 被服务端拒绝。
	// 反过来只卡字节也不行，客户端解析压力与失败回滚代价都得靠行数控制。
	// 两个阈值谁先到谁生效。
	defaultBatchBytes = 1 << 20
)

// ---------------- INSERT 语句 ----------------

var (
	stmtEnd = []byte(";\n\n")
	commaNL = []byte(",\n")
)

type insertWriter struct {
	w     *bufio.Writer
	head  []byte // 预拼好的 "INSERT INTO … VALUES\n"
	types []string
	cols  int

	batchRows int
	batchByte int
	pending   int // 当前批已累积行数
	pendByte  int
	rows      int64
	batches   int64
}

// newInsertWriter 的 table 需是拼好的限定名（含反引号），由 qualifyTable 产出。
func newInsertWriter(w *bufio.Writer, table string, cols, types []string, batchRows, batchByte int) *insertWriter {
	if batchRows <= 0 {
		batchRows = defaultBatchRows
	}
	if batchRows > maxBatchRows {
		batchRows = maxBatchRows
	}
	if batchByte <= 0 {
		batchByte = defaultBatchBytes
	}
	head := make([]byte, 0, 128)
	head = append(head, "INSERT INTO "...)
	head = append(head, table...)
	head = append(head, " ("...)
	for i, c := range cols {
		if i > 0 {
			head = append(head, ',', ' ')
		}
		head = append(head, quoteIdent(c)...)
	}
	head = append(head, ") VALUES\n"...)
	return &insertWriter{w: w, head: head, types: types, cols: len(cols),
		batchRows: batchRows, batchByte: batchByte}
}

func (x *insertWriter) WritePreamble(text string) error {
	_, err := x.w.WriteString(text)
	return err
}

func (x *insertWriter) WriteRow(vals []interface{}) error {
	row := make([]byte, 0, 256)
	row = append(row, '(')
	for i := 0; i < x.cols; i++ {
		if i > 0 {
			row = append(row, ',', ' ')
		}
		var v interface{}
		if i < len(vals) {
			v = vals[i]
		}
		var dt string
		if i < len(x.types) {
			dt = x.types[i]
		}
		row = appendSQLValue(row, v, dt)
	}
	row = append(row, ')')

	switch {
	case x.pending == 0:
		if _, err := x.w.Write(x.head); err != nil {
			return err
		}
		x.batches++
	case x.pending >= x.batchRows || x.pendByte+len(row) > x.batchByte:
		if _, err := x.w.Write(stmtEnd); err != nil {
			return err
		}
		if _, err := x.w.Write(x.head); err != nil {
			return err
		}
		x.pending, x.pendByte = 0, 0
		x.batches++
	default:
		if _, err := x.w.Write(commaNL); err != nil {
			return err
		}
	}
	if _, err := x.w.Write(row); err != nil {
		return err
	}
	x.pending++
	x.pendByte += len(row) + len(commaNL)
	x.rows++
	return nil
}

// Batches 已经写出多少条 INSERT 语句（即自动分批切了多少批）。
func (x *insertWriter) Batches() int64 { return x.batches }

func (x *insertWriter) Close() error {
	if x.pending > 0 {
		if _, err := x.w.Write(stmtEnd); err != nil {
			return err
		}
	}
	if x.rows > 0 {
		if _, err := fmt.Fprintf(x.w, "-- 导出完成，共 %d 行\n", x.rows); err != nil {
			return err
		}
	}
	return nil
}

// ---------------- CSV ----------------

type csvWriter struct {
	w    *bufio.Writer
	cols int
	line []byte
	rows int64
}

func newCSVWriter(w *bufio.Writer, cols []string) (*csvWriter, error) {
	// Excel 打开 UTF-8 的 CSV 要靠 BOM 认编码，否则中文列名是乱码
	if _, err := w.WriteString("\ufeff"); err != nil {
		return nil, err
	}
	c := &csvWriter{w: w, cols: len(cols), line: make([]byte, 0, 512)}
	line := c.line[:0]
	for i, name := range cols {
		if i > 0 {
			line = append(line, ',')
		}
		line = appendCSVCell(line, name)
	}
	line = append(line, '\r', '\n')
	c.line = line
	_, err := w.Write(line)
	return c, err
}

func (c *csvWriter) WriteRow(vals []interface{}) error {
	// line 复用同一块底层数组是安全的：bufio 会在 Write 时把内容拷进自己的缓冲
	line := c.line[:0]
	for i := 0; i < c.cols; i++ {
		if i > 0 {
			line = append(line, ',')
		}
		var v interface{}
		if i < len(vals) {
			v = vals[i]
		}
		line = appendCSVCell(line, cellText(v))
	}
	line = append(line, '\r', '\n')
	c.line = line
	c.rows++
	_, err := c.w.Write(line)
	return err
}

func (c *csvWriter) Close() error { return nil }

// ---------------- XLSX ----------------

// xlsxWriter 把驱动值转成表格里该有的形态后再交给通用写出器
// （数值写成数字单元格，超精度整数退回文本）。
type xlsxWriter struct {
	x     *xlsx.Writer
	types []string
	rows  int
}

func newXLSXWriter(w io.Writer, sheet string, cols, types []string) (*xlsxWriter, error) {
	x, err := xlsx.NewWriter(w, sheet, cols)
	if err != nil {
		return nil, err
	}
	return &xlsxWriter{x: x, types: types}, nil
}

func (x *xlsxWriter) WriteRow(vals []interface{}) error {
	row := make([]interface{}, len(vals))
	for i, v := range vals {
		var dt string
		if i < len(x.types) {
			dt = x.types[i]
		}
		row[i] = xlsxValue(v, dt)
	}
	x.rows++
	return x.x.WriteRow(row)
}

func (x *xlsxWriter) Close() error { return x.x.Close() }
