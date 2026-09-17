package redis

import (
	"fmt"
	"sort"
	"strings"

	"infra-ops/store/stackkit"
)

// probe.go：Redis 探活（脚本尾片段 / 期望容器 / 输出解析 / 接入提示）。
// 原 api/stack 的 stack_verify_script.go（redis 分支）、stack_verify_parse.go（redis 分支）、
// stack_verify_redis.go 原样迁入，检查项顺序与文案逐字节一致。

// 组件归属（探活对话框按组件分页签，骨架 common/verify-dialog.js 的 probeAttributed 据此过滤）：
// 哨兵模式下 Redis 本体与 Sentinel 是两个独立容器/进程，各占一个页签；
// 主从与集群模式只有 Redis 本体，前端 verifyTabbed 因此只对哨兵返回 true。
const (
	compRedis    = "redis"
	compSentinel = "sentinel"
)

// containerComponent 容器名 → 组件键。哨兵容器固定叫 redis-sentinel，
// 数据容器叫 redis-sentinel-data（名字里含 sentinel 但属于 Redis 本体），故按精确名判断。
func containerComponent(name string) string {
	if name == "redis-sentinel" {
		return compSentinel
	}
	return compRedis
}

// Containers 本机期望容器名。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string {
	switch ctx.Mode {
	case "cluster":
		return []string{"redis-cluster"}
	case "sentinel":
		return []string{"redis-sentinel-data", "redis-sentinel"}
	default:
		return []string{"redis-repl"}
	}
}

// ScriptTail 追加 Redis 专属探活片段（PING / INFO replication / INFO server / CLUSTER / SENTINEL）。
func (d *Driver) ScriptTail(ctx stackkit.ProbeCtx) string {
	dataCtr := "redis-repl"
	switch ctx.Mode {
	case "cluster":
		dataCtr = "redis-cluster"
	case "sentinel":
		dataCtr = "redis-sentinel-data"
	}
	var b strings.Builder
	b.WriteString("DATA_CTR=" + stackkit.ShellQuote(dataCtr) + "\n")
	b.WriteString(`redis_cli() {
  docker exec -e REDISCLI_AUTH="$PASS" "$1" redis-cli --raw --no-auth-warning "${@:2}" 2>/dev/null
}
echo "__IO_PING__=$(redis_cli "$DATA_CTR" PING | tr -d '\r')"
echo __IO_REPL_BEGIN__
redis_cli "$DATA_CTR" INFO replication
echo __IO_REPL_END__
echo __IO_SRV_BEGIN__
redis_cli "$DATA_CTR" INFO server
echo __IO_SRV_END__
`)
	if ctx.Mode == "cluster" {
		b.WriteString(`echo __IO_CLUSTER_BEGIN__
redis_cli "$DATA_CTR" CLUSTER INFO
echo __IO_CLUSTER_END__
echo __IO_NODES_BEGIN__
redis_cli "$DATA_CTR" CLUSTER NODES
echo __IO_NODES_END__
`)
	}
	if ctx.Mode == "sentinel" {
		sPort := stackkit.PickParam(ctx.Params, "sentinel_port", "26379")
		mName := stackkit.PickParam(ctx.Params, "master_name", "mymaster")
		b.WriteString("SENT_CTR=redis-sentinel\n")
		b.WriteString("SENT_PORT=" + stackkit.ShellQuote(sPort) + "\n")
		b.WriteString("SENT_MASTER=" + stackkit.ShellQuote(mName) + "\n")
		b.WriteString(`echo "__IO_SENT_PING__=$(redis_cli "$SENT_CTR" -p "$SENT_PORT" PING | tr -d '\r')"
echo __IO_SENT_BEGIN__
redis_cli "$SENT_CTR" -p "$SENT_PORT" SENTINEL master "$SENT_MASTER"
echo __IO_SENT_END__
`)
	}
	return b.String()
}

