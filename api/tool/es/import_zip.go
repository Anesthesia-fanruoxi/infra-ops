// import_zip.go 压缩包来源：解压到临时目录，之后按普通文件处理。
//
// 为什么不边解压边喂给解析器：包内文件要先按内容嗅探才知道该按 JSON 还是文本读，
// 而嗅探要读到开头若干字节；解压到磁盘一次铺开、逐个按需打开，逻辑最简单，
// 临时空间也比内存便宜。临时目录在 Close 时整体删除。
//
// 只支持 .zip / .tar.gz（标准库 archive/zip + compress/gzip + archive/tar）。
// 7z / rar 需要第三方解压库，与「自研优先、不轻易引 SDK」的约定不符，不做。
package es

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 解压上限，用来挡 zip bomb 与误选的大包。
const (
	archiveMaxEntryBytes = 2 << 30 // 单个条目 2GB
	archiveMaxTotalBytes = 8 << 30 // 解压总量 8GB
	archiveMaxEntries    = 5000    // 条目数
)

// openSource 打开一个本地文件作为导入来源：压缩包先解压（filter 过滤包内条目），
// 其余按内容分派（filter 对普通文件无意义，忽略）。
func openSource(path, filter string) (recordSource, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"),
		strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"):
		return newZipSource(path, filter)
	}
	return newFileSource(path, "")
}

// zipEntryMatch 判断压缩包条目是否命中包内过滤规则。
//
// 规则针对条目路径的文件名部分：含 * / ? 时按通配符整名匹配（大小写不敏感），
// 否则按子串包含匹配；空规则 = 全部命中。规则里带 / 时改按完整条目路径匹配
// （可筛目录，如 2026-10-13/*.log）。日志包常把 access / error 同目录混放
// （用户口径：只要 *_access_*.log 这类格式准确的日志），按文件名过滤最直观。
func zipEntryMatch(name, filter string) bool {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" {
		return true
	}
	target := strings.ToLower(path.Base(name))
	if strings.ContainsRune(f, '/') {
		target = strings.ToLower(path.Clean(name))
	}
	if strings.ContainsAny(f, "*?") {
		ok, err := path.Match(f, target)
		return err == nil && ok
	}
	return strings.Contains(target, f)
}

// zipStats 压缩包条目统计（检查旁白用）。
type zipStats struct {
	total  int // 条目总数（不含目录）
	marked int // 被过滤规则排除的条目数
	kept   int // 实际保留（可导入）的条目数
}

// zipEntry 一个解压出来的文件。
type zipEntry struct {
	path string // 本地临时路径
	name string // 包内相对路径（展示与排错用）
}

// zipSource 把压缩包内多个文件的记录串成一条流。
type zipSource struct {
	tmpDir string
	filter string // 包内条目过滤规则（空 = 全部）：见 zipEntryMatch
	files  []zipEntry
	total  int      // 条目总数（不含目录）
	marked int      // 被过滤规则排除的条目数
	sample []string // 前几个条目名（无匹配时报错给用户对照）
	idx    int
	cur    recordSource
	closed bool
}

