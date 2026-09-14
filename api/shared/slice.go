package shared

import "strings"

// ContainsString 判断字符串切片是否包含给定值。
func ContainsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ContainsInt64 判断 int64 切片是否包含给定值。
func ContainsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// MergeStringList 将值追加到字符串切片，若已存在则原样返回。
func MergeStringList(base []string, add string) []string {
	if ContainsString(base, add) {
		return base
	}
	return append(append([]string{}, base...), add)
}

// FilterEmpty 过滤空白项并去除首尾空格。
func FilterEmpty(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// SplitLogLines 按行拆分脚本输出块：去除 \r，跳过空行。
func SplitLogLines(chunk string) []string {
	if chunk == "" {
		return nil
	}
	out := []string{}
	for _, line := range strings.Split(chunk, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
