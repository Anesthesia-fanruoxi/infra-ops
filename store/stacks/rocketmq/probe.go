package rocketmq

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

// probe.go：RocketMQ 探活（本机容器 / 注册（clusterList）/ NameServer / brokerStatus）。
// 全部实现落在本套件内，引擎只做分发与聚合（api/stack/stack_verify_*.go 零分支）。
//
// 与 kafka / nacos 探活同构：容器名带运行期变量（rmq-namesrv-<cluster>、
// rmq-broker-<broker_name>-<broker_id>，broker_id 为 __seq 或 boot 的 0），
// 探活上下文（stackkit.ProbeCtx）拿不到这些值 ⇒ 无法预知容器名。因此：
//   - Containers 返回空 ⇒ 引擎先做通用 compose 状态检查（「容器」检查项）；
//   - ScriptTail 用 `docker ps` 现取真实容器名，复用引擎已定义的 inspect_ctr 逐个输出；
//   - ParseSuite 按真实名字分配检查项，产出「实例 / 版本 / 注册 / 名称服务 / 状态」。
//
// 探活数据面（apache/rocketmq:4.9.7 真机实测，见 docs/单模式套件方案.md §12）：
//   - mqadmin 的真实路径是 $ROCKETMQ_HOME/bin/mqadmin —— 镜像内 /home/rocketmq/bin
//     并不存在（node.sh 自校验曾因此静默失效），必须经容器内 bash 展开；
//   - clusterList：首行 `#Cluster Name #Broker Name #BID #Addr ...` 表头，
//     数据行为空格对齐；同组多行 = 各 broker（BID=0 为 master，>0 为 slave）；
//   - brokerStatus：`key : value` 逐行（无表头），含 brokerVersionDesc 与
//     commitLogDirCapacity（commitlog 写满会拒写，是「服务在跑但写不进」的信号）；
//   - namesrvAddr 从 broker.conf 现读：部署时 sed 注入 master_ip:ns_port，
//     是「NameServer 单点」判定的唯一事实源（探活上下文无 master_ip）。
//
// 注册检查是全套件的核心：同组同名不同 brokerId 会互相覆盖注册（4.9.7 实测：
// 仅 brokerId 非 0 而未显式 brokerRole=SLAVE 时，broker 仍以 brokerId=0 启动，
// 与 master 每 30 秒交替覆盖 namesrv 注册）——此时本机地址会从 clusterList 中消失，
// 由「注册」检查项红灯显式报出，这是该故障形态在探活层的唯一可观测入口。

const compRocket = "rocketmq"

// ctrLineRE 匹配引擎探活脚本输出的容器状态行（含 ScriptTail 里循环调用的 inspect_ctr）。
var ctrLineRE = regexp.MustCompile(`(?m)^__IO_CTR__([^=\s]+)__=(.*)$`)

// ctrNameRE 本套件的容器名：rmq-namesrv-<cluster>（仅首节点）、rmq-broker-<name>-<id>。
var ctrNameRE = regexp.MustCompile(`^rmq-(namesrv|broker)-.+$`)

// Containers 本机期望容器名：**故意返回空**。
//
// 容器名含运行期变量（cluster_name / broker_name / broker_id），探活上下文拿不到；
// 引擎在容器清单为空时做通用 compose 检查，再由本文件的 ParseSuite 补齐专属检查项。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string { return nil }

