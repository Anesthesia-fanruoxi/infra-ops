// 本文件实现批量新增主机：IP 范围解析器与 POST /hosts/batch handler。
package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// 批量新增错误码（见 docs/04-api.md 错误码表）。
const (
	codeBatchParse   = 3001 // IP 列表解析失败
	codeBatchTooMany = 3002 // 数量超过上限
)

// ---- 批量创建 ----

// batchCreateReq 批量新增请求体（契约见 docs/04-api.md POST /hosts/batch）。
type batchCreateReq struct {
	CredentialID int64  `json:"credential_id" binding:"required"`
	IPs          string `json:"ips" binding:"required"`
	Port         int    `json:"port"`
	Tag          string `json:"tag"`
	LegacyRole   string `json:"role"` // deprecated role alias
	Remark       string `json:"remark"`
	AutoTest     *bool  `json:"auto_test"` // default true
}

// batchResultItem 单条批量结果。
type batchResultItem struct {
	IP        string `json:"ip"`
	Status    string `json:"status"` // created_online/created_offline/created/skipped_exists
	HostID    int64  `json:"host_id,omitempty"`
	Name      string `json:"name,omitempty"`
	ErrorCode int    `json:"error_code,omitempty"`
}

// toTest 待并发测试的主机及其结果索引。
type toTest struct {
	idx  int
	host *model.Host
}

// BatchCreate POST /hosts/batch 批量新增主机。
func (h *Handler) BatchCreate(c *gin.Context) {
	var req batchCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	req.Tag = normalizeHostTag(req.Tag, req.LegacyRole)
	autoTest := true
	if req.AutoTest != nil {
		autoTest = *req.AutoTest
	}

	// 校验凭据存在
	cred, err := h.credRepo.GetByID(req.CredentialID)
	if err != nil || cred == nil {
		resp.Fail(c, resp.CodeBadRequest, "凭据不存在")
		return
	}

	// 解析 IP 列表（失败整体拒绝，不落任何库）
	ips, err := parseIPList(req.IPs)
	if err != nil {
		if errors.Is(err, errTooManyIPs) {
			resp.ErrHTTP(c, 400, codeBatchTooMany, err.Error())
			return
		}
		resp.ErrHTTP(c, 400, codeBatchParse, "IP 列表解析失败: "+err.Error())
		return
	}

	// 库内已有 IP（跳过判断）与名称（自动命名冲突判断）
	all, err := h.hostRepo.ListAll()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询主机失败")
		return
	}
	existingIP := make(map[string]bool, len(all))
	usedName := make(map[string]bool, len(all))
	for _, hst := range all {
		existingIP[hst.IP] = true
		usedName[hst.Name] = true
	}

	// 预分配结果保序；逐台创建，命中库内 IP 跳过不覆盖
	results := make([]batchResultItem, len(ips))
	var toTests []toTest
	for i, ip := range ips {
		results[i] = batchResultItem{IP: ip}
		if existingIP[ip] {
			results[i].Status = "skipped_exists"
			continue
		}
		hst := &model.Host{
			Name: ip, IP: ip, Port: req.Port, Tag: req.Tag,
			Remark: req.Remark, CredentialID: req.CredentialID,
		}
		id, err := h.hostRepo.Create(hst)
		if err != nil {
			results[i].Status = "failed"
			results[i].ErrorCode = resp.CodeInternal
			continue
		}
		hst.ID = id
		existingIP[ip] = true
		results[i].Status = "created"
		results[i].HostID = id
		results[i].Name = ip
		toTests = append(toTests, toTest{idx: i, host: hst})
	}

	// 并发连接测试（上限 5），单台失败不阻塞整批
	if autoTest && len(toTests) > 0 {
		h.runBatchTests(toTests, results, usedName)
	}

	// 通知 SSE：主机列表与状态均已变化
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicHostChanged, map[string]interface{}{"action": "batch_create", "count": len(ips)})
	}

	// 汇总计数
	created, skipped := 0, 0
	for _, r := range results {
		switch r.Status {
		case "skipped_exists":
			skipped++
		case "failed":
		default:
			created++
		}
	}
	resp.OK(c, gin.H{
		"total": len(ips), "created": created, "skipped": skipped, "results": results,
	})
}

// runBatchTests 并发执行连接测试与自动命名，结果写回各自索引。
func (h *Handler) runBatchTests(list []toTest, results []batchResultItem, usedName map[string]bool) {
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for _, tt := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func(tt toTest) {
			defer wg.Done()
			defer func() { <-sem }()

			res := &results[tt.idx] // 各 goroutine 只写自己的索引，无竞争
			result, code := h.testConnection(tt.host)
			if code != 0 {
				h.hostRepo.UpdateProbeResult(tt.host.ID, "offline", 0, "{}")
				res.Status = "created_offline"
				res.ErrorCode = code
				return
			}
			infoJSON, _ := json.Marshal(result.Info)
			name := strings.TrimSpace(result.Info.Hostname)
			if name == "" {
				name = tt.host.IP
			}
			name = uniqueName(name, usedName)
			if name != tt.host.Name {
				h.hostRepo.Rename(tt.host.ID, name)
			}
			h.hostRepo.UpdateProbeResult(tt.host.ID, "online", int(result.LatencyMs), string(infoJSON))
			res.Status = "created_online"
			res.Name = name
		}(tt)
	}
	wg.Wait()
}

// uniqueName 名称冲突时追加 -2/-3 直至唯一（范围 = 库内 ∪ 本批已分配）。
func uniqueName(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}
