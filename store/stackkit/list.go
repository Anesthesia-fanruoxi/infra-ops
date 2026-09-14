package stackkit

import "strings"

// 通用字符串列表工具：引擎与套件共用同一份实现（避免行为漂移）。
// 契约层不 import api/**，故 api/stack 与 store/stacks 都从这里取。

// ParseCSV 解析逗号分隔列表（去首尾空白、丢空项）。
func ParseCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Contains 列表是否包含目标项。
func Contains(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

// MergeList 归并两个列表（保持 base 顺序，追加 add 中未出现的项）。
func MergeList(base, add []string) []string {
	out := make([]string, 0, len(base)+len(add))
	out = append(out, base...)
	for _, a := range add {
		if !Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// SubtractList 从 from 中移除 remove 中的项。
func SubtractList(from, remove []string) []string {
	out := make([]string, 0, len(from))
	for _, f := range from {
		if !Contains(remove, f) {
			out = append(out, f)
		}
	}
	return out
}

// IntersectList 求交集（保持 a 的顺序）。
func IntersectList(a, b []string) []string {
	out := make([]string, 0, len(a))
	for _, x := range a {
		if Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}