// ScriptTail 追加 RocketMQ 专属探活片段：
//   - 本机 rmq-* 容器逐个 inspect（容器名从 docker ps 现取）；
//   - 从 broker.conf 读 namesrvAddr，并做 NameServer 端口连通性探测（worker 视角）；
//   - clusterList：本 broker 是否已注册 + BID（角色/副本判定的唯一入口）；
//   - brokerStatus：版本与 commitlog 磁盘容量。
func (d *Driver) ScriptTail(ctx stackkit.ProbeCtx) string {
	brokerPort := stackkit.ShellQuote(stackkit.PickParam(ctx.Params, "broker_port", "10911"))
	var b strings.Builder
	b.WriteString(`for _rc in $(docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^rmq-(namesrv|broker)-'); do
  inspect_ctr "${_rc}"
done
_RMQ_SELF=` + stackkit.ShellQuote(ctx.HostIP) + `
_RMQ_BPORT=` + brokerPort + `
_RMQ_BCTR="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^rmq-broker-' | head -n 1)"
if [ -n "${_RMQ_BCTR}" ]; then
  _RMQ_NS="$(docker exec "${_RMQ_BCTR}" grep '^namesrvAddr' /home/rocketmq/conf/broker.conf 2>/dev/null | head -n 1 | cut -d= -f2 | tr -d ' \r')"
  echo __IO_RMQ_NS__="${_RMQ_NS}"
  if [ -n "${_RMQ_NS}" ]; then
    _RMQ_NS_IP="${_RMQ_NS%%:*}"
    _RMQ_NS_PORT="${_RMQ_NS##*:}"
    if docker exec "${_RMQ_BCTR}" bash -c "</dev/tcp/${_RMQ_NS_IP}/${_RMQ_NS_PORT}" 2>/dev/null; then
      echo __IO_RMQ_NSOK__=1
    else
      echo __IO_RMQ_NSOK__=0
    fi
    echo __IO_RMQ_CL_BEGIN__
    docker exec "${_RMQ_BCTR}" bash -c "sh \$ROCKETMQ_HOME/bin/mqadmin clusterList -n ${_RMQ_NS} 2>&1" || true
    echo __IO_RMQ_CL_END__
  fi
  echo __IO_RMQ_BS_BEGIN__
  docker exec "${_RMQ_BCTR}" bash -c "sh \$ROCKETMQ_HOME/bin/mqadmin brokerStatus -b ${_RMQ_SELF}:${_RMQ_BPORT} ${_RMQ_NS:+-n ${_RMQ_NS}} 2>&1" || true
  echo __IO_RMQ_BS_END__
fi
`)
	return b.String()
}

// ParseSuite 解析 RocketMQ 探活输出。
// 不设 OK：本主机是否通过由引擎按全部检查项共同判定（ChecksOK）。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, _ []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	ctrs := parseRocketContainers(raw)
	hasBroker, hasNS := false, false
	image := ""
	for _, c := range ctrs {
		ok, detail, img, _ := stackkit.ParseCtrStateEx(c.name, c.state)
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "实例", Component: compRocket, OK: ok, Detail: c.name + " " + detail,
		})
		if strings.HasPrefix(c.name, "rmq-broker-") {
			hasBroker = true
			if image == "" {
				image = img
			}
		}
		if strings.HasPrefix(c.name, "rmq-namesrv-") {
			hasNS = true
		}
	}

	ns := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_RMQ_NS__="))
	bs := parseColonKV(stackkit.ExtractBlock(raw, "__IO_RMQ_BS_BEGIN__", "__IO_RMQ_BS_END__"))

	if v := stackkit.Nz(bs["brokerVersionDesc"], image); v != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", Component: compRocket, OK: true, Detail: v})
	}

	if hasNS || hasBroker {
		out.Checks = append(out.Checks, rmqNamesrvCheck(ctx, raw, hasNS, hasBroker, ns))
	}
	if hasBroker {
		reg := rmqRegisterCheck(ctx, raw, ns)
		out.Checks = append(out.Checks, reg)
		if reg.OK {
			out.LiveRole = rmqLiveRole(reg.Detail)
			out.LiveRoleFromOutput = true
		}
		out.Checks = append(out.Checks, rmqStatusCheck(bs))
	}
	return out
}

// rmqNamesrvCheck 「名称服务」检查：NameServer 全集群只在 master 机部署（单点），
// 显式报出它的存在形态——master 机看容器，worker 机看 namesrvAddr 的端口可达性。
func rmqNamesrvCheck(ctx stackkit.ProbeCtx, raw string, hasNS, hasBroker bool, ns string) stackkit.Check {
	c := stackkit.Check{Name: "名称服务", Component: compRocket}
	if hasNS {
		st := ""
		for _, m := range ctrLineRE.FindAllStringSubmatch(raw, -1) {
			if strings.HasPrefix(strings.TrimSpace(m[1]), "rmq-namesrv-") {
				st = strings.TrimSpace(m[2])
			}
		}
		ok, _, _, _ := stackkit.ParseCtrStateEx("rmq-namesrv", st)
		c.OK = ok
		c.Detail = "本机 NameServer（集群唯一入口，单点）"
		return c
	}
	if ns == "" {
		c.Detail = "未取到 namesrvAddr，无法核对 NameServer"
		return c
	}
	if s := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_RMQ_NSOK__=")); s == "1" {
		c.OK = true
		c.Detail = "NameServer " + ns + " 可达（位于 master 节点，单点）"
		return c
	}
	if ctx.Role == "master" {
		c.Detail = "本机未发现 NameServer 容器（master 应部署 NameServer）"
		return c
	}
	c.Detail = "NameServer " + ns + " 不可达"
	return c
}

