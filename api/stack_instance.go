package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

type stackInstancePatchReq struct {
	Name string `json:"name"`
	// 可选：实例级参数局部覆盖（如 image_registry 指向内网私有镜像仓库）
	Params map[string]string `json:"params"`
}

// ListInstances GET /api/stacks/instances
func (h *stackHandler) ListInstances(c *gin.Context) {
	page, pageSize := parsePage(c)
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

func (h *stackHandler) buildCreateHosts(bp *store.BuiltinStack, mode *model.StackMode, ids []int64, masterID int64, params map[string]string, hostParams map[string]map[string]string) ([]model.StackRunHost, error) {
	var hosts []model.StackRunHost
	for i, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			return nil, fmt.Errorf("主机 %d 不存在", id)
		}
		hp := hostParams[strconv.FormatInt(id, 10)]
		merged, err := mergeStackParams(bp.Blueprint(), mode.Key, params, hp, mode.DefaultHomeDir)
		if err != nil {
			return nil, fmt.Errorf("主机 %s: %w", hh.Name, err)
		}
		role := stackHostRole(bp, mode, id, masterID)
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
	// bigdata：角色规划下 bootstrap（HDFS 初始化）落在 NameNode 所在主机，可能与主节点分离
	if bp.Key == "bigdata" && mode.HasBootstrap {
		ms := stackBigdataMasters(hosts)
		if nn := strings.TrimSpace(ms["hdfs"]); nn != "" {
			for i := range hosts {
				if hosts[i].HostIP == nn {
					hosts[i].BootstrapStatus = "pending"
				} else if hosts[i].BootstrapStatus == "pending" {
					hosts[i].BootstrapStatus = "skipped"
				}
			}
		}
	}
	return hosts, nil
}

