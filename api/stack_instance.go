package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

type stackInstancePatchReq struct {
	Name string `json:"name"`
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
		resp.Fail(c, resp.CodeBadRequest, "名称不能为空")
		return
	}
	if err := h.repo.UpdateInstance(id, name, "", "", ""); err != nil {
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
func (h *stackHandler) Uninstall(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	req := stackRunReq{InstanceID: id, Op: "uninstall"}
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
	return nil
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

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
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
		if !containsString(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func validateBigdataComponents(csv string, requireHDFS bool) error {
	comps := parseComponentsCSV(csv)
	if len(comps) == 0 {
		return fmt.Errorf("请选择部署组件")
	}
	valid := map[string]bool{"hdfs": true, "zookeeper": true, "yarn": true, "spark": true, "flink": true, "hive": true, "hbase": true, "trino": true}
	hasHDFS := false
	for _, c := range comps {
		if !valid[c] {
			return fmt.Errorf("不支持的组件: %s", c)
		}
		if c == "hdfs" {
			hasHDFS = true
		}
	}
	if requireHDFS && !hasHDFS {
		return fmt.Errorf("HDFS 为必选组件（大数据底座的存储基座）")
	}
	// 组件依赖：HBase 依赖 ZooKeeper；Trino 依赖 Hive Metastore
	if containsString(comps, "hbase") && !containsString(comps, "zookeeper") {
		return fmt.Errorf("HBase 依赖 ZooKeeper，请同时勾选 ZooKeeper")
	}
	if containsString(comps, "trino") && !containsString(comps, "hive") {
		return fmt.Errorf("Trino 依赖 Hive Metastore，请同时勾选 Hive")
	}
	return nil
}

func validateBigdataAdd(added, installed []string) error {
	if len(added) == 0 {
		return fmt.Errorf("请选择要加装的组件")
	}
	if err := validateBigdataComponents(strings.Join(added, ","), false); err != nil {
		return err
	}
	for _, c := range added {
		if c == "hdfs" {
			return fmt.Errorf("HDFS 已作为底座存在，无需加装")
		}
		if containsString(installed, c) {
			return fmt.Errorf("组件 %s 已安装", c)
		}
	}
	return nil
}

func validateBigdataRemove(removing, installed []string) error {
	if len(removing) == 0 {
		return fmt.Errorf("请选择要卸载的组件")
	}
	for _, c := range removing {
		if c == "hdfs" {
			return fmt.Errorf("不能单独卸载 HDFS，请使用卸载集群")
		}
		if !containsString(installed, c) {
			return fmt.Errorf("组件 %s 未安装", c)
		}
	}
	remain := subtractComponents(installed, removing)
	if containsString(remain, "hbase") && !containsString(remain, "zookeeper") {
		return fmt.Errorf("HBase 依赖 ZooKeeper，请先卸载 HBase 或保留 ZooKeeper")
	}
	if containsString(remain, "trino") && !containsString(remain, "hive") {
		return fmt.Errorf("Trino 依赖 Hive，请先卸载 Trino 或保留 Hive")
	}
	return nil
}

func subtractComponents(from, remove []string) []string {
	out := make([]string, 0, len(from))
	for _, c := range from {
		if !containsString(remove, c) {
			out = append(out, c)
		}
	}
	return out
}

func mergeStringList(base []string, add string) []string {
	if containsString(base, add) {
		return base
	}
	return append(append([]string{}, base...), add)
}
