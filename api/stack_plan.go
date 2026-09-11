package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

// 本文件实现「部署前角色物化（RolePlan）」：docs/role-plan-design.md。
// planner 以 stackBigdataRole 为唯一算法（一行不改），只把调用时机前移到部署前，
// 结果物化为 RolePlan 落库；部署 / 探活 / 扩缩容 / 重装共读同一份计划。

// PlanOptions planner 输入选项。
type PlanOptions struct {
	Op           string            // create / add_component / scale_out / scale_in / reinstall / replan
	Mode         string            // 套件模式 key（通用套件角色目录按 stack+mode 取角色语义）
	Params       map[string]string // 套件参数（enable_ui / db_host / replicas 等影响角色展开）
	Masters      map[string]string // bigdata 权威落点输入（key ∈ bigdataRoleKeys）；缺省键自动落位
	ManualKeys   []string          // 用户显式指定的 masters 键（用于来源标注，跨重规划继承）
	ManualMaster bool              // 通用套件：用户显式指定了主/引导节点（来源标注 manual）
}

// planBigdataRoles 以 stackBigdataRole 为唯一算法，把权威落点展开为可持久化 RolePlan。
// hosts 需为本次操作的全部成员（含既有成员）；hosts[0].ParamsJSON 须携带 ha/components/masters。
func planBigdataRoles(hosts []model.StackRunHost, opts PlanOptions) (model.RolePlan, error) {
	plan := model.RolePlan{Op: opts.Op, GeneratedAt: nowLocal(), Rev: 1}
	if len(hosts) == 0 {
		return plan, fmt.Errorf("角色规划需要至少一台主机")
	}
	// 规范化：按 Seq 排序；把 opts.Masters 注入首主机参数（stackBigdataRole 只读 hosts[0] 的参数）
	norm := normalizePlanHosts(hosts, opts.Masters)
	params := map[string]string{}
	_ = json.Unmarshal([]byte(norm[0].ParamsJSON), &params)
	if params == nil {
		params = map[string]string{}
	}

	// masters 键合法性（未知键会被算法静默忽略，必须在规划期拦截）
	allowed := map[string]bool{}
	for _, k := range bigdataRoleKeys {
		allowed[k] = true
	}
	for k := range opts.Masters {
		if !allowed[k] {
			return plan, fmt.Errorf("角色规划包含未知项: %s", k)
		}
	}
	// 手动指定的 IP 必须落在成员集合内
	member := map[string]bool{}
	for _, h := range norm {
		member[h.HostIP] = true
	}
	for k, v := range opts.Masters {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" && !member[p] {
				return plan, fmt.Errorf("角色规划参数 %s: IP %s 不在本次成员主机中", k, p)
			}
		}
	}

	comps := parseComponentsCSV(params["components"])
	if len(comps) == 0 {
		comps = []string{"hdfs"} // 防御：底座至少有 HDFS
	}
	has := func(c string) bool { return shared.ContainsString(comps, c) }
	ha := strings.EqualFold(strings.TrimSpace(params["ha"]), "true")

	// HA 前置校验（§4.4-1~3）
	if ha {
		if !has("zookeeper") {
			return plan, fmt.Errorf("HA 模式依赖 ZooKeeper，请勾选 ZooKeeper")
		}
		if len(norm) < 3 {
			return plan, fmt.Errorf("HA 模式至少需要 3 台主机")
		}
		if has("hive") && !has("metastore_db") {
			return plan, fmt.Errorf("HA 模式开启 Hive 需一并选择 metastore_db 组件（外部元数据库）")
		}
	}

	// 权威落点（唯一算法）
	r := stackBigdataRole(norm)
	plan.Ha = r.Ha

	// 同组件主从互斥（§4.4-4）
	pairErr := func(a, b, name string) error {
		if a != "" && b != "" && a == b {
			return fmt.Errorf("HA 模式 %s 主备不能在同一台主机（%s）", name, a)
		}
		return nil
	}
	if err := pairErr(r.Nn1, r.Nn2, "HDFS NameNode"); err != nil {
		return plan, err
	}
	if err := pairErr(r.Rm1, r.Rm2, "YARN ResourceManager"); err != nil {
		return plan, err
	}
	if err := pairErr(r.SparkM1, r.SparkM2, "Spark Master"); err != nil {
		return plan, err
	}
	if err := pairErr(r.FlinkJM1, r.FlinkJm2, "Flink JobManager"); err != nil {
		return plan, err
	}
	if err := pairErr(r.HMaster1, r.HMaster2, "HBase HMaster"); err != nil {
		return plan, err
	}
	if len(r.MSs) >= 2 {
		if err := pairErr(r.MSs[0], r.MSs[1], "Hive Metastore"); err != nil {
			return plan, err
		}
	}
	if len(r.Hs2s) >= 2 {
		if err := pairErr(r.Hs2s[0], r.Hs2s[1], "HiveServer2"); err != nil {
			return plan, err
		}
	}

	// 来源标注：用户显式指定的键集合（跨重规划由 ManualKeys 继承）
	manual := map[string]bool{}
	for _, k := range opts.ManualKeys {
		manual[k] = true
	}
	src := func(key string) string {
		if manual[key] {
			return "manual"
		}
		return "auto"
	}

	// 展开到主机
	byIP := map[string][]model.RolePlanRole{}
	addAt := func(comp, role, label, ip, key, scope string) {
		if ip == "" || !member[ip] {
			return
		}
		byIP[ip] = append(byIP[ip], model.RolePlanRole{
			Comp: comp, Role: role, Label: label, Source: src(key), Scope: scope,
		})
	}
	addAll := func(comp, role, label string) {
		for _, h := range norm {
			byIP[h.HostIP] = append(byIP[h.HostIP], model.RolePlanRole{
				Comp: comp, Role: role, Label: label, Source: "auto", Scope: "all",
			})
		}
	}

	// zookeeper
	if has("zookeeper") {
		zkManual := manual["zookeeper_ips"]
		for _, z := range shared.FilterEmpty(strings.Split(r.ZKIps, ",")) {
			byIP[z] = append(byIP[z], model.RolePlanRole{
				Comp: "zookeeper", Role: "zk", Label: "ZooKeeper",
				Source: mapSrc(zkManual), Scope: "",
			})
		}
	}
	// hdfs
	if has("hdfs") {
		addAt("hdfs", "nn1", "NameNode (Active)", r.Nn1, "hdfs", "")
		if r.Ha && r.Nn2 != "" {
			addAt("hdfs", "nn2", "NameNode (Standby)", r.Nn2, "hdfs_nn2", "")
		}
		for _, jn := range r.JNs {
			addAt("hdfs", "jn", "JournalNode", jn, "hdfs_jns", "")
		}
		if r.Ha { // zkfc 与 NN 同机（§2.4），随 NN 冻结
			addAt("hdfs", "zkfc", "ZKFC", r.Nn1, "", "all")
			addAt("hdfs", "zkfc", "ZKFC", r.Nn2, "", "all")
		}
		addAll("hdfs", "dn", "DataNode")
	}
	// yarn
	if has("yarn") {
		addAt("yarn", "rm1", "ResourceManager (Active)", r.Rm1, "yarn", "")
		if r.Ha && r.Rm2 != "" {
			addAt("yarn", "rm2", "ResourceManager (Standby)", r.Rm2, "yarn_rm2", "")
		}
		addAll("yarn", "nm", "NodeManager")
	}
	// spark
	if has("spark") {
		addAt("spark", "spark_m1", "Spark Master", r.SparkM1, "spark", "")
		if r.Ha && r.SparkM2 != "" {
			addAt("spark", "spark_m2", "Spark Master (Standby)", r.SparkM2, "spark_m2", "")
		}
		addAll("spark", "spark_worker", "Spark Worker")
	}
	// flink
	if has("flink") {
		addAt("flink", "jm1", "JobManager (Active)", r.FlinkJM1, "flink", "")
		if r.Ha && r.FlinkJm2 != "" {
			addAt("flink", "jm2", "JobManager (Standby)", r.FlinkJm2, "flink_jm2", "")
		}
		addAll("flink", "flink_tm", "TaskManager")
	}
	// hbase
	if has("hbase") {
		addAt("hbase", "hm1", "HMaster (Active)", r.HMaster1, "hbase", "")
		if r.Ha && r.HMaster2 != "" {
			addAt("hbase", "hm2", "HMaster (Backup)", r.HMaster2, "hbase_hm2", "")
		}
		addAll("hbase", "rs", "RegionServer")
	}
	// hive（metastore_db 属 hive 依赖，一并物化）
	if has("hive") {
		if len(r.MSs) > 0 {
			addAt("hive", "ms1", "Metastore", r.MSs[0], "hive", "")
		}
		if len(r.MSs) > 1 {
			addAt("hive", "ms2", "Metastore (Standby)", r.MSs[1], "hive_ms2", "")
		}
		if len(r.Hs2s) > 0 {
			addAt("hive", "hs2a", "HiveServer2", r.Hs2s[0], "hive", "")
		}
		if len(r.Hs2s) > 1 {
			addAt("hive", "hs2b", "HiveServer2 (Standby)", r.Hs2s[1], "hive_hs2b", "")
		}
		addAt("hive", "db", "MetaDB (MySQL)", r.HiveDB, "hive_db", "")
	}
	// trino
	if has("trino") {
		primaryIP := ""
		for _, h := range norm {
			if h.Role == "master" {
				primaryIP = h.HostIP
				break
			}
		}
		if primaryIP == "" {
			primaryIP = norm[0].HostIP
		}
		coordinator := primaryIP
		if o := strings.TrimSpace(params["masters"]); o != "" {
			ov := map[string]string{}
			_ = json.Unmarshal([]byte(o), &ov)
			if v := strings.TrimSpace(ov["trino"]); v != "" {
				coordinator = v
			}
		}
		addAt("trino", "coordinator", "Trino Coordinator", coordinator, "trino", "")
		addAll("trino", "trino_worker", "Trino Worker")
	}

	// 主机行（保持 Seq 有序）
	for _, h := range norm {
		roles := byIP[h.HostIP]
		if roles == nil {
			roles = []model.RolePlanRole{}
		}
		plan.Hosts = append(plan.Hosts, model.RolePlanHost{
			HostID: h.HostID, HostName: h.HostName, HostIP: h.HostIP, Seq: h.Seq, Roles: roles,
		})
	}

	// 注入变量：物化 masterExtra + HA 变量表 + HA XML 块，部署期直接取用（§4.1-4）
	injected := map[string]string{}
	for k, v := range bigdataMasterExtra(norm) {
		injected[k] = v
	}
	for k, v := range bigdataHAVarTable(r) {
		injected[k] = v
	}
	for k, v := range bigdataHAXMLBlocks(r, params) {
		injected[k] = v
	}
	plan.Injected = injected

	// 权威 masters 全量投影（含自动落点的备角色）：运行期推导的唯一输入（§2.5）
	plan.Masters = planMastersProjection(r)

	// 自动落点提示（§2.2 warnings）
	warn := func(key, name, ip string) {
		if !manual[key] && ip != "" {
			plan.Warnings = append(plan.Warnings, name+" 由自动分配落到 "+ip)
		}
	}
	warn("hdfs_nn2", "NameNode (Standby)", r.Nn2)
	warn("yarn_rm2", "ResourceManager (Standby)", r.Rm2)
	warn("spark_m2", "Spark Master (Standby)", r.SparkM2)
	warn("flink_jm2", "Flink JobManager (Standby)", r.FlinkJm2)
	warn("hbase_hm2", "HBase HMaster (Backup)", r.HMaster2)
	if len(r.MSs) > 1 {
		warn("hive_ms2", "Hive Metastore (Standby)", r.MSs[1])
	}
	if len(r.Hs2s) > 1 {
		warn("hive_hs2b", "HiveServer2 (Standby)", r.Hs2s[1])
	}
	if !manual["hdfs_jns"] {
		plan.Warnings = append(plan.Warnings, "JournalNode 由自动分配落到前 3 台（"+strings.Join(r.JNs, ", ")+"）")
	}
	if !manual["zookeeper_ips"] {
		plan.Warnings = append(plan.Warnings, "ZooKeeper 由自动分配落到前 3 台（"+r.ZKIps+"）")
	}

	if len(opts.ManualKeys) > 0 {
		plan.GeneratedBy = "manual"
	} else {
		plan.GeneratedBy = "auto"
	}
	plan.ManualKeys = append([]string{}, opts.ManualKeys...)
	return plan, nil
}

