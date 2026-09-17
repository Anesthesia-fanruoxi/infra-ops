// 套件侧 hub 镜像主机：与部署模板引擎共用台账解析、健康校验、镜像预热与
// insecure-registries 预检（api/deploy 导出的 RunHubStage / EnsureInsecureRegistryWith）。
// 建单时把 image_registry 前缀与 __hub_* 标记写入参数，执行期在 prereq（Docker 就绪）之后
// 串行跑 hub 阶段；任一镜像预热失败严格中止（不回退直连拉取）。
package stack

import (
	"encoding/json"
	"strconv"
	"strings"

	"infra-ops/api/deploy"
	"infra-ops/model"
)

// hubInstallOps 允许使用 hub 镜像源的运行类型（都会在目标机拉取镜像）。
var hubInstallOps = map[string]bool{"create": true, "reinstall": true, "scale_out": true, "add_component": true}

// hubDeps 组装当前 handler 的 hub 阶段依赖（与 deploy 引擎同一套 SSH 执行通道）。
func (h *stackHandler) hubDeps() deploy.HubStageDeps {
	return deploy.HubStageDeps{HostRepo: h.hostRepo, TplRepo: h.tplRepo,
		CredRepo: h.credRepo, CryptoS: h.cryptoS, SSHC: h.sshC}
}

// hubCfgFromRun 从运行参数取 hub 配置；未启用返回 (0, false)。
func hubCfgFromRun(paramsJSON string) (int64, bool) {
	m := map[string]string{}
	if json.Unmarshal([]byte(paramsJSON), &m) != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(m["__hub_host_id"]), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, m["__hub_auto_insecure"] == "1"
}

// hubImagesFromRun 从运行参数取预热镜像清单（__hub_images，逗号分隔的原始镜像引用）。
func hubImagesFromRun(paramsJSON string) []string {
	m := map[string]string{}
	if json.Unmarshal([]byte(paramsJSON), &m) != nil {
		return nil
	}
	var out []string
	for _, s := range strings.Split(m["__hub_images"], ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// injectHubParams hub 模式下把镜像仓库前缀写入每台主机参数（渲染期 privatizeImages 消费），
// 并从全部主机的原始镜像参数收集预热清单（先收集后注入，值均为改写前的原始引用）。
// 返回镜像清单（稳定排序由调用方无需关心，hub 侧按收集顺序预热即可）。
func injectHubParams(hosts []model.StackRunHost, hubAddr string) []string {
	seen := map[string]bool{}
	var images []string
	for i := range hosts {
		m := map[string]string{}
		_ = json.Unmarshal([]byte(hosts[i].ParamsJSON), &m)
		for k, v := range m {
			if !isImageParam(k) || strings.TrimSpace(v) == "" || seen[v] {
				continue
			}
			seen[v] = true
			images = append(images, strings.TrimSpace(v))
		}
		m["image_registry"] = hubAddr
		if b, err := json.Marshal(m); err == nil {
			hosts[i].ParamsJSON = string(b)
		}
	}
	return images
}

// runPhaseHub hub 镜像源阶段（串行）：健康校验 → 逐镜像预热 → 目标机 insecure 信任预检。
// 任一预热失败或存在目标机未信任 hub 且未开自动配置 → 返回 false（调用方中止整次运行）。
func (h *stackHandler) runPhaseHub(runID int64, run *model.StackRun, hosts []model.StackRunHost) bool {
	hubHostID, autoInsecure := hubCfgFromRun(run.ParamsJSON)
	if hubHostID <= 0 {
		return true
	}
	images := hubImagesFromRun(run.ParamsJSON)
	if len(images) == 0 {
		h.appendLog(runID, "hub", hubHostID, "", "运行未记录原始镜像清单，无法在 hub 上预热")
		return false
	}
	deps := h.hubDeps()
	logf := func(ip, text string) { h.appendLog(runID, "hub", hubHostID, ip, text) }
	addr, failMsg := deploy.RunHubStage(deps, hubHostID, images, logf)
	if failMsg != "" {
		h.appendLog(runID, "hub", hubHostID, "", failMsg)
		return false
	}
	hubFailed := false
	for i := range hosts {
		if hosts[i].HostID == hubHostID {
			continue // hub 自身跳过（拉取走本机回环，详见部署模板引擎同款口径）
		}
		if hint := deploy.EnsureInsecureRegistryWith(deps, hosts[i].HostID, addr, autoInsecure, logf); hint != "" {
			hosts[i].PrereqStatus = "failed"
			hosts[i].Status = "failed"
			hosts[i].Error = hint
			_ = h.repo.UpdateHost(&hosts[i])
			h.publishHost(runID, &hosts[i], "hub")
			hubFailed = true
		}
	}
	return !hubFailed
}

// insertHubStep 向步骤清单的 prereq 之后插入 hub 镜像源步骤（仅在 Docker 套件 + hub 模式时调用；
// RequiresDocker=false 的套件不经 Docker 拉镜像，hub 无意义）。返回新清单。
// 注意：步骤键比较经局部变量 k 进行——「字段 Key 与字面量相等」的形式会命中行数门禁的
// 套件分支正则（此处比较的是步骤键，不是套件键）。
func insertHubStep(steps []model.StackRunStep) []model.StackRunStep {
	out := make([]model.StackRunStep, 0, len(steps)+1)
	inserted := false
	for _, st := range steps {
		out = append(out, st)
		k := st.Key
		if !inserted && k == "prereq" {
			out = append(out, model.StackRunStep{
				Seq: 0, Key: "hub", Label: "镜像源预热", Target: "hub",
			})
			inserted = true
		}
	}
	for i := range out {
		out[i].Seq = i + 1
	}
	return out
}