// ParseSuite 解析 Redis 探活输出：容器 / PING / 角色 / 复制 / 版本 / 集群 / 哨兵。
// 不设 OK：本主机是否通过由引擎按全部检查项共同判定（与拆分前一致）。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, containers []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	for _, name := range containers {
		st := stackkit.ExtractLine(raw, "__IO_CTR__"+name+"__=")
		ok, detail := stackkit.ParseCtrState(name, st)
		out.Checks = append(out.Checks, stackkit.Check{Name: "容器 " + name, Component: containerComponent(name), OK: ok, Detail: detail})
	}

	ping := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_PING__="))
	pingOK := strings.EqualFold(ping, "PONG")
	pingDetail := ping
	if ping == "" {
		pingDetail = "无响应"
	}
	out.Checks = append(out.Checks, stackkit.Check{Name: "PING", Component: compRedis, OK: pingOK, Detail: pingDetail})

	repl := stackkit.ExtractBlock(raw, "__IO_REPL_BEGIN__", "__IO_REPL_END__")
	info := parseRedisInfo(repl)
	live := strings.TrimSpace(info["role"])
	out.LiveRole = live
	out.LiveRoleFromOutput = true
	if live != "" {
		roleOK := redisRoleMatches(ctx.Role, live)
		detail := liveRoleLabel(live)
		if n := strings.TrimSpace(info["connected_slaves"]); live == "master" && n != "" {
			detail += " · 从库 " + n
		}
		if mh := strings.TrimSpace(info["master_host"]); live != "master" && mh != "" {
			detail += " · 跟随 " + mh
		}
		out.Checks = append(out.Checks, stackkit.Check{Name: "角色", Component: compRedis, OK: roleOK, Detail: detail})
	}
	if live == "slave" || live == "replica" {
		link := strings.TrimSpace(info["master_link_status"])
		out.Checks = append(out.Checks, stackkit.Check{Name: "复制", Component: compRedis, OK: link == "up", Detail: "master_link_status=" + stackkit.Nz(link, "未知")})
	}
	if live == "master" {
		if slaves := parseConnectedSlaves(info); len(slaves) > 0 {
			out.Checks = append(out.Checks, stackkit.Check{Name: "复制", Component: compRedis, OK: true, Detail: strings.Join(slaves, "; ")})
		}
	}

	if ver := redisVersion(stackkit.ExtractBlock(raw, "__IO_SRV_BEGIN__", "__IO_SRV_END__")); ver != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", Component: compRedis, OK: true, Detail: ver})
	}

	if ctx.Mode == "cluster" {
		ci := parseRedisInfo(stackkit.ExtractBlock(raw, "__IO_CLUSTER_BEGIN__", "__IO_CLUSTER_END__"))
		state := strings.TrimSpace(ci["cluster_state"])
		detail := "cluster_state:" + stackkit.Nz(state, "未知")
		if v := ci["cluster_slots_assigned"]; v != "" {
			detail += " · slots " + v
		}
		if v := ci["cluster_known_nodes"]; v != "" {
			detail += " · nodes " + v
		}
		out.Checks = append(out.Checks, stackkit.Check{Name: "集群", Component: compRedis, OK: state == "ok", Detail: detail})
	}
	if ctx.Mode == "sentinel" {
		sp := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_SENT_PING__="))
		sentPing := strings.EqualFold(sp, "PONG")
		sm := parseSentinelMaster(stackkit.ExtractBlock(raw, "__IO_SENT_BEGIN__", "__IO_SENT_END__"))
		flags := sm["flags"]
		sentOK := sentPing && strings.Contains(flags, "master") && !strings.Contains(flags, "down")
		detail := "PING " + stackkit.Nz(sp, "无")
		if flags != "" {
			detail += " · flags=" + flags
		}
		if sm["ip"] != "" {
			detail += " · 主 " + sm["ip"] + ":" + sm["port"]
		}
		out.Checks = append(out.Checks, stackkit.Check{Name: "哨兵", Component: compSentinel, OK: sentOK, Detail: detail})
	}
	return out
}

// Summary Redis 汇总提示与客户端命令 + 拓扑结论（原 redisVerifyHints + applyRedisClusterChecks）。
func (d *Driver) Summary(ctx stackkit.ProbeCtx, hosts []stackkit.ProbeHostRow) ([]string, string) {
	port := stackkit.PickParam(ctx.Params, "port", "6379")
	ip := ""
	for _, h := range hosts {
		if h.LiveRole == "master" || h.Role == "master" {
			ip = h.HostIP
			break
		}
	}
	if ip == "" && len(hosts) > 0 {
		ip = hosts[0].HostIP
	}
	var notes []string
	var hint string
	switch ctx.Mode {
	case "cluster":
		notes = []string{"集群模式请用 redis-cli -c 连接任一节点。"}
		hint = "redis-cli -c -h " + ip + " -p " + port
	case "sentinel":
		sp := stackkit.PickParam(ctx.Params, "sentinel_port", "26379")
		mName := stackkit.PickParam(ctx.Params, "master_name", "mymaster")
		notes = []string{"应用侧连 Sentinel，主从地址以 SENTINEL 查询为准。"}
		hint = "redis-cli -h " + ip + " -p " + sp + " SENTINEL get-master-addr-by-name " + mName
	default:
		notes = []string{"业务请连主节点；从库默认只读。"}
		hint = "redis-cli -h " + ip + " -p " + port
	}
	notes = append(notes, topologyNotes(ctx.Mode, hosts)...)
	return notes, hint
}

