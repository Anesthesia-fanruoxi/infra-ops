// 套件探活字符串/JSON 工具：参数解析合并、引号转义、日志脱敏、字段抽取。
// 探活区块/行抽取与引号转义已统一到 store/stackkit（引擎与套件共用同一份实现）。
package stack

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

func parseJSONMap(s string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return out
	}
	_ = jsonUnmarshalMap(s, out)
	return out
}

func jsonUnmarshalMap(s string, out map[string]string) error {
	var raw map[string]any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return err
	}
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = strconv.FormatInt(int64(t), 10)
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return nil
}

func mergeParamMaps(base, over map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	return out
}

func pickParam(m map[string]string, key, def string) string { return stackkit.PickParam(m, key, def) }

func bashQuote(s string) string { return stackkit.ShellQuote(s) }

func redactSecret(s, secret string) string {
	if secret == "" || !strings.Contains(s, secret) {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

func extractLine(s, prefix string) string { return stackkit.ExtractLine(s, prefix) }

func extractBlock(s, begin, end string) string { return stackkit.ExtractBlock(s, begin, end) }

func nz(s, fallback string) string { return stackkit.Nz(s, fallback) }
