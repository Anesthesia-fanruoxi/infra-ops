package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/common/sysutil"
	"infra-ops/model"
)

type stackVerifyCheck struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
}

type stackVerifyHost struct {
	HostID   int64              `json:"host_id"`
	HostName string             `json:"host_name"`
	HostIP   string             `json:"host_ip"`
	Role     string             `json:"role"`
	LiveRole string             `json:"live_role,omitempty"`
	OK       bool               `json:"ok"`
	Error    string             `json:"error,omitempty"`
	Checks   []stackVerifyCheck `json:"checks"`
}

type stackVerifyEndpoint struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	URL       string `json:"url"`
	Role      string `json:"role,omitempty"`
}

type stackVerifyResult struct {
	OK         bool                  `json:"ok"`
	Summary    string                `json:"summary"`
	CheckedAt  string                `json:"checked_at"`
	Hint       string                `json:"hint,omitempty"`
	Endpoints  []stackVerifyEndpoint `json:"endpoints"`
	Hosts      []stackVerifyHost     `json:"hosts"`
	Notes      []string              `json:"notes,omitempty"`
	ClientHint string                `json:"client_hint,omitempty"`
}

// VerifyInstance POST /api/stacks/instances/:id/verify
// 控制面 SSH 到各成员探活：Redis 跑 PING / INFO / CLUSTER，其它套件检查 compose 容器是否在跑。
func (h *stackHandler) VerifyInstance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	if inst.Status == "uninstalled" {
		resp.Fail(c, resp.CodeBadRequest, "集群已卸载，无法探活")
		return
	}
	hosts := activeInstanceHosts(inst)
	if len(hosts) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "集群没有在役成员")
		return
	}
	// bigdata：探活前确保角色计划已物化（缺失时惰性生成并落库，docs/role-plan-design.md §6.1），
	// 之后 overrideBigdataMasters 以 Plan 的全量 masters 投影为唯一事实源。
	if inst.StackKey == "bigdata" {
		_, _ = h.loadRolePlan(inst)
	}
	instParams := parseJSONMap(inst.ParamsJSON)
	outHosts := make([]stackVerifyHost, len(hosts))
	conc := h.conc
	if conc <= 0 {
		conc = sysutil.AdaptiveConcurrency(len(hosts))
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outHosts[idx] = h.verifyInstanceHost(inst, hosts[idx], instParams)
		}(i)
	}
	wg.Wait()

	result := assembleStackVerify(inst, instParams, outHosts)
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.instance.verify", TargetType: "stack_instance", TargetID: id,
		Detail: fmt.Sprintf("name=%s ok=%v %s", inst.Name, result.OK, result.Summary), RemoteIP: c.ClientIP(),
	})
	resp.OK(c, result)
}

func (h *stackHandler) verifyInstanceHost(inst *model.StackInstance, host model.StackInstanceHost, instParams map[string]string) stackVerifyHost {
	row := stackVerifyHost{
		HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP, Role: host.Role,
		Checks: []stackVerifyCheck{},
	}
	params := mergeParamMaps(instParams, parseJSONMap(host.ParamsJSON))
	overrideBigdataMasters(inst, params)
	script := buildStackVerifyScript(inst.StackKey, inst.Mode, host.Role, host.HostIP, params)
	raw, execErr := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, host.HostID, script, nil)
	pass := strings.TrimSpace(params["password"])
	raw = redactSecret(raw, pass)
	if execErr != nil {
		row.Error = redactSecret(execErr.Error(), pass)
		if strings.TrimSpace(raw) == "" {
			row.OK = false
			row.Checks = append(row.Checks, stackVerifyCheck{Name: "连通", OK: false, Detail: row.Error})
			return row
		}
	}
	parseStackVerifyOutput(inst.StackKey, inst.Mode, host, params, raw, &row)
	return row
}

// CaCert GET /api/stacks/instances/:id/ca
// 下载 Elasticsearch 冷热温自签 SSL 的 CA 公钥（PEM）。任一在役节点均可（全员证书一致）。
func (h *stackHandler) CaCert(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	if inst.StackKey != "elasticsearch" || inst.Mode != "cold_warm_hot" {
		resp.Fail(c, resp.CodeBadRequest, "仅冷热温模式支持 SSL CA 下载")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(parseJSONMap(inst.ParamsJSON)["ssl_enabled"]), "true") {
		resp.Fail(c, resp.CodeBadRequest, "当前未启用 SSL，无 CA 证书")
		return
	}
	hosts := activeInstanceHosts(inst)
	if len(hosts) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "集群没有在役成员")
		return
	}
	hp := parseJSONMap(hosts[0].ParamsJSON)
	home := strings.TrimSpace(hp["home_dir"])
	if home == "" {
		home = "/data/elasticsearch"
	}
	raw, _ := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hosts[0].HostID,
		"cat '"+home+"/certs/ca.crt' 2>/dev/null || true", nil)
	c.Data(http.StatusOK, "application/x-pem-file", []byte(raw))
}

