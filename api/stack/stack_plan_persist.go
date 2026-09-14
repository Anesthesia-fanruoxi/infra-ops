package stack

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"infra-ops/model"
	"infra-ops/store"
)

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
// 全部套件适用：清单式计划套件（大数据底座）走 loadBigdataRolePlan，其余走通用 planner。
func (h *stackHandler) loadRolePlan(inst *model.StackInstance) (*model.RolePlan, error) {
	if inst == nil {
		return nil, fmt.Errorf("实例不存在")
	}
	if p := decodeRolePlan(inst.RolePlanJSON); p != nil {
		return p, nil
	}
	if suiteSelfPlans(inst.StackKey) {
		return h.loadBigdataRolePlan(inst)
	}
	d := store.FindBuiltinStack(inst.StackKey)
	if d == nil {
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
	plan, err := planGenericRoles(d, runHosts, PlanOptions{
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
