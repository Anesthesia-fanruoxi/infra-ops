// 套件部署 API：蓝图列表、Docker 预检、创建运行。
package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/repo"
)

type stackHandler struct {
	repo      *repo.StackRepo
	tplRepo   *repo.DeployRepo
	hostRepo  *repo.HostRepo
	credRepo  *repo.CredentialRepo
	cryptoS   *icrypto.Service
	sshC      *sshx.Client
	bus       *eventbus.Bus
	auditRepo *repo.AuditRepo
	conc      int
}

func NewStackHandler(repo *repo.StackRepo, tplRepo *repo.DeployRepo, hostRepo *repo.HostRepo,
	credRepo *repo.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *repo.AuditRepo, concurrency int) *stackHandler {
	return &stackHandler{repo: repo, tplRepo: tplRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo, conc: concurrency}
}

type stackPreflightReq struct {
	HostIDs []int64 `json:"host_ids" binding:"required,min=1"`
}

type stackPreflightHost struct {
	HostID int64  `json:"host_id"`
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Docker string `json:"docker"` // present / missing / unreachable
	Error  string `json:"error,omitempty"`
}

type stackRunReq struct {
	StackKey     string                       `json:"stack_key"`
	Mode         string                       `json:"mode"`
	Name         string                       `json:"name"`
	Op           string                       `json:"op"`
	InstanceID   int64                        `json:"instance_id"`
	HostIDs      []int64                      `json:"host_ids"`
	MasterHostID int64                        `json:"master_host_id"`
	Params       map[string]string            `json:"params"`
	HostParams   map[string]map[string]string `json:"host_params"`
}

// List GET /api/stacks
func (h *stackHandler) List(c *gin.Context) {
	resp.OK(c, store.ListStackBlueprints())
}

// Preflight POST /api/stacks/preflight：探测目标机是否已有 Docker。
func (h *stackHandler) Preflight(c *gin.Context) {
	var req stackPreflightReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	ids := dedupInt64(req.HostIDs)
	out := make([]stackPreflightHost, 0, len(ids))
	for _, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			out = append(out, stackPreflightHost{HostID: id, Docker: "unreachable", Error: "主机不存在"})
			continue
		}
		item := stackPreflightHost{HostID: hh.ID, Name: hh.Name, IP: hh.IP}
		raw, execErr := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hh.ID,
			`if command -v docker >/dev/null 2>&1; then echo INFRAOPS_DOCKER=yes; else echo INFRAOPS_DOCKER=no; fi`, nil)
		if execErr != nil {
			item.Docker = "unreachable"
			item.Error = execErr.Error()
		} else if strings.Contains(raw, "INFRAOPS_DOCKER=yes") {
			item.Docker = "present"
		} else {
			item.Docker = "missing"
		}
		out = append(out, item)
	}
	willInstall, willSkip, unreachable := []stackPreflightHost{}, []stackPreflightHost{}, []stackPreflightHost{}
	for _, it := range out {
		switch it.Docker {
		case "missing":
			willInstall = append(willInstall, it)
		case "present":
			willSkip = append(willSkip, it)
		default:
			unreachable = append(unreachable, it)
		}
	}
	resp.OK(c, gin.H{
		"hosts": out, "will_install": willInstall, "will_skip": willSkip, "unreachable": unreachable,
	})
}

// Run POST /api/stacks/run
func (h *stackHandler) Run(c *gin.Context) {
	var req stackRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	runID, err := h.createAndRun(req, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"run_id": runID})
}