// 这是探活与部署之间唯一的一致性来源：备主落点（含 flink_jm2/hbase_hm2/hive_ms2/hive_hs2b）
// 由引擎按「第一台非主主机」自动分配，而持久化的 masters 里往往只有用户显式指定过的键。
func bigdataRoleMasters(inst *model.StackInstance) (map[string]string, bool) {
	if inst == nil || inst.StackKey != "bigdata" {
		return nil, false
	}
	hs := activeInstanceHosts(inst)
	if len(hs) == 0 {
		return nil, false
	}
	role := stackBigdataRole(instanceHostsAsRunHosts(hs))
	if !role.Ha {
		return nil, false
	}
	ms2, hs2b := "", ""
	if len(role.MSs) > 1 {
		ms2 = role.MSs[1]
	}
	if len(role.Hs2s) > 1 {
		hs2b = role.Hs2s[1]
	}
	hivePrimary := ""
	if len(role.MSs) > 0 {
		hivePrimary = role.MSs[0]
	}
	return map[string]string{
		"hdfs": role.Nn1, "hdfs_nn2": role.Nn2, "hdfs_jns": strings.Join(role.JNs, ","),
		"yarn": role.Rm1, "yarn_rm2": role.Rm2,
		"spark": role.SparkM1, "spark_m2": role.SparkM2,
		"flink": role.FlinkJM1, "flink_jm2": role.FlinkJm2,
		"hbase": role.HMaster1, "hbase_hm2": role.HMaster2,
		"hive": hivePrimary, "hive_ms2": ms2, "hive_hs2b": hs2b, "hive_db": role.HiveDB,
		"zookeeper_ips": role.ZKIps,
	}, true
}

// overrideBigdataMasters 在合并主机级参数之后，确定权威 masters 矩阵。
// 优先读已物化角色计划的 plan.Masters（唯一事实源，杜绝 41 类不一致）；
// 无 Plan 时回落到引擎同款算法现场推导（兜底）。
func overrideBigdataMasters(inst *model.StackInstance, params map[string]string) {
	if p := decodeRolePlan(inst.RolePlanJSON); p != nil && len(p.Masters) > 0 {
		if b, err := json.Marshal(p.Masters); err == nil {
			params["masters"] = string(b)
			return
		}
	}
	m, ok := bigdataRoleMasters(inst)
	if !ok {
		return
	}
	if b, err := json.Marshal(m); err == nil {
		params["masters"] = string(b)
	}
}

func assembleStackVerify(inst *model.StackInstance, params map[string]string, hosts []stackVerifyHost) stackVerifyResult {
	okN := 0
	for _, h := range hosts {
		if h.OK {
			okN++
		}
	}
	res := stackVerifyResult{
		CheckedAt: time.Now().Format("2006-01-02 15:04:05"),
		Endpoints: stackVerifyEndpoints(inst, params, hosts),
		Hosts:     hosts,
		Hint:      "控制面已 SSH 到各节点检查，不必再登录服务器。",
	}
	switch inst.StackKey {
	case "redis":
		res.Notes, res.ClientHint = redisVerifyHints(inst, params, hosts)
		applyRedisClusterChecks(inst.Mode, &res)
	case "bigdata":
		res.Notes = []string{"按各组件 compose / 容器运行状态探活（HDFS、ZK、Spark 等分目录部署）。"}
	default:
		res.Notes = []string{"当前套件按容器运行状态探活。"}
	}
	res.OK = okN == len(hosts) && len(hosts) > 0
	if res.OK {
		res.Summary = fmt.Sprintf("%d/%d 节点正常", okN, len(hosts))
	} else {
		res.Summary = fmt.Sprintf("%d/%d 节点正常", okN, len(hosts))
	}
	for _, n := range res.Notes {
		if strings.Contains(n, "异常") || strings.Contains(n, "未就绪") || strings.Contains(n, "不符") {
			res.OK = false
		}
	}
	if res.OK {
		res.Summary += " · 探活通过"
	} else {
		res.Summary += " · 存在异常"
	}
	return res
}

