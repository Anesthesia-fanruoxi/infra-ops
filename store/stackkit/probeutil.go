package stackkit

import "strings"

// 探活通用字符串/容器状态辅助：引擎与套件共用同一份实现（避免行为漂移）。
// 原 api/stack/stack_verify_util.go 与 stack_verify_container.go 的对应函数原样迁入。

// ShellQuote 单引号包裹并转义内嵌单引号（用于生成探活脚本）。
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, `'`, `'"'"'`) + "'" }

// ExtractLine 取首个以 prefix 开头的行（去掉行尾 \r 与 prefix）。
func ExtractLine(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

// ExtractBlock 取 begin..end 之间的块内容（去首尾空白；缺 end 时取到末尾）。
func ExtractBlock(s, begin, end string) string {
	i := strings.Index(s, begin)
	if i < 0 {
		return ""
	}
	i += len(begin)
	j := strings.Index(s[i:], end)
	if j < 0 {
		return strings.TrimSpace(s[i:])
	}
	return strings.TrimSpace(s[i : i+j])
}

// Nz 空值回落。
func Nz(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// PickParam 取参数（去空白），空则用默认值。
func PickParam(m map[string]string, key, def string) string {
	if v := strings.TrimSpace(m[key]); v != "" {
		return v
	}
	return def
}

// IDSet 把 int64 列表转为集合（缩容保护判定用）。
func IDSet(ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// ParseCtrState 解析容器状态行（只看 OK 与明细）。
func ParseCtrState(name, st string) (bool, string) {
	ok, detail, _, _ := ParseCtrStateEx(name, st)
	return ok, detail
}

// ParseCtrStateEx 解析容器状态行：
// 输入格式 status|running(0/1)|health|image|startedAt。
func ParseCtrStateEx(name, st string) (ok bool, detail, image, started string) {
	st = strings.TrimSpace(st)
	if st == "" || st == "missing" {
		return false, name + " 不存在", "", ""
	}
	parts := strings.Split(st, "|")
	status := parts[0]
	running := len(parts) > 1 && parts[1] == "1"
	health := ""
	if len(parts) > 2 {
		health = parts[2]
	}
	if len(parts) > 3 {
		image = parts[3]
	}
	if len(parts) > 4 {
		started = parts[4]
	}
	detail = status
	if health != "" {
		detail += " · " + health
	}
	ok = running && (health == "" || health == "healthy" || health == "starting")
	if health == "unhealthy" {
		ok = false
	}
	return ok, detail, image, started
}

// ParseComposeStates 解析 compose ps 输出，返回是否全部 running 与明细。
func ParseComposeStates(raw string) (bool, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, "未找到 compose 服务"
	}
	var names []string
	any, allUp := false, true
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "NAME") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 && !strings.Contains(line, " ") {
			any = true
			name, state := line[:i], strings.ToLower(line[i+1:])
			names = append(names, name+":"+state)
			if state != "running" {
				allUp = false
			}
			continue
		}
		if strings.Contains(line, " Up ") || strings.HasSuffix(strings.ToLower(line), " running") {
			any = true
			names = append(names, line)
			continue
		}
		if strings.Contains(line, " Exit ") || strings.Contains(strings.ToLower(line), "exited") {
			any = true
			allUp = false
			names = append(names, line)
		}
	}
	if !any {
		return false, "compose 无运行中的容器"
	}
	detail := strings.Join(names, "; ")
	if len(detail) > 240 {
		detail = detail[:240] + "…"
	}
	return allUp, detail
}

// ChecksOK 全部检查项是否通过。
func ChecksOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}
