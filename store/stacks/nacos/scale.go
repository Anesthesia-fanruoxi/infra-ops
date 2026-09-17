package nacos

import (
	"fmt"

	"infra-ops/store/stackkit"
)

// scale.go：Nacos 缩容保护（N1）。
//
// 通用层已拦「移除唯一成员」（Remain < 1）与引导机落点（boot 角色 scope==""，
// api/stack 的 planProtectedHosts），这里只补生产建议级拦截：缩到 3 台以下即失去
// 容错冗余——节点故障时剩余节点更少、客户端可用入口变窄（数据本身在共享 MySQL，不丢）。
func (d *Driver) ScaleGuard(ctx stackkit.ScaleCtx) error {
	if ctx.Mode != "cluster" || len(ctx.Active) == 0 {
		return nil
	}
	if ctx.Remain < 3 {
		return fmt.Errorf("Nacos 集群缩容后至少保留 3 台主机（本次将剩余 %d 台）：低于 3 台将失去容错冗余，生产建议保持 3 台以上奇数", ctx.Remain)
	}
	return nil
}