func stackVerifyEndpoints(inst *model.StackInstance, params map[string]string, _ []stackVerifyHost) []stackVerifyEndpoint {
	port := pickParam(params, "port", "6379")
	out := []stackVerifyEndpoint{}
	comps := parseComponentsCSV(params["components"])
	for _, h := range activeInstanceHosts(inst) {
		hp := mergeParamMaps(params, parseJSONMap(h.ParamsJSON))
		overrideBigdataMasters(inst, hp)
		p := pickParam(hp, "port", port)
		switch inst.StackKey {
		case "redis":
			name := "Redis"
			if inst.Mode == "replication" || inst.Mode == "sentinel" {
				if h.Role == "master" {
					name = "Redis 主"
				} else {
					name = "Redis 从"
				}
			}
			out = append(out, stackVerifyEndpoint{Name: name, URL: "redis://" + h.HostIP + ":" + p, Role: h.Role})
			if inst.Mode == "sentinel" {
				sp := pickParam(hp, "sentinel_port", "26379")
				out = append(out, stackVerifyEndpoint{Name: "Sentinel", URL: "redis-sentinel://" + h.HostIP + ":" + sp, Role: h.Role})
			}
		case "bigdata":
			masters := map[string]string{}
			_ = json.Unmarshal([]byte(hp["masters"]), &masters)
			out = append(out, bigdataVerifyEndpoints(h, comps, masters, hp)...)
		case "elasticsearch":
			p := pickParam(hp, "port", "9200")
			roles := strings.Split(pickParam(hp, "roles", "coordinator"), ",")
			scheme := "http"
			if strings.EqualFold(strings.TrimSpace(pickParam(hp, "ssl_enabled", "false")), "true") {
				scheme = "https"
			}
			for _, r := range roles {
				r = strings.TrimSpace(r)
				if r == "" {
					continue
				}
				label := esTierLabel(r)
				out = append(out, stackVerifyEndpoint{Name: "Elasticsearch-" + label, URL: scheme + "://" + h.HostIP + ":" + p, Role: r})
			}
			if len(roles) == 0 {
				out = append(out, stackVerifyEndpoint{Name: "Elasticsearch", URL: scheme + "://" + h.HostIP + ":" + p, Role: h.Role})
			}
		default:
			out = append(out, stackVerifyEndpoint{Name: inst.StackName, URL: "tcp://" + h.HostIP, Role: h.Role})
		}
	}
	return out
}

// esTierLabel 将 ES 冷热温角色映射为面向用户的接入标签。
func esTierLabel(role string) string {
	switch role {
	case "master":
		return "master"
	case "coordinator":
		return "协调"
	case "data_hot":
		return "数据-hot"
	case "data_warm":
		return "数据-warm"
	case "data_cold":
		return "数据-cold"
	}
	return role
}