// rmqRegisterCheck 「注册」检查：本机 broker 地址是否出现在 clusterList（含 BID）——
// 同组多 broker 互相覆盖注册时本机地址会消失，是「双 master 抢注」的唯一探活出口。
func rmqRegisterCheck(ctx stackkit.ProbeCtx, raw string, ns string) stackkit.Check {
	c := stackkit.Check{Name: "注册", Component: compRocket}
	self := ctx.HostIP + ":" + stackkit.PickParam(ctx.Params, "broker_port", "10911")
	if !strings.Contains(raw, "__IO_RMQ_CL_BEGIN__") {
		c.Detail = "未取到 namesrvAddr（" + stackkit.Nz(ns, "空") + "），无法核对注册"
		return c
	}
	for _, r := range parseClusterList(stackkit.ExtractBlock(raw, "__IO_RMQ_CL_BEGIN__", "__IO_RMQ_CL_END__")) {
		if r.addr != self {
			continue
		}
		c.OK = true
		c.Detail = self + " · BID=" + strconv.Itoa(r.bid) + "（" + rmqBidRole(r.bid) + "）"
		return c
	}
	c.Detail = self + " 未出现在 clusterList（未注册，或已被同组同名 broker 覆盖注册）"
	return c
}

// rmqStatusCheck 「状态」检查：brokerStatus 可响应 + commitlog 磁盘容量。
// 磁盘展示不做阈值判断（避免经验参数误报）：写满拒写前用户已能从这里看到剩余量。
func rmqStatusCheck(bs map[string]string) stackkit.Check {
	c := stackkit.Check{Name: "状态", Component: compRocket}
	if len(bs) == 0 {
		c.Detail = "brokerStatus 无响应"
		return c
	}
	c.OK = true
	c.Detail = "brokerStatus 已响应"
	if d := rmqDiskSummary(bs["commitLogDirCapacity"]); d != "" {
		c.Detail = "commitlog 磁盘 " + d
	}
	return c
}

// rmqDiskSummary 从 `Total : 1006.9 GiB, Free : 946.8 GiB.` 提取 `Free x / Total y`。
func rmqDiskSummary(s string) string {
	m := diskCapRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return "Free " + m[2] + " / Total " + m[1]
}

// rmqBidRole brokerId → 实例角色文案（0 = master，非 0 = worker，对齐 run host 角色体系）。
func rmqBidRole(bid int) string {
	if bid == 0 {
		return "master"
	}
	return "worker"
}

// rmqLiveRole 从「注册」检查项明细里取实测角色（master / worker）。
func rmqLiveRole(detail string) string {
	if strings.Contains(detail, "（master）") {
		return "master"
	}
	if strings.Contains(detail, "（worker）") {
		return "worker"
	}
	return ""
}

// ---------- 解析助手 ----------

// diskCapRE 匹配 brokerStatus 的 commitLogDirCapacity 值（实测 `Total : 1006.9 GiB, Free : 946.8 GiB.`，
// Free 侧带结尾句点，单位固定为纯字母，故限定 [A-Za-z]+ 避免吞掉标点）。
var diskCapRE = regexp.MustCompile(`Total : ([\d.]+ [A-Za-z]+), Free : ([\d.]+ [A-Za-z]+)`)

type rocketCtr struct {
	name  string
	state string
}

// parseRocketContainers 从探活原始输出里取本套件容器（按名字排序，保证快照稳定）。
func parseRocketContainers(raw string) []rocketCtr {
	var out []rocketCtr
	for _, m := range ctrLineRE.FindAllStringSubmatch(raw, -1) {
		name := strings.TrimSpace(m[1])
		if !ctrNameRE.MatchString(name) {
			continue
		}
		out = append(out, rocketCtr{name: name, state: strings.TrimSpace(m[2])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

type rocketBrokerRow struct {
	broker string
	bid    int
	addr   string
}

// parseClusterList 解析 mqadmin clusterList 输出：
// 跳过 `#` 表头与错误行，数据行按空白切分取 brokerName / BID / Addr。
func parseClusterList(block string) []rocketBrokerRow {
	var out []rocketBrokerRow
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		bid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		out = append(out, rocketBrokerRow{broker: f[1], bid: bid, addr: f[3]})
	}
	return out
}

// parseColonKV 解析「Key : value」逐行输出（brokerStatus；无冒号行跳过）。
func parseColonKV(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}
