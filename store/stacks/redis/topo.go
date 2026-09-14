package redis

import (
	"fmt"

	"infra-ops/store/stackkit"
)

// topo.go：Redis 拓扑校验（原 api/stack/stack_topo.go 的 validateRedisTopology 原样迁入）。
//
// Redis 是唯一有专属拓扑规则的套件：主从 / 哨兵要求指定主节点，集群模式对副本倍数、
// 主机数与分片数有整除约束。返回的报错文案被用户与测试依赖，逐字不得改动。

// ValidateTopology 实现 stackkit.TopologyBuilder。
//
// 语义与拆分前一致：Redis 走**专属判定并直接返回**（不叠加引擎通用规则）——
// 因此「未知模式」在此报错，而不是回落到 MinHosts 判定。
func (d *Driver) ValidateTopology(ctx stackkit.TopoCtx) error {
	switch ctx.Mode {
	case "replication":
		if ctx.Hosts < 2 {
			return fmt.Errorf("主从模式至少需要 2 台主机")
		}
		return stackkit.MasterMustBeSelected(ctx.MasterID, ctx.HostIDs)
	case "sentinel":
		if ctx.Hosts < 3 {
			return fmt.Errorf("哨兵模式至少需要 3 台主机")
		}
		return stackkit.MasterMustBeSelected(ctx.MasterID, ctx.HostIDs)
	case "cluster":
		replicas := ctx.Replicas
		if replicas != 0 && replicas != 1 {
			return fmt.Errorf("集群副本数只能是 0（三主）或 1（三主三从）")
		}
		if replicas == 0 && ctx.Hosts < 3 {
			return fmt.Errorf("三主集群至少需要 3 台主机")
		}
		if replicas == 1 && (ctx.Hosts < 6 || ctx.Hosts%2 != 0) {
			return fmt.Errorf("三主三从至少需要 6 台主机且主机数为偶数")
		}
		div := replicas + 1
		if ctx.Hosts%div != 0 {
			return fmt.Errorf("主机数必须能被 %d 整除（replicas+1）", div)
		}
		if ctx.Hosts/div < 3 {
			return fmt.Errorf("集群至少需要 3 个主节点")
		}
	default:
		return fmt.Errorf("不支持的模式: %s", ctx.Mode)
	}
	return nil
}