func (h *stackHandler) createAndRun(req stackRunReq, remoteIP string) (int64, error) {
	op := strings.TrimSpace(req.Op)
	if op == "" {
		op = "create"
	}
	switch op {
	case "create", "scale_out", "scale_in", "add_component", "uninstall", "remove_component":
	default:
		return 0, fmt.Errorf("不支持的操作: %s", op)
	}

	var inst *model.StackInstance
	if op != "create" {
		if req.InstanceID <= 0 {
			return 0, fmt.Errorf("缺少集群实例")
		}
		full, err := h.repo.GetInstanceFull(req.InstanceID)
		if err != nil || full == nil {
			return 0, fmt.Errorf("集群实例不存在")
		}
		busy, err := h.repo.InstanceHasRunning(full.ID)
		if err != nil {
			return 0, err
		}
		if busy {
			return 0, fmt.Errorf("该集群有正在执行的流程，请稍后再试")
		}
		inst = full
		req.StackKey = inst.StackKey
		req.Mode = inst.Mode
	}

	bp := store.FindBuiltinStack(req.StackKey)
	if bp == nil {
		return 0, fmt.Errorf("套件不存在")
	}
	if strings.TrimSpace(req.Mode) == "" {
		return 0, fmt.Errorf("请选择模式")
	}
	mode := bp.ModeDef(req.Mode)
	if mode == nil {
		return 0, fmt.Errorf("不支持的模式: %s", req.Mode)
	}
	ids := dedupInt64(req.HostIDs)
	if (op == "uninstall" || op == "remove_component") && len(ids) == 0 && inst != nil {
		for _, h := range activeInstanceHosts(inst) {
			ids = append(ids, h.HostID)
		}
	}
	if len(ids) == 0 {
		if op == "uninstall" && inst != nil {
			_ = h.repo.UpdateInstance(inst.ID, "", "uninstalled", "", "")
			return 0, nil
		}
		return 0, fmt.Errorf("请选择主机")
	}

	params := req.Params
	if params == nil {
		params = map[string]string{}
	}
	reqComponents := strings.TrimSpace(params["components"])
	if inst != nil {
		base := map[string]string{}
		_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
		for k, v := range base {
			if strings.TrimSpace(params[k]) == "" {
				params[k] = v
			}
		}
	}

	if op == "create" {
		replicas := 0
		if bp.Key == "redis" && req.Mode == "cluster" {
			replicas = parseReplicas(params["replicas"])
		}
		if err := validateStackTopology(bp, mode, len(ids), replicas, req.MasterHostID, ids); err != nil {
			return 0, err
		}
		if bp.Key == "kafka" && req.Mode == "kraft" && strings.TrimSpace(params["cluster_id"]) == "" {
			id, err := randomKafkaClusterID()
			if err != nil {
				return 0, fmt.Errorf("生成 Kafka 集群 ID 失败: %w", err)
			}
			params["cluster_id"] = id
		}
		if bp.Key == "rabbitmq" && strings.TrimSpace(params["erlang_cookie"]) == "" {
			cookie, err := randomErlangCookie()
			if err != nil {
				return 0, fmt.Errorf("生成 Erlang Cookie 失败: %w", err)
			}
			params["erlang_cookie"] = cookie
		}
		if bp.Key == "bigdata" {
			if err := validateBigdataComponents(params["components"], true); err != nil {
				return 0, err
			}
		}
	}
	if op == "add_component" && bp.Key == "bigdata" {
		installed := parseJSONStringList(inst.ComponentsJSON)
		if len(installed) == 0 {
			base := map[string]string{}
			_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
			installed = parseComponentsCSV(base["components"])
		}
		added := parseComponentsCSV(reqComponents)
		if err := validateBigdataAdd(added, installed); err != nil {
			return 0, err
		}
		params["components"] = strings.Join(mergeComponentList(installed, added), ",")
	}
	if op == "remove_component" {
		if bp.Category != "platform" {
			return 0, fmt.Errorf("仅组合套件支持按组件卸载")
		}
		installed := parseJSONStringList(inst.ComponentsJSON)
		if len(installed) == 0 {
			base := map[string]string{}
			_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
			installed = parseComponentsCSV(base["components"])
		}
		removing := parseComponentsCSV(reqComponents)
		if err := validateBigdataRemove(removing, installed); err != nil {
			return 0, err
		}
		params["remove_components"] = strings.Join(removing, ",")
		params["components"] = strings.Join(subtractComponents(installed, removing), ",")
	}
	if op == "uninstall" && inst != nil && inst.Status == "uninstalled" {
		return 0, fmt.Errorf("集群已卸载")
	}

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
		hosts, err = h.buildScaleOutHosts(bp, mode, inst, ids, params, hostParams)
	case "add_component":
		hosts, err = h.buildAddComponentHosts(bp, mode, inst, ids, req.MasterHostID, params, hostParams)
	default:
		hosts, err = h.buildCreateHosts(bp, mode, ids, req.MasterHostID, params, hostParams)
	}
	if err != nil {
		return 0, err
	}

	if op == "create" {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = bp.Name + "-" + mode.Label
		}
		compList := []string{req.Mode}
		if bp.Key == "bigdata" {
			compList = parseComponentsCSV(params["components"])
		}
		comps, _ := json.Marshal(compList)
		taskParams, _ := json.Marshal(params)
		id, cerr := h.repo.CreateInstance(&model.StackInstance{
			Name: name, StackKey: bp.Key, StackName: bp.Name, Mode: req.Mode,
			Category: bp.Category, Status: "deploying",
			ParamsJSON: string(taskParams), ComponentsJSON: string(comps),
		})
		if cerr != nil {
			return 0, fmt.Errorf("创建集群实例失败: %w", cerr)
		}
		req.InstanceID = id
	}

	taskParams, _ := json.Marshal(params)
	runID, err := h.repo.CreateRun(&model.StackRun{
		InstanceID: req.InstanceID, Op: op,
		StackKey: bp.Key, StackName: bp.Name, Mode: req.Mode, ParamsJSON: string(taskParams),
	}, hosts)
	if err != nil {
		return 0, fmt.Errorf("创建套件运行失败: %w", err)
	}
	if op != "create" {
		_ = h.repo.UpdateInstance(req.InstanceID, "", "deploying", "", "")
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.run", TargetType: "stack_run", TargetID: runID,
		Detail:   fmt.Sprintf("op=%s stack=%s mode=%s hosts=%d instance=%d", op, bp.Name, req.Mode, len(hosts), req.InstanceID),
		RemoteIP: remoteIP,
	})
	go h.execute(runID)
	return runID, nil
}