// bigdataVerifyEndpoints 生成某台主机的访问入口。HA 下同一主机可能同时是「主」与「备」
// （如 NN2 主机同时跑 DataNode、JN），因此按角色矩阵逐条追加，而不是非主即工作节点的二选一。
func bigdataVerifyEndpoints(h model.StackInstanceHost, comps []string, masters, params map[string]string) []stackVerifyEndpoint {
	roleOf := func(comp string) string {
		if ip := strings.TrimSpace(masters[comp]); ip != "" {
			return ip
		}
		if h.Role == "master" {
			return h.HostIP
		}
		return ""
	}
	isMasterOf := func(comp string) bool { return roleOf(comp) == h.HostIP }
	inList := func(key string) bool {
		for _, p := range strings.Split(strings.TrimSpace(masters[key]), ",") {
			if strings.TrimSpace(p) == h.HostIP {
				return true
			}
		}
		return false
	}
	webui := pickParam(params, "webui_port", "8080")
	trinoPort := pickParam(params, "trino_http_port", "8083")
	jnRpc := pickParam(params, "jn_rpc_port", "8485")
	ha := strings.EqualFold(strings.TrimSpace(params["ha"]), "true")
	var out []stackVerifyEndpoint
	add := func(comp, name, url string) {
		out = append(out, stackVerifyEndpoint{Name: name, Component: comp, URL: url, Role: h.Role})
	}
	for _, c := range comps {
		switch c {
		case "hdfs":
			if isMasterOf("hdfs") {
				add("hdfs", "HDFS NameNode", "http://"+h.HostIP+":9870")
			}
			if inList("hdfs_nn2") {
				add("hdfs", "HDFS NameNode-2", "http://"+h.HostIP+":9870")
			}
			if inList("hdfs_jns") {
				add("hdfs", "HDFS JournalNode", "http://"+h.HostIP+":"+jnRpc)
			}
			// HA 下 DataNode 全节点部署（含 NN1/NN2 主机）；非 HA 仅工作节点
			if ha || !isMasterOf("hdfs") {
				add("hdfs", "HDFS DataNode", "http://"+h.HostIP+":9864")
			}
		case "zookeeper":
			add("zookeeper", "ZooKeeper", "zookeeper://"+h.HostIP+":2181")
		case "yarn":
			if isMasterOf("yarn") {
				add("yarn", "YARN RM", "http://"+h.HostIP+":8088")
			}
			if inList("yarn_rm2") {
				add("yarn", "YARN RM-2", "http://"+h.HostIP+":8088")
			}
			if !isMasterOf("yarn") && !inList("yarn_rm2") {
				add("yarn", "YARN NM", "http://"+h.HostIP+":8042")
			}
		case "spark":
			if isMasterOf("spark") {
				add("spark", "Spark Master", "http://"+h.HostIP+":"+webui)
			}
			if inList("spark_m2") {
				add("spark", "Spark Master-2", "http://"+h.HostIP+":8081")
			}
			if !isMasterOf("spark") && !inList("spark_m2") {
				add("spark", "Spark Worker", "http://"+h.HostIP+":8081")
			}
		case "flink":
			if isMasterOf("flink") {
				add("flink", "Flink JM", "http://"+h.HostIP+":8081")
			}
			if inList("flink_jm2") {
				add("flink", "Flink JM-2", "http://"+h.HostIP+":8081")
			}
		case "hive":
			if isMasterOf("hive") {
				add("hive", "Hive Metastore", "thrift://"+h.HostIP+":9083")
				add("hive", "HiveServer2", "jdbc:hive2://"+h.HostIP+":10000")
			}
			if inList("hive_ms2") {
				add("hive", "Hive Metastore-2", "thrift://"+h.HostIP+":9083")
			}
			if inList("hive_hs2b") {
				add("hive", "HiveServer2-2", "jdbc:hive2://"+h.HostIP+":10000")
			}
		case "hbase":
			if isMasterOf("hbase") {
				add("hbase", "HBase Master", "http://"+h.HostIP+":16010")
			}
			if inList("hbase_hm2") {
				add("hbase", "HBase Backup Master", "http://"+h.HostIP+":16010")
			}
			if !isMasterOf("hbase") && !inList("hbase_hm2") {
				add("hbase", "HBase RS", "http://"+h.HostIP+":16030")
			}
		case "trino":
			if isMasterOf("trino") {
				add("trino", "Trino Coord", "http://"+h.HostIP+":"+trinoPort)
			} else {
				add("trino", "Trino Worker", "http://"+h.HostIP+":"+trinoPort)
			}
		}
	}
	return out
}

func redisVerifyHints(inst *model.StackInstance, params map[string]string, hosts []stackVerifyHost) ([]string, string) {
	port := pickParam(params, "port", "6379")
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
	switch inst.Mode {
	case "cluster":
		return []string{"集群模式请用 redis-cli -c 连接任一节点。"},
			fmt.Sprintf("redis-cli -c -h %s -p %s", ip, port)
	case "sentinel":
		sp := pickParam(params, "sentinel_port", "26379")
		return []string{"应用侧连 Sentinel，主从地址以 SENTINEL 查询为准。"},
			fmt.Sprintf("redis-cli -h %s -p %s SENTINEL get-master-addr-by-name mymaster", ip, sp)
	default:
		return []string{"业务请连主节点；从库默认只读。"},
			fmt.Sprintf("redis-cli -h %s -p %s", ip, port)
	}
}

func applyRedisClusterChecks(mode string, res *stackVerifyResult) {
	switch mode {
	case "replication":
		masters, replicas, linkDown := 0, 0, 0
		for _, h := range res.Hosts {
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
			res.Notes = append(res.Notes, fmt.Sprintf("主从拓扑异常：存活主节点 %d 个（应为 1）", masters))
		}
		if linkDown > 0 {
			res.Notes = append(res.Notes, "有从库复制链路未就绪")
		}
		if masters == 1 && replicas > 0 && linkDown == 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("主从复制正常（1 主 %d 从）", replicas))
		}
	case "cluster":
		for _, h := range res.Hosts {
			for _, c := range h.Checks {
				if c.Name == "集群" && strings.Contains(c.Detail, "cluster_state:ok") {
					return
				}
			}
		}
		if anyHostOK(*res) {
			res.Notes = append(res.Notes, "集群状态未就绪（cluster_state 不是 ok）")
		}
	case "sentinel":
		okSent := 0
		for _, h := range res.Hosts {
			for _, c := range h.Checks {
				if c.Name == "哨兵" && c.OK {
					okSent++
				}
			}
		}
		if okSent < 3 && len(res.Hosts) >= 3 {
			res.Notes = append(res.Notes, fmt.Sprintf("哨兵探活成功 %d 个，建议至少 3 个", okSent))
		}
	}
}

