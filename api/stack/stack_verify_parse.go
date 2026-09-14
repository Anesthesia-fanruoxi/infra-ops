// 套件探活输出解析：引擎通用 compose 兜底 + 套件专属解析（stackkit.ProbePlugin）。
package stack

import (
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// parseStackVerifyOutput 解析单机探活输出并写入 row。
//
// 契约（见 store/stackkit/probe.go）：
//   - 期望容器非空 → 容器/实例检查项整体交给套件（ParseSuite 拿 containers）；
//   - 期望容器为空 → 引擎自己做 compose 状态检查，再让套件追加专属检查项（ParseSuite 拿 nil）；
//   - 套件未实现探活能力 → 纯 compose 检查（与拆分前其它套件的行为一致）。
func parseStackVerifyOutput(stackKey, mode string, host model.StackInstanceHost, params map[string]string, raw string, row *stackVerifyHost) {
	if v := extractLine(raw, "__IO_ERR__="); v == "no-docker" {
		row.OK = false
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "Docker", OK: false, Detail: "未检测到 Docker"})
		return
	}
	ctx := stackkit.ProbeCtx{Mode: mode, Role: host.Role, HostIP: host.HostIP, Params: params}
	var plugin stackkit.ProbePlugin
	if p, ok := store.FindStackDriver(stackKey).(stackkit.ProbePlugin); ok {
		plugin = p
	}
	var ctrs []string
	if plugin != nil {
		ctrs = plugin.Containers(ctx)
	}
	// 套件自渲染容器/实例检查项（redis / bigdata）
	if len(ctrs) > 0 {
		out := plugin.ParseSuite(ctx, raw, ctrs)
		appendSuiteChecks(row, out)
		if out.OK != nil {
			row.OK = *out.OK
		} else {
			row.OK = stackkit.ChecksOK(out.Checks)
		}
		row.LiveRole = liveRoleOf(out, host)
		return
	}
	// 通用兜底：compose 状态即容器检查项，套件只在其后追加专属检查项
	compose := extractBlock(raw, "__IO_COMPOSE_BEGIN__", "__IO_COMPOSE_END__")
	ok, detail := parseComposeStates(compose)
	row.Checks = append(row.Checks, stackVerifyCheck{Name: "容器", OK: ok, Detail: detail})
	if plugin == nil {
		row.OK = ok
		row.LiveRole = host.Role
		return
	}
	out := plugin.ParseSuite(ctx, raw, nil)
	appendSuiteChecks(row, out)
	if out.OK != nil {
		ok = ok && *out.OK
	} else {
		ok = ok && stackkit.ChecksOK(out.Checks)
	}
	row.OK = ok
	row.LiveRole = liveRoleOf(out, host)
}

// appendSuiteChecks 把套件检查项并入主机结果。
func appendSuiteChecks(row *stackVerifyHost, out stackkit.ProbeOutcome) {
	for _, c := range out.Checks {
		row.Checks = append(row.Checks, stackVerifyCheck{Name: c.Name, Component: c.Component, OK: c.OK, Detail: c.Detail})
	}
}

// liveRoleOf 取实测角色：套件未从输出实测（LiveRoleFromOutput=false）时回落为该主机的期望角色。
func liveRoleOf(out stackkit.ProbeOutcome, host model.StackInstanceHost) string {
	if out.LiveRoleFromOutput {
		return out.LiveRole
	}
	return host.Role
}
