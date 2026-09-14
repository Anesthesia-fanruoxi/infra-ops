package bigdata

import (
	"encoding/json"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// validate.go：大数据底座的创建前置校验（原 api/stack/stack_validate_bigdata.go 与
// stack_create.go 的 bigdata 分支原样迁入）。
//
// 三类校验合并在一个入口里按 Op 分流，保持与拆分前完全相同的执行顺序与报错文案：
//   - create        ：角色规划（masters）落点合法性 → 组件清单（HDFS 必选 + 组件依赖）
//   - add_component ：增量合法性 → 合并组件清单与 masters → 合并后重新校验落点
//   - remove_component：卸载合法性（HDFS 不可单卸 + 依赖 + HA 基座）
//
// Params 可就地改写（加装要合并 components / add_components / masters），与既有实现一致。

// ComponentSet 大数据底座支持的组件白名单。
var ComponentSet = map[string]bool{
	"hdfs": true, "zookeeper": true, "yarn": true, "spark": true, "flink": true,
	"hive": true, "hbase": true, "trino": true, "metastore_db": true,
}

// ValidateCreate 实现 stackkit.CreateValidator。
func (d *Driver) ValidateCreate(ctx stackkit.ValidateCtx) error {
	switch ctx.Op {
	case "create":
		if err := validateMasters(ctx.Params, ctx.HostIDs, ctx.MasterID, ctx.LookupHost); err != nil {
			return err
		}
		return validateComponents(ctx.Params["components"], true)
	case "add_component":
		return validateAddFlow(ctx)
	case "remove_component":
		return validateRemoveFlow(ctx)
	}
	return nil
}

// validateAddFlow 加装组件：校验增量 → 合并组件清单与角色规划 → 按合并结果重新校验落点。
func validateAddFlow(ctx stackkit.ValidateCtx) error {
	inst := ctx.Instance
	installed := parseJSONList(inst.ComponentsJSON)
	if len(installed) == 0 {
		base := map[string]string{}
		_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
		installed = ParseComponentsCSV(base["components"])
	}
	added := ParseComponentsCSV(ctx.ReqComponents)
	if err := validateAdd(added, installed); err != nil {
		return err
	}
	ctx.Params["components"] = strings.Join(stackkit.MergeList(installed, added), ",")
	// 记录本次新增组件：流水线据此裁剪阶段（只跑新增组件对应的阶段），
	// 否则加装会退化成整机重装，甚至执行 reset 把既有数据清掉。
	ctx.Params["add_components"] = strings.Join(added, ",")
	// 合并角色规划：保留实例已有组件主角色，覆盖本次加装指定
	baseParams := map[string]string{}
	_ = json.Unmarshal([]byte(inst.ParamsJSON), &baseParams)
	mergedMasters := map[string]string{}
	_ = json.Unmarshal([]byte(baseParams["masters"]), &mergedMasters)
	newMasters := map[string]string{}
	_ = json.Unmarshal([]byte(ctx.Params["masters"]), &newMasters)
	for k, v := range newMasters {
		if strings.TrimSpace(v) != "" {
			mergedMasters[k] = v
		}
	}
	if len(mergedMasters) > 0 {
		if b, err := json.Marshal(mergedMasters); err == nil {
			ctx.Params["masters"] = string(b)
		}
	}
	return validateMasters(ctx.Params, ctx.HostIDs, ctx.MasterID, ctx.LookupHost)
}

// validateRemoveFlow 按组件卸载：校验卸载集合，写回剩余组件清单。
func validateRemoveFlow(ctx stackkit.ValidateCtx) error {
	inst := ctx.Instance
	installed := parseJSONList(inst.ComponentsJSON)
	if len(installed) == 0 {
		base := map[string]string{}
		_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
		installed = ParseComponentsCSV(base["components"])
	}
	removing := ParseComponentsCSV(ctx.ReqComponents)
	base := map[string]string{}
	_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
	instHA := strings.EqualFold(strings.TrimSpace(base["ha"]), "true")
	if err := validateRemove(removing, installed, instHA); err != nil {
		return err
	}
	ctx.Params["remove_components"] = strings.Join(removing, ",")
	ctx.Params["components"] = strings.Join(stackkit.SubtractList(installed, removing), ",")
	return nil
}

// validateMasters 校验角色规划参数（JSON：组件→主角色主机 IP）：
// 组件/角色 key 须在白名单内，IP 须在本次所选主机内；空值跳过（回落主节点）。
// §4.3：ha=true 时追加 ZK 必勾、≥3 台、同组件主从互斥、Hive 强制 metastore_db。
func validateMasters(params map[string]string, ids []int64, masterID int64, lookup func(int64) (model.StackRunHost, bool)) error {
	raw := strings.TrimSpace(params["masters"])
	m := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return fmt.Errorf("角色规划参数格式错误: %w", err)
		}
	}
	allowed := map[string]bool{}
	for _, k := range RoleKeys() {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("角色规划包含未知项: %s", k)
		}
	}
	// 解析所选主机 IP 集合与合成角色落点（与引擎一致：主节点首先生成）
	valid := map[string]bool{}
	var hosts []model.StackRunHost
	roleParams, _ := json.Marshal(map[string]string{"ha": params["ha"], "masters": params["masters"]})
	for i, id := range ids {
		hh, ok := lookup(id)
		if !ok {
			return fmt.Errorf("主机 %d 不存在", id)
		}
		valid[hh.HostIP] = true
		role := "node"
		if (masterID == 0 && i == 0) || (masterID != 0 && id == masterID) {
			role = "master"
		}
		hosts = append(hosts, model.StackRunHost{HostIP: hh.HostIP, Role: role, Seq: i + 1, ParamsJSON: string(roleParams)})
	}
	// 每个角色值（含逗号列表：hdfs_jns）的 IP 归属校验
	checkIP := func(ip string) error {
		if strings.TrimSpace(ip) == "" {
			return nil
		}
		for _, p := range strings.Split(ip, ",") {
			if p = strings.TrimSpace(p); p != "" && !valid[p] {
				return fmt.Errorf("角色规划的 IP %s 不在本次所选主机中", p)
			}
		}
		return nil
	}
	for k, v := range m {
		if err := checkIP(v); err != nil {
			return fmt.Errorf("参数 %s: %w", k, err)
		}
	}

	ha := strings.EqualFold(strings.TrimSpace(params["ha"]), "true")
	if !ha {
		return nil
	}
	comps := ParseComponentsCSV(params["components"])
	if !containsString(comps, "zookeeper") {
		return fmt.Errorf("HA 模式依赖 ZooKeeper，请勾选 ZooKeeper")
	}
	if len(hosts) < 3 {
		return fmt.Errorf("HA 模式至少需要 3 台主机")
	}
	if containsString(comps, "hive") && !containsString(comps, "metastore_db") {
		return fmt.Errorf("HA 模式开启 Hive 需一并选择 metastore_db 组件（外部元数据库）")
	}
	// 同组件主从角色互斥（覆盖自动分配与显式指定两种来源）
	r := ResolveRole(hosts)
	if r.Nn1 != "" && r.Nn2 != "" && r.Nn1 == r.Nn2 {
		return fmt.Errorf("HA 模式 HDFS NameNode 主备不能在同一台主机")
	}
	if r.Rm1 != "" && r.Rm2 != "" && r.Rm1 == r.Rm2 {
		return fmt.Errorf("HA 模式 YARN ResourceManager 主备不能在同一台主机")
	}
	if p := masterIPOf(hosts); p != "" {
		if r.SparkM2 != "" && p == r.SparkM2 {
			return fmt.Errorf("HA 模式 Spark Master 主备不能在同一台主机")
		}
		if r.FlinkJm2 != "" && p == r.FlinkJm2 {
			return fmt.Errorf("HA 模式 Flink JobManager 主备不能在同一台主机")
		}
		if r.HMaster2 != "" && p == r.HMaster2 {
			return fmt.Errorf("HA 模式 HBase HMaster 主备不能在同一台主机")
		}
	}
	if len(r.MSs) >= 2 && r.MSs[0] != "" && r.MSs[0] == r.MSs[1] {
		return fmt.Errorf("HA 模式 Hive Metastore 双实例不能在同一台主机")
	}
	return nil
}

