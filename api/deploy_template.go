// 部署模板 CRUD：含变量声明校验与占位符一致性检查。
package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

var (
	varNameRe     = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	placeholderRe = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)
)

type deployTemplateHandler struct {
	tplRepo   *store.DeployRepo
	schedRepo *store.DeployScheduleRepo
}

func NewDeployTemplateHandler(tplRepo *store.DeployRepo, schedRepo *store.DeployScheduleRepo) *deployTemplateHandler {
	return &deployTemplateHandler{tplRepo: tplRepo, schedRepo: schedRepo}
}

type templateReq struct {
	Name        string          `json:"name" binding:"required"`
	Description string          `json:"description"`
	Script      string          `json:"script" binding:"required"`
	Variables   json.RawMessage `json:"variables"`
}

// tplVar 模板变量声明。
type tplVar struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Default  string `json:"default"`
	Required bool   `json:"required"`
}

// List GET /api/deploy/templates
func (h *deployTemplateHandler) List(c *gin.Context) {
	items, err := h.tplRepo.ListTemplates()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询模板失败: "+err.Error())
		return
	}
	if items == nil {
		items = []model.DeployTemplate{}
	}
	resp.OK(c, items)
}

// Create POST /api/deploy/templates
func (h *deployTemplateHandler) Create(c *gin.Context) {
	req, vars, ok := h.bindAndValidate(c)
	if !ok {
		return
	}
	t := &model.DeployTemplate{
		Name: req.Name, Description: req.Description,
		Script: req.Script, Variables: mustMarshal(vars),
	}
	id, err := h.tplRepo.CreateTemplate(t)
	if err != nil {
		resp.Fail(c, resp.CodeConflict, "创建失败：模板名可能已存在")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/deploy/templates/:id
func (h *deployTemplateHandler) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	existing, err := h.tplRepo.GetTemplate(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "模板不存在")
		return
	}
	if existing.IsBuiltin {
		resp.Fail(c, resp.CodeForbidden, "内置模板不可修改")
		return
	}
	req, vars, ok := h.bindAndValidate(c)
	if !ok {
		return
	}
	existing.Name = req.Name
	existing.Description = req.Description
	existing.Script = req.Script
	existing.Variables = mustMarshal(vars)
	if err := h.tplRepo.UpdateTemplate(existing); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新失败")
		return
	}
	resp.OK(c, nil)
}

// Delete DELETE /api/deploy/templates/:id
func (h *deployTemplateHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	existing, err := h.tplRepo.GetTemplate(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "模板不存在")
		return
	}
	if existing.IsBuiltin {
		resp.Fail(c, resp.CodeForbidden, "内置模板不可删除")
		return
	}
	// 引用保护：有定时任务关联时禁止删除
	if cnt, err := h.schedRepo.CountByTemplate(id); err == nil && cnt > 0 {
		resp.Fail(c, resp.CodeConflict, fmt.Sprintf("该模板被 %d 个定时任务引用，请先解除关联", cnt))
		return
	}
	if err := h.tplRepo.DeleteTemplate(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除失败")
		return
	}
	resp.OK(c, nil)
}

// bindAndValidate 绑定请求并校验变量名合法性与占位符一致性。
func (h *deployTemplateHandler) bindAndValidate(c *gin.Context) (*templateReq, []tplVar, bool) {
	var req templateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return nil, nil, false
	}
	vars, err := parseVariables(req.Variables)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return nil, nil, false
	}
	if err := validatePlaceholders(req.Script, vars); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return nil, nil, false
	}
	return &req, vars, true
}

// parseVariables 解析并校验变量声明表。
func parseVariables(raw json.RawMessage) ([]tplVar, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var vars []tplVar
	if err := json.Unmarshal(raw, &vars); err != nil {
		return nil, fmt.Errorf("variables 格式错误")
	}
	seen := map[string]bool{}
	for _, v := range vars {
		if !varNameRe.MatchString(v.Name) {
			return nil, fmt.Errorf("非法变量名: %s", v.Name)
		}
		if seen[v.Name] {
			return nil, fmt.Errorf("重复变量名: %s", v.Name)
		}
		seen[v.Name] = true
	}
	return vars, nil
}

// validatePlaceholders 脚本中的 {{xxx}} 必须都在变量表中声明。
func validatePlaceholders(script string, vars []tplVar) error {
	declared := map[string]bool{}
	for _, v := range vars {
		declared[v.Name] = true
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(script, -1) {
		name := m[1]
		// 内置变量(__name/__ip/__seq/__ip_last)由执行引擎在运行时注入，免声明
		if strings.HasPrefix(name, "__") {
			continue
		}
		if !declared[name] {
			return fmt.Errorf("脚本中的占位符 {{%s}} 未在变量表中声明", name)
		}
	}
	return nil
}

// renderScript 用参数渲染脚本；缺失必填项报错。
func renderScript(script string, rawVars json.RawMessage, params map[string]string) (string, error) {
	vars, err := parseVariables(rawVars)
	if err != nil {
		return "", err
	}
	out := script
	for _, v := range vars {
		val := v.Default
		if p, ok := params[v.Name]; ok && strings.TrimSpace(p) != "" {
			val = p
		}
		if v.Required && strings.TrimSpace(val) == "" {
			return "", fmt.Errorf("缺少必填参数: %s(%s)", v.Label, v.Name)
		}
		out = strings.ReplaceAll(out, "{{"+v.Name+"}}", val)
	}
	return out, nil
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