func anyHostOK(res stackVerifyResult) bool {
	for _, h := range res.Hosts {
		if h.OK {
			return true
		}
	}
	return false
}

func buildStackVerifyScript(stackKey, mode, role, hostIP string, params map[string]string) string {
	home := bashQuote(pickParam(params, "home_dir", ""))
	pass := bashQuote(strings.TrimSpace(params["password"]))
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset +e\n")
	b.WriteString("echo __IO_BEGIN__\n")
	b.WriteString("HOME_DIR=" + home + "\n")
	b.WriteString("PASS=" + pass + "\n")
	b.WriteString("ROLE=" + bashQuote(role) + "\n")
	b.WriteString(`if ! command -v docker >/dev/null 2>&1; then echo __IO_ERR__=no-docker; echo __IO_END__; exit 0; fi
# 单 compose（Redis 等）+ 多目录 compose（大数据底座 home/*/compose.yml）
echo __IO_COMPOSE_BEGIN__
if [ -n "$HOME_DIR" ]; then
  for cf in "$HOME_DIR/compose.yml" "$HOME_DIR/docker-compose.yml" "$HOME_DIR"/*/compose.yml; do
    [ -f "$cf" ] || continue
    docker compose -f "$cf" ps -a --format '{{.Name}}={{.State}}' 2>/dev/null || true
  done
fi
echo __IO_COMPOSE_END__
inspect_ctr() {
  local n="$1"
  local st
  st=$(docker inspect -f '{{.State.Status}}|{{if .State.Running}}1{{else}}0{{end}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{.Config.Image}}|{{.State.StartedAt}}' "$n" 2>/dev/null)
  if [ -z "$st" ]; then echo "__IO_CTR__${n}__=missing"; else echo "__IO_CTR__${n}__=$st"; fi
}
`)
	ctrs := verifyContainers(stackKey, mode, role, hostIP, params)
	for _, c := range ctrs {
		b.WriteString("inspect_ctr " + bashQuote(c) + "\n")
	}
	if stackKey == "bigdata" {
		// 实际运行的大数据容器：用于如实回显本机实例清单（期望集之外的容器只做提示，不算失败）
		b.WriteString(`echo __IO_LIVE_BEGIN__
docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^(bigdata-|hadoop-|spark-|flink-|hive-|hbase-|trino-)' | sort | tr '\n' ','
echo
echo __IO_LIVE_END__
`)
	}
	if stackKey == "redis" {
		dataCtr := "redis-repl"
		switch mode {
		case "cluster":
			dataCtr = "redis-cluster"
		case "sentinel":
			dataCtr = "redis-sentinel-data"
		}
		b.WriteString("DATA_CTR=" + bashQuote(dataCtr) + "\n")
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
		if mode == "cluster" {
			b.WriteString(`echo __IO_CLUSTER_BEGIN__
redis_cli "$DATA_CTR" CLUSTER INFO
echo __IO_CLUSTER_END__
echo __IO_NODES_BEGIN__
redis_cli "$DATA_CTR" CLUSTER NODES
echo __IO_NODES_END__
`)
		}
		if mode == "sentinel" {
			b.WriteString("SENT_CTR=redis-sentinel\n")
			b.WriteString("SENT_PORT=" + bashQuote(pickParam(params, "sentinel_port", "26379")) + "\n")
			b.WriteString(`echo "__IO_SENT_PING__=$(redis_cli "$SENT_CTR" -p "$SENT_PORT" PING | tr -d '\r')"
echo __IO_SENT_BEGIN__
redis_cli "$SENT_CTR" -p "$SENT_PORT" SENTINEL master mymaster
echo __IO_SENT_END__
`)
		}
	}
	b.WriteString("echo __IO_END__\nexit 0\n")
	return b.String()
}

func verifyContainers(stackKey, mode, role, hostIP string, params map[string]string) []string {
	if stackKey == "redis" {
		switch mode {
		case "cluster":
			return []string{"redis-cluster"}
		case "sentinel":
			return []string{"redis-sentinel-data", "redis-sentinel"}
		default:
			return []string{"redis-repl"}
		}
	}
	if stackKey == "bigdata" {
		return bigdataExpectedContainers(hostIP, role, params)
	}
	return nil
}

