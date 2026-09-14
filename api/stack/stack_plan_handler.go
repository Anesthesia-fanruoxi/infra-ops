package stack

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// PlanPreview POST /api/stacks/plan/preview
// dry-run：给定套件+模式+host_ids+参数（bigdata 可含手动角色）→ 返回 RolePlan（不落库），供前端预览。
// 全部套件适用：部署前必须先看到角色落点（docs/角色物化设计.md §二·全套件覆盖）。
func (h *stackHandler) PlanPreview(c *gin.Context) {
	var req struct {
		StackKey     string                       `json:"stack_key"`
		Mode         string                       `json:"mode"`
		HostIDs      []int64                      `json:"host_ids"`
		MasterHostID int64                        `json:"master_host_id"`
		Params       map[string]string            `json:"params"`
		HostParams   map[string]map[string]string `json:"host_params"`
		Masters      map[string]string            `json:"masters"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if len(req.HostIDs) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "请选择主机")
		return
	}
	stackKey := strings.TrimSpace(req.StackKey)
	if stackKey == "" {
		stackKey = "bigdata"
	}
	d := store.FindBuiltinStack(stackKey)
	if d == nil {
		resp.Fail(c, resp.CodeNotFound, "套件不存在: "+stackKey)
		return
	}
	mode := stackkit.ModeDef(d.Blueprint(), req.Mode)
	if mode == nil {
		resp.Fail(c, resp.CodeBadRequest, "未知模式: "+req.Mode)
		return
	}
	params := req.Params
	if params == nil {
		params = map[string]string{}
	}
	// 预览在部署前置（参数填写完成后）触发：走与真实部署一致的完整构建与参数校验。
	hosts, err := h.buildCreateHosts(d, mode, req.HostIDs, req.MasterHostID, params, req.HostParams)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	if suiteSelfPlans(stackKey) {
		if len(req.Masters) > 0 {
			if b, jerr := json.Marshal(req.Masters); jerr == nil {
				params["masters"] = string(b)
			}
		}
		manualKeys := make([]string, 0, len(req.Masters))
		for k, v := range req.Masters {
			if strings.TrimSpace(v) != "" {
				manualKeys = append(manualKeys, k)
			}
		}
		plan, perr := planBigdataRoles(hosts, PlanOptions{Op: "create", Masters: req.Masters, ManualKeys: manualKeys})
		if perr != nil {
			// 冲突（主备同机/IP 越界/HA 前置缺失）在此返回，前端标红拦截
			resp.Fail(c, resp.CodeBadRequest, perr.Error())
			return
		}
		resp.OK(c, plan)
		return
	}
	plan, perr := planGenericRoles(d, hosts, PlanOptions{
		Op: "create", Mode: req.Mode, Params: params, ManualMaster: req.MasterHostID > 0,
	})
	if perr != nil {
		resp.Fail(c, resp.CodeBadRequest, perr.Error())
		return
	}
	resp.OK(c, plan)
}

// GetPlan GET /api/stacks/instances/:id/plan
// 读取当前角色计划；缺失时惰性生成并落库（存量兼容）。
func (h *stackHandler) GetPlan(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	plan, perr := h.loadRolePlan(inst)
	if perr != nil {
		resp.Fail(c, resp.CodeBadRequest, perr.Error())
		return
	}
	resp.OK(c, plan)
}

// ReplanPlan POST /api/stacks/instances/:id/replan
// 对已有实例按当前成员重规划 → 持久化 rev+1（落点角色沿全量投影冻结，来源标注继承）。
func (h *stackHandler) ReplanPlan(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	old, oerr := h.loadRolePlan(inst)
	if oerr != nil {
		resp.Fail(c, resp.CodeBadRequest, oerr.Error())
		return
	}
	all := instanceHostsAsRunHosts(activeInstanceHosts(inst))
	var plan model.RolePlan
	if !suiteSelfPlans(inst.StackKey) {
		d := store.FindBuiltinStack(inst.StackKey)
		if d == nil {
			resp.Fail(c, resp.CodeBadRequest, "套件不存在: "+inst.StackKey)
			return
		}
		p, perr := planGenericRoles(d, all, PlanOptions{
			Op: "replan", Mode: inst.Mode, Params: parseJSONMap(inst.ParamsJSON),
			ManualMaster: anyMasterRoleHost(all),
		})
		if perr != nil {
			resp.Fail(c, resp.CodeBadRequest, perr.Error())
			return
		}
		plan = p
	} else {
		applyPlanToHosts(all, old)
		p, perr := planBigdataRoles(all, PlanOptions{Op: "replan", Masters: old.Masters, ManualKeys: old.ManualKeys})
		if perr != nil {
			resp.Fail(c, resp.CodeBadRequest, perr.Error())
			return
		}
		plan = p
	}
	plan.Rev = old.Rev + 1
	if b := encodeRolePlan(&plan); b != "" {
		_ = h.repo.SetInstanceRolePlan(inst.ID, b)
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.plan.replan", TargetType: "stack_instance", TargetID: inst.ID,
		Detail:   fmt.Sprintf("name=%s rev=%d hosts=%d", inst.Name, plan.Rev, len(all)),
		RemoteIP: c.ClientIP(),
	})
	resp.OK(c, plan)
}
