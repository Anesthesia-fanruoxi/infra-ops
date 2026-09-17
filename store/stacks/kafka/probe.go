package kafka

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

// probe.go：Kafka 探活（本机容器 / KRaft 仲裁 / ZK ensemble / 汇总提示）。
// 全部实现落在本套件内，引擎只做分发与聚合（api/stack/stack_verify_*.go 零分支）。
//
// 与 redis 探活的一处结构差异：kafka 的容器名带**运行期序号**
// （kraft 下 kafka-<seq>、zk 下 kafka-<seq> 与 kafka-zk-<seq>），而探活上下文
// （stackkit.ProbeCtx）只有主机 IP 与参数，拿不到该序号 ⇒ 无法预知容器名。
// 因此：
//   - Containers 返回空 ⇒ 引擎先做通用 compose 状态检查（「容器」检查项，含每个容器名）；
//   - ScriptTail 用 `docker ps` 现取真实容器名，复用引擎已定义的 inspect_ctr 逐个输出；
//   - ParseSuite 按真实名字分配组件归属，产出「实例 / 版本 / 仲裁 / 集群」检查项。
//
// 这样将来容器命名规则再变（例如改为按主机名命名）探活也不会失效。

// 组件归属（探活对话框按组件分页签；骨架 common/verify-dialog.js 的 probeAttributed
// 依据端点与检查项上的 component 过滤出真实存在的页签）。
const (
	compKafka = "kafka"
	compZK    = "zookeeper"
	compUI    = "ui"
)

// ctrLineRE 匹配引擎探活脚本输出的容器状态行（含 ScriptTail 里循环调用的 inspect_ctr）。
var ctrLineRE = regexp.MustCompile(`(?m)^__IO_CTR__([^=\s]+)__=(.*)$`)

// ctrNameRE 本套件的容器名：kafka-<n>（broker）、kafka-zk-<n>（ZooKeeper）、kafka-ui。
var ctrNameRE = regexp.MustCompile(`^kafka(-zk)?-(\d+|ui)$`)

// votersRE 从「仲裁」检查项明细里取 voters 数（Summary 聚合用）。
var votersRE = regexp.MustCompile(`voters=(\d+)`)

// containerComponent 容器名 → 组件键。
func containerComponent(name string) string {
	switch {
	case name == "kafka-ui":
		return compUI
	case strings.HasPrefix(name, "kafka-zk-"):
		return compZK
	default:
		return compKafka
	}
}

// Containers 本机期望容器名：**故意返回空**。
//
// 容器名含运行期序号（kafka-<seq>），探活上下文拿不到 seq 故无法预知；
// 引擎在容器清单为空时做通用 compose 检查，再由本文件的 ParseSuite 补齐组件化检查项。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string { return nil }

// ScriptTail 追加 Kafka 专属探活片段：
//   - 本机 kafka 相关容器逐个 inspect（复用引擎脚本里的 inspect_ctr 函数）；
//   - KRaft：controller quorum 状态（voters 数 vs leader）——缩容失去多数派的唯一可观测入口；
//   - ZK：ensemble 的 `srvr` 四字命令（Mode: leader/follower）。
func (d *Driver) ScriptTail(ctx stackkit.ProbeCtx) string {
	var b strings.Builder
	// 容器名从 docker ps 现取：kafka-<n> / kafka-zk-<n> / kafka-ui（UI 仅首台有，其他主机自然为空）
	b.WriteString(`for _kc in $(docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^kafka(-zk)?-[0-9]+$|^kafka-ui$'); do
  inspect_ctr "${_kc}"
done
`)
	if ctx.Mode == "kraft" {
		// kafka-metadata-quorum describe --status 直接给出 CurrentVoters 与 LeaderId
		b.WriteString(`_KCTR="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^kafka-[0-9]+$' | head -1)"
if [ -n "${_KCTR}" ]; then
  echo __IO_QUORUM_BEGIN__
  docker exec "${_KCTR}" /opt/kafka/bin/kafka-metadata-quorum.sh --bootstrap-server localhost:9092 describe --status 2>&1 || true
  echo __IO_QUORUM_END__
fi
`)
	}
	if ctx.Mode == "zk" {
		zkPort := stackkit.ShellQuote(stackkit.PickParam(ctx.Params, "zk_port", "2181"))
		b.WriteString("_ZK_PORT=" + zkPort + "\n")
		// srvr 会返回 Mode: leader / follower / standalone；服务无响应时块内为空 ⇒ 判失败
		b.WriteString(`_ZCTR="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^kafka-zk-[0-9]+$' | head -1)"
if [ -n "${_ZCTR}" ]; then
  echo __IO_ZK_SRVR_BEGIN__
  ( exec 3<>/dev/tcp/127.0.0.1/${_ZK_PORT}; printf 'srvr\n' >&3; head -n 20 <&3 ) 2>/dev/null || true
  echo __IO_ZK_SRVR_END__
fi
`)
	}
	return b.String()
}