// bigdataExpectedContainers 按组件与角色规划推断本机应存在的容器名（与 node.sh/ha.sh 对齐）。
func bigdataExpectedContainers(hostIP, role string, params map[string]string) []string {
	comps := parseComponentsCSV(params["components"])
	masters := map[string]string{}
	_ = json.Unmarshal([]byte(params["masters"]), &masters)
	ha := strings.EqualFold(strings.TrimSpace(params["ha"]), "true")
	isPrimary := func(comp string) bool {
		if ip := strings.TrimSpace(masters[comp]); ip != "" {
			return ip == hostIP
		}
		return role == "master"
	}
	// inList 判断 hostIP 是否落在逗号列表角色（如 hdfs_nn2 单值 / hdfs_jns 多值）
	inList := func(key string) bool {
		for _, p := range strings.Split(strings.TrimSpace(masters[key]), ",") {
			if strings.TrimSpace(p) == hostIP {
				return true
			}
		}
		return false
	}
	has := func(k string) bool {
		for _, c := range comps {
			if c == k {
				return true
			}
		}
		return false
	}
	var out []string
	if has("zookeeper") {
		out = append(out, "bigdata-zookeeper")
	}
	// HA 模式：严格对齐 ha.sh / node.sh 的落点。要点：
	//   1) JournalNode 部署在 JNS 全部主机（含 NN1/NN2），见 ha.sh「JNS 内全部主机（含 NN1/NN2）都部署」；
	//   2) DataNode 全节点部署（deploy_hdfs_ha_dn 不按角色排除），NN1/NN2 主机同样跑 DataNode；
	//   3) 备主/备实例与主实例可同主机并存（如 NN2 主机同时是 DataNode + JournalNode），
	//      所以这里是「按角色逐条追加」，不是非主即工作的二选一。
	if ha {
		if has("hdfs") {
			if isPrimary("hdfs") {
				out = append(out, "hadoop-namenode", "hadoop-zkfc")
			}
			if inList("hdfs_nn2") {
				out = append(out, "hadoop-namenode2", "hadoop-zkfc2")
			}
			if inList("hdfs_jns") {
				out = append(out, "hadoop-journalnode")
			}
			out = append(out, "hadoop-datanode")
		}
		if has("yarn") {
			switch {
			case isPrimary("yarn"):
				out = append(out, "hadoop-resourcemanager")
			case inList("yarn_rm2"):
				out = append(out, "hadoop-resourcemanager2")
			default:
				out = append(out, "hadoop-nodemanager")
			}
		}
		if has("spark") {
			switch {
			case isPrimary("spark"):
				out = append(out, "spark-master")
			case inList("spark_m2"):
				out = append(out, "spark-master2")
			default:
				out = append(out, "spark-worker")
			}
		}
		if has("flink") {
			switch {
			case isPrimary("flink"):
				out = append(out, "flink-jobmanager")
			case inList("flink_jm2"):
				out = append(out, "flink-jobmanager2")
			default:
				out = append(out, "flink-taskmanager")
			}
		}
		if has("hbase") {
			switch {
			case isPrimary("hbase"):
				out = append(out, "hbase-master")
			case inList("hbase_hm2"):
				out = append(out, "hbase-backup-master")
			default:
				out = append(out, "hbase-regionserver")
			}
		}
		if has("hive") {
			if isPrimary("hive") {
				out = append(out, "hive-metastore", "hive-hiveserver2")
			}
			if inList("hive_ms2") {
				out = append(out, "hive-metastore2")
			}
			if inList("hive_hs2b") {
				out = append(out, "hive-hiveserver2-2")
			}
			if inList("hive_db") {
				out = append(out, "hive-metastore-db")
			}
		}
		if has("trino") {
			out = append(out, "trino-node")
		}
		return out
	}
	if has("hdfs") {
		if isPrimary("hdfs") {
			out = append(out, "hadoop-namenode")
		} else {
			out = append(out, "hadoop-datanode")
		}
	}
	if has("yarn") {
		if isPrimary("yarn") {
			out = append(out, "hadoop-resourcemanager")
		} else {
			out = append(out, "hadoop-nodemanager")
		}
	}
	if has("spark") {
		if isPrimary("spark") {
			out = append(out, "spark-master")
		} else {
			out = append(out, "spark-worker")
		}
	}
	if has("flink") {
		if isPrimary("flink") {
			out = append(out, "flink-jobmanager")
		} else {
			out = append(out, "flink-taskmanager")
		}
	}
	if has("hive") && isPrimary("hive") {
		out = append(out, "hive-metastore", "hive-hiveserver2")
	}
	if has("hbase") {
		if isPrimary("hbase") {
			out = append(out, "hbase-master")
		} else {
			out = append(out, "hbase-regionserver")
		}
	}
	if has("trino") {
		out = append(out, "trino-node")
	}
	return out
}

