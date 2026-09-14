package bigdata

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"infra-ops/model"
)

// ParseComponentsCSV 解析逗号分隔组件列表（去空白、丢空项）。
// 等价 api/stack.parseComponentsCSV。
func ParseComponentsCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// MasterVars 大数据底座可规划主角色的组件 → 注入脚本的变量名。
var MasterVars = map[string]string{
	"hdfs":  "__master_hdfs",
	"yarn":  "__master_yarn",
	"spark": "__master_spark",
	"flink": "__master_flink",
	"hive":  "__master_hive",
	"hbase": "__master_hbase",
	"trino": "__master_trino",
}

// SecondaryVars 组件 → 从角色规划的 masters 参数 key（§4.2）。
// 除 hdfs_jns 为逗号列表外，其余为单个主机 IP。
var SecondaryVars = map[string]string{
	"hdfs":  "hdfs_nn2",
	"yarn":  "yarn_rm2",
	"spark": "spark_m2",
	"flink": "flink_jm2",
	"hbase": "hbase_hm2",
	"hive":  "hive_ms2",
}

// RoleKeys 角色规划允许的全部 masters key（主角色 + 从角色 + Hive 额外项）。
func RoleKeys() []string {
	keys := make([]string, 0, len(MasterVars)+len(SecondaryVars)+4)
	for k := range MasterVars {
		keys = append(keys, k)
	}
	for _, k := range SecondaryVars {
		keys = append(keys, k)
	}
	keys = append(keys, "hdfs_jns", "hive_hs2b", "hive_db", "zookeeper_ips")
	return keys
}

// Masters 解析 masters 参数（JSON：组件→主角色主机 IP），未指定的组件回落到主节点。
// 等价 api/stack.stackBigdataMasters。
func Masters(hosts []model.StackRunHost) map[string]string {
	primary := ""
	for _, h := range hosts {
		if h.Role == "master" {
			primary = h.HostIP
			break
		}
	}
	if primary == "" && len(hosts) > 0 {
		primary = hosts[0].HostIP
	}
	out := map[string]string{}
	if len(hosts) == 0 {
		return out
	}
	p := map[string]string{}
	_ = json.Unmarshal([]byte(hosts[0].ParamsJSON), &p)
	var override map[string]string
	_ = json.Unmarshal([]byte(p["masters"]), &override)
	for comp := range MasterVars {
		ip := primary
		if o := strings.TrimSpace(override[comp]); o != "" {
			ip = o
		}
		out[comp] = ip
	}
	return out
}

// MasterExtra 生成注入变量（__master_<comp> → 主角色 IP），对所有主机相同。
// 等价 api/stack.bigdataMasterExtra。
func MasterExtra(hosts []model.StackRunHost) map[string]string {
	ms := Masters(hosts)
	out := make(map[string]string, len(ms))
	for comp, key := range MasterVars {
		out[key] = ms[comp]
	}
	return out
}

// MastersProjection 把权威角色结果投影为全量 masters 映射（含自动落点的备角色键）。
// 该投影写入实例/运行主机参数后，运行期任何子集主机上的 ResolveRole 推导
// 都会得到与 Plan 逐字节一致的结果（41 类不一致的根治机制）。
func MastersProjection(r Role) map[string]string {
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

// ValidatePairs 同组件主从互斥校验（§4.4-4）。
func ValidatePairs(r Role) error {
	pairErr := func(a, b, name string) error {
		if a != "" && b != "" && a == b {
			return fmt.Errorf("HA 模式 %s 主备不能在同一台主机（%s）", name, a)
		}
		return nil
	}
	if err := pairErr(r.Nn1, r.Nn2, "HDFS NameNode"); err != nil {
		return err
	}
	if err := pairErr(r.Rm1, r.Rm2, "YARN ResourceManager"); err != nil {
		return err
	}
	if err := pairErr(r.SparkM1, r.SparkM2, "Spark Master"); err != nil {
		return err
	}
	if err := pairErr(r.FlinkJM1, r.FlinkJm2, "Flink JobManager"); err != nil {
		return err
	}
	if err := pairErr(r.HMaster1, r.HMaster2, "HBase HMaster"); err != nil {
		return err
	}
	if len(r.MSs) >= 2 {
		if err := pairErr(r.MSs[0], r.MSs[1], "Hive Metastore"); err != nil {
			return err
		}
	}
	if len(r.Hs2s) >= 2 {
		if err := pairErr(r.Hs2s[0], r.Hs2s[1], "HiveServer2"); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeHosts 复制主机列表并按 Seq 排序；把 userMasters（非空值）合并进首主机参数的 masters，
// 使 ResolveRole 消费与用户意图一致的手动输入。等价 api/stack.normalizePlanHosts。
func NormalizeHosts(hosts []model.StackRunHost, userMasters map[string]string) []model.StackRunHost {
	norm := make([]model.StackRunHost, len(hosts))
	copy(norm, hosts)
	sort.SliceStable(norm, func(i, j int) bool { return norm[i].Seq < norm[j].Seq })
	if len(norm) == 0 {
		return norm
	}
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