// masterIPOf 返回主机列表中的主节点 IP（未指定主节点时回退首台）。
func masterIPOf(hosts []model.StackRunHost) string {
	for _, h := range hosts {
		if h.Role == "master" {
			return h.HostIP
		}
	}
	if len(hosts) > 0 {
		return hosts[0].HostIP
	}
	return ""
}

// validateDeps 校验组件依赖关系。comps 必须是「组件全集」——
// 单看某一次增量（如加装）会漏掉集群里已存在的依赖方，从而误报缺依赖。
func validateDeps(comps []string) error {
	// 组件依赖：HBase 依赖 ZooKeeper；Trino 依赖 Hive Metastore
	if containsString(comps, "hbase") && !containsString(comps, "zookeeper") {
		return fmt.Errorf("HBase 依赖 ZooKeeper，请同时勾选 ZooKeeper")
	}
	if containsString(comps, "trino") && !containsString(comps, "hive") {
		return fmt.Errorf("Trino 依赖 Hive Metastore，请同时勾选 Hive")
	}
	return nil
}

func validateComponents(csv string, requireHDFS bool) error {
	comps := ParseComponentsCSV(csv)
	if len(comps) == 0 {
		return fmt.Errorf("请选择部署组件")
	}
	hasHDFS := false
	for _, c := range comps {
		if !ComponentSet[c] {
			return fmt.Errorf("不支持的组件: %s", c)
		}
		if c == "hdfs" {
			hasHDFS = true
		}
	}
	if requireHDFS && !hasHDFS {
		return fmt.Errorf("HDFS 为必选组件（大数据底座的存储基座）")
	}
	return validateDeps(comps)
}

