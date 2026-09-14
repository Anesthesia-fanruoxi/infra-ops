// 套件实例校验：缩容保护（通用落点保护 + 套件专属 ScaleGuard）、主机角色判定。
package stack

import (
	"fmt"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

func validateScaleIn(d stackkit.Driver, inst *model.StackInstance, active []model.StackInstanceHost, removeIDs []int64) error {
	if len(removeIDs) == 0 {
		return fmt.Errorf("请选择要移除的主机")
	}
	activeMap := map[int64]model.StackInstanceHost{}
	for _, h := range active {
		activeMap[h.HostID] = h
	}
	removeSet := map[int64]bool{}
	for _, id := range removeIDs {
		if _, ok := activeMap[id]; !ok {
			return fmt.Errorf("主机 %d 不是当前集群成员", id)
		}
		removeSet[id] = true
	}
	remain := len(active) - len(removeIDs)
	if remain < 1 {
		return fmt.Errorf("不能移除全部节点，请使用卸载")
	}
	// 套件专属保护：不得移除唯一主节点 / 集群节点下限 / 无计划时的落点兜底
	if g, ok := d.(stackkit.ScaleGuard); ok {
		if err := g.ScaleGuard(stackkit.ScaleCtx{
			Mode: inst.Mode, Instance: inst, Active: active, RemoveIDs: removeIDs, Remain: remain,
		}); err != nil {
			return err
		}
	}
	// 承载落点角色（scope==""）的主机不可缩容：全部套件适用，优先读已物化角色计划
	// （拍板 ②：直接拦截，不提供「先重规划再缩」的出路）。
	protected, hasProtected := planProtectedHosts(decodeRolePlan(inst.RolePlanJSON))
	if !hasProtected {
		return nil
	}
	for id := range removeSet {
		ip := activeMap[id].HostIP
		if label, ok := protected[ip]; ok {
			return fmt.Errorf("主机 %s（%s）承载落点角色，不可缩容；落点角色主机不在扩缩容范围内", ip, label)
		}
	}
	return nil
}

// stackHostRole 运行期主机角色命名：先给套件一次改名机会（RoleNamer，如 redis 主从的 replica），
// 否则按「AssignMaster → master/worker，未分配 → node」通用规则。
func stackHostRole(d stackkit.Driver, mode *model.StackMode, hostID, masterID int64) string {
	isMaster := hostID == masterID
	if n, ok := d.(stackkit.RoleNamer); ok {
		if r := n.HostRole(mode.Key, hostID, masterID, mode.AssignMaster); r != "" {
			return r
		}
	}
	if mode.AssignMaster {
		if isMaster {
			return "master"
		}
		return "worker"
	}
	return "node"
}
