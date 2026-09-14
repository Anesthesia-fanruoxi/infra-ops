// 套件实例重装主机编排与成员计划重放。
package stack

import (
	"encoding/json"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

func (h *stackHandler) buildReinstallHosts(inst *model.StackInstance) ([]model.StackRunHost, error) {
	if inst == nil {
		return nil, fmt.Errorf("集群实例不存在")
	}
	runs, err := h.repo.ListRunsByInstance(inst.ID)
	if err != nil {
		return nil, err
	}
	events := make([]stackMemberPlanEvent, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		rh, herr := h.repo.RunHosts(runs[i].ID)
		if herr != nil {
			return nil, herr
		}
		events = append(events, stackMemberPlanEvent{Op: runs[i].Op, Status: runs[i].Status, Hosts: rh})
	}
	planned := replayStackMemberPlan(events)
	if len(planned) == 0 {
		planned = instanceHostsAsRunHosts(activeInstanceHosts(inst))
	}
	if len(planned) == 0 {
		return nil, fmt.Errorf("没有可重装的主机记录，请删除实例后重新新建")
	}
	d := store.FindBuiltinStack(inst.StackKey)
	var mode *model.StackMode
	if d != nil {
		mode = stackkit.ModeDef(d.Blueprint(), inst.Mode)
	}
	masterID := int64(0)
	for _, p := range planned {
		if p.Role == "master" {
			masterID = p.HostID
			break
		}
	}
	out := make([]model.StackRunHost, 0, len(planned))
	for i, p := range planned {
		hh, herr := h.hostRepo.GetByID(p.HostID)
		if herr != nil || hh == nil {
			return nil, fmt.Errorf("主机 %s (%d) 已不存在，无法重装", p.HostName, p.HostID)
		}
		boot := "skipped"
		if mode != nil && mode.HasBootstrap && ((masterID == 0 && i == 0) || (masterID != 0 && p.HostID == masterID)) {
			boot = "pending"
		}
		out = append(out, model.StackRunHost{
			HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP,
			Role: p.Role, Seq: i + 1, ParamsJSON: p.ParamsJSON,
			PrereqStatus: "pending", NodeStatus: "pending", BootstrapStatus: boot,
		})
	}
	// 套件可指定引导主机（重装时大数据底座的 HDFS 初始化同样落在 NameNode 所在主机）
	applyBootstrapHost(d, mode, out)
	// 合并实例当前参数到每台主机：历史 run 记录可能不含后来新增的参数（如 image_registry 私有镜像仓库前缀），
	// 以实例当前配置为准覆盖，确保重装沿用最新设置；主机级差异参数保持不动。
	if inst != nil && strings.TrimSpace(inst.ParamsJSON) != "" {
		instParams := map[string]string{}
		if json.Unmarshal([]byte(inst.ParamsJSON), &instParams) == nil {
			for i := range out {
				m := map[string]string{}
				_ = json.Unmarshal([]byte(out[i].ParamsJSON), &m)
				if m == nil {
					m = map[string]string{}
				}
				for k, v := range instParams {
					if strings.TrimSpace(k) == "" {
						continue
					}
					m[k] = v
				}
				if b, merr := json.Marshal(m); merr == nil {
					out[i].ParamsJSON = string(b)
				}
			}
		}
	}
	return out, nil
}

type stackMemberPlanEvent struct {
	Op     string
	Status string
	Hosts  []model.StackRunHost
}

// replayStackMemberPlan 按时间重放创建/扩容/缩容，得到当前应安装的成员（忽略卸载）。
func replayStackMemberPlan(events []stackMemberPlanEvent) []model.StackRunHost {
	byID := map[int64]model.StackRunHost{}
	order := []int64{}
	reset := func(hosts []model.StackRunHost) {
		byID = map[int64]model.StackRunHost{}
		order = nil
		for _, host := range hosts {
			byID[host.HostID] = host
			order = append(order, host.HostID)
		}
	}
	removeID := func(id int64) {
		delete(byID, id)
		next := order[:0]
		for _, x := range order {
			if x != id {
				next = append(next, x)
			}
		}
		order = next
	}
	for _, ev := range events {
		op := strings.TrimSpace(ev.Op)
		if op == "" {
			op = "create"
		}
		switch op {
		case "create", "reinstall":
			reset(ev.Hosts)
		case "scale_out":
			for _, host := range ev.Hosts {
				if _, ok := byID[host.HostID]; !ok {
					order = append(order, host.HostID)
				}
				byID[host.HostID] = host
			}
		case "scale_in":
			for _, host := range ev.Hosts {
				if host.Status == "success" {
					removeID(host.HostID)
				}
			}
		}
	}
	out := make([]model.StackRunHost, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func activeInstanceHosts(inst *model.StackInstance) []model.StackInstanceHost {
	out := []model.StackInstanceHost{}
	if inst == nil {
		return out
	}
	for _, h := range inst.Hosts {
		if h.Status == "active" || h.Status == "" {
			out = append(out, h)
		}
	}
	return out
}

func instanceHostsAsRunHosts(hs []model.StackInstanceHost) []model.StackRunHost {
	out := make([]model.StackRunHost, 0, len(hs))
	for _, h := range hs {
		if h.Status != "active" && h.Status != "" {
			continue
		}
		out = append(out, model.StackRunHost{
			HostID: h.HostID, HostName: h.HostName, HostIP: h.HostIP,
			Role: h.Role, Seq: h.Seq, ParamsJSON: h.ParamsJSON,
		})
	}
	return out
}

func instanceMasterIP(inst *model.StackInstance) string {
	for _, h := range activeInstanceHosts(inst) {
		if h.Role == "master" {
			return h.HostIP
		}
	}
	act := activeInstanceHosts(inst)
	if len(act) > 0 {
		return act[0].HostIP
	}
	return ""
}

func instanceMasterHostID(inst *model.StackInstance) int64 {
	for _, h := range activeInstanceHosts(inst) {
		if h.Role == "master" {
			return h.HostID
		}
	}
	return 0
}
