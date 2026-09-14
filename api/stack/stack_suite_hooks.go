// 套件能力分发的引擎侧薄封装：把「按能力接口分流」的样板从业务文件里抽出来，
// 让引擎各阶段只留一句调用（设计文档 §7：引擎零套件分支）。
package stack

import (
	"strings"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// suiteSelfPlans 报告套件的角色计划是否为**清单式**（自建主机行与 Injected/Masters 投影，
// 需落点冻结语义）。目前仅大数据底座；引擎据此分流，不再判套件 key。
func suiteSelfPlans(key string) bool {
	if sp, ok := store.FindStackDriver(key).(stackkit.SelfPlanning); ok {
		return sp.SelfPlans()
	}
	return false
}

// suiteHasComponents 报告套件是否以「组件清单」为部署单元（蓝图声明了 components 参数）。
// 组件型套件的实例组件列表取自运行参数 components；非组件型套件按模式/组件集合推导。
func suiteHasComponents(key string) bool {
	d := store.FindStackDriver(key)
	return d != nil && stackkit.DeclaresVar(d.Blueprint(), "components")
}

// suiteScaleInGuard 缩容装配后的套件专属保护（如冷热温不允许移除最后一台 master 候选主机）。
// 在写库前拦截，调用位置与拆分前一致。
func suiteScaleInGuard(d stackkit.Driver, mode string, inst *model.StackInstance, hosts []model.StackRunHost) error {
	if d == nil || inst == nil {
		return nil
	}
	g, ok := d.(stackkit.ScaleInGuard)
	if !ok {
		return nil
	}
	return g.ScaleInGuard(stackkit.ScaleCtx{Mode: mode, Instance: inst, RemoveIDs: runHostIDs(hosts)})
}

// hasPhase 报告套件某模式是否声明了某阶段脚本。
// 引擎据此做声明式分流（如「声明了扩容脚本才跑扩容收尾」），不再判套件 key。
func hasPhase(key, mode, phase string) bool {
	d := store.FindStackDriver(key)
	if d == nil {
		return false
	}
	f, ok := d.Phase(mode, phase)
	return ok && f.Path != ""
}

// runHostIDs 抽取运行主机的 HostID 列表（保持原顺序）。
func runHostIDs(hosts []model.StackRunHost) []int64 {
	out := make([]int64, 0, len(hosts))
	for i := range hosts {
		out = append(out, hosts[i].HostID)
	}
	return out
}

// applyBootstrapHost 让套件覆盖「哪台主机承担引导（bootstrap）」。
//
// 引擎通用规则已在装配时把主节点标为 pending；套件实现 stackkit.BootstrapHostResolver 时以
// 其返回的 IP 为准（例如大数据底座的 HDFS 初始化落在 NameNode 所在主机，可能与主节点分离），
// 命中主机改 pending、其余原 pending 改 skipped；返回空串表示沿用通用规则。
func applyBootstrapHost(d stackkit.Driver, mode *model.StackMode, hosts []model.StackRunHost) {
	if d == nil || mode == nil || !mode.HasBootstrap {
		return
	}
	r, ok := d.(stackkit.BootstrapHostResolver)
	if !ok {
		return
	}
	ip := strings.TrimSpace(r.BootstrapHostIP(hosts))
	if ip == "" {
		return
	}
	for i := range hosts {
		if hosts[i].HostIP == ip {
			hosts[i].BootstrapStatus = "pending"
		} else if hosts[i].BootstrapStatus == "pending" {
			hosts[i].BootstrapStatus = "skipped"
		}
	}
}

// hostLookup 按 ID 解析主机（供 stackkit.ValidateCtx.LookupHost 惰性回调使用）：
// 只在套件真正需要主机 IP 时触发查库，保持与拆分前一致的报错时机与文案。
func (h *stackHandler) hostLookup(id int64) (model.StackRunHost, bool) {
	hh, err := h.hostRepo.GetByID(id)
	if err != nil || hh == nil {
		return model.StackRunHost{}, false
	}
	return model.StackRunHost{HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP}, true
}

// runClusterExtra 计算本次运行的 per-host 集群变量注入表：
// 引擎先算通用表（种子/主节点/角色），再给套件一次增补或整体替换的机会
//   - 增补（返回 nil）：大数据底座在通用变量之上叠加主从落点与 HA XML 块；
//   - 替换（返回非 nil）：冷热温以每台主机勾选的 roles 为权威重算整表。
//
// 清单式计划套件优先使用已物化角色计划（docs/角色物化设计.md）：扩容/加装时运行主机只是成员
// 子集，现场推导会算错落点，Plan.Injected 是部署渲染的唯一事实源。
func (h *stackHandler) runClusterExtra(d stackkit.Driver, run *model.StackRun, op string,
	existing, hosts []model.StackRunHost) map[int64]map[string]string {
	plan := h.runRolePlan(d, run)
	extraAll := stackkit.ClusterExtraMerged(op, existing, hosts)
	if vi, ok := d.(stackkit.VarInjector); ok {
		if out := vi.ExtraVars(stackkit.VarsCtx{
			Mode: run.Mode, Op: op, Existing: existing, Hosts: hosts, Plan: plan, Extra: extraAll,
		}); out != nil {
			extraAll = out
		}
	}
	return extraAll
}

// runRolePlan 清单式计划套件的运行期角色计划（其他套件返回 nil，不产生库读开销）。
func (h *stackHandler) runRolePlan(d stackkit.Driver, run *model.StackRun) *model.RolePlan {
	if d == nil || run.InstanceID <= 0 || !suiteSelfPlans(d.Key()) {
		return nil
	}
	inst, ierr := h.repo.GetInstance(run.InstanceID)
	if ierr != nil || inst == nil {
		return nil
	}
	p, perr := h.loadRolePlan(inst)
	if perr != nil {
		return nil
	}
	return p
}
