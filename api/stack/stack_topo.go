// 套件拓扑/参数校验：拓扑能力分发、参数合并。
//
// B9 后本文件**不含任何套件分支**：套件专属拓扑规则（Redis 三模式）经
// stackkit.TopologyBuilder 由驱动自实现；通用规则（最小主机数 / 必选主节点）留在这里。
package stack

import (
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// validateStackTopology 拓扑校验：实现了 stackkit.TopologyBuilder 的套件由其**全权判定**
// （直接返回，不叠加通用规则，保证「套件不认识模式时仍报自己的文案」这一边界与拆分前一致）；
// 其余套件走通用规则：最小主机数 → 必选主节点。
func validateStackTopology(d stackkit.Driver, mode *model.StackMode, n int, masterID int64, hostIDs []int64, params map[string]string) error {
	if tb, ok := d.(stackkit.TopologyBuilder); ok {
		return tb.ValidateTopology(stackkit.TopoCtx{
			Mode:     mode.Key,
			Hosts:    n,
			Replicas: stackkit.ParseReplicas(params["replicas"]),
			MasterID: masterID,
			HostIDs:  hostIDs,
			Params:   params,
		})
	}
	if n < mode.MinHosts {
		return fmt.Errorf("%s 模式至少需要 %d 台主机", mode.Label, mode.MinHosts)
	}
	if mode.AssignMaster {
		return stackkit.MasterMustBeSelected(masterID, hostIDs)
	}
	return nil
}

func mergeStackParams(bp model.StackBlueprint, mode string, shared, host map[string]string, defaultHome string) (map[string]string, error) {
	merged := map[string]string{}
	apply := func(v model.StackVar) error {
		if len(v.Modes) > 0 && !stackVarInMode(v.Modes, mode) {
			return nil
		}
		val := v.Default
		if v.Name == "home_dir" && defaultHome != "" {
			val = defaultHome
		}
		if shared != nil && strings.TrimSpace(shared[v.Name]) != "" {
			val = shared[v.Name]
		}
		if host != nil && strings.TrimSpace(host[v.Name]) != "" {
			val = host[v.Name]
		}
		if v.Required && strings.TrimSpace(val) == "" {
			return fmt.Errorf("缺少必填参数: %s", v.Label)
		}
		merged[v.Name] = val
		return nil
	}
	for _, v := range bp.SharedVars {
		if err := apply(v); err != nil {
			return nil, err
		}
	}
	for _, v := range bp.HostVars {
		if err := apply(v); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

func stackVarInMode(modes []string, mode string) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}