func (h *stackHandler) buildScaleOutHosts(bp *store.BuiltinStack, mode *model.StackMode, inst *model.StackInstance, ids []int64, params map[string]string, hostParams map[string]map[string]string) ([]model.StackRunHost, error) {
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
		merged, err := mergeStackParams(bp.Blueprint(), mode.Key, params, hp, mode.DefaultHomeDir)
		if err != nil {
			return nil, fmt.Errorf("主机 %s: %w", hh.Name, err)
		}
		role := "node"
		if bp.Key == "redis" && (mode.Key == "replication" || mode.Key == "sentinel") {
			role = "replica"
		} else if mode.AssignMaster {
			role = "worker"
		}
		boot := "skipped"
		if bp.Key == "redis" && mode.Key == "cluster" && i == 0 {
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

func (h *stackHandler) buildAddComponentHosts(bp *store.BuiltinStack, mode *model.StackMode, inst *model.StackInstance, ids []int64, masterID int64, params map[string]string, hostParams map[string]map[string]string) ([]model.StackRunHost, error) {
	if bp.Category != "platform" {
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
		merged, err := mergeStackParams(bp.Blueprint(), mode.Key, params, hp, mode.DefaultHomeDir)
		if err != nil {
			return nil, fmt.Errorf("主机 %s: %w", hh.Name, err)
		}
		seq := 0
		role := stackHostRole(bp, mode, id, masterID)
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
	bp := store.FindBuiltinStack(inst.StackKey)
	if bp == nil {
		return nil, fmt.Errorf("套件不存在")
	}
	active := activeInstanceHosts(inst)
	if err := validateScaleIn(bp, inst, active, ids); err != nil {
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

func validateScaleIn(bp *store.BuiltinStack, inst *model.StackInstance, active []model.StackInstanceHost, removeIDs []int64) error {
	if len(removeIDs) == 0 {
		return fmt.Errorf("请选择要移除的主机")
	}
	activeMap := map[int64]model.StackInstanceHost{}
	for _, h := range active {
		activeMap[h.HostID] = h
	}
	removeSet := map[int64]bool{}
	for _, id := range removeIDs {
		if _, ok := activeMap[id]; !ok {
			return fmt.Errorf("主机 %d 不是当前集群成员", id)
		}
		removeSet[id] = true
	}
	remain := len(active) - len(removeIDs)
	if remain < 1 {
		return fmt.Errorf("不能移除全部节点，请使用卸载")
	}
	remainMaster := 0
	for _, h := range active {
		if removeSet[h.HostID] {
			continue
		}
		if h.Role == "master" {
			remainMaster++
		}
	}
	needKeepMaster := (bp.Key == "redis" && (inst.Mode == "replication" || inst.Mode == "sentinel")) ||
		(bp.Key == "rocketmq" && inst.Mode == "broker") ||
		bp.Key == "bigdata"
	if needKeepMaster && remainMaster == 0 {
		return fmt.Errorf("不能移除唯一主节点")
	}
	if bp.Key == "redis" && inst.Mode == "cluster" && remain < 3 {
		return fmt.Errorf("Redis 集群至少保留 3 个节点")
	}
	// 承载落点角色（scope==""）的主机不可缩容：全部套件适用，优先读已物化角色计划
	// （拍板 ②：直接拦截，不提供「先重规划再缩」的出路）；bigdata 无 Plan 时回落现场推导。
	protected, hasProtected := planProtectedHosts(decodeRolePlan(inst.RolePlanJSON))
	if !hasProtected && bp.Key == "bigdata" {
		protected, hasProtected = bigdataProtectedHosts(active)
	}
	if hasProtected {
		for id := range removeSet {
			ip := activeMap[id].HostIP
			if label, ok := protected[ip]; ok {
				return fmt.Errorf("主机 %s（%s）承载落点角色，不可缩容；落点角色主机不在扩缩容范围内", ip, label)
			}
		}
	}
	return nil
}

// bigdataProtectedHosts 返回 bigdata 实例的 HA 保护角色清单（map[ip]=角色标签）。
// 非 HA 实例返回 isHA=false。
func bigdataProtectedHosts(active []model.StackInstanceHost) (map[string]string, bool) {
	runHosts := make([]model.StackRunHost, 0, len(active))
	for _, h := range active {
		if h.Status != "active" && h.Status != "" {
			continue
		}
		runHosts = append(runHosts, model.StackRunHost{
			HostID: h.HostID, HostName: h.HostName, HostIP: h.HostIP,
			Role: h.Role, Seq: h.Seq, ParamsJSON: h.ParamsJSON,
		})
	}
	r := stackBigdataRole(runHosts)
	if !r.Ha {
		return nil, false
	}
	label := map[string]string{}
	add := func(ip, name string) {
		if ip == "" {
			return
		}
		if label[ip] == "" {
			label[ip] = name
		} else {
			label[ip] += "+" + name
		}
	}
	add(r.Nn1, "NN1(Active)")
	add(r.Nn2, "NN2(Standby)")
	for _, jn := range r.JNs {
		add(jn, "JN")
	}
	add(r.Rm1, "RM1")
	add(r.Rm2, "RM2")
	add(r.SparkM1, "SparkM1")
	add(r.SparkM2, "SparkM2")
	add(r.FlinkJM1, "JM1")
	add(r.FlinkJm2, "JM2")
	add(r.HMaster1, "HMaster1")
	add(r.HMaster2, "HMaster2")
	for _, m := range r.MSs {
		add(m, "MS")
	}
	for _, s := range r.Hs2s {
		add(s, "HS2")
	}
	add(r.HiveDB, "MetaDB")
	for _, z := range shared.FilterEmpty(strings.Split(r.ZKIps, ",")) {
		add(z, "ZK")
	}
	return label, true
}

func stackHostRole(bp *store.BuiltinStack, mode *model.StackMode, hostID, masterID int64) string {
	if bp.Key == "redis" && (mode.Key == "replication" || mode.Key == "sentinel") {
		if hostID == masterID {
			return "master"
		}
		return "replica"
	}
	if mode.AssignMaster {
		if hostID == masterID {
			return "master"
		}
		return "worker"
	}
	return "node"
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
	bp := store.FindBuiltinStack(inst.StackKey)
	var mode *model.StackMode
	if bp != nil {
		mode = bp.ModeDef(inst.Mode)
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
	// bigdata：重装时 bootstrap（HDFS 初始化）同样落在 NameNode 所在主机，可能与主节点分离
	if bp != nil && bp.Key == "bigdata" && mode != nil && mode.HasBootstrap {
		ms := stackBigdataMasters(out)
		if nn := strings.TrimSpace(ms["hdfs"]); nn != "" {
			for i := range out {
				if out[i].HostIP == nn {
					out[i].BootstrapStatus = "pending"
				} else if out[i].BootstrapStatus == "pending" {
					out[i].BootstrapStatus = "skipped"
				}
			}
		}
	}
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

func parseJSONStringList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out
	}
	return nil
}

func encodeJSONStringList(list []string) string {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// validateBigdataMasters 校验角色规划参数（JSON：组件→主角色主机 IP）：
// 组件/角色 key 须在白名单内，IP 须在本次所选主机内；空值跳过（回落主节点）。
// §4.3：ha=true 时追加 ZK 必勾、≥3 台、同组件主从互斥、Hive 强制 metastore_db。
func (h *stackHandler) validateBigdataMasters(params map[string]string, ids []int64, masterID int64) error {
	raw := strings.TrimSpace(params["masters"])
	m := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return fmt.Errorf("角色规划参数格式错误: %w", err)
		}
	}
	allowed := map[string]bool{}
	for _, k := range bigdataRoleKeys {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("角色规划包含未知项: %s", k)
		}
	}
	// 解析所选主机 IP 集合与合成角色落点（与引擎一致：主节点首先生成）
	valid := map[string]bool{}
	var hosts []model.StackRunHost
	roleParams, _ := json.Marshal(map[string]string{"ha": params["ha"], "masters": params["masters"]})
	for i, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			return fmt.Errorf("主机 %d 不存在", id)
		}
		valid[hh.IP] = true
		role := "node"
		if (masterID == 0 && i == 0) || (masterID != 0 && id == masterID) {
			role = "master"
		}
		hosts = append(hosts, model.StackRunHost{HostIP: hh.IP, Role: role, Seq: i + 1, ParamsJSON: string(roleParams)})
	}
	// 每个角色值（含逗号列表：hdfs_jns）的 IP 归属校验
	checkIP := func(ip string) error {
		if strings.TrimSpace(ip) == "" {
			return nil
		}
		for _, p := range strings.Split(ip, ",") {
			if p = strings.TrimSpace(p); p != "" && !valid[p] {
				return fmt.Errorf("角色规划的 IP %s 不在本次所选主机中", p)
			}
		}
		return nil
	}
	for k, v := range m {
		if err := checkIP(v); err != nil {
			return fmt.Errorf("参数 %s: %w", k, err)
		}
	}

	ha := strings.EqualFold(strings.TrimSpace(params["ha"]), "true")
	if !ha {
		return nil
	}
	comps := parseComponentsCSV(params["components"])
	if !shared.ContainsString(comps, "zookeeper") {
		return fmt.Errorf("HA 模式依赖 ZooKeeper，请勾选 ZooKeeper")
	}
	if len(hosts) < 3 {
		return fmt.Errorf("HA 模式至少需要 3 台主机")
	}
	if shared.ContainsString(comps, "hive") && !shared.ContainsString(comps, "metastore_db") {
		return fmt.Errorf("HA 模式开启 Hive 需一并选择 metastore_db 组件（外部元数据库）")
	}
	// 同组件主从角色互斥（覆盖自动分配与显式指定两种来源）
	r := stackBigdataRole(hosts)
	if r.Nn1 != "" && r.Nn2 != "" && r.Nn1 == r.Nn2 {
		return fmt.Errorf("HA 模式 HDFS NameNode 主备不能在同一台主机")
	}
	if r.Rm1 != "" && r.Rm2 != "" && r.Rm1 == r.Rm2 {
		return fmt.Errorf("HA 模式 YARN ResourceManager 主备不能在同一台主机")
	}
	if p := masterIPOf(hosts); p != "" {
		if r.SparkM2 != "" && p == r.SparkM2 {
			return fmt.Errorf("HA 模式 Spark Master 主备不能在同一台主机")
		}
		if r.FlinkJm2 != "" && p == r.FlinkJm2 {
			return fmt.Errorf("HA 模式 Flink JobManager 主备不能在同一台主机")
		}
		if r.HMaster2 != "" && p == r.HMaster2 {
			return fmt.Errorf("HA 模式 HBase HMaster 主备不能在同一台主机")
		}
	}
	if len(r.MSs) >= 2 && r.MSs[0] != "" && r.MSs[0] == r.MSs[1] {
		return fmt.Errorf("HA 模式 Hive Metastore 双实例不能在同一台主机")
	}
	return nil
}

// masterIPOf 返回主机列表中的主节点 IP（未指定主节点时回退首台）。
func masterIPOf(hosts []model.StackRunHost) string {
	for _, h := range hosts {
		if h.Role == "master" {
			return h.HostIP
		}
	}
	if len(hosts) > 0 {
		return hosts[0].HostIP
	}
	return ""
}

func parseComponentsCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mergeComponentList(base, add []string) []string {
	out := append([]string{}, base...)
	for _, c := range add {
		if !shared.ContainsString(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// bigdataComponentSet 大数据底座支持的组件白名单。
var bigdataComponentSet = map[string]bool{
	"hdfs": true, "zookeeper": true, "yarn": true, "spark": true, "flink": true,
	"hive": true, "hbase": true, "trino": true, "metastore_db": true,
}

// validateBigdataDeps 校验组件依赖关系。comps 必须是「组件全集」——
// 单看某一次增量（如加装）会漏掉集群里已存在的依赖方，从而误报缺依赖。
func validateBigdataDeps(comps []string) error {
	// 组件依赖：HBase 依赖 ZooKeeper；Trino 依赖 Hive Metastore
	if shared.ContainsString(comps, "hbase") && !shared.ContainsString(comps, "zookeeper") {
		return fmt.Errorf("HBase 依赖 ZooKeeper，请同时勾选 ZooKeeper")
	}
	if shared.ContainsString(comps, "trino") && !shared.ContainsString(comps, "hive") {
		return fmt.Errorf("Trino 依赖 Hive Metastore，请同时勾选 Hive")
	}
	return nil
}

func validateBigdataComponents(csv string, requireHDFS bool) error {
	comps := parseComponentsCSV(csv)
	if len(comps) == 0 {
		return fmt.Errorf("请选择部署组件")
	}
	hasHDFS := false
	for _, c := range comps {
		if !bigdataComponentSet[c] {
			return fmt.Errorf("不支持的组件: %s", c)
		}
		if c == "hdfs" {
			hasHDFS = true
		}
	}
	if requireHDFS && !hasHDFS {
		return fmt.Errorf("HDFS 为必选组件（大数据底座的存储基座）")
	}
	return validateBigdataDeps(comps)
}

func validateBigdataAdd(added, installed []string) error {
	if len(added) == 0 {
		return fmt.Errorf("请选择要加装的组件")
	}
	for _, c := range added {
		if !bigdataComponentSet[c] {
			return fmt.Errorf("不支持的组件: %s", c)
		}
		if c == "hdfs" {
			return fmt.Errorf("HDFS 已作为底座存在，无需加装")
		}
		if shared.ContainsString(installed, c) {
			return fmt.Errorf("组件 %s 已安装", c)
		}
	}
	// 依赖按「已安装 ∪ 本次加装」的并集判定：ZooKeeper/Hive 等基座通常已在集群中，
	// 只有增量集合的话会把「加装 Trino」这类合法请求误判为缺依赖。
	return validateBigdataDeps(mergeComponentList(installed, added))
}

func validateBigdataRemove(removing, installed []string, ha bool) error {
	if len(removing) == 0 {
		return fmt.Errorf("请选择要卸载的组件")
	}
	for _, c := range removing {
		if c == "hdfs" {
			return fmt.Errorf("不能单独卸载 HDFS，请使用卸载集群")
		}
		if !shared.ContainsString(installed, c) {
			return fmt.Errorf("组件 %s 未安装", c)
		}
	}
	remain := subtractComponents(installed, removing)
	if shared.ContainsString(remain, "hbase") && !shared.ContainsString(remain, "zookeeper") {
		return fmt.Errorf("HBase 依赖 ZooKeeper，请先卸载 HBase 或保留 ZooKeeper")
	}
	if shared.ContainsString(remain, "trino") && !shared.ContainsString(remain, "hive") {
		return fmt.Errorf("Trino 依赖 Hive，请先卸载 Trino 或保留 Hive")
	}
	// §4.3-6：HA 模式下 ZooKeeper / metastore_db 是各组件 HA 的基座，需先卸载依赖组件
	if ha {
		if shared.ContainsString(removing, "zookeeper") {
			zkDeps := intersectComponents(remain, []string{"yarn", "spark", "flink", "hbase", "hive"})
			if len(zkDeps) > 0 {
				return fmt.Errorf("HA 模式下 ZooKeeper 是 %s 选主基座，请先卸载这些组件或整体重装为非 HA", strings.Join(zkDeps, "/"))
			}
		}
		if shared.ContainsString(removing, "metastore_db") {
			if shared.ContainsString(remain, "hive") {
				return fmt.Errorf("HA 模式 Hive 依赖 metastore_db 元数据库，请先卸载 Hive")
			}
		}
	}
	return nil
}

func intersectComponents(a, b []string) []string {
	out := []string{}
	for _, x := range a {
		if shared.ContainsString(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func subtractComponents(from, remove []string) []string {
	out := make([]string, 0, len(from))
	for _, c := range from {
		if !shared.ContainsString(remove, c) {
			out = append(out, c)
		}
	}
	return out
}
