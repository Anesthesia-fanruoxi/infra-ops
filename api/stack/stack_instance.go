package stack

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
)

type stackInstancePatchReq struct {
	Name string `json:"name"`
	// 可选：实例级参数局部覆盖（如 image_registry 指向内网私有镜像仓库）
	Params map[string]string `json:"params"`
}

// ListInstances GET /api/stacks/instances
func (h *stackHandler) ListInstances(c *gin.Context) {
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.ListInstances(page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询集群实例失败")
		return
	}
	if items == nil {
		items = []model.StackInstance{}
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

// GetInstance GET /api/stacks/instances/:id
func (h *stackHandler) GetInstance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	if inst.Hosts == nil {
		inst.Hosts = []model.StackInstanceHost{}
	}
	resp.OK(c, inst)
}

// PatchInstance PATCH /api/stacks/instances/:id
func (h *stackHandler) PatchInstance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstance(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	var req stackInstancePatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		// 允许只改参数（如切换内网镜像仓库 image_registry）而不改名称
		name = inst.Name
	}
	paramsJSON := ""
	if len(req.Params) > 0 {
		base := map[string]string{}
		_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
		for k, v := range req.Params {
			if strings.TrimSpace(k) == "" {
				continue
			}
			base[k] = v
		}
		if b, merr := json.Marshal(base); merr == nil {
			paramsJSON = string(b)
		}
	}
	if err := h.repo.UpdateInstance(id, name, "", paramsJSON, ""); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新失败")
		return
	}
	resp.OK(c, gin.H{"id": id, "name": name})
}

// DeleteInstance DELETE /api/stacks/instances/:id
func (h *stackHandler) DeleteInstance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	if inst.Status != "uninstalled" {
		resp.Fail(c, resp.CodeBadRequest, "请先卸载后再删除：卸载会停止服务器上的服务但保留本地配置，删除仅用于清除本地配置与流程记录")
		return
	}
	busy, err := h.repo.InstanceHasRunning(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询失败")
		return
	}
	if busy {
		resp.Fail(c, resp.CodeBadRequest, "该集群有正在执行的流程，无法删除")
		return
	}
	if err := h.repo.DeleteInstance(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除失败")
		return
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.instance.delete", TargetType: "stack_instance", TargetID: id,
		Detail: fmt.Sprintf("name=%s", inst.Name), RemoteIP: c.ClientIP(),
	})
	resp.OK(c, gin.H{"ok": true})
}

// ScaleOut POST /api/stacks/instances/:id/scale-out
func (h *stackHandler) ScaleOut(c *gin.Context) {
	h.runInstanceOp(c, "scale_out")
}

// ScaleIn POST /api/stacks/instances/:id/scale-in
func (h *stackHandler) ScaleIn(c *gin.Context) {
	h.runInstanceOp(c, "scale_in")
}

// AddComponent POST /api/stacks/instances/:id/add-component
func (h *stackHandler) AddComponent(c *gin.Context) {
	h.runInstanceOp(c, "add_component")
}

// Uninstall POST /api/stacks/instances/:id/uninstall
// body 可选 {"purge": true}：卸载时同时清理残留容器与数据/配置目录（用于部署失败后的彻底清理重装）
func (h *stackHandler) Uninstall(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	req := stackRunReq{InstanceID: id, Op: "uninstall"}
	var body struct {
		Purge bool `json:"purge"`
	}
	if err := c.ShouldBindJSON(&body); err == nil && body.Purge {
		req.Params = map[string]string{"purge": "true"}
	}
	for _, host := range activeInstanceHosts(inst) {
		req.HostIDs = append(req.HostIDs, host.HostID)
	}
	runID, err := h.createAndRun(req, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"run_id": runID, "instance_id": id})
}

// Reinstall POST /api/stacks/instances/:id/reinstall
func (h *stackHandler) Reinstall(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	req := stackRunReq{InstanceID: id, Op: "reinstall"}
	// 可选：重装时覆盖实例级参数（如 image_registry 指向内网私有镜像仓库）；
	// 空 body / 空 params 时保持原行为，向后兼容。
	var body struct {
		Params map[string]string `json:"params"`
	}
	if err := c.ShouldBindJSON(&body); err == nil && len(body.Params) > 0 {
		req.Params = body.Params
	}
	runID, err := h.createAndRun(req, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"run_id": runID, "instance_id": id})
}

// RemoveComponent POST /api/stacks/instances/:id/remove-component
func (h *stackHandler) RemoveComponent(c *gin.Context) {
	h.runInstanceOp(c, "remove_component")
}

func (h *stackHandler) runInstanceOp(c *gin.Context, op string) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req stackRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.InstanceID = id
	req.Op = op
	runID, err := h.createAndRun(req, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"run_id": runID, "instance_id": id})
}

// InstanceRuns GET /api/stacks/instances/:id/runs
func (h *stackHandler) InstanceRuns(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstance(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	items, err := h.repo.ListRunsByInstance(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询流程失败")
		return
	}
	if items == nil {
		items = []model.StackRun{}
	}
	resp.OK(c, items)
}
