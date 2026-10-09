// Package ailog 统一 AI 调用记录：跨工具按菜单（页面 id）+ 连接查询、汇总与清理。
//
// 各工具只负责写入（trace 装配在各自包内，menu 取自身页面 id），读取侧全部收敛到
// 这一个入口：工具视图传自己的 menu（可叠加选中连接的 conn_id），审计页不传 menu
// 看全部。删除同样按 menu + conn_id 圈定范围。
package ailog

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
	"infra-ops/store/setting"
)

const (
	defaultLogPage = 20
	maxLogPage     = 200
)

// Handler 统一 AI 调用记录接口。
type Handler struct {
	repo     *repo.AILogRepo
	settings *setting.SettingsRepo
}

// NewHandler 创建统一 AI 调用记录处理器。
func NewHandler(r *repo.AILogRepo, s *setting.SettingsRepo) *Handler {
	return &Handler{repo: r, settings: s}
}

// parseFilter 解析查询条件。
func parseFilter(c *gin.Context) model.AILogFilter {
	connID, _ := strconv.ParseInt(c.Query("conn_id"), 10, 64)
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("page_size"))
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = defaultLogPage
	}
	if size > maxLogPage {
		size = maxLogPage
	}
	return model.AILogFilter{
		Menu:     strings.TrimSpace(c.Query("menu")),
		ConnID:   connID,
		Kind:     strings.TrimSpace(c.Query("kind")),
		Status:   strings.TrimSpace(c.Query("status")),
		Page:     page,
		PageSize: size,
	}
}

// retentionDays 记录保留天数，与部署历史共用同一个设置（默认 30 天）。
func (h *Handler) retentionDays() int {
	if h.settings == nil {
		return 0
	}
	v, _ := h.settings.Get(setting.SettingLogRetentionDays)
	days, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || days <= 0 {
		return 0
	}
	return days
}

// List GET /api/ai/logs?menu=&conn_id=&kind=&status=&page=&page_size=
func (h *Handler) List(c *gin.Context) {
	f := parseFilter(c)
	list, total, err := h.repo.List(f)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "读取 AI 调用记录失败")
		return
	}
	sum, err := h.repo.Summarize(f)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "汇总 AI 调用记录失败")
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "summary": sum, "retention_days": h.retentionDays()})
}

// Clear DELETE /api/ai/logs?menu=&conn_id=
// menu 为空表示清全部菜单（审计页用）；conn_id 为 0 表示不限连接。
func (h *Handler) Clear(c *gin.Context) {
	menu := strings.TrimSpace(c.Query("menu"))
	connID, _ := strconv.ParseInt(c.Query("conn_id"), 10, 64)
	n, err := h.repo.Clear(menu, connID)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "清空 AI 调用记录失败")
		return
	}
	resp.OK(c, gin.H{"deleted": n})
}
