package bigdata

import (
	"encoding/json"

	"infra-ops/store/stackkit"
)

// vars.go：大数据底座「实例级参数补全」（原 api/stack/stack_verify_masters.go 的兜底路径原样迁入）。
//
// 探活与接入端点生成前，引擎先用已物化角色计划的全量 masters 投影覆盖参数（唯一事实源）；
// **计划缺失时**由本方法按 HA 角色矩阵现场推导，保证与 Plan 逐字节一致（41 类不一致的根治机制）。

// InjectInstanceVars 无计划兜底：HA 实例按 ResolveRole 结果推导权威 masters，就地写入 ctx.Params。
// 非 HA 或实例无在役成员时不动（与拆分前 bigdataRoleMasters 的 ok=false 分支等价）。
func (d *Driver) InjectInstanceVars(ctx stackkit.InstanceVarsCtx) {
	if len(ctx.Hosts) == 0 {
		return
	}
	r := ResolveRole(ctx.Hosts)
	if !r.Ha {
		return
	}
	if b, err := json.Marshal(MastersProjection(r)); err == nil {
		ctx.Params["masters"] = string(b)
	}
}

// ExtraVars 实现 stackkit.VarInjector：在引擎通用集群变量之上**就地增补**大数据底座的主从落点与
// HA 变量（返回 nil 表示不替换整表）。
//
// 存在已物化 Plan 时直接取 plan.Injected（部署渲染的唯一事实源，41 类不一致的根治机制）；
// 否则按旧路径现场推导（bootstrap 等无计划场景的兜底）。
// 原 api/stack/stack_bigdata_render.go 的 mergeBigdataMasterExtra 原样迁入。
func (d *Driver) ExtraVars(ctx stackkit.VarsCtx) map[int64]map[string]string {
	if len(ctx.Extra) == 0 {
		return nil
	}
	if ctx.Plan != nil && len(ctx.Plan.Injected) > 0 {
		for id, extra := range ctx.Extra {
			if extra == nil {
				extra = map[string]string{}
				ctx.Extra[id] = extra
			}
			for k, v := range ctx.Plan.Injected {
				extra[k] = v
			}
		}
		return nil
	}
	hosts := ctx.Hosts
	m := MasterExtra(hosts)
	role := ResolveRole(hosts)
	ha := HAVarTable(role)
	params := map[string]string{}
	if len(hosts) > 0 {
		_ = json.Unmarshal([]byte(hosts[0].ParamsJSON), &params)
	}
	for k, v := range HAXMLBlocks(role, params) {
		ha[k] = v
	}
	for id, extra := range ctx.Extra {
		if extra == nil {
			extra = map[string]string{}
			ctx.Extra[id] = extra
		}
		for k, v := range m {
			extra[k] = v
		}
		for k, v := range ha {
			extra[k] = v
		}
	}
	return nil
}