// newZipSource 解压并准备读取；filter 非空时只保留命中规则的条目（不命中的不解压）。
// 包内没有可用文本文件时直接报错（而不是静默导 0 条）。
func newZipSource(p, filter string) (*zipSource, error) {
	filter = strings.TrimSpace(filter)
	if strings.ContainsAny(filter, "*?") {
		if _, err := path.Match(filter, "probe"); err != nil {
			return nil, fmt.Errorf("包内过滤规则不是合法通配符: %v", err)
		}
	}
	tmpDir, err := os.MkdirTemp("", "es-import-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	s := &zipSource{tmpDir: tmpDir, filter: filter}
	if strings.HasSuffix(strings.ToLower(p), ".zip") {
		err = s.extractZip(p)
	} else {
		err = s.extractTarGz(p)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	if len(s.files) == 0 {
		s.Close()
		if filter != "" {
			hint := ""
			if len(s.sample) > 0 {
				hint = "，包内条目如 " + strings.Join(s.sample, "、")
			}
			return nil, fmt.Errorf("压缩包内没有匹配「%s」的文件（共 %d 个条目%s），请检查包内过滤规则",
				filter, s.total, hint)
		}
		return nil, fmt.Errorf("压缩包里没有可导入的文本文件（二进制与不支持的类型已跳过）")
	}
	return s, nil
}

// observe 记录条目总数与前几个条目名（过滤统计与无匹配报错用）。
func (s *zipSource) observe(name string) {
	s.total++
	if len(s.sample) < 3 {
		s.sample = append(s.sample, name)
	}
}

func (s *zipSource) extractZip(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer zr.Close()

	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		s.observe(f.Name)
		if !zipEntryMatch(f.Name, s.filter) {
			s.marked++
			continue // 不命中过滤规则：连解压都不做
		}
		if f.UncompressedSize64 > archiveMaxEntryBytes {
			continue // 单条目过大：跳过它，不牵连其余文件
		}
		if total += int64(f.UncompressedSize64); total > archiveMaxTotalBytes {
			return fmt.Errorf("解压总量超过 %d GB，请先自行筛选后再导入", archiveMaxTotalBytes>>30)
		}
		if len(s.files) >= archiveMaxEntries {
			return fmt.Errorf("压缩包内文件超过 %d 个，请先自行筛选后再导入", archiveMaxEntries)
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		err = s.saveEntry(rc, f.Name)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *zipSource) extractTarGz(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("打开 gzip 失败: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败: %w", err)
		}
		// 只要普通文件：符号链接、设备文件、目录都跳过
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		s.observe(hdr.Name)
		if !zipEntryMatch(hdr.Name, s.filter) {
			s.marked++
			continue // 不命中过滤规则：连解压都不做
		}
		if hdr.Size > archiveMaxEntryBytes {
			continue
		}
		if total += hdr.Size; total > archiveMaxTotalBytes {
			return fmt.Errorf("解压总量超过 %d GB，请先自行筛选后再导入", archiveMaxTotalBytes>>30)
		}
		if len(s.files) >= archiveMaxEntries {
			return fmt.Errorf("压缩包内文件超过 %d 个，请先自行筛选后再导入", archiveMaxEntries)
		}
		if err := s.saveEntry(tr, hdr.Name); err != nil {
			return err
		}
	}
}

// saveEntry 把单个条目落盘；二进制文件与路径越界的条目直接忽略。
func (s *zipSource) saveEntry(rc io.Reader, name string) error {
	dst, ok := safeJoin(s.tmpDir, name)
	if !ok {
		return nil // zip slip：条目名带 ../ 想写到临时目录之外，丢掉
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	br := bufio.NewReaderSize(rc, 64<<10)
	head, _ := br.Peek(sniffBytes)
	if looksBinary(head) {
		return nil // 图片 / 可执行文件：当文本读只会写进去一堆乱码
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, br); err != nil {
		return err
	}
	s.files = append(s.files, zipEntry{path: dst, name: filepath.ToSlash(name)})
	return nil
}

func (s *zipSource) Next() (*record, error) {
	for {
		if s.cur == nil {
			if s.idx >= len(s.files) {
				return nil, io.EOF
			}
			e := s.files[s.idx]
			s.idx++
			src, err := newFileSource(e.path, e.name)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.name, err)
			}
			s.cur = src
		}
		rec, err := s.cur.Next()
		if err == io.EOF {
			s.cur.Close()
			s.cur = nil
			continue // 这个文件读完了，换下一个
		}
		if err != nil {
			return nil, err
		}
		return rec, nil
	}
}

func (s *zipSource) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cur != nil {
		s.cur.Close()
		s.cur = nil
	}
	return os.RemoveAll(s.tmpDir)
}

// FileCursor 实现 fileStepper：正在读第几个条目（1-based）与条目总数。
func (s *zipSource) FileCursor() (int, int) { return s.idx, len(s.files) }

// safeJoin 把包内条目名拼到根目录下，并确认结果没跑出根目录。
//
// 归一化必须按 POSIX 语义（path 而非 filepath）：压缩包条目名恒用 '/' 分隔，
// 而在 Windows 上 filepath 会把 "/abs/evil" 当成相对路径（没有盘符就不算绝对），
// 于是 zip slip 检查会被绕过。
func safeJoin(root, name string) (string, bool) {
	clean := path.Clean(name)
	if clean == "." || clean == ".." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", false
	}
	local := filepath.FromSlash(clean)
	// Windows 专有风险：条目名带盘符（C:\x）或 "C:evil"（ADS 写法）都会跑出临时目录
	if filepath.IsAbs(local) || filepath.VolumeName(local) != "" {
		return "", false
	}
	dst := filepath.Join(root, local)
	rel, err := filepath.Rel(root, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return dst, true
}

// looksBinary 判断是否二进制内容：前 512 字节里出现 NUL 基本可以断定。
func looksBinary(head []byte) bool {
	return bytes.IndexByte(head, 0) >= 0
}