// ParseSuite 解析 Kafka 探活输出。
// 不设 OK：本主机是否通过由引擎按全部检查项共同判定（ChecksOK）。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, _ []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	ctrs := parseKafkaContainers(raw)
	hasBroker, hasZK := false, false
	version := ""
	for _, c := range ctrs {
		comp := containerComponent(c.name)
		ok, detail, image, _ := stackkit.ParseCtrStateEx(c.name, c.state)
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "实例", Component: comp, OK: ok, Detail: c.name + " " + detail,
		})
		switch comp {
		case compKafka:
			hasBroker = true
			if version == "" {
				version = image
			}
		case compZK:
			hasZK = true
		}
	}
	if version != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", Component: compKafka, OK: true, Detail: version})
	}

	if ctx.Mode == "kraft" && hasBroker {
		info := parseColonKV(stackkit.ExtractBlock(raw, "__IO_QUORUM_BEGIN__", "__IO_QUORUM_END__"))
		leader := strings.TrimSpace(info["LeaderId"])
		detail := "leader=" + stackkit.Nz(leader, "无")
		if n := parseBracketCount(info["CurrentVoters"]); n > 0 {
			detail += " · voters=" + strconv.Itoa(n)
		}
		if lag := strings.TrimSpace(info["MaxFollowerLag"]); lag != "" {
			detail += " · 最大落后=" + lag
		}
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "仲裁", Component: compKafka, OK: leader != "", Detail: detail,
		})
	}

	if ctx.Mode == "zk" && hasZK {
		info := parseColonKV(stackkit.ExtractBlock(raw, "__IO_ZK_SRVR_BEGIN__", "__IO_ZK_SRVR_END__"))
		mode := strings.ToLower(strings.TrimSpace(info["Mode"]))
		ok := mode == "leader" || mode == "follower" || mode == "standalone"
		detail := "mode=" + stackkit.Nz(mode, "无响应")
		if nc := strings.TrimSpace(info["Node count"]); nc != "" {
			detail += " · nodes=" + nc
		}
		out.Checks = append(out.Checks, stackkit.Check{
			Name: "集群", Component: compZK, OK: ok, Detail: detail,
		})
	}
	return out
}

// Summary Kafka 汇总提示与客户端命令。
//
// 拓扑结论（原引擎无此套件的聚合逻辑，属本套件新增）：
//   - KRaft：把 controller voters 数与探活通过的主机数对照 —— 缩容后「集群看起来在跑、
//     却已低于仲裁多数派」是最隐蔽的故障，这里是指出它的唯一入口；
//   - ZK：leader 必须唯一。
func (d *Driver) Summary(ctx stackkit.ProbeCtx, hosts []stackkit.ProbeHostRow) ([]string, string) {
	port := stackkit.PickParam(ctx.Params, "port", "9092")
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

	var notes []string
	switch ctx.Mode {
	case "kraft":
		notes = append(notes, "KRaft 由 broker 内嵌 controller 仲裁，客户端连任一存活 broker 即可。")
		notes = append(notes, quorumNotes(hosts, live)...)
	case "zk":
		notes = append(notes, "ZooKeeper 仅承载元数据，客户端应连 broker 端口而非 ZK。")
		notes = append(notes, zkEnsembleNotes(hosts)...)
	default:
		notes = append(notes, "客户端连任一存活 broker 即可。")
	}
	if stackkit.IsYes(ctx.Params["enable_ui"]) && !hasUI(hosts) {
		notes = append(notes, "KafkaUI 异常：enable_ui 已开启但未探到 kafka-ui 容器（应只部署在首台主机）")
	}
	return notes, "/opt/kafka/bin/kafka-topics.sh --bootstrap-server " + ip + ":" + port + " --list"
}

