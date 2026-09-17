package rocketmq

import (
	"fmt"

	"infra-ops/store/stackkit"
)

// scale.go：RocketMQ 的缩容保护（M4，套件专属，零通用层分支）。
//
// 与其余套件的差别：RocketMQ 主从无自动切换（非 DLedger），副本数 = 组内 slave 数。
// 缩到只剩 1 台 = 无副本 + NameServer 单点（与 master 同机）：
//   - master 宕机即整集群不可用，且未同步消息丢失；
//   - 通用层只拦「缩掉引导机」，成员侧的这层下限由这里补。
func (d *Driver) ScaleGuard(ctx stackkit.ScaleCtx) error {
	if ctx.Mode != "cluster" || len(ctx.Active) == 0 {
		return nil
	}
	if ctx.Remain < 2 {
		return fmt.Errorf("RocketMQ 集群缩容后至少保留 2 台主机（本次将剩余 %d 台）：单台时 Broker 无任何副本且无自动切换，master 故障即集群不可用", ctx.Remain)
	}
	return nil
}
