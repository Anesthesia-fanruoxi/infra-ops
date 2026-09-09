package api

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

// registerTemplateServices 模板在某主机成功执行后，按其 services 声明登记/刷新服务清单。
// url 支持占位符：{{ip}} 替换为主机 IP，{{变量名}} 替换为该主机执行时的变量值。
// 模板无服务声明时静默跳过。
func registerTemplateServices(tplRepo *repo.DeployRepo, hostID int64, hostIP string, templateID int64, vars map[string]string) error {
	tpl, err := tplRepo.GetTemplate(templateID)
	if err != nil || tpl == nil {
		return err
	}
	if len(bytes.TrimSpace(tpl.Services)) == 0 {
		return nil
	}
	var svcs []model.TemplateService
	if err := json.Unmarshal(tpl.Services, &svcs); err != nil || len(svcs) == 0 {
		return nil
	}
	for _, s := range svcs {
		url := strings.ReplaceAll(s.URL, "{{ip}}", hostIP)
		for k, v := range vars {
			url = strings.ReplaceAll(url, "{{"+k+"}}", v)
		}
		if err := tplRepo.UpsertHostService(&model.HostService{
			HostID: hostID, HostIP: hostIP, ServiceName: s.Name,
			URL: url, Web: s.Web, TemplateID: templateID,
		}); err != nil {
			return err
		}
	}
	return nil
}

type serviceHandler struct {
	tplRepo *repo.DeployRepo
}

func NewServiceHandler(tplRepo *repo.DeployRepo) *serviceHandler {
	return &serviceHandler{tplRepo: tplRepo}
}

// List GET /api/services 全量服务清单（聚合所有主机的 web 服务，总览用）。
func (h *serviceHandler) List(c *gin.Context) {
	items, err := h.tplRepo.ListServices()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询服务清单失败")
		return
	}
	resp.OK(c, items)
}

// HostList GET /api/hosts/:id/services 单台主机的服务清单。
func (h *serviceHandler) HostList(c *gin.Context) {
	hostID := parseID(c)
	if hostID == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}
	items, err := h.tplRepo.HostServices(hostID)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询主机服务失败")
		return
	}
	resp.OK(c, items)
}
