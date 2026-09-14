package bigdata

import (
	"encoding/json"
	"sort"
	"strings"

	"infra-ops/model"
)

// filterEmpty 去掉空白项并做 TrimSpace（等价 api/shared.FilterEmpty，避免 store→api 依赖）。
func filterEmpty(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// containsString 等价 api/shared.ContainsString。
func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Role 一次 HA 角色分配的确定性结果（§4.1/§4.2）。
type Role struct {
	Ha                 bool
	ZKIps              string   // ZK ensemble 前 3 台逗号列表
	HdfsEntry          string   // HDFS 入口 URI
	Nn1, Nn2           string   // NameNode Active / Standby
	JNs                []string // JournalNode 主机
	Rm1, Rm2           string   // ResourceManager Active / Standby
	SparkM1, SparkM2   string   // Spark Master 主 / 备
	FlinkJM1, FlinkJm2 string   // Flink JobManager 主 / 备
	HMaster1, HMaster2 string   // HBase HMaster 主 / backup
	MSs, Hs2s          []string // Hive Metastore / HiveServer2 主机
	HiveDB             string
}

// ResolveRole 按 §4.2 确定性算法解析大数据底座全部角色落点。
// 读取 hosts[0].ParamsJSON 中的 ha/masters 参数；hosts 需按 Seq 有序。
// primary = 角色矩阵指定 ?? 主节点；secondary = 角色矩阵指定 ?? seq 最小且非 primary 的主机；
// jn/zk = 角色矩阵指定 ?? 前 3 台；hive_db_host = 角色矩阵指定 ?? primary(hive) 主机。
//
// 原 api/stack/stack_bigdata.go 的 stackBigdataRole 原样迁入，逐字节等价。
func ResolveRole(hosts []model.StackRunHost) Role {
	res := Role{}
	if len(hosts) == 0 {
		return res
	}
	sorted := make([]model.StackRunHost, len(hosts))
	copy(sorted, hosts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	ips := make([]string, 0, len(sorted))
	for _, h := range sorted {
		ips = append(ips, h.HostIP)
	}
	p := map[string]string{}
	_ = json.Unmarshal([]byte(sorted[0].ParamsJSON), &p)
	res.Ha = strings.EqualFold(strings.TrimSpace(p["ha"]), "true")
	override := map[string]string{}
	_ = json.Unmarshal([]byte(p["masters"]), &override)

	primary := ""
	for _, h := range sorted {
		if h.Role == "master" {
			primary = h.HostIP
			break
		}
	}
	if primary == "" {
		primary = ips[0]
	}
	masterOf := func(comp string) string {
		if ip := strings.TrimSpace(override[comp]); ip != "" {
			return ip
		}
		return primary
	}
	// seq 最小的非 primary 主机（component 从角色自动落点）
	autoSecondary := func(comp string) string {
		for _, ip := range ips {
			if ip != masterOf(comp) {
				return ip
			}
		}
		return ""
	}
	secondaryOf := func(comp, key string) string {
		if ip := strings.TrimSpace(override[key]); ip != "" {
			return ip
		}
		if res.Ha {
			return autoSecondary(comp)
		}
		return ""
	}
	firstN := func(n int) []string {
		if len(ips) > n {
			return ips[:n]
		}
		return ips
	}

	zk := firstN(3)
	if s := strings.TrimSpace(override["zookeeper_ips"]); s != "" {
		zk = filterEmpty(strings.Split(s, ","))
	}
	res.ZKIps = strings.Join(zk, ",")

	nn1 := masterOf("hdfs")
	nn2 := secondaryOf("hdfs", "hdfs_nn2")
	jns := firstN(3)
	if s := strings.TrimSpace(override["hdfs_jns"]); s != "" {
		jns = filterEmpty(strings.Split(s, ","))
	}
	res.Nn1, res.Nn2 = nn1, nn2
	res.JNs = jns

	ns := strings.TrimSpace(p["hdfs_nameservice"])
	if ns == "" {
		ns = "ns1"
	}
	if res.Ha {
		res.HdfsEntry = "hdfs://" + ns
	} else {
		rpc := strings.TrimSpace(p["nn_rpc_port"])
		if rpc == "" {
			rpc = "9000"
		}
		res.HdfsEntry = "hdfs://" + nn1 + ":" + rpc
	}

	res.Rm1 = masterOf("yarn")
	res.Rm2 = secondaryOf("yarn", "yarn_rm2")
	res.SparkM1 = masterOf("spark")
	res.SparkM2 = secondaryOf("spark", "spark_m2")
	res.FlinkJM1 = masterOf("flink")
	res.FlinkJm2 = secondaryOf("flink", "flink_jm2")
	res.HMaster1 = masterOf("hbase")
	res.HMaster2 = secondaryOf("hbase", "hbase_hm2")

	hiveP := masterOf("hive")
	res.HiveDB = strings.TrimSpace(override["hive_db"])
	if res.HiveDB == "" {
		res.HiveDB = hiveP
	}
	// Metastore 双实例：ms1=primary，ms2=矩阵指定 ?? 自动第二台
	ms2 := secondaryOf("hive", "hive_ms2")
	res.MSs = []string{hiveP}
	if ms2 != "" && ms2 != hiveP {
		res.MSs = []string{hiveP, ms2}
	}
	// HiveServer2 双实例：独立落点键 hive_hs2b
	res.Hs2s = res.MSs
	if s := strings.TrimSpace(override["hive_hs2b"]); s != "" && s != hiveP {
		res.Hs2s = []string{hiveP, s}
	}
	return res
}

// HAVarTable 将 HA 角色分配结果转为注入变量表（§4.1，非 HA 组件位一律注入空串）。
func HAVarTable(r Role) map[string]string {
	jnStr := strings.Join(r.JNs, ",")
	msUris := make([]string, 0, len(r.MSs))
	for _, m := range r.MSs {
		msUris = append(msUris, "thrift://"+m+":9083")
	}
	return map[string]string{
		"__zk_ips":       r.ZKIps,
		"__hdfs_entry":   r.HdfsEntry,
		"__nn1_ip":       r.Nn1,
		"__nn2_ip":       r.Nn2,
		"__jn_ips":       jnStr,
		"__rm1_ip":       r.Rm1,
		"__rm2_ip":       r.Rm2,
		"__spark_m2_ip":  r.SparkM2,
		"__flink_jm2_ip": r.FlinkJm2,
		"__hmaster2_ip":  r.HMaster2,
		"__hs2_ips":      strings.Join(r.Hs2s, ","),
		"__ms_ips":       strings.Join(r.MSs, ","),
		"__hive_ms_uris": strings.Join(msUris, ","),
		"__hive_db_ip":   r.HiveDB,
	}
}

// xmlProp 生成单个 Hadoop/云原生 XML property 块（值做 XML 转义）。
func xmlProp(name, value string) string {
	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", `'`, "&apos;")
		return r.Replace(s)
	}
	return "  <property>\n    <name>" + esc(name) + "</name>\n    <value>" + esc(value) + "</value>\n  </property>\n"
}

// HAXMLBlocks 生成配置模板中以 {{__ha_*}} 注入的 XML 条件块（§8.2）。
// 非 HA / 未选组件一律返回空串或与现状一致的单实例值，保证非 HA 渲染零差异。
// 原 api/stack/stack_bigdata_render.go 的 bigdataHAXMLBlocks 原样迁入，逐字节等价。
func HAXMLBlocks(r Role, params map[string]string) map[string]string {
	out := map[string]string{
		"__ha_core_props": "", "__ha_hdfs_props": "", "__ha_yarn_props": "",
		"__ha_hive_jdo": "", "__ha_hive_ms": "", "__ha_hive_zk": "",
	}
	ns := strings.TrimSpace(params["hdfs_nameservice"])
	if ns == "" {
		ns = "ns1"
	}
	nnRpc := strings.TrimSpace(params["nn_rpc_port"])
	if nnRpc == "" {
		nnRpc = "9000"
	}
	jnRpc := strings.TrimSpace(params["jn_rpc_port"])
	if jnRpc == "" {
		jnRpc = "8485"
	}
	compOn := func(c string) bool {
		for _, x := range ParseComponentsCSV(params["components"]) {
			if strings.EqualFold(strings.TrimSpace(x), c) {
				return true
			}
		}
		return false
	}

	if r.Ha {
		var b strings.Builder
		b.WriteString(xmlProp("ha.zookeeper.quorum", r.ZKIps))
		b.WriteString(xmlProp("dfs.nameservices", ns))
		b.WriteString(xmlProp("dfs.ha.namenodes."+ns, "nn1,nn2"))
		b.WriteString(xmlProp("dfs.client.failover.proxy.provider."+ns, "org.apache.hadoop.hdfs.server.namenode.ha.ConfiguredFailoverProxyProvider"))
		out["__ha_core_props"] = b.String()

		b.Reset()
		b.WriteString(xmlProp("dfs.namenode.shared.edits.dir", "qjournal://"+strings.Join(r.JNs, ":"+jnRpc+";")+":"+jnRpc+"/"+ns))
		b.WriteString(xmlProp("dfs.journalnode.edits.dir", "/hadoop/dfs/journal"))
		b.WriteString(xmlProp("dfs.namenode.rpc-address."+ns+".nn1", r.Nn1+":"+nnRpc))
		b.WriteString(xmlProp("dfs.namenode.rpc-address."+ns+".nn2", r.Nn2+":"+nnRpc))
		b.WriteString(xmlProp("dfs.namenode.http-address."+ns+".nn1", r.Nn1+":9870"))
		b.WriteString(xmlProp("dfs.namenode.http-address."+ns+".nn2", r.Nn2+":9870"))
		b.WriteString(xmlProp("dfs.ha.automatic-failover.enabled", "true"))
		b.WriteString(xmlProp("dfs.ha.fencing.methods", "shell(/bin/true)"))
		// 容器内主机名为 IP 字面量时默认反解失败会拒收 DataNode 注册，需关闭该检查
		b.WriteString(xmlProp("dfs.namenode.datanode.registration.ip-hostname-check", "false"))
		out["__ha_hdfs_props"] = b.String()

		if compOn("yarn") {
			b.Reset()
			b.WriteString(xmlProp("yarn.resourcemanager.ha.enabled", "true"))
			b.WriteString(xmlProp("yarn.resourcemanager.ha.rm-ids", "rm1,rm2"))
			b.WriteString(xmlProp("yarn.resourcemanager.hostname.rm1", r.Rm1))
			b.WriteString(xmlProp("yarn.resourcemanager.hostname.rm2", r.Rm2))
			// HA 下 MR AppMaster 的 AmFilterInitializer 会读取 yarn.resourcemanager.webapp.address.<rmId>
			// 拼装 RM_HA_URLS；若缺失则该值为 null，StringUtils.join 直接抛 NPE，
			// 导致 AM WebApp 启动失败 → MRClientService.getHttpPort() NPE → AM exit 1。
			b.WriteString(xmlProp("yarn.resourcemanager.webapp.address.rm1", r.Rm1+":8088"))
			b.WriteString(xmlProp("yarn.resourcemanager.webapp.address.rm2", r.Rm2+":8088"))
			b.WriteString(xmlProp("yarn.resourcemanager.cluster-id", "bigdata-yarn"))
			b.WriteString(xmlProp("yarn.resourcemanager.zk-address", r.ZKIps))
			b.WriteString(xmlProp("yarn.resourcemanager.recovery.enabled", "true"))
			b.WriteString(xmlProp("yarn.resourcemanager.store.class", "org.apache.hadoop.yarn.server.resourcemanager.recovery.ZKRMStateStore"))
			b.WriteString(xmlProp("yarn.resourcemanager.ha.automatic-failover.enabled", "true"))
			b.WriteString(xmlProp("yarn.client.failover-proxy-provider", "org.apache.hadoop.yarn.client.ConfiguredRMFailoverProxyProvider"))
			out["__ha_yarn_props"] = b.String()
		}

		if compOn("hive") && len(r.MSs) > 0 {
			msList := make([]string, 0, len(r.MSs))
			for _, m := range r.MSs {
				msList = append(msList, "thrift://"+m+":9083")
			}
			out["__ha_hive_ms"] = strings.Join(msList, ",")
			pw := strings.TrimSpace(params["hive_db_password"])
			if pw == "" {
				pw = "HiveDb@123"
			}
			b.Reset()
			b.WriteString(xmlProp("javax.jdo.option.ConnectionURL", "jdbc:mysql://"+r.HiveDB+":3306/metastore?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8"))
			b.WriteString(xmlProp("javax.jdo.option.ConnectionDriverName", "com.mysql.cj.jdbc.Driver"))
			b.WriteString(xmlProp("javax.jdo.option.ConnectionUserName", "root"))
			b.WriteString(xmlProp("javax.jdo.option.ConnectionPassword", pw))
			out["__ha_hive_jdo"] = b.String()
			b.Reset()
			b.WriteString(xmlProp("hive.server2.support.dynamic.service.discovery", "true"))
			b.WriteString(xmlProp("hive.zookeeper.quorum", r.ZKIps))
			b.WriteString(xmlProp("hive.zookeeper.namespace", "hiveserver2"))
			out["__ha_hive_zk"] = b.String()
		}
	}
	if !r.Ha || !compOn("hive") {
		// 非 HA / 未选 Hive：保留 Derby 单实例元数据连接（与现状一致）
		out["__ha_hive_jdo"] = xmlProp("javax.jdo.option.ConnectionURL", "jdbc:derby:;databaseName=/opt/hive/data/derby;create=true")
		out["__ha_hive_zk"] = ""
		if compOn("hive") && len(r.MSs) > 0 {
			out["__ha_hive_ms"] = "thrift://" + r.MSs[0] + ":9083"
		} else {
			out["__ha_hive_ms"] = ""
		}
	}
	return out
}
