package nacos

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

// probe.go：Nacos 探活（本机容器 / readiness 健康 / 汇总提示）。
// 全部实现落在本套件内，引擎只做分发与聚合（api/stack/stack_verify_*.go 零分支）。
//
// 与 kafka 探活同构：节点容器名带**运行期序号**（nacos-<seq>），探活上下文
// （stackkit.ProbeCtx）拿不到该序号 ⇒ 无法预知容器名。因此：
//   - Containers 返回空 ⇒ 引擎先做通用 compose 状态检查；
//   - ScriptTail 用 `docker ps` 现取真实容器名（含首节点条件服务 nacos-mysql），
//     复用引擎已定义的 inspect_ctr 逐个输出，并探测 readiness 健康端点；
//   - ParseSuite 按真实名字分配组件归属，产出「实例 / 版本 / 就绪」检查项。

// 组件归属（探活对话框按组件分页签；骨架 common/verify-dialog.js 的 probeAttributed
// 依据端点与检查项上的 component 过滤出真实存在的页签）。
const (
	compNacos = "nacos"
	compMySQL = "mysql"
)

// ctrLineRE 匹配引擎探活脚本输出的容器状态行（含 ScriptTail 里循环调用的 inspect_ctr）。
var ctrLineRE = regexp.MustCompile(`(?m)^__IO_CTR__([^=\s]+)__=(.*)$`)

// ctrNameRE 本套件的容器名：nacos-<n>（节点）、nacos-mysql（首节点条件服务）。
var ctrNameRE = regexp.MustCompile(`^nacos-(\d+|mysql)$`)

// containerComponent 容器名 → 组件键。
func containerComponent(name string) string {
	if name == "nacos-mysql" {
		return compMySQL
	}
	return compNacos
}

// Containers 本机期望容器名：**故意返回空**。
//
// 节点容器名含运行期序号（nacos-<seq>），探活上下文拿不到 seq 故无法预知；
// 引擎在容器清单为空时做通用 compose 检查，再由本文件的 ParseSuite 补齐组件化检查项。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string { return nil }

// ScriptTail 追加 Nacos 专属探活片段：
//   - 本机 nacos / mysql 容器逐个 inspect（容器名从 docker ps 现取）；
//   - readiness 健康端点（无鉴权，200 为就绪）——只看容器 running 会把
//     「容器在跑但应用尚未起来」误判为健康，readiness 才是就绪的唯一门。
func (d *Driver) ScriptTail(ctx stackkit.ProbeCtx) string {
	port := stackkit.ShellQuote(stackkit.PickParam(ctx.Params, "port", "8848"))
	var b strings.Builder
	b.WriteString(`for _nc in $(docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^nacos-[0-9]+$|^nacos-mysql$'); do
  inspect_ctr "${_nc}"
done
`)
	b.WriteString("_NC_PORT=" + port + "\n")
	// readiness 无响应时 curl 输出 000（或为空）⇒ 由解析端判失败
	b.WriteString(`_NCTR="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^nacos-[0-9]+$' | head -1)"
if [ -n "${_NCTR}" ]; then
  echo __IO_NC_HEALTH__=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${_NC_PORT}/nacos/v1/console/health/readiness" 2>/dev/null || true)
fi
`)
	return b.String()
}

// ParseSuite 解析 Nacos 探活输出。
// 不设 OK：本主机是否通过由引擎按全部检查项共同判定（ChecksOK）。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, _ []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	ctrs := parseNacosContainers(raw)
	hasNacos := false
	version := ""
	for _, c := range ctrs {
		comp := containerComponent(c.name)
		ok, detail, image, _ := stackkit.ParseCtrStateEx(c.name, c.state)
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "实例", Component: comp, OK: ok, Detail: c.name + " " + detail,
		})
		if comp == compNacos {
			hasNacos = true
			if version == "" {
				version = image
			}
		}
	}
	if version != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", Component: compNacos, OK: true, Detail: version})
	}
	if hasNacos {
		code := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_NC_HEALTH__="))
		detail := "readiness HTTP " + code
		if code == "" || code == "000" {
			detail = "readiness 无响应"
		}
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "就绪", Component: compNacos, OK: code == "200", Detail: detail,
		})
	}
	return out
}

// Summary Nacos 汇总提示与客户端命令。
func (d *Driver) Summary(ctx stackkit.ProbeCtx, hosts []stackkit.ProbeHostRow) ([]string, string) {
	port := stackkit.PickParam(ctx.Params, "port", "8848")
	ip := ""
	live := 0
	for _, h := range hosts {
		if h.OK {
			live++
			if ip == "" {
				ip = h.HostIP
			}
		}
	}
	if ip == "" && len(hosts) > 0 {
		ip = hosts[0].HostIP
	}
	notes := []string{
		"客户端直连任一存活节点即可（host 网络直通 " + port + "），配置/服务数据在共享 MySQL。",
		strconv.Itoa(len(hosts)) + " 台成员中 " + strconv.Itoa(live) + " 台探活通过。",
	}
	return notes, "curl -s http://" + ip + ":" + port + "/nacos/v1/console/health/readiness"
}

// ---------- 解析助手 ----------

type nacosCtr struct {
	name  string
	state string
}

// parseNacosContainers 从探活原始输出里取本套件容器（按名字排序，保证快照稳定）。
func parseNacosContainers(raw string) []nacosCtr {
	var out []nacosCtr
	for _, m := range ctrLineRE.FindAllStringSubmatch(raw, -1) {
		name := strings.TrimSpace(m[1])
		if !ctrNameRE.MatchString(name) {
			continue
		}
		out = append(out, nacosCtr{name: name, state: strings.TrimSpace(m[2])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