func parseStackVerifyOutput(stackKey, mode string, host model.StackInstanceHost, params map[string]string, raw string, row *stackVerifyHost) {
	if v := extractLine(raw, "__IO_ERR__="); v == "no-docker" {
		row.OK = false
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "Docker", OK: false, Detail: "未检测到 Docker"})
		return
	}
	ctrs := verifyContainers(stackKey, mode, host.Role, host.HostIP, params)
	allCtrOK := true
	if len(ctrs) == 0 {
		compose := extractBlock(raw, "__IO_COMPOSE_BEGIN__", "__IO_COMPOSE_END__")
		ok, detail := parseComposeStates(compose)
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "容器", OK: ok, Detail: detail})
		allCtrOK = ok
	} else if stackKey == "bigdata" {
		allCtrOK = parseBigdataCtrChecks(ctrs, raw, row)
	} else {
		for _, name := range ctrs {
			st := extractLine(raw, "__IO_CTR__"+name+"__=")
			ok, detail := parseCtrState(name, st)
			row.Checks = append(row.Checks, stackVerifyCheck{Name: "容器 " + name, OK: ok, Detail: detail})
			if !ok {
				allCtrOK = false
			}
		}
	}
	if stackKey != "redis" {
		row.OK = allCtrOK
		row.LiveRole = host.Role
		return
	}
	ping := strings.TrimSpace(extractLine(raw, "__IO_PING__="))
	pingOK := strings.EqualFold(ping, "PONG")
	pingDetail := ping
	if ping == "" {
		pingDetail = "无响应"
	}
	row.Checks = append(row.Checks, stackVerifyCheck{Name: "PING", OK: pingOK, Detail: pingDetail})

	repl := extractBlock(raw, "__IO_REPL_BEGIN__", "__IO_REPL_END__")
	info := parseRedisInfo(repl)
	live := strings.TrimSpace(info["role"])
	row.LiveRole = live
	if live != "" {
		roleOK := redisRoleMatches(host.Role, live)
		detail := liveRoleLabel(live)
		if n := strings.TrimSpace(info["connected_slaves"]); live == "master" && n != "" {
			detail += " · 从库 " + n
		}
		if mh := strings.TrimSpace(info["master_host"]); live != "master" && mh != "" {
			detail += " · 跟随 " + mh
		}
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "角色", OK: roleOK, Detail: detail})
	}
	if live == "slave" || live == "replica" {
		link := strings.TrimSpace(info["master_link_status"])
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "复制", OK: link == "up", Detail: "master_link_status=" + nz(link, "未知")})
	}
	if live == "master" {
		if slaves := parseConnectedSlaves(info); len(slaves) > 0 {
			row.Checks = append(row.Checks, stackVerifyCheck{Name: "复制", OK: true, Detail: strings.Join(slaves, "; ")})
		}
	}

	if ver := redisVersion(extractBlock(raw, "__IO_SRV_BEGIN__", "__IO_SRV_END__")); ver != "" {
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "版本", OK: true, Detail: ver})
	}

	if mode == "cluster" {
		ci := parseRedisInfo(extractBlock(raw, "__IO_CLUSTER_BEGIN__", "__IO_CLUSTER_END__"))
		state := strings.TrimSpace(ci["cluster_state"])
		detail := "cluster_state:" + nz(state, "未知")
		if v := ci["cluster_slots_assigned"]; v != "" {
			detail += " · slots " + v
		}
		if v := ci["cluster_known_nodes"]; v != "" {
			detail += " · nodes " + v
		}
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "集群", OK: state == "ok", Detail: detail})
	}
	if mode == "sentinel" {
		sp := strings.TrimSpace(extractLine(raw, "__IO_SENT_PING__="))
		sentPing := strings.EqualFold(sp, "PONG")
		sm := parseSentinelMaster(extractBlock(raw, "__IO_SENT_BEGIN__", "__IO_SENT_END__"))
		flags := sm["flags"]
		sentOK := sentPing && strings.Contains(flags, "master") && !strings.Contains(flags, "down")
		detail := "PING " + nz(sp, "无")
		if flags != "" {
			detail += " · flags=" + flags
		}
		if sm["ip"] != "" {
			detail += " · 主 " + sm["ip"] + ":" + sm["port"]
		}
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "哨兵", OK: sentOK, Detail: detail})
	}

	row.OK = true
	for _, c := range row.Checks {
		if !c.OK {
			row.OK = false
			break
		}
	}
}