// quorumNotes 按 voters 与存活台数给仲裁结论。
func quorumNotes(hosts []stackkit.ProbeHostRow, live int) []string {
	voters := 0
	for _, h := range hosts {
		for _, c := range h.Checks {
			if c.Name != "仲裁" {
				continue
			}
			if m := votersRE.FindStringSubmatch(c.Detail); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil {
					voters = n
				}
			}
		}
	}
	if voters == 0 {
		return nil
	}
	if need := voters/2 + 1; live < need {
		return []string{strconv.Itoa(voters) + " 台 controller voters 中仅 " + strconv.Itoa(live) +
			" 台探活通过，已低于多数派 " + strconv.Itoa(need) + "：仲裁异常，请恢复下线节点或改回各节点 controller.quorum.voters 后重启"}
	}
	if voters%2 == 0 {
		return []string{strconv.Itoa(voters) + " 台 voters 为偶数，建议保持 3 或 5 台以避免对等分区"}
	}
	return []string{"仲裁正常：controller voters " + strconv.Itoa(voters) + " 台，探活通过 " + strconv.Itoa(live) + " 台"}
}

// zkEnsembleNotes ZK 模式：leader 必须唯一。
func zkEnsembleNotes(hosts []stackkit.ProbeHostRow) []string {
	leaders, followers, total := 0, 0, 0
	for _, h := range hosts {
		for _, c := range h.Checks {
			if c.Name != "集群" || c.Component != compZK {
				continue
			}
			total++
			if strings.Contains(c.Detail, "mode=leader") {
				leaders++
			}
			if strings.Contains(c.Detail, "mode=follower") {
				followers++
			}
		}
	}
	if total == 0 {
		return nil
	}
	if leaders == 0 {
		return []string{"ZooKeeper ensemble 未选出 leader，仲裁异常"}
	}
	if leaders > 1 {
		return []string{"ZooKeeper ensemble 出现多个 leader（" + strconv.Itoa(leaders) + " 个），仲裁异常"}
	}
	return []string{"ZooKeeper ensemble 正常：1 leader + " + strconv.Itoa(followers) + " follower（共 " + strconv.Itoa(total) + " 台）"}
}

func hasUI(hosts []stackkit.ProbeHostRow) bool {
	for _, h := range hosts {
		for _, c := range h.Checks {
			if c.Component == compUI {
				return true
			}
		}
	}
	return false
}

// ---------- 解析助手 ----------

type kafkaCtr struct {
	name  string
	state string
}

// parseKafkaContainers 从探活原始输出里取本套件容器（按名字排序，保证快照稳定）。
func parseKafkaContainers(raw string) []kafkaCtr {
	var out []kafkaCtr
	for _, m := range ctrLineRE.FindAllStringSubmatch(raw, -1) {
		name := strings.TrimSpace(m[1])
		if !ctrNameRE.MatchString(name) {
			continue
		}
		out = append(out, kafkaCtr{name: name, state: strings.TrimSpace(m[2])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// parseColonKV 解析「Key: value」逐行输出（kafka-metadata-quorum、zk 的 srvr 都是这个格式）。
func parseColonKV(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
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

// parseBracketCount 解析 `[1,2,3]` 形式的成员列表长度（kafka-metadata-quorum 的 CurrentVoters）。
func parseBracketCount(s string) int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if strings.TrimSpace(s) == "" {
		return 0
	}
	n := 0
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}