// Runs GET /api/stacks/runs
func (h *stackHandler) Runs(c *gin.Context) {
	page, pageSize := parsePage(c)
	items, total, err := h.repo.ListRuns(page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询套件运行失败")
		return
	}
	if items == nil {
		items = []model.StackRun{}
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

// RunDetail GET /api/stacks/runs/:id
func (h *stackHandler) RunDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	run, err := h.repo.GetRun(id)
	if err != nil || run == nil {
		resp.Fail(c, resp.CodeNotFound, "运行记录不存在")
		return
	}
	hosts, _ := h.repo.RunHosts(id)
	if hosts == nil {
		hosts = []model.StackRunHost{}
	}
	resp.OK(c, gin.H{
		"id": run.ID, "instance_id": run.InstanceID, "op": run.Op,
		"stack_key": run.StackKey, "stack_name": run.StackName, "mode": run.Mode,
		"status": run.Status, "total": run.Total, "success_cnt": run.SuccessCnt, "fail_cnt": run.FailCnt,
		"created_at": run.CreatedAt, "finished_at": run.FinishedAt, "hosts": hosts,
	})
}

func parseReplicas(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// randomKafkaClusterID 生成 KRaft 集群 ID：与 kafka-storage.sh random-uuid 同构（16 字节 base64url 去填充）。
func randomKafkaClusterID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomErlangCookie() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// validateStackTopology 按套件分派拓扑校验：Redis 有专用规则，其余套件走通用规则。
func validateStackTopology(bp *store.BuiltinStack, mode *model.StackMode, n, replicas int, masterID int64, hostIDs []int64) error {
	if bp.Key == "redis" {
		return validateRedisTopology(mode.Key, n, replicas, masterID, hostIDs)
	}
	if n < mode.MinHosts {
		return fmt.Errorf("%s 模式至少需要 %d 台主机", mode.Label, mode.MinHosts)
	}
	if mode.AssignMaster {
		return masterMustBeSelected(masterID, hostIDs)
	}
	return nil
}

func masterMustBeSelected(masterID int64, hostIDs []int64) error {
	if masterID == 0 {
		return fmt.Errorf("请指定一台主节点")
	}
	for _, id := range hostIDs {
		if id == masterID {
			return nil
		}
	}
	return fmt.Errorf("主节点必须在已选主机中")
}

func validateRedisTopology(mode string, n, replicas int, masterID int64, hostIDs []int64) error {
	switch mode {
	case "replication":
		if n < 2 {
			return fmt.Errorf("主从模式至少需要 2 台主机")
		}
		return masterMustBeSelected(masterID, hostIDs)
	case "sentinel":
		if n < 3 {
			return fmt.Errorf("哨兵模式至少需要 3 台主机")
		}
		return masterMustBeSelected(masterID, hostIDs)
	case "cluster":
		if replicas != 0 && replicas != 1 {
			return fmt.Errorf("集群副本数只能是 0（三主）或 1（三主三从）")
		}
		if replicas == 0 && n < 3 {
			return fmt.Errorf("三主集群至少需要 3 台主机")
		}
		if replicas == 1 && (n < 6 || n%2 != 0) {
			return fmt.Errorf("三主三从至少需要 6 台主机且主机数为偶数")
		}
		div := replicas + 1
		if n%div != 0 {
			return fmt.Errorf("主机数必须能被 %d 整除（replicas+1）", div)
		}
		if n/div < 3 {
			return fmt.Errorf("集群至少需要 3 个主节点")
		}
	default:
		return fmt.Errorf("不支持的模式: %s", mode)
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
