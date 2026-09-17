// 套件部署 API：蓝图列表、Docker 预检、创建运行。
package stack

import (
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/deploy"
	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
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
	// 启动恢复：服务重启中断的流程标记为失败，避免实例被孤儿 run 锁死
	if instIDs, err := repo.FailStaleRuns(); err == nil && len(instIDs) > 0 {
		for _, id := range instIDs {
			_ = repo.UpdateInstance(id, "", "failed", "", "")
		}
	}
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
	// hub 镜像主机（可选）：>0 表示从该主机的 Docker Registry 拉取镜像，
	// 引擎在执行前做健康校验与镜像预热；0=直连拉取。
	HubHostID       int64 `json:"hub_host_id"`
	HubAutoInsecure bool  `json:"hub_auto_insecure"`
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
	ids := deploy.DedupInt64(req.HostIDs)
	out := make([]stackPreflightHost, 0, len(ids))
	for _, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			out = append(out, stackPreflightHost{HostID: id, Docker: "unreachable", Error: "主机不存在"})
			continue
		}
		item := stackPreflightHost{HostID: hh.ID, Name: hh.Name, IP: hh.IP}
		raw, execErr := deploy.ExecHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hh.ID,
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
