package powerjob

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

// probe.go：PowerJob 探活（本机容器 / 控制台 HTTP / Akka 端口 / 汇总提示）。
// 全部实现落在本套件内，引擎只做分发与聚合（api/stack/stack_verify_*.go 零分支）。
//
// 与 kafka / nacos 探活同构：节点容器名带**运行期序号**（powerjob-server-<seq>），
// 探活上下文（stackkit.ProbeCtx）拿不到该序号 ⇒ 无法预知容器名。因此：
//   - Containers 返回空 ⇒ 引擎先做通用 compose 状态检查；
//   - ScriptTail 用 `docker ps` 现取真实容器名（含首节点条件服务 powerjob-mysql），
//     复用引擎已定义的 inspect_ctr 逐个输出，并探测控制台 HTTP 与 Akka 端口；
//   - ParseSuite 按真实名字分配组件归属，产出「实例 / 版本 / 控制台 / 通信」检查项。
//
// 两个数据面探针的由来（对照 node.sh 的就绪判据）：
//   - 控制台：Spring Boot 起没起来只有 HTTP 可查（容器 running ≠ 应用就绪）；
//     未登录跳登录页（3xx）与首页（2xx）都算已响应，000 / 空 = 无响应；
//   - 通信：worker 经 Akka 端口（host 直通）接入，端口不通时控制台照常打开
//     但任务无法下发——「看着健康却调不动」的隐蔽形态，必须单独报出。

// 组件归属（探活对话框按组件分页签）。
const (
	compPowerjob = "powerjob"
	compMySQL    = "mysql"
)

// ctrLineRE 匹配引擎探活脚本输出的容器状态行（含 ScriptTail 里循环调用的 inspect_ctr）。
var ctrLineRE = regexp.MustCompile(`(?m)^__IO_CTR__([^=\s]+)__=(.*)$`)

// ctrNameRE 本套件的容器名：powerjob-server-<n>（节点）、powerjob-mysql（首节点条件服务）。
var ctrNameRE = regexp.MustCompile(`^powerjob-(server-\d+|mysql)$`)

// containerComponent 容器名 → 组件键。
func containerComponent(name string) string {
	if name == "powerjob-mysql" {
		return compMySQL
	}
	return compPowerjob
}

// Containers 本机期望容器名：**故意返回空**。
//
// 节点容器名含运行期序号（powerjob-server-<seq>），探活上下文拿不到 seq 故无法预知；
// 引擎在容器清单为空时做通用 compose 检查，再由本文件的 ParseSuite 补齐组件化检查项。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string { return nil }

// ScriptTail 追加 PowerJob 专属探活片段：
//   - 本机 powerjob-server-* / powerjob-mysql 容器逐个 inspect（容器名从 docker ps 现取）；
//   - 控制台 HTTP 码（000 / 空 = 无响应，由解析端判失败）；
//   - Akka 端口可达性（host 直通，worker 接入依赖它）。
func (d *Driver) ScriptTail(ctx stackkit.ProbeCtx) string {
	port := stackkit.ShellQuote(stackkit.PickParam(ctx.Params, "server_port", "7700"))
	akka := stackkit.ShellQuote(stackkit.PickParam(ctx.Params, "akka_port", "10086"))
	var b strings.Builder
	b.WriteString(`for _pjc in $(docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^powerjob-server-[0-9]+$|^powerjob-mysql$'); do
  inspect_ctr "${_pjc}"
done
`)
	b.WriteString("_PJ_PORT=" + port + "\n")
	b.WriteString("_PJ_AKKA=" + akka + "\n")
	b.WriteString(`if docker ps --format '{{.Names}}' 2>/dev/null | grep -qE '^powerjob-server-[0-9]+$'; then
  echo __IO_PJ_CONSOLE__=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${_PJ_PORT}/" 2>/dev/null || true)
  if bash -c "</dev/tcp/127.0.0.1/${_PJ_AKKA}" 2>/dev/null; then
    echo __IO_PJ_AKKA__=1
  else
    echo __IO_PJ_AKKA__=0
  fi
fi
`)
	return b.String()
}

// ParseSuite 解析 PowerJob 探活输出。
// 不设 OK：本主机是否通过由引擎按全部检查项共同判定（ChecksOK）。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, _ []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	ctrs := parsePjContainers(raw)
	hasServer := false
	image := ""
	for _, c := range ctrs {
		comp := containerComponent(c.name)
		ok, detail, img, _ := stackkit.ParseCtrStateEx(c.name, c.state)
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "实例", Component: comp, OK: ok, Detail: c.name + " " + detail,
		})
		if comp == compPowerjob {
			hasServer = true
			if image == "" {
				image = img
			}
		}
	}
	if image != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", Component: compPowerjob, OK: true, Detail: image})
	}
	if hasServer {
		out.Checks = append(out.Checks, pjConsoleCheck(raw))
		out.Checks = append(out.Checks, pjAkkaCheck(ctx, raw))
	}
	return out
}

// pjConsoleCheck 「控制台」检查：HTTP 响应码（2xx / 3xx 均视为已响应——未登录跳登录页是正常形态）。
func pjConsoleCheck(raw string) stackkit.Check {
	c := stackkit.Check{Name: "控制台", Component: compPowerjob}
	code := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_PJ_CONSOLE__="))
	switch {
	case code == "" || code == "000":
		c.Detail = "控制台无响应"
	case code[0] == '2' || code[0] == '3':
		c.OK = true
		c.Detail = "控制台 HTTP " + code
	default:
		c.Detail = "控制台 HTTP " + code
	}
	return c
}

// pjAkkaCheck 「通信」检查：Akka 端口可达性（host 直通，worker 接入依赖它）。
func pjAkkaCheck(ctx stackkit.ProbeCtx, raw string) stackkit.Check {
	akka := stackkit.PickParam(ctx.Params, "akka_port", "10086")
	c := stackkit.Check{Name: "通信", Component: compPowerjob}
	switch strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_PJ_AKKA__=")) {
	case "1":
		c.OK = true
		c.Detail = "Akka " + akka + " 端口可达"
	case "0":
		c.Detail = "Akka " + akka + " 端口不可达"
	default:
		c.Detail = "未探测到 Akka " + akka + " 端口"
	}
	return c
}

// Summary PowerJob 汇总提示与控制台入口。
func (d *Driver) Summary(ctx stackkit.ProbeCtx, hosts []stackkit.ProbeHostRow) ([]string, string) {
	port := stackkit.PickParam(ctx.Params, "server_port", "7700")
	akka := stackkit.PickParam(ctx.Params, "akka_port", "10086")
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
		"worker 配置任一存活节点地址即可接入（Akka " + akka + " 端口 host 直通），元数据在共享 MySQL。",
		strconv.Itoa(len(hosts)) + " 台成员中 " + strconv.Itoa(live) + " 台探活通过。",
	}
	return notes, "http://" + ip + ":" + port + "/"
}

// ---------- 解析助手 ----------

type pjCtr struct {
	name  string
	state string
}

// parsePjContainers 从探活原始输出里取本套件容器（按名字排序，保证快照稳定）。
func parsePjContainers(raw string) []pjCtr {
	var out []pjCtr
	for _, m := range ctrLineRE.FindAllStringSubmatch(raw, -1) {
		name := strings.TrimSpace(m[1])
		if !ctrNameRE.MatchString(name) {
			continue
		}
		out = append(out, pjCtr{name: name, state: strings.TrimSpace(m[2])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
