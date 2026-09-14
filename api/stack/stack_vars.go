package stack

import (
	"encoding/json"
	"strings"

	"infra-ops/api/deploy"
	"infra-ops/api/shared"
	"infra-ops/model"
	"infra-ops/store/repo"
	"infra-ops/store/stackkit"
)

// privatizeImages 在参数中提供了 image_registry（内网/私有镜像仓库前缀，如 192.168.7.13:5000）时，
// 把 image / image_* / *_image 类镜像参数统一改写为 <前缀>/<仓库>:<标签>，便于离线内网部署；
// 未提供前缀时原样返回，不影响既有行为。
func privatizeImages(params map[string]string) map[string]string {
	reg := strings.TrimSuffix(strings.TrimSpace(params["image_registry"]), "/")
	if reg == "" {
		return params
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = v
	}
	for k, v := range out {
		if !isImageParam(k) {
			continue
		}
		img := strings.TrimSpace(v)
		if img == "" {
			continue
		}
		out[k] = reg + "/" + trimRegistryPrefix(img)
	}
	return out
}

func isImageParam(k string) bool {
	if k == "image_registry" {
		return false
	}
	return k == "image" || strings.HasPrefix(k, "image_") || strings.HasSuffix(k, "_image")
}

// trimRegistryPrefix 去掉镜像地址中已存在的 registry 段（首段含 . 或 : 即视为 registry），
// 保证前缀重复应用时不会叠加成 a:5000/a:5000/img。
func trimRegistryPrefix(img string) string {
	parts := strings.SplitN(img, "/", 2)
	if len(parts) == 2 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":")) {
		return parts[1]
	}
	return img
}

// applyStackVars 依次注入通用主机变量与 extra 提供的集群变量（{{key}} 字面量替换）。
func applyStackVars(script string, seq int, host model.StackRunHost, extra map[string]string) string {
	rec := repo.HostRecord{DeployTaskHost: model.DeployTaskHost{
		HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
	}}
	out := shared.ApplyHostVars(script, seq, rec)
	if len(extra) == 0 {
		return out
	}
	pairs := make([]string, 0, len(extra)*2)
	for k, v := range extra {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(out)
}

// stackInstallOp 判断是否为全新安装/重装类操作。
func stackInstallOp(op string) bool {
	return op == "create" || op == "reinstall"
}

// selectPipelinePhases 按运行类型裁剪套件流水线阶段：
//   - create/reinstall：整机部署，全量执行；
//   - add_component：只执行本次新增组件对应的阶段（Always 阶段恒执行），
//     并跳过 FullOnly 阶段（典型是 reset，否则"加装"会把既有集群数据清空）；
//   - scale_out：新节点入列，执行各组件阶段，但跳过 FullOnly（reset）
//     与 Target=leader 的收尾阶段（bootstrap 必须在元老主节点上跑）。
//
// 返回 nil 表示本次操作不走流水线（交由调用方回落到 node→bootstrap 路径）。
func selectPipelinePhases(d stackkit.Driver, run *model.StackRun, op string) []model.StackPhase {
	all := d.Pipeline(run.Mode)
	if len(all) == 0 {
		return nil
	}
	switch op {
	case "create", "reinstall":
		return all
	case "add_component":
		p := map[string]string{}
		_ = json.Unmarshal([]byte(run.ParamsJSON), &p)
		added := parseComponentsCSV(p["add_components"])
		if len(added) == 0 {
			// 兼容早期未落 add_components 的历史运行：退回"合并后组件 − 已安装组件"的差集由调用方判定为空
			return nil
		}
		want := map[string]bool{}
		for _, c := range added {
			want[strings.TrimSpace(c)] = true
		}
		out := make([]model.StackPhase, 0, len(all))
		for _, ph := range all {
			if ph.FullOnly {
				continue
			}
			if ph.Always || phaseHasComponent(ph, want) {
				out = append(out, ph)
			}
		}
		return out
	case "scale_out":
		out := make([]model.StackPhase, 0, len(all))
		for _, ph := range all {
			if ph.FullOnly || ph.Target == "leader" {
				continue
			}
			out = append(out, ph)
		}
		return out
	}
	return nil
}

// phaseHasComponent 判断阶段是否归属给定组件集合（Component 为逗号分隔的组件名列表）。
func phaseHasComponent(ph model.StackPhase, want map[string]bool) bool {
	for _, c := range strings.Split(ph.Component, ",") {
		c = strings.TrimSpace(c)
		if c != "" && want[c] {
			return true
		}
	}
	return false
}

// stackVarsJSON 生成套件蓝图当前模式下声明的变量列表。
func stackVarsJSON(bp model.StackBlueprint, mode string) json.RawMessage {
	vars := make([]deploy.TplVar, 0, len(bp.SharedVars)+len(bp.HostVars))
	add := func(list []model.StackVar) {
		for _, v := range list {
			if len(v.Modes) > 0 && !stackVarInMode(v.Modes, mode) {
				continue
			}
			vars = append(vars, deploy.TplVar{Name: v.Name, Label: v.Label, Default: v.Default, Required: v.Required})
		}
	}
	add(bp.SharedVars)
	add(bp.HostVars)
	b, _ := json.Marshal(vars)
	return b
}