// topologyNotes 拓扑结论（原 applyRedisClusterChecks，复用该模式下的检查项）。
func topologyNotes(mode string, hosts []stackkit.ProbeHostRow) []string {
	var notes []string
	switch mode {
	case "replication":
		masters, replicas, linkDown := 0, 0, 0
		for _, h := range hosts {
			switch h.LiveRole {
			case "master":
				masters++
			case "slave", "replica":
				replicas++
			}
			for _, c := range h.Checks {
				if c.Name == "复制" && !c.OK {
					linkDown++
				}
			}
		}
		if masters != 1 {
			notes = append(notes, fmt.Sprintf("主从拓扑异常：存活主节点 %d 个（应为 1）", masters))
		}
		if linkDown > 0 {
			notes = append(notes, "有从库复制链路未就绪")
		}
		if masters == 1 && replicas > 0 && linkDown == 0 {
			notes = append(notes, fmt.Sprintf("主从复制正常（1 主 %d 从）", replicas))
		}
	case "cluster":
		for _, h := range hosts {
			for _, c := range h.Checks {
				if c.Name == "集群" && strings.Contains(c.Detail, "cluster_state:ok") {
					return nil
				}
			}
		}
		for _, h := range hosts {
			if h.OK {
				notes = append(notes, "集群状态未就绪（cluster_state 不是 ok）")
				break
			}
		}
	case "sentinel":
		okSent := 0
		for _, h := range hosts {
			for _, c := range h.Checks {
				if c.Name == "哨兵" && c.OK {
					okSent++
				}
			}
		}
		if okSent < 3 && len(hosts) >= 3 {
			notes = append(notes, fmt.Sprintf("哨兵探活成功 %d 个，建议至少 3 个", okSent))
		}
	}
	return notes
}

// Endpoints Redis 接入端点（原 stackVerifyEndpoints 的 redis 分支）。
// Component 决定探活对话框里的页签归属：哨兵模式下 redis:// 与 redis-sentinel:// 分属两个页签。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	h := ctx.Host
	p := stackkit.PickParam(ctx.Params, "port", "6379")
	var out []stackkit.Endpoint
	name := "Redis"
	if ctx.Mode == "replication" || ctx.Mode == "sentinel" {
		if h.Role == "master" {
			name = "Redis 主"
		} else {
			name = "Redis 从"
		}
	}
	out = append(out, stackkit.Endpoint{Name: name, Component: compRedis, URL: "redis://" + h.HostIP + ":" + p, Role: h.Role})
	if ctx.Mode == "sentinel" {
		sp := stackkit.PickParam(ctx.Params, "sentinel_port", "26379")
		out = append(out, stackkit.Endpoint{Name: "Sentinel", Component: compSentinel, URL: "redis-sentinel://" + h.HostIP + ":" + sp, Role: h.Role})
	}
	return out
}

// ---------- Redis INFO / SENTINEL 解析（原 stack_verify_redis.go） ----------

func parseRedisInfo(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
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

func parseConnectedSlaves(info map[string]string) []string {
	var keys []string
	for k := range info {
		if strings.HasPrefix(k, "slave") && k != "connected_slaves" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		ip, state := "", ""
		for _, p := range strings.Split(info[k], ",") {
			kk, vv, ok := strings.Cut(p, "=")
			if !ok {
				continue
			}
			switch kk {
			case "ip":
				ip = vv
			case "state":
				state = vv
			}
		}
		if ip != "" {
			out = append(out, strings.TrimSpace(ip+" "+state))
		}
	}
	return out
}

func parseSentinelMaster(s string) map[string]string {
	out := map[string]string{}
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if a := strings.Index(line, `"`); a >= 0 {
			if b := strings.LastIndex(line, `"`); b > a {
				line = line[a+1 : b]
			}
		}
		lines = append(lines, line)
	}
	for i := 0; i+1 < len(lines); i += 2 {
		out[lines[i]] = lines[i+1]
	}
	return out
}

func redisVersion(s string) string {
	info := parseRedisInfo(s)
	v := strings.TrimSpace(info["redis_version"])
	if v == "" {
		return ""
	}
	if u := strings.TrimSpace(info["uptime_in_days"]); u != "" {
		return v + " · 已运行 " + u + " 天"
	}
	return v
}

func redisRoleMatches(expected, live string) bool {
	if expected == "" || expected == "node" {
		return true
	}
	live = strings.ToLower(live)
	switch expected {
	case "master":
		return live == "master"
	case "replica":
		return live == "slave" || live == "replica"
	}
	return true
}

func liveRoleLabel(role string) string {
	switch strings.ToLower(role) {
	case "master":
		return "master"
	case "slave", "replica":
		return "replica"
	default:
		return role
	}
}
