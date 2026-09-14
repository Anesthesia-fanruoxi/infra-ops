// 套件探活入口：并行 SSH 探活各成员、汇总结果、Redis 集群校验与权威角色矩阵。
package stack

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/deploy"
	"infra-ops/common/resp"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

type stackVerifyCheck struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
}

type stackVerifyHost struct {
	HostID   int64              `json:"host_id"`
	HostName string             `json:"host_name"`
	HostIP   string             `json:"host_ip"`
	Role     string             `json:"role"`
	LiveRole string             `json:"live_role,omitempty"`
	OK       bool               `json:"ok"`
	Error    string             `json:"error,omitempty"`
	Checks   []stackVerifyCheck `json:"checks"`
}

type stackVerifyEndpoint struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	URL       string `json:"url"`
	Role      string `json:"role,omitempty"`
}

type stackVerifyResult struct {
	OK         bool                  `json:"ok"`
	Summary    string                `json:"summary"`
	CheckedAt  string                `json:"checked_at"`
	Hint       string                `json:"hint,omitempty"`
	Endpoints  []stackVerifyEndpoint `json:"endpoints"`
	Hosts      []stackVerifyHost     `json:"hosts"`
	Notes      []string              `json:"notes,omitempty"`
	ClientHint string                `json:"client_hint,omitempty"`
}

// VerifyInstance POST /api/stacks/instances/:id/verify
// 控制面 SSH 到各成员探活：Redis 跑 PING / INFO / CLUSTER，其它套件检查 compose 容器是否在跑。
func (h *stackHandler) VerifyInstance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	if inst.Status == "uninstalled" {
		resp.Fail(c, resp.CodeBadRequest, "集群已卸载，无法探活")
		return
	}
	hosts := activeInstanceHosts(inst)
	if len(hosts) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "集群没有在役成员")
		return
	}
	// 套件角色计划由驱动全权负责（清单式）时，探活前确保计划已物化（缺失时惰性生成并落库，
	// docs/角色物化设计.md §6.1）；之后 overrideBigdataMasters 以 Plan 的全量 masters 投影为唯一事实源。
	if _, ok := store.FindStackDriver(inst.StackKey).(stackkit.SelfPlanning); ok {
		_, _ = h.loadRolePlan(inst)
	}
	instParams := parseJSONMap(inst.ParamsJSON)
	outHosts := make([]stackVerifyHost, len(hosts))
	conc := h.conc
	if conc <= 0 {
		conc = sysutil.AdaptiveConcurrency(len(hosts))
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outHosts[idx] = h.verifyInstanceHost(inst, hosts[idx], instParams)
		}(i)
	}
	wg.Wait()

	result := assembleStackVerify(inst, instParams, outHosts)
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.instance.verify", TargetType: "stack_instance", TargetID: id,
		Detail: fmt.Sprintf("name=%s ok=%v %s", inst.Name, result.OK, result.Summary), RemoteIP: c.ClientIP(),
	})
	resp.OK(c, result)
}

func (h *stackHandler) verifyInstanceHost(inst *model.StackInstance, host model.StackInstanceHost, instParams map[string]string) stackVerifyHost {
	row := stackVerifyHost{
		HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP, Role: host.Role,
		Checks: []stackVerifyCheck{},
	}
	params := mergeParamMaps(instParams, parseJSONMap(host.ParamsJSON))
	overrideBigdataMasters(inst, params)
	script := buildStackVerifyScript(inst.StackKey, inst.Mode, host.Role, host.HostIP, params)
	raw, execErr := deploy.ExecHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, host.HostID, script, nil)
	pass := strings.TrimSpace(params["password"])
	raw = redactSecret(raw, pass)
	if execErr != nil {
		row.Error = redactSecret(execErr.Error(), pass)
		if strings.TrimSpace(raw) == "" {
			row.OK = false
			row.Checks = append(row.Checks, stackVerifyCheck{Name: "连通", OK: false, Detail: row.Error})
			return row
		}
	}
	parseStackVerifyOutput(inst.StackKey, inst.Mode, host, params, raw, &row)
	return row
}

// CaCert GET /api/stacks/instances/:id/ca
// 下载 Elasticsearch 冷热温自签 SSL 的 CA 公钥（PEM）。任一在役节点均可（全员证书一致）。
func (h *stackHandler) CaCert(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	inst, err := h.repo.GetInstanceFull(id)
	if err != nil || inst == nil {
		resp.Fail(c, resp.CodeNotFound, "集群实例不存在")
		return
	}
	if inst.StackKey != "elasticsearch" || inst.Mode != "cold_warm_hot" {
		resp.Fail(c, resp.CodeBadRequest, "仅冷热温模式支持 SSL CA 下载")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(parseJSONMap(inst.ParamsJSON)["ssl_enabled"]), "true") {
		resp.Fail(c, resp.CodeBadRequest, "当前未启用 SSL，无 CA 证书")
		return
	}
	hosts := activeInstanceHosts(inst)
	if len(hosts) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "集群没有在役成员")
		return
	}
	hp := parseJSONMap(hosts[0].ParamsJSON)
	home := strings.TrimSpace(hp["home_dir"])
	if home == "" {
		home = "/data/elasticsearch"
	}
	raw, _ := deploy.ExecHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hosts[0].HostID,
		"cat '"+home+"/certs/ca.crt' 2>/dev/null || true", nil)
	c.Data(http.StatusOK, "application/x-pem-file", []byte(raw))
}

func assembleStackVerify(inst *model.StackInstance, params map[string]string, hosts []stackVerifyHost) stackVerifyResult {
	okN := 0
	for _, h := range hosts {
		if h.OK {
			okN++
		}
	}
	res := stackVerifyResult{
		CheckedAt: time.Now().Format("2006-01-02 15:04:05"),
		Endpoints: stackVerifyEndpoints(inst, params, hosts),
		Hosts:     hosts,
		Hint:      "控制面已 SSH 到各节点检查，不必再登录服务器。",
		Notes:     []string{"当前套件按容器运行状态探活。"},
	}
	if p, ok := store.FindStackDriver(inst.StackKey).(stackkit.ProbePlugin); ok {
		rows := make([]stackkit.ProbeHostRow, 0, len(hosts))
		for _, hh := range hosts {
			row := stackkit.ProbeHostRow{
				HostID: hh.HostID, HostName: hh.HostName, HostIP: hh.HostIP,
				Role: hh.Role, LiveRole: hh.LiveRole, OK: hh.OK,
			}
			for _, c := range hh.Checks {
				row.Checks = append(row.Checks, stackkit.Check{Name: c.Name, Component: c.Component, OK: c.OK, Detail: c.Detail})
			}
			rows = append(rows, row)
		}
		notes, hint := p.Summary(stackkit.ProbeCtx{Mode: inst.Mode, Params: params}, rows)
		if len(notes) > 0 {
			res.Notes = notes
		}
		res.ClientHint = hint
	}
	res.OK = okN == len(hosts) && len(hosts) > 0
	if res.OK {
		res.Summary = fmt.Sprintf("%d/%d 节点正常", okN, len(hosts))
	} else {
		res.Summary = fmt.Sprintf("%d/%d 节点正常", okN, len(hosts))
	}
	for _, n := range res.Notes {
		if strings.Contains(n, "异常") || strings.Contains(n, "未就绪") || strings.Contains(n, "不符") {
			res.OK = false
		}
	}
	if res.OK {
		res.Summary += " · 探活通过"
	} else {
		res.Summary += " · 存在异常"
	}
	return res
}
