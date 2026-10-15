// import_multi.go 多文件来源：把一次选择的多个文件串成一条记录流。
//
// 每个文件单独 openSource（压缩包各自解压到自己的临时目录），记录按选择顺序
// 首尾相接 —— 检查（抽样/生成 pattern）、预览、导入三处共用，天然支持混合
// 「文本 + JSON + 压缩包」的选择。来源标识（Origin：文件名 / 压缩包内条目名）
// 由各读取器自带，失败行据此指回哪个文件。
package es

import (
	"fmt"
	"io"
	"path/filepath"
)

// openSources 打开一次选择的多份文件：单个时与原来完全一致（含 zip 自动解压），
// 多个时串成一条流。filter 只作用于压缩包内的条目（普通文件忽略）。上限与存在性
// 由 validateSources 在入口处把关。
func openSources(paths []string, filter string) (recordSource, error) {
	switch len(paths) {
	case 0:
		return nil, fmt.Errorf("请先选择要导入的文件")
	case 1:
		return openSource(paths[0], filter)
	default:
		return &multiSource{paths: paths, filter: filter}, nil
	}
}

// multiSource 顺序读取多个文件的记录流。
type multiSource struct {
	paths  []string
	filter string // 包内条目过滤规则（只作用于压缩包）
	idx    int
	cur    recordSource
}

func (s *multiSource) Next() (*record, error) {
	for {
		if s.cur == nil {
			if s.idx >= len(s.paths) {
				return nil, io.EOF
			}
			p := s.paths[s.idx]
			s.idx++
			src, err := openSource(p, s.filter)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
			}
			s.cur = src
		}
		rec, err := s.cur.Next()
		if err == io.EOF {
			_ = s.cur.Close()
			s.cur = nil
			continue // 这个文件读完了，换下一个
		}
		if err != nil {
			return nil, err
		}
		return rec, nil
	}
}

func (s *multiSource) Close() error {
	if s.cur != nil {
		_ = s.cur.Close()
		s.cur = nil
	}
	return nil
}

// FileCursor 实现 fileStepper：正在读第几个文件（1-based）与文件总数。
func (s *multiSource) FileCursor() (int, int) { return s.idx, len(s.paths) }