func validateAdd(added, installed []string) error {
	if len(added) == 0 {
		return fmt.Errorf("请选择要加装的组件")
	}
	for _, c := range added {
		if !ComponentSet[c] {
			return fmt.Errorf("不支持的组件: %s", c)
		}
		if c == "hdfs" {
			return fmt.Errorf("HDFS 已作为底座存在，无需加装")
		}
		if containsString(installed, c) {
			return fmt.Errorf("组件 %s 已安装", c)
		}
	}
	// 依赖按「已安装 ∪ 本次加装」的并集判定：ZooKeeper/Hive 等基座通常已在集群中，
	// 只有增量集合的话会把「加装 Trino」这类合法请求误判为缺依赖。
	return validateDeps(stackkit.MergeList(installed, added))
}

func validateRemove(removing, installed []string, ha bool) error {
	if len(removing) == 0 {
		return fmt.Errorf("请选择要卸载的组件")
	}
	for _, c := range removing {
		if c == "hdfs" {
			return fmt.Errorf("不能单独卸载 HDFS，请使用卸载集群")
		}
		if !containsString(installed, c) {
			return fmt.Errorf("组件 %s 未安装", c)
		}
	}
	remain := stackkit.SubtractList(installed, removing)
	if containsString(remain, "hbase") && !containsString(remain, "zookeeper") {
		return fmt.Errorf("HBase 依赖 ZooKeeper，请先卸载 HBase 或保留 ZooKeeper")
	}
	if containsString(remain, "trino") && !containsString(remain, "hive") {
		return fmt.Errorf("Trino 依赖 Hive，请先卸载 Trino 或保留 Hive")
	}
	// §4.3-6：HA 模式下 ZooKeeper / metastore_db 是各组件 HA 的基座，需先卸载依赖组件
	if ha {
		if containsString(removing, "zookeeper") {
			zkDeps := stackkit.IntersectList(remain, []string{"yarn", "spark", "flink", "hbase", "hive"})
			if len(zkDeps) > 0 {
				return fmt.Errorf("HA 模式下 ZooKeeper 是 %s 选主基座，请先卸载这些组件或整体重装为非 HA", strings.Join(zkDeps, "/"))
			}
		}
		if containsString(removing, "metastore_db") {
			if containsString(remain, "hive") {
				return fmt.Errorf("HA 模式 Hive 依赖 metastore_db 元数据库，请先卸载 Hive")
			}
		}
	}
	return nil
}

// parseJSONList 解析 JSON 字符串数组；空/`[]`/损坏均返回 nil（与既有实现一致）。
func parseJSONList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out
	}
	return nil
}