// bigdataContainerComponent 由容器名推断所属组件（与 node.sh/ha.sh 命名对齐）。
func bigdataContainerComponent(name string) string {
	switch {
	case name == "bigdata-zookeeper":
		return "zookeeper"
	case strings.HasPrefix(name, "hadoop-namenode"),
		strings.HasPrefix(name, "hadoop-journalnode"),
		strings.HasPrefix(name, "hadoop-datanode"),
		strings.HasPrefix(name, "hadoop-zkfc"),
		strings.HasPrefix(name, "hadoop-resourcemanager"),
		name == "hadoop-nodemanager":
		// hadoop-* 需区分 hdfs 与 yarn：rm 走 yarn，其余按 hdfs
		if name == "hadoop-nodemanager" || name == "hadoop-resourcemanager" || name == "hadoop-resourcemanager2" {
			return "yarn"
		}
		return "hdfs"
	case strings.HasPrefix(name, "spark-"):
		return "spark"
	case strings.HasPrefix(name, "flink-"):
		return "flink"
	case strings.HasPrefix(name, "hive-"):
		return "hive"
	case strings.HasPrefix(name, "hbase-"):
		return "hbase"
	case name == "trino-node" || strings.HasPrefix(name, "trino-"):
		return "trino"
	}
	return ""
}

// parseBigdataCtrChecks 解析本机应有的大数据容器；全部 running 才算通过。
func parseBigdataCtrChecks(ctrs []string, raw string, row *stackVerifyHost) bool {
	if len(ctrs) == 0 {
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "实例", OK: false, Detail: "未勾选任何组件"})
		return false
	}
	var running []string
	var image, started string
	allOK := true
	for _, name := range ctrs {
		st := extractLine(raw, "__IO_CTR__"+name+"__=")
		ok, detail, img, start := parseCtrStateEx(name, st)
		comp := bigdataContainerComponent(name)
		if img != "" && image == "" {
			image = img
		}
		if start != "" && started == "" {
			started = start
		}
		if !ok {
			allOK = false
			row.Checks = append(row.Checks, stackVerifyCheck{Name: "容器 " + name, Component: comp, OK: false, Detail: detail})
			continue
		}
		running = append(running, name)
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "容器 " + name, Component: comp, OK: true, Detail: detail})
	}
	instDetail := strings.Join(running, ", ")
	if instDetail == "" {
		instDetail = "无运行中实例"
	}
	ok := allOK && len(running) > 0
	row.Checks = append(row.Checks, stackVerifyCheck{Name: "实例", OK: ok, Detail: instDetail})
	// 如实回显本机实际运行的大数据容器；仅当出现「未预期的额外容器」时在明细里点出（不作为失败）
	if live := parseLiveContainers(raw); len(live) > 0 {
		extra := make([]string, 0, len(live))
		for _, n := range live {
			if !shared.ContainsString(ctrs, n) {
				extra = append(extra, n)
			}
		}
		detail := strings.Join(live, ", ")
		if len(extra) > 0 {
			detail += "（未预期: " + strings.Join(extra, ", ") + "）"
		}
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "实际运行", OK: true, Detail: detail})
	}
	verDetail := imageShort(image)
	if started != "" {
		if verDetail != "" {
			verDetail += " · "
		}
		verDetail += formatDockerStarted(started)
	}
	if verDetail != "" {
		row.Checks = append(row.Checks, stackVerifyCheck{Name: "版本", OK: true, Detail: verDetail})
	}
	return ok
}

// parseLiveContainers 解析本机实际运行的大数据容器（逗号分隔）。
func parseLiveContainers(raw string) []string {
	blk := strings.TrimSpace(extractBlock(raw, "__IO_LIVE_BEGIN__", "__IO_LIVE_END__"))
	if blk == "" {
		return nil
	}
	out := []string{}
	for _, s := range strings.Split(blk, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func imageShort(image string) string {
	if image == "" {
		return ""
	}
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func formatDockerStarted(iso string) string {
	iso = strings.TrimSpace(iso)
	if iso == "" || iso == "0001-01-01T00:00:00Z" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t, err = time.Parse(time.RFC3339, iso)
	}
	if err != nil {
		if len(iso) >= 19 {
			return strings.ReplaceAll(iso[:19], "T", " ")
		}
		return iso
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	days := int(d.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("已运行 %d 天", days)
	}
	hours := int(d.Hours())
	if hours > 0 {
		return fmt.Sprintf("已运行 %d 小时", hours)
	}
	return fmt.Sprintf("已运行 %d 分钟", int(d.Minutes()))
}

func parseCtrState(name, st string) (bool, string) {
	ok, detail, _, _ := parseCtrStateEx(name, st)
	return ok, detail
}

func parseCtrStateEx(name, st string) (ok bool, detail, image, started string) {
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

func parseComposeStates(raw string) (bool, string) {
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

func pickParam(m map[string]string, key, def string) string {
	if v := strings.TrimSpace(m[key]); v != "" {
		return v
	}
	return def
}

func bashQuote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'"'"'`) + "'"
}

func redactSecret(s, secret string) string {
	if secret == "" || !strings.Contains(s, secret) {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

func extractLine(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func extractBlock(s, begin, end string) string {
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

func nz(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
