// 套件实例通用辅助：JSON/CSV 列表解析、组件集合运算。
package stack

import (
	"encoding/json"
	"strings"

	"infra-ops/api/shared"
)

func parseJSONStringList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out
	}
	return nil
}

func encodeJSONStringList(list []string) string {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func parseComponentsCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mergeComponentList(base, add []string) []string {
	out := append([]string{}, base...)
	for _, c := range add {
		if !shared.ContainsString(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func intersectComponents(a, b []string) []string {
	out := []string{}
	for _, x := range a {
		if shared.ContainsString(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func subtractComponents(from, remove []string) []string {
	out := make([]string, 0, len(from))
	for _, c := range from {
		if !shared.ContainsString(remove, c) {
			out = append(out, c)
		}
	}
	return out
}
