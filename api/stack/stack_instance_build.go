// 套件实例主机构建：新建/扩容/缩容/加装/卸载/重装的运行主机编排。
package stack

import (
	"encoding/json"
	"fmt"
	"strconv"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

func (h *stackHandler) buildCreateHosts(d stackkit.Driver, mode *model.StackMode, ids []int64, masterID int64, params map[string]string, hostParams map[string]map[string]string) ([]model.StackRunHost, error) {
	var hosts []model.StackRunHost
	for i, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			return nil, fmt.Errorf("主机 %d 不存在", id)
		}
		hp := hostParams[strconv.FormatInt(id, 10)]
		merged, err := mergeStackParams(d.Blueprint(), mode.Key, params, hp, mode.DefaultHomeDir)
		if err != nil {
			return nil, fmt.Errorf("主机 %s: %w", hh.Name, err)
		}
		role := stackHostRole(d, mode, id, masterID)
		boot := "skipped"
		if mode.HasBootstrap && ((masterID == 0 && i == 0) || (masterID != 0 && id == masterID)) {
			boot = "pending"
		}
		pb, _ := json.Marshal(merged)
		hosts = append(hosts, model.StackRunHost{
			HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP, Role: role, Seq: i + 1,
			ParamsJSON: string(pb), PrereqStatus: "pending", NodeStatus: "pending", BootstrapStatus: boot,
		})
	}
	// 套件可指定引导主机（大数据底座的 HDFS 初始化落在 NameNode 所在主机，可能与主节点分离）
	applyBootstrapHost(d, mode, hosts)
	return hosts, nil
}

func (h *stackHandler) buildScaleOutHosts(d stackkit.Driver, mode *model.StackMode, inst *model.StackInstance, ids []int64, params map[string]string, hostParams map[string]map[string]string) ([]model.StackRunHost, error) {
	active := activeInstanceHosts(inst)
	if len(active) == 0 {
		return nil, fmt.Errorf("集群尚无可用成员，无法扩容")
	}
	exist := map[int64]bool{}
	maxSeq := 0
	for _, h := range active {
		exist[h.HostID] = true
		if h.Seq > maxSeq {
			maxSeq = h.Seq
		}
	}
	var hosts []model.StackRunHost
	for i, id := range ids {
		if exist[id] {
			return nil, fmt.Errorf("主机已在集群中，不能重复扩容")
		}
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			return nil, fmt.Errorf("主机 %d 不存在", id)
		}
		hp := hostParams[strconv.FormatInt(id, 10)]
		merged, err := mergeStackParams(d.Blueprint(), mode.Key, params, hp, mode.DefaultHomeDir)
		if err != nil {
			return nil, fmt.Errorf("主机 %s: %w", hh.Name, err)
		}
		role := "node"
		if mode.AssignMaster {
			role = "worker"
		}
		if rn, ok := d.(stackkit.RoleNamer); ok {
			if r := rn.HostRole(mode.Key, id, 0, mode.AssignMaster); r != "" {
				role = r
			}
		}
		// 声明了扩容收尾脚本的套件：首个新节点承担扩容引导（由 runPhaseScaleOut 选为执行目标）
		boot := "skipped"
		if i == 0 && hasPhase(d.Key(), mode.Key, stackkit.PhaseScaleOut) {
			boot = "pending"
		}
		pb, _ := json.Marshal(merged)
		hosts = append(hosts, model.StackRunHost{
			HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP, Role: role, Seq: maxSeq + i + 1,
			ParamsJSON: string(pb), PrereqStatus: "pending", NodeStatus: "pending", BootstrapStatus: boot,
		})
	}
	return hosts, nil
}

func (h *stackHandler) buildAddComponentHosts(d stackkit.Driver, mode *model.StackMode, inst *model.StackInstance, ids []int64, masterID int64, params map[string]string, hostParams map[string]map[string]string) ([]model.StackRunHost, error) {
	if d.Blueprint().Category != "platform" {
		return nil, fmt.Errorf("仅组合套件支持加装组件")
	}
	if mode.Key != inst.Mode {
		return nil, fmt.Errorf("加装须在原集群模式 %s 上进行", inst.Mode)
	}
	active := activeInstanceHosts(inst)
	if len(active) == 0 {
		return nil, fmt.Errorf("集群尚无可用成员，无法加装")
	}
	exist := map[int64]model.StackInstanceHost{}
	maxSeq := 0
	for _, h := range active {
		exist[h.HostID] = h
		if h.Seq > maxSeq {
			maxSeq = h.Seq
		}
	}
	if masterID == 0 {
		masterID = instanceMasterHostID(inst)
	}
	if mode.AssignMaster {
		if masterID == 0 {
			masterID = ids[0]
		}
	}
	var hosts []model.StackRunHost
	nextSeq := maxSeq
	for _, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			return nil, fmt.Errorf("主机 %d 不存在", id)
		}
		hp := hostParams[strconv.FormatInt(id, 10)]
		merged, err := mergeStackParams(d.Blueprint(), mode.Key, params, hp, mode.DefaultHomeDir)
		if err != nil {
			return nil, fmt.Errorf("主机 %s: %w", hh.Name, err)
		}
		seq := 0
		role := stackHostRole(d, mode, id, masterID)
		if old, ok := exist[id]; ok {
			seq = old.Seq
			if old.Role != "" {
				role = old.Role
			}
		} else {
			nextSeq++
			seq = nextSeq
		}
		pb, _ := json.Marshal(merged)
		hosts = append(hosts, model.StackRunHost{
			HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP, Role: role, Seq: seq,
			ParamsJSON: string(pb), PrereqStatus: "pending", NodeStatus: "pending", BootstrapStatus: "skipped",
		})
	}
	return hosts, nil
}

func (h *stackHandler) buildUninstallHosts(inst *model.StackInstance, ids []int64) ([]model.StackRunHost, error) {
	active := activeInstanceHosts(inst)
	byID := map[int64]model.StackInstanceHost{}
	for _, h := range active {
		byID[h.HostID] = h
	}
	var hosts []model.StackRunHost
	for i, id := range ids {
		old, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("主机 %d 不是当前集群成员", id)
		}
		hosts = append(hosts, model.StackRunHost{
			HostID: old.HostID, HostName: old.HostName, HostIP: old.HostIP,
			Role: old.Role, Seq: i + 1, ParamsJSON: old.ParamsJSON,
			PrereqStatus: "skipped", NodeStatus: "pending", BootstrapStatus: "skipped",
		})
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("没有可卸载的成员")
	}
	return hosts, nil
}

func (h *stackHandler) buildScaleInHosts(inst *model.StackInstance, ids []int64) ([]model.StackRunHost, error) {
	d := store.FindBuiltinStack(inst.StackKey)
	if d == nil {
		return nil, fmt.Errorf("套件不存在")
	}
	active := activeInstanceHosts(inst)
	if err := validateScaleIn(d, inst, active, ids); err != nil {
		return nil, err
	}
	byID := map[int64]model.StackInstanceHost{}
	for _, h := range active {
		byID[h.HostID] = h
	}
	var hosts []model.StackRunHost
	for i, id := range ids {
		old := byID[id]
		hosts = append(hosts, model.StackRunHost{
			HostID: old.HostID, HostName: old.HostName, HostIP: old.HostIP,
			Role: old.Role, Seq: i + 1, ParamsJSON: old.ParamsJSON,
			PrereqStatus: "skipped", NodeStatus: "pending", BootstrapStatus: "skipped",
		})
	}
	return hosts, nil
}
