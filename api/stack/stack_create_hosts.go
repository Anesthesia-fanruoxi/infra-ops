package stack

import (
	"encoding/json"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// buildHostSetForOp 按操作类型装配运行主机集合，并完成冷热温缩容保护与实例级参数合并。
// 从 createAndRun 的前置装配段原样抽出：不改变任何业务行为。
func (h *stackHandler) buildHostSetForOp(op string, d stackkit.Driver, mode *model.StackMode,
	inst *model.StackInstance, req stackRunReq, ids []int64, params map[string]string) ([]model.StackRunHost, error) {
	hostParams := req.HostParams
	if hostParams == nil {
		hostParams = map[string]map[string]string{}
	}

	var (
		hosts []model.StackRunHost
		err   error
	)
	switch op {
	case "scale_in":
		hosts, err = h.buildScaleInHosts(inst, ids)
	case "uninstall", "remove_component":
		hosts, err = h.buildUninstallHosts(inst, ids)
	case "scale_out":
		hosts, err = h.buildScaleOutHosts(d, mode, inst, ids, params, hostParams)
	case "add_component":
		hosts, err = h.buildAddComponentHosts(d, mode, inst, ids, req.MasterHostID, params, hostParams)
	case "reinstall":
		hosts, err = h.buildReinstallHosts(inst)
	default:
		hosts, err = h.buildCreateHosts(d, mode, ids, req.MasterHostID, params, hostParams)
	}
	if err != nil {
		return nil, err
	}
	// 缩容装配后的套件专属保护（如 ES 冷热温不允许移除最后一台 master 候选主机）
	if op == "scale_in" {
		if err := suiteScaleInGuard(d, req.Mode, inst, hosts); err != nil {
			return nil, err
		}
	}

	// 本次请求显式传入的实例级参数（如重装时切换内网镜像仓库 image_registry），
	// 合并进每台主机的参数表，使运行期渲染立即生效；主机级差异参数不受影响。
	if len(req.Params) > 0 {
		for i := range hosts {
			m := map[string]string{}
			_ = json.Unmarshal([]byte(hosts[i].ParamsJSON), &m)
			for k, v := range req.Params {
				if strings.TrimSpace(k) == "" {
					continue
				}
				m[k] = v
			}
			if b, merr := json.Marshal(m); merr == nil {
				hosts[i].ParamsJSON = string(b)
			}
		}
	}
	return hosts, nil
}
