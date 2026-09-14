package stack

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// Runs GET /api/stacks/runs
func (h *stackHandler) Runs(c *gin.Context) {
	page, pageSize := shared.ParsePage(c)
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
