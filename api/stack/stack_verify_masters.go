package stack

import (
	"encoding/json"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// 这是探活与部署之间唯一的一致性来源：备主落点（含 flink_jm2/hbase_hm2/hive_ms2/hive_hs2b）
// 由引擎按「第一台非主主机」自动分配，而持久化的 masters 里往往只有用户显式指定过的键。
//
// overrideBigdataMasters 在合并主机级参数之后，确定权威 masters 矩阵。
// 优先读已物化角色计划的 plan.Masters（唯一事实源，杜绝 41 类不一致）；
// 无 Plan 时交给套件按自身规则现场推导（stackkit.InstanceVarInjector；大数据底座为 HA 角色矩阵兜底）。
func overrideBigdataMasters(inst *model.StackInstance, params map[string]string) {
	if p := decodeRolePlan(inst.RolePlanJSON); p != nil && len(p.Masters) > 0 {
		if b, err := json.Marshal(p.Masters); err == nil {
			params["masters"] = string(b)
			return
		}
	}
	d, ok := store.FindStackDriver(inst.StackKey).(stackkit.InstanceVarInjector)
	if !ok {
		return
	}
	d.InjectInstanceVars(stackkit.InstanceVarsCtx{
		Instance: inst, Params: params,
		Hosts: instanceHostsAsRunHosts(activeInstanceHosts(inst)),
	})
}
