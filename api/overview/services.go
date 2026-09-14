package overview

import (
	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/store/repo"
)

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
	hostID := shared.ParseID(c)
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
