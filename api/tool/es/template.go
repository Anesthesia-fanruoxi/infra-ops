// 索引模板 / 组件模板（B11，§13.3）：CRUD（W5–W8）+ _simulate 模拟预览。
// 同样受 §3.3 四条硬约束约束（名称白名单 / 内置只读 / 审计；模拟为只读）。
package es

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
)

// ListIndexTemplates 索引模板列表。
func (h *Handler) ListIndexTemplates(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodGet, "/_index_template", nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取索引模板失败").Error())
		return
	}
	var m map[string]interface{}
	json.Unmarshal(body, &m)
	resp.OK(c, m)
}

// GetIndexTemplate 模板详情（原始 JSON，含 composed_of）。
func (h *Handler) GetIndexTemplate(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := c.Param("name")
	status, body, err := client.do(c.Request.Context(), http.MethodGet, "/_index_template/"+name, nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取模板详情失败").Error())
		return
	}
	resp.OK(c, gin.H{"name": name, "managed": isManaged(name, body), "template": json.RawMessage(body)})
}

// PutIndexTemplate 新建 / 覆盖索引模板（W5）。
func (h *Handler) PutIndexTemplate(c *gin.Context) {
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
		failWErr(c, &dslErr{code: esCodeManagedReadonly, msg: "模板 " + name + " 由 ES 管理，只读"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodPut, "/_index_template/"+name, raw)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "保存模板失败").Error())
		return
	}
	h.auditWrite(c, "es.index_template.put", "es_index_template", name)
	resp.OK(c, nil)
}

// DeleteIndexTemplate 删除索引模板（W6）。
func (h *Handler) DeleteIndexTemplate(c *gin.Context) {
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
	if name[0] == '.' {
		failWErr(c, &dslErr{code: esCodeManagedReadonly, msg: "模板 " + name + " 为内置资源，只读"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodDelete, "/_index_template/"+name, nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "删除模板失败").Error())
		return
	}
	h.auditWrite(c, "es.index_template.delete", "es_index_template", name)
	resp.OK(c, nil)
}

// ListComponentTemplates 组件模板列表。
func (h *Handler) ListComponentTemplates(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodGet, "/_component_template", nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "获取组件模板失败").Error())
		return
	}
	var m map[string]interface{}
	json.Unmarshal(body, &m)
	resp.OK(c, m)
}

// PutComponentTemplate 新建 / 覆盖组件模板（W7）。
func (h *Handler) PutComponentTemplate(c *gin.Context) {
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
		failWErr(c, &dslErr{code: esCodeManagedReadonly, msg: "组件模板 " + name + " 由 ES 管理，只读"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodPut, "/_component_template/"+name, raw)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "保存组件模板失败").Error())
		return
	}
	h.auditWrite(c, "es.component_template.put", "es_component_template", name)
	resp.OK(c, nil)
}

// DeleteComponentTemplate 删除组件模板（W8）。
func (h *Handler) DeleteComponentTemplate(c *gin.Context) {
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
	if name[0] == '.' {
		failWErr(c, &dslErr{code: esCodeManagedReadonly, msg: "组件模板 " + name + " 为内置资源，只读"})
		return
	}
	status, body, err := client.do(c.Request.Context(), http.MethodDelete, "/_component_template/"+name, nil)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "删除组件模板失败").Error())
		return
	}
	h.auditWrite(c, "es.component_template.delete", "es_component_template", name)
	resp.OK(c, nil)
}

// SimulateIndexTemplate 模拟：给定模板与假想索引名，预览合并结果（只读，§13.3 必做能力）。
func (h *Handler) SimulateIndexTemplate(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		Name      string          `json:"name"`
		Template  json.RawMessage `json:"template"`
		IndexName string          `json:"index_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	var simReq map[string]interface{}
	var path string
	switch {
	case req.Name != "":
		// 按已保存模板模拟
		path = "/_index_template/_simulate/" + req.Name + "/" + pathEscape(req.IndexName)
	case len(req.Template) > 0:
		if err := json.Unmarshal(req.Template, &simReq); err != nil {
			resp.Fail(c, resp.CodeBadRequest, "template 不是合法 JSON: "+err.Error())
			return
		}
		simReq["index_patterns"] = []string{req.IndexName}
		path = "/_index_template/_simulate"
	default:
		resp.Fail(c, resp.CodeBadRequest, "需要 name 或 template 之一")
		return
	}
	var payload []byte
	if simReq != nil {
		payload = mustJSON(simReq)
	}
	status, body, err := client.do(c.Request.Context(), http.MethodPost, path, payload)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, body, "模拟失败").Error())
		return
	}
	var m map[string]interface{}
	json.Unmarshal(body, &m)
	resp.OK(c, m)
}
