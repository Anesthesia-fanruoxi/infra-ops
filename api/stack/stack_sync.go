package stack

import (
	"encoding/json"
	"strings"

	"infra-ops/api/shared"
	"infra-ops/model"
)

// syncInstanceAfterRun 运行结束后同步集群实例与各主机装配状态。
func (h *stackHandler) syncInstanceAfterRun(run *model.StackRun, status string, hosts []model.StackRunHost) {
	if run == nil || run.InstanceID == 0 {
		return
	}
	inst, err := h.repo.GetInstance(run.InstanceID)
	if err != nil || inst == nil {
		return
	}
	op := strings.TrimSpace(run.Op)
	if op == "" {
		op = "create"
	}
	for i := range hosts {
		host := hosts[i]
		if host.Status != "success" {
			continue
		}
		// 加装组件是集群级原子变更：只有全部成员都成功才落库，
		// 否则会出现"实例已声明组件、部分机器并未部署"的中间态，且无法重试。
		if op == "add_component" && status != "success" {
			continue
		}
		switch op {
		case "scale_in":
			_ = h.repo.MarkInstanceHostRemoved(run.InstanceID, host.HostID)
		case "uninstall":
			_ = h.repo.MarkInstanceHostRemoved(run.InstanceID, host.HostID)
		case "remove_component":
			params := map[string]string{}
			_ = json.Unmarshal([]byte(run.ParamsJSON), &params)
			remain := parseComponentsCSV(params["components"])
			oldComps := remain
			if existing, err := h.repo.InstanceHosts(run.InstanceID, false); err == nil {
				for _, eh := range existing {
					if eh.HostID == host.HostID {
						oldComps = subtractComponents(parseJSONStringList(eh.ComponentsJSON), parseComponentsCSV(params["remove_components"]))
						if len(oldComps) == 0 {
							oldComps = remain
						}
						break
					}
				}
			}
			_ = h.repo.UpsertInstanceHost(&model.StackInstanceHost{
				InstanceID: run.InstanceID, HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
				Role: host.Role, Seq: host.Seq, ParamsJSON: host.ParamsJSON,
				ComponentsJSON: encodeJSONStringList(oldComps), Status: "active",
			})
		default:
			params := map[string]string{}
			_ = json.Unmarshal([]byte(run.ParamsJSON), &params)
			comps := parseJSONStringList(inst.ComponentsJSON)
			if suiteHasComponents(run.StackKey) {
				comps = parseComponentsCSV(params["components"])
			} else if op == "add_component" {
				comps = shared.MergeStringList(comps, run.Mode)
			} else if stackInstallOp(op) && !shared.ContainsString(comps, run.Mode) {
				comps = shared.MergeStringList(comps, run.Mode)
			}
			hostComps := comps
			if existing, err := h.repo.InstanceHosts(run.InstanceID, false); err == nil {
				for _, eh := range existing {
					if eh.HostID == host.HostID {
						hostComps = mergeComponentList(parseJSONStringList(eh.ComponentsJSON), comps)
						break
					}
				}
			}
			_ = h.repo.UpsertInstanceHost(&model.StackInstanceHost{
				InstanceID: run.InstanceID, HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
				Role: host.Role, Seq: host.Seq, ParamsJSON: host.ParamsJSON,
				ComponentsJSON: encodeJSONStringList(hostComps), Status: "active",
			})
			if op == "add_component" || stackInstallOp(op) {
				_ = h.repo.UpdateInstance(run.InstanceID, "", "", "", encodeJSONStringList(comps))
			}
		}
	}
	instStatus := "ready"
	switch {
	case op == "uninstall" && status == "success":
		instStatus = "uninstalled"
	case status == "success":
		instStatus = "ready"
	case op == "add_component":
		// 加装失败/部分成功不影响既有集群可用性：恢复运行前状态，
		// 否则实例会被留在 deploying（失败详情保存在该次运行记录里）
		instStatus = "partial"
		if p := map[string]string{}; json.Unmarshal([]byte(run.ParamsJSON), &p) == nil {
			if prev := strings.TrimSpace(p["prev_instance_status"]); prev != "" && prev != "deploying" {
				instStatus = prev
			}
		}
	case status == "partial":
		instStatus = "partial"
	case stackInstallOp(op):
		instStatus = "failed"
	default:
		instStatus = "partial"
	}
	paramsJSON, compsJSON := "", ""
	if op == "remove_component" {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(run.ParamsJSON), &p)
		compsJSON = encodeJSONStringList(parseComponentsCSV(p["components"]))
		if status == "success" {
			paramsJSON = run.ParamsJSON
		}
	}
	_ = h.repo.UpdateInstance(run.InstanceID, "", instStatus, paramsJSON, compsJSON)
}
