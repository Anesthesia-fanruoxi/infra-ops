// 生命周期管理（B11，§13.1/§13.2）：ILM 策略 CRUD + explain + retry + 数据流保留期。
// 写操作 W1–W4（§3.3 登记项），四条硬约束逐条落地：
// 名称白名单（4015）/ 内置资源只读（4013）/ 删除被引用策略回显原始报错（4014）/ 写操作落审计。
package es

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

// WithAuditRepo 注入审计仓储（写操作落审计，§3.3 硬约束 4）。
func (h *Handler) WithAuditRepo(ar *repo.AuditRepo) *Handler {
	h.auditRepo = ar
	return h
}

// auditWrite 写操作统一落审计。AuditLog.TargetID 为 int64，
// 资源名称（策略/模板/数据流名）记入 Detail 字段。
func (h *Handler) auditWrite(c *gin.Context, action, targetType, targetID string) {
	if h.auditRepo == nil {
		return
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: action, TargetType: targetType, Detail: targetID, RemoteIP: c.ClientIP(),
	})
}

// validateResourceName 名称白名单（§3.3 硬约束 1）：非空、不含 * , ?、不以 _ 或 . 开头 → 4015。
func validateResourceName(name string) *dslErr {
	if name == "" || strings.ContainsAny(name, "*,?") ||
		strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return &dslErr{code: esCodeBadResourceName,
			msg: "资源名称非法：不得为空、不得含 * , ?、不得以 _ 或 . 开头"}
	}
	return nil
}

// isManaged 内置资源（_meta.managed=true 或名称以 . 开头）→ 只读（4013）。
func isManaged(name string, raw []byte) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	var p struct {
		Meta struct {
			Managed bool `json:"managed"`
		} `json:"_meta"`
	}
	if json.Unmarshal(raw, &p) == nil {
		return p.Meta.Managed
	}
	return false
}

// failWErr 写操作错误映射：4015/400 → 400，4013 → 403，4014 → 409。
func failWErr(c *gin.Context, derr *dslErr) {
	switch derr.code {
	case esCodeManagedReadonly:
		resp.ErrHTTP(c, http.StatusForbidden, derr.code, derr.msg)
	case esCodePolicyInUse:
		resp.ErrHTTP(c, http.StatusConflict, derr.code, derr.msg)
	case esCodeBadResourceName:
		resp.ErrHTTP(c, http.StatusBadRequest, derr.code, derr.msg)
	default:
		resp.ErrHTTP(c, http.StatusBadRequest, derr.code, derr.msg)
	}
}

// ---------- ILM 策略（读 + W1/W2/W3） ----------

// ListILMPolicies 策略列表（含阶段摘要与 managed 标记）。
func (h *Handler) ListILMPolicies(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodGet, "/_ilm/policy", nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取策略列表失败: "+err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取策略列表失败").Error())
		return
	}
	var policies map[string]json.RawMessage
	json.Unmarshal(body, &policies)
	list := make([]gin.H, 0, len(policies))
	for name, raw := range policies {
		list = append(list, gin.H{"name": name, "managed": isManaged(name, raw), "policy": json.RawMessage(raw)})
	}
	resp.OK(c, gin.H{"list": list})
}

// GetILMPolicy 策略详情（原始 JSON）。
func (h *Handler) GetILMPolicy(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := c.Param("name")
	status, body, err := client.do(c.Request.Context(), http.MethodGet, "/_ilm/policy/"+name, nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取策略详情失败").Error())
		return
	}
	resp.OK(c, gin.H{"name": name, "managed": isManaged(name, body), "policy": json.RawMessage(body)})
}

// PutILMPolicy 新建 / 覆盖策略（W1）。
func (h *Handler) PutILMPolicy(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := c.Param("name")
	if derr := validateResourceName(name); derr != nil {
		failWErr(c, derr)
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "读取请求体失败")
		return
	}
	if isManaged(name, raw) {
		failWErr(c, &dslErr{code: esCodeManagedReadonly, msg: "策略 " + name + " 由 ES 管理，只读"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodPut, "/_ilm/policy/"+name, raw)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "保存策略失败").Error())
		return
	}
	h.auditWrite(c, "es.ilm.put", "es_ilm_policy", name)
	resp.OK(c, nil)
}

// DeleteILMPolicy 删除策略（W2）：被索引引用时 ES 原始报错完整回显 → 4014，不自动解绑。
func (h *Handler) DeleteILMPolicy(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := c.Param("name")
	if derr := validateResourceName(name); derr != nil {
		failWErr(c, derr)
		return
	}
	if strings.HasPrefix(name, ".") {
		failWErr(c, &dslErr{code: esCodeManagedReadonly, msg: "策略 " + name + " 为内置资源，只读"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodDelete, "/_ilm/policy/"+name, nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		// 删除被引用策略：ES 的原始报错（含引用它的索引名）完整回显（§13.1）
		resp.ErrHTTP(c, http.StatusConflict, esCodePolicyInUse, esErr(status, body, "删除策略失败").Error())
		return
	}
	h.auditWrite(c, "es.ilm.delete", "es_ilm_policy", name)
	resp.OK(c, nil)
}

// ExplainILM 索引 ILM 状态（接受 pattern，视图详情页可直接用）。
func (h *Handler) ExplainILM(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	pattern := c.Query("pattern")
	if pattern == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少 pattern 参数")
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodGet,
		"/"+pathEscape(pattern)+"/_ilm/explain?only_errors=false", nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取 ILM 状态失败").Error())
		return
	}
	var m map[string]interface{}
	json.Unmarshal(body, &m)
	resp.OK(c, m)
}

// RetryILM 重试卡住的 ILM 步骤（W3，仅具体索引名）。
func (h *Handler) RetryILM(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		Index string `json:"index" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	// 仅接受具体索引名（不接受 pattern / 通配）
	if derr := validateResourceName(req.Index); derr != nil || strings.ContainsAny(req.Index, "*?") {
		failWErr(c, &dslErr{code: esCodeBadResourceName, msg: "重试仅接受具体索引名"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodPost,
		"/"+pathEscape(req.Index)+"/_ilm/retry", nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "重试失败").Error())
		return
	}
	h.auditWrite(c, "es.ilm.retry", "es_index", req.Index)
	resp.OK(c, nil)
}

// ---------- 数据流与 DLM（读 + W4） ----------

// ListDataStreams 数据流列表与保留期。
func (h *Handler) ListDataStreams(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodGet, "/_data_stream", nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取数据流失败").Error())
		return
	}
	var m map[string]interface{}
	json.Unmarshal(body, &m)
	resp.OK(c, m)
}

// PutDataStreamLifecycle 设置数据流保留期（W4）。
func (h *Handler) PutDataStreamLifecycle(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := c.Param("name")
	if derr := validateResourceName(name); derr != nil {
		failWErr(c, derr)
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "读取请求体失败")
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodPut,
		"/_data_stream/"+pathEscape(name)+"/_lifecycle", raw)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "设置保留期失败").Error())
		return
	}
	h.auditWrite(c, "es.dlm.put", "es_data_stream", name)
	resp.OK(c, nil)
}