func mapSrc(m bool) string {
	if m {
		return "manual"
	}
	return "auto"
}

// planGenericRoles 通用套件（非 bigdata）的角色规划（docs/role-plan-design.md §二·全套件覆盖）。
// 引导节点判定与部署引擎 stackClusterExtra 一致：Role==master 的主机，否则首台兜底；
// scope 语义："" = 落点角色（缩容保护），"all" = 全员角色，"rest" = 非引导成员角色，"aux" = 辅助组件（不保护）。
func planGenericRoles(bp *store.BuiltinStack, hosts []model.StackRunHost, opts PlanOptions) (model.RolePlan, error) {
	plan := model.RolePlan{Op: opts.Op, GeneratedAt: nowLocal(), Rev: 1}
	if bp == nil {
		return plan, fmt.Errorf("套件不存在")
	}
	if len(hosts) == 0 {
		return plan, fmt.Errorf("角色规划需要至少一台主机")
	}
	norm := make([]model.StackRunHost, len(hosts))
	copy(norm, hosts)
	sort.SliceStable(norm, func(i, j int) bool { return norm[i].Seq < norm[j].Seq })
	md := bp.ModeDef(opts.Mode)
	if md != nil && len(norm) < md.MinHosts {
		return plan, fmt.Errorf("%s「%s」至少需要 %d 台主机（当前 %d 台）", bp.Name, md.Label, md.MinHosts, len(norm))
	}
	bootIdx, hasMaster := -1, false
	for i, h := range norm {
		if h.Role == "master" {
			bootIdx, hasMaster = i, true
			break
		}
	}
	if bootIdx < 0 {
		bootIdx = 0
	}
	boot := norm[bootIdx]
	rest := make([]model.StackRunHost, 0, len(norm))
	for i, h := range norm {
		if i != bootIdx {
			rest = append(rest, h)
		}
	}
	params := opts.Params
	if params == nil {
		params = map[string]string{}
	}
	get := func(k string) string { return strings.TrimSpace(params[k]) }
	isYes := func(k string) bool {
		v := strings.ToLower(get(k))
		return v == "yes" || v == "true" || v == "1"
	}

	byIP := map[string][]model.RolePlanRole{}
	addAt := func(comp, role, label, scope, source string, h model.StackRunHost) {
		byIP[h.HostIP] = append(byIP[h.HostIP], model.RolePlanRole{
			Comp: comp, Role: role, Label: label, Source: source, Scope: scope,
		})
	}
	addAll := func(comp, role, label string) {
		for _, h := range norm {
			addAt(comp, role, label, "all", "auto", h)
		}
	}
	addRest := func(comp, role, label string) {
		for _, h := range rest {
			addAt(comp, role, label, "rest", "auto", h)
		}
	}
	src := mapSrc(opts.ManualMaster)

	switch {
	case bp.Key == "redis" && opts.Mode == "replication":
		addAt("redis", "master", "Redis Master", "", src, boot)
		addRest("redis", "replica", "Redis Replica")
	case bp.Key == "redis" && opts.Mode == "sentinel":
		addAt("redis", "master", "Redis Master", "", src, boot)
		addRest("redis", "replica", "Redis Replica")
		addAll("redis", "sentinel", "Sentinel")
	case bp.Key == "redis" && opts.Mode == "cluster":
		addAll("redis", "node", "Redis Cluster 节点")
		plan.Warnings = append(plan.Warnings,
			"主从与哈希槽由 Redis Cluster 自动分配（--cluster-replicas "+firstNonEmpty(get("replicas"), "0")+"），预览不指定具体主从")
	case bp.Key == "kafka" && opts.Mode == "kraft":
		addAll("kafka", "broker", "Broker + Controller（KRaft 仲裁）")
		if isYes("enable_ui") {
			addAt("kafka", "ui", "Kafka UI", "aux", "auto", norm[0])
			plan.Warnings = append(plan.Warnings, "Kafka UI 部署在首台 "+norm[0].HostIP)
		}
	case bp.Key == "kafka" && opts.Mode == "zk":
		addAll("kafka", "zk", "ZooKeeper")
		addAll("kafka", "broker", "Broker")
		if isYes("enable_ui") {
			addAt("kafka", "ui", "Kafka UI", "aux", "auto", norm[0])
			plan.Warnings = append(plan.Warnings, "Kafka UI 部署在首台 "+norm[0].HostIP)
		}
	case bp.Key == "elasticsearch":
		addAt("elasticsearch", "boot_master", "引导主节点（master, data）", "", src, boot)
		addRest("elasticsearch", "data", "数据节点（data）")
	case bp.Key == "rabbitmq":
		addAt("rabbitmq", "seed", "初始化节点", "", src, boot)
		addRest("rabbitmq", "member", "集群成员")
	case bp.Key == "rocketmq":
		addAt("rocketmq", "master", "NameServer + Broker Master", "", src, boot)
		addRest("rocketmq", "slave", "Broker Slave")
	case bp.Key == "nacos":
		label := "引导节点（MySQL + Nacos）"
		if get("db_host") != "" {
			label = "引导节点"
			plan.Warnings = append(plan.Warnings, "使用外部 MySQL "+get("db_host")+"，引导节点不再自动部署 MySQL")
		}
		addAt("nacos", "boot", label, "", src, boot)
		addRest("nacos", "member", "Nacos 成员")
	case bp.Key == "powerjob":
		label := "引导节点（MySQL + Server）"
		if get("db_host") != "" {
			label = "引导节点"
			plan.Warnings = append(plan.Warnings, "使用外部 MySQL "+get("db_host")+"，引导节点不再自动部署 MySQL")
		}
		addAt("powerjob", "boot", label, "", src, boot)
		addRest("powerjob", "server", "Server 成员")
	default:
		addAt(bp.Key, "master", "主节点", "", src, boot)
		addRest(bp.Key, "member", "成员节点")
	}

	if md != nil && md.AssignMaster && !hasMaster {
		plan.Warnings = append(plan.Warnings, "主节点自动落到 "+boot.HostIP+"（未显式指定，默认首台）")
	}

	for _, h := range norm {
		roles := byIP[h.HostIP]
		if roles == nil {
			roles = []model.RolePlanRole{}
		}
		plan.Hosts = append(plan.Hosts, model.RolePlanHost{
			HostID: h.HostID, HostName: h.HostName, HostIP: h.HostIP, Seq: h.Seq, Roles: roles,
		})
	}
	if opts.ManualMaster {
		plan.GeneratedBy = "manual"
	} else {
		plan.GeneratedBy = "auto"
	}
	return plan, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// normalizePlanHosts 复制主机列表并按 Seq 排序；把 userMasters（非空值）合并进首主机参数的 masters，
// 使 stackBigdataRole 消费与用户意图一致的手动输入。
func normalizePlanHosts(hosts []model.StackRunHost, userMasters map[string]string) []model.StackRunHost {
	norm := make([]model.StackRunHost, len(hosts))
	copy(norm, hosts)
	sort.SliceStable(norm, func(i, j int) bool { return norm[i].Seq < norm[j].Seq })
	p := map[string]string{}
	_ = json.Unmarshal([]byte(norm[0].ParamsJSON), &p)
	if p == nil {
		p = map[string]string{}
	}
	existing := map[string]string{}
	_ = json.Unmarshal([]byte(p["masters"]), &existing)
	changed := false
	for k, v := range userMasters {
		if strings.TrimSpace(v) != "" && existing[k] != v {
			existing[k] = strings.TrimSpace(v)
			changed = true
		}
	}
	if changed {
		if b, err := json.Marshal(existing); err == nil {
			p["masters"] = string(b)
		}
		if b, err := json.Marshal(p); err == nil {
			norm[0].ParamsJSON = string(b)
		}
	}
	return norm
}

// planMastersProjection 把权威角色结果投影为全量 masters 映射（含自动落点的备角色键）。
// 该投影写入实例/运行主机参数后，运行期任何子集主机上的 stackBigdataRole 推导
// 都会得到与 Plan 逐字节一致的结果（41 类不一致的根治机制）。
func planMastersProjection(r bigdataRole) map[string]string {
	ms2, hs2b, hivePrimary := "", "", ""
	if len(r.MSs) > 0 {
		hivePrimary = r.MSs[0]
	}
	if len(r.MSs) > 1 {
		ms2 = r.MSs[1]
	}
	if len(r.Hs2s) > 1 {
		hs2b = r.Hs2s[1]
	}
	return map[string]string{
		"hdfs": r.Nn1, "hdfs_nn2": r.Nn2, "hdfs_jns": strings.Join(r.JNs, ","),
		"yarn": r.Rm1, "yarn_rm2": r.Rm2,
		"spark": r.SparkM1, "spark_m2": r.SparkM2,
		"flink": r.FlinkJM1, "flink_jm2": r.FlinkJm2,
		"hbase": r.HMaster1, "hbase_hm2": r.HMaster2,
		"hive": hivePrimary, "hive_ms2": ms2, "hive_hs2b": hs2b, "hive_db": r.HiveDB,
		"zookeeper_ips": r.ZKIps,
	}
}

// decodeRolePlan 解析已持久化的角色计划；空/损坏返回 nil。
func decodeRolePlan(s string) *model.RolePlan {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var p model.RolePlan
	if err := json.Unmarshal([]byte(s), &p); err != nil || len(p.Hosts) == 0 {
		return nil
	}
	return &p
}

// encodeRolePlan 序列化角色计划。
func encodeRolePlan(p *model.RolePlan) string {
	if p == nil {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// applyPlanMasters 把计划的全量 masters 投影注入参数表（覆盖旧的覆盖式 masters）。
func applyPlanMasters(params map[string]string, plan *model.RolePlan) {
	if plan == nil || len(plan.Masters) == 0 || params == nil {
		return
	}
	if b, err := json.Marshal(plan.Masters); err == nil {
		params["masters"] = string(b)
	}
}

// applyPlanToHosts 把全量 masters 投影注入每台运行主机的参数（运行期推导与 Plan 逐字节一致）。
func applyPlanToHosts(hosts []model.StackRunHost, plan *model.RolePlan) {
	if plan == nil || len(plan.Masters) == 0 {
		return
	}
	mb, err := json.Marshal(plan.Masters)
	if err != nil {
		return
	}
	for i := range hosts {
		m := map[string]string{}
		_ = json.Unmarshal([]byte(hosts[i].ParamsJSON), &m)
		if m == nil {
			m = map[string]string{}
		}
		m["masters"] = string(mb)
		if b, merr := json.Marshal(m); merr == nil {
			hosts[i].ParamsJSON = string(b)
		}
	}
}

// planProtectedHosts 从计划得出不可缩容主机（承载落点角色，scope==""）。
// 适用全部套件：bigdata HA 的 NN/RM/JN/ZK 等落点、通用套件的主/引导节点；
// 全员（all）/随成员（rest）/辅助（aux）角色不保护。返回 map[ip]=角色标签列表。
func planProtectedHosts(p *model.RolePlan) (map[string]string, bool) {
	if p == nil {
		return nil, false
	}
	label := map[string]string{}
	for _, h := range p.Hosts {
		for _, rr := range h.Roles {
			if rr.Scope != "" {
				continue
			}
			if label[h.HostIP] == "" {
				label[h.HostIP] = rr.Label
			} else {
				label[h.HostIP] += "+" + rr.Label
			}
		}
	}
	if len(label) == 0 {
		return nil, false
	}
	return label, true
}

// loadRolePlan 读取实例已物化的角色计划；缺失时以当前成员+参数惰性生成并落库（§6.1 存量兼容）。
// 全部套件适用：bigdata 走 planBigdataRoles，其余套件走 planGenericRoles。
func (h *stackHandler) loadRolePlan(inst *model.StackInstance) (*model.RolePlan, error) {
	if inst == nil {
		return nil, fmt.Errorf("实例不存在")
	}
	if p := decodeRolePlan(inst.RolePlanJSON); p != nil {
		return p, nil
	}
	if inst.StackKey == "bigdata" {
		return h.loadBigdataRolePlan(inst)
	}
	bp := store.FindBuiltinStack(inst.StackKey)
	if bp == nil {
		return nil, fmt.Errorf("套件 %s 不存在，无法生成角色计划", inst.StackKey)
	}
	instParams := parseJSONMap(inst.ParamsJSON)
	runHosts := make([]model.StackRunHost, 0, 8)
	for _, hh := range activeInstanceHosts(inst) {
		merged := mergeParamMaps(instParams, parseJSONMap(hh.ParamsJSON))
		pb, _ := json.Marshal(merged)
		runHosts = append(runHosts, model.StackRunHost{
			HostID: hh.HostID, HostName: hh.HostName, HostIP: hh.HostIP,
			Role: hh.Role, Seq: hh.Seq, ParamsJSON: string(pb),
		})
	}
	if len(runHosts) == 0 {
		return nil, fmt.Errorf("集群没有在役成员，无法生成角色计划")
	}
	plan, err := planGenericRoles(bp, runHosts, PlanOptions{
		Op: "replan", Mode: inst.Mode, Params: instParams,
		ManualMaster: anyMasterRoleHost(runHosts),
	})
	if err != nil {
		return nil, err
	}
	inst.RolePlanJSON = encodeRolePlan(&plan)
	if inst.ID > 0 {
		_ = h.repo.SetInstanceRolePlan(inst.ID, inst.RolePlanJSON)
	}
	return &plan, nil
}

// loadBigdataRolePlan bigdata 专属的惰性生成（原 loadRolePlan 主体）。
func (h *stackHandler) loadBigdataRolePlan(inst *model.StackInstance) (*model.RolePlan, error) {
	if p := decodeRolePlan(inst.RolePlanJSON); p != nil {
		return p, nil
	}
	// 惰性生成：实例参数 + 各主机参数合并（与探活同源的输入）
	instParams := parseJSONMap(inst.ParamsJSON)
	runHosts := make([]model.StackRunHost, 0, 8)
	for _, hh := range activeInstanceHosts(inst) {
		merged := mergeParamMaps(instParams, parseJSONMap(hh.ParamsJSON))
		pb, _ := json.Marshal(merged)
		runHosts = append(runHosts, model.StackRunHost{
			HostID: hh.HostID, HostName: hh.HostName, HostIP: hh.HostIP,
			Role: hh.Role, Seq: hh.Seq, ParamsJSON: string(pb),
		})
	}
	if len(runHosts) == 0 {
		return nil, fmt.Errorf("集群没有在役成员，无法生成角色计划")
	}
	userMasters := map[string]string{}
	_ = json.Unmarshal([]byte(instParams["masters"]), &userMasters)
	plan, err := planBigdataRoles(runHosts, PlanOptions{Op: "replan", Masters: userMasters})
	if err != nil {
		return nil, err
	}
	inst.RolePlanJSON = encodeRolePlan(&plan)
	if inst.ID > 0 {
		_ = h.repo.SetInstanceRolePlan(inst.ID, inst.RolePlanJSON)
	}
	return &plan, nil
}

// anyMasterRoleHost 判断主机集合中是否存在显式 master 角色（通用套件来源标注用）。
func anyMasterRoleHost(hosts []model.StackRunHost) bool {
	for _, h := range hosts {
		if h.Role == "master" {
			return true
		}
	}
	return false
}

// planFullMemberSet 合并既有成员与新主机，产出规划用的完整成员集（按 Seq 有序）。
func planFullMemberSet(existing []model.StackInstanceHost, newHosts []model.StackRunHost) []model.StackRunHost {
	all := instanceHostsAsRunHosts(existing)
	all = append(all, newHosts...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Seq < all[j].Seq })
	return all
}

func nowLocal() string {
	return time.Now().Format("2006-01-02T15:04:05+08:00")
}

// PlanPreview POST /api/stacks/plan/preview
// dry-run：给定套件+模式+host_ids+参数（bigdata 可含手动角色）→ 返回 RolePlan（不落库），供前端预览。
// 全部套件适用：部署前必须先看到角色落点（docs/role-plan-design.md §二·全套件覆盖）。
func (h *stackHandler) PlanPreview(c *gin.Context) {
	var req struct {
		StackKey     string                       `json:"stack_key"`
		Mode         string                       `json:"mode"`
		HostIDs      []int64                      `json:"host_ids"`
		MasterHostID int64                        `json:"master_host_id"`
		Params       map[string]string            `json:"params"`
		HostParams   map[string]map[string]string `json:"host_params"`
		Masters      map[string]string            `json:"masters"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if len(req.HostIDs) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "请选择主机")
		return
	}
	stackKey := strings.TrimSpace(req.StackKey)
	if stackKey == "" {
		stackKey = "bigdata"
	}
	bp := store.FindBuiltinStack(stackKey)
	if bp == nil {
		resp.Fail(c, resp.CodeNotFound, "套件不存在: "+stackKey)
		return
	}
	mode := bp.ModeDef(req.Mode)
	if mode == nil {
		resp.Fail(c, resp.CodeBadRequest, "未知模式: "+req.Mode)
		return
	}
	params := req.Params
	if params == nil {
		params = map[string]string{}
	}
	// 预览在部署前置（参数填写完成后）触发：走与真实部署一致的完整构建与参数校验。
	hosts, err := h.buildCreateHosts(bp, mode, req.HostIDs, req.MasterHostID, params, req.HostParams)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	if stackKey == "bigdata" {
		if len(req.Masters) > 0 {
			if b, jerr := json.Marshal(req.Masters); jerr == nil {
				params["masters"] = string(b)
			}
		}
		manualKeys := make([]string, 0, len(req.Masters))
		for k, v := range req.Masters {
			if strings.TrimSpace(v) != "" {
				manualKeys = append(manualKeys, k)
			}
		}
		plan, perr := planBigdataRoles(hosts, PlanOptions{Op: "create", Masters: req.Masters, ManualKeys: manualKeys})
		if perr != nil {
			// 冲突（主备同机/IP 越界/HA 前置缺失）在此返回，前端标红拦截
			resp.Fail(c, resp.CodeBadRequest, perr.Error())
			return
		}
		resp.OK(c, plan)
		return
	}
	plan, perr := planGenericRoles(bp, hosts, PlanOptions{
		Op: "create", Mode: req.Mode, Params: params, ManualMaster: req.MasterHostID > 0,
	})
	if perr != nil {
		resp.Fail(c, resp.CodeBadRequest, perr.Error())
		return
	}
	resp.OK(c, plan)
}

// GetPlan GET /api/stacks/instances/:id/plan
// 读取当前角色计划；缺失时惰性生成并落库（存量兼容）。
func (h *stackHandler) GetPlan(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	plan, perr := h.loadRolePlan(inst)
	if perr != nil {
		resp.Fail(c, resp.CodeBadRequest, perr.Error())
		return
	}
	resp.OK(c, plan)
}

// ReplanPlan POST /api/stacks/instances/:id/replan
// 对已有实例按当前成员重规划 → 持久化 rev+1（落点角色沿全量投影冻结，来源标注继承）。
func (h *stackHandler) ReplanPlan(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	old, oerr := h.loadRolePlan(inst)
	if oerr != nil {
		resp.Fail(c, resp.CodeBadRequest, oerr.Error())
		return
	}
	all := instanceHostsAsRunHosts(activeInstanceHosts(inst))
	var plan model.RolePlan
	if inst.StackKey != "bigdata" {
		bp := store.FindBuiltinStack(inst.StackKey)
		if bp == nil {
			resp.Fail(c, resp.CodeBadRequest, "套件不存在: "+inst.StackKey)
			return
		}
		p, perr := planGenericRoles(bp, all, PlanOptions{
			Op: "replan", Mode: inst.Mode, Params: parseJSONMap(inst.ParamsJSON),
			ManualMaster: anyMasterRoleHost(all),
		})
		if perr != nil {
			resp.Fail(c, resp.CodeBadRequest, perr.Error())
			return
		}
		plan = p
	} else {
		applyPlanToHosts(all, old)
		p, perr := planBigdataRoles(all, PlanOptions{Op: "replan", Masters: old.Masters, ManualKeys: old.ManualKeys})
		if perr != nil {
			resp.Fail(c, resp.CodeBadRequest, perr.Error())
			return
		}
		plan = p
	}
	plan.Rev = old.Rev + 1
	if b := encodeRolePlan(&plan); b != "" {
		_ = h.repo.SetInstanceRolePlan(inst.ID, b)
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.plan.replan", TargetType: "stack_instance", TargetID: inst.ID,
		Detail:   fmt.Sprintf("name=%s rev=%d hosts=%d", inst.Name, plan.Rev, len(all)),
		RemoteIP: c.ClientIP(),
	})
	resp.OK(c, plan)
}

// planForStackOp 按操作类型产出角色计划（docs/role-plan-design.md §3.1 时机矩阵）：
//   - create / add_component：生成或重规划（rev+1），手动项继承；
//   - scale_out / scale_in：增量刷新——落点角色冻结，全员/成员角色随成员伸缩（拍板 ②）；
//   - reinstall：只读沿用当前 Plan（缺失则惰性生成），保证重装后落点不变。
//
// 全部套件适用：bigdata 走 planBigdataRoles，其余套件走 planForGenericOp。
// 返回 nil 表示该操作不涉及计划。
func (h *stackHandler) planForStackOp(op string, bp *store.BuiltinStack, mode *model.StackMode,
	inst *model.StackInstance, params map[string]string, hosts []model.StackRunHost,
	removeIDs []int64, masterHostID int64) (*model.RolePlan, error) {
	if bp == nil {
		return nil, nil
	}
	if bp.Key != "bigdata" {
		return h.planForGenericOp(op, bp, mode, inst, params, hosts, removeIDs, masterHostID)
	}
	switch op {
	case "create", "add_component":
		userMasters := map[string]string{}
		_ = json.Unmarshal([]byte(params["masters"]), &userMasters)
		manualKeys := make([]string, 0, len(userMasters))
		for k, v := range userMasters {
			if strings.TrimSpace(v) != "" {
				manualKeys = append(manualKeys, k)
			}
		}
		plan, err := planBigdataRoles(hosts, PlanOptions{Op: op, Masters: userMasters, ManualKeys: manualKeys})
		if err != nil {
			return nil, err
		}
		// 重规划：rev 在既有计划上递增（§3.2 只保留最近一份）
		if old := decodeRolePlan(inst.RolePlanJSON); old != nil && old.Rev > 0 {
			plan.Rev = old.Rev + 1
		}
		return &plan, nil

	case "reinstall":
		if inst == nil {
			return nil, nil
		}
		return h.loadRolePlan(inst)

	case "scale_out":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		all := planFullMemberSet(activeInstanceHosts(inst), hosts)
		applyPlanToHosts(all, old) // 冻结落点：全量投影进成员参数，重算结果与旧 Plan 一致
		plan, perr := planBigdataRoles(all, PlanOptions{Op: "scale_out", Masters: old.Masters, ManualKeys: old.ManualKeys})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil

	case "scale_in":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		removeSet := map[int64]bool{}
		for _, id := range removeIDs {
			removeSet[id] = true
		}
		remaining := make([]model.StackInstanceHost, 0, len(inst.Hosts))
		for _, hh := range activeInstanceHosts(inst) {
			if !removeSet[hh.HostID] {
				remaining = append(remaining, hh)
			}
		}
		all := instanceHostsAsRunHosts(remaining)
		applyPlanToHosts(all, old) // 冻结落点：移除的只是全员角色节点，落点保持不变
		plan, perr := planBigdataRoles(all, PlanOptions{Op: "scale_in", Masters: old.Masters, ManualKeys: old.ManualKeys})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil
	}
	return nil, nil
}

// planForGenericOp 通用套件（非 bigdata）按操作产出角色计划：
//   - create：生成（主/引导节点来自 masterHostID，缺省回落首台，标注来源）；
//   - scale_out：全成员集重算——引导节点由既有成员的 master 角色锚定，新机自动承担 rest 角色；
//   - scale_in：剩余成员重算（引导节点若在移除集内已被前置校验拦截）；
//   - reinstall：只读沿用当前 Plan。
func (h *stackHandler) planForGenericOp(op string, bp *store.BuiltinStack, mode *model.StackMode,
	inst *model.StackInstance, params map[string]string, hosts []model.StackRunHost,
	removeIDs []int64, masterHostID int64) (*model.RolePlan, error) {
	modeKey := ""
	if mode != nil {
		modeKey = mode.Key
	}
	switch op {
	case "create":
		plan, err := planGenericRoles(bp, hosts, PlanOptions{
			Op: op, Mode: modeKey, Params: params, ManualMaster: masterHostID > 0,
		})
		if err != nil {
			return nil, err
		}
		if old := decodeRolePlan(inst.RolePlanJSON); old != nil && old.Rev > 0 {
			plan.Rev = old.Rev + 1
		}
		return &plan, nil

	case "reinstall":
		if inst == nil {
			return nil, nil
		}
		return h.loadRolePlan(inst)

	case "scale_out":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		all := planFullMemberSet(activeInstanceHosts(inst), hosts)
		plan, perr := planGenericRoles(bp, all, PlanOptions{
			Op: op, Mode: inst.Mode, Params: params, ManualMaster: anyMasterRoleHost(all),
		})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil

	case "scale_in":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		removeSet := map[int64]bool{}
		for _, id := range removeIDs {
			removeSet[id] = true
		}
		remaining := make([]model.StackInstanceHost, 0, len(inst.Hosts))
		for _, hh := range activeInstanceHosts(inst) {
			if !removeSet[hh.HostID] {
				remaining = append(remaining, hh)
			}
		}
		all := instanceHostsAsRunHosts(remaining)
		plan, perr := planGenericRoles(bp, all, PlanOptions{
			Op: op, Mode: inst.Mode, Params: params, ManualMaster: anyMasterRoleHost(all),
		})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil
	}
	return nil, nil
}
