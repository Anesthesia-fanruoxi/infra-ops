package bigdata

import (
	"encoding/json"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles 实现 stackkit.RolePlanner：把权威落点展开为可持久化 RolePlan。
// hosts 需为本次操作的全部成员（含既有成员）；hosts[0].ParamsJSON 须携带 ha/components/masters。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	return Plan(in.Hosts, in.Op, in.Masters, in.ManualKeys)
}

// Plan 以 ResolveRole 为唯一算法，把权威落点展开为可持久化 RolePlan。
// 原 api/stack/stack_plan.go 的 planBigdataRoles 原样迁入，逐字节等价。
func Plan(hosts []model.StackRunHost, op string, userMasters map[string]string, manualKeys []string) (model.RolePlan, error) {
	plan := model.RolePlan{Op: op, GeneratedAt: stackkit.NowLocal(), Rev: 1}
	if len(hosts) == 0 {
		return plan, fmt.Errorf("角色规划需要至少一台主机")
	}
	// 规范化：按 Seq 排序；把 userMasters 注入首主机参数（ResolveRole 只读 hosts[0] 的参数）
	norm := NormalizeHosts(hosts, userMasters)
	params := map[string]string{}
	_ = json.Unmarshal([]byte(norm[0].ParamsJSON), &params)
	if params == nil {
		params = map[string]string{}
	}

	// masters 键合法性（未知键会被算法静默忽略，必须在规划期拦截）
	allowed := map[string]bool{}
	for _, k := range RoleKeys() {
		allowed[k] = true
	}
	for k := range userMasters {
		if !allowed[k] {
			return plan, fmt.Errorf("角色规划包含未知项: %s", k)
		}
	}
	// 手动指定的 IP 必须落在成员集合内
	member := map[string]bool{}
	for _, h := range norm {
		member[h.HostIP] = true
	}
	for k, v := range userMasters {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" && !member[p] {
				return plan, fmt.Errorf("角色规划参数 %s: IP %s 不在本次成员主机中", k, p)
			}
		}
	}

	comps := ParseComponentsCSV(params["components"])
	if len(comps) == 0 {
		comps = []string{"hdfs"} // 防御：底座至少有 HDFS
	}
	has := func(c string) bool { return containsString(comps, c) }
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
	r := ResolveRole(norm)
	plan.Ha = r.Ha

	// 同组件主从互斥（§4.4-4）
	if err := ValidatePairs(r); err != nil {
		return plan, err
	}

	// 来源标注：用户显式指定的键集合（跨重规划由 ManualKeys 继承）
	manual := map[string]bool{}
	for _, k := range manualKeys {
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
		for _, z := range filterEmpty(strings.Split(r.ZKIps, ",")) {
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
	for k, v := range MasterExtra(norm) {
		injected[k] = v
	}
	for k, v := range HAVarTable(r) {
		injected[k] = v
	}
	for k, v := range HAXMLBlocks(r, params) {
		injected[k] = v
	}
	plan.Injected = injected

	// 权威 masters 全量投影（含自动落点的备角色）：运行期推导的唯一输入（§2.5）
	plan.Masters = MastersProjection(r)

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

	if len(manualKeys) > 0 {
		plan.GeneratedBy = "manual"
	} else {
		plan.GeneratedBy = "auto"
	}
	plan.ManualKeys = append([]string{}, manualKeys...)
	return plan, nil
}

// mapSrc 落点来源标注（与 api/stack.mapSrc 等价）。
func mapSrc(m bool) string {
	if m {
		return "manual"
	}
	return "auto"
}
