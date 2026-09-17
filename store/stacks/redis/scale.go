package redis

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"infra-ops/store/stackkit"
)

// scale.go：Redis 的缩容保护与缩容软下线（套件专属，与探活解耦）。
//
// 两项能力的分工（见 store/stackkit/capability.go）：
//   - ScaleGuard     写库前拦截「缩容后会失去仲裁/承载能力」的请求（三种模式各有下限）；
//   - ScaleInDrainer 集群缩容前，在存活成员上先把待移除节点的哈希槽迁走并 del-node，
//     再交由主流程停容器。未声明该能力时引擎降级为「直接停服」，哈希槽会残留在
//     已消失的节点上（cluster_state 可能变 fail），需要人工 --cluster del-node 收尾。

// ScaleGuard 实现 stackkit.ScaleGuard：
//   - 主从 / 哨兵：不得移除唯一主节点（移除后无从库可提升，失去写入点）；
//   - 哨兵：模式内每台主机各跑一个 Sentinel（PlanRoles 的 AddAll），缩容后哨兵个数即剩余主机数；
//     少于 3 台时 quorum（引擎按 len(hosts)/2+1 计算）无法达成，主节点故障时不能自动切换；
//   - 集群：至少保留 3 个节点（16384 个哈希槽需要 3 个主节点承载）。
func (d *Driver) ScaleGuard(ctx stackkit.ScaleCtx) error {
	removeSet := stackkit.IDSet(ctx.RemoveIDs)
	if ctx.Mode == "replication" || ctx.Mode == "sentinel" {
		remainMaster := 0
		for _, h := range ctx.Active {
			if removeSet[h.HostID] {
				continue
			}
			if h.Role == "master" {
				remainMaster++
			}
		}
		if remainMaster == 0 {
			return fmt.Errorf("不能移除唯一主节点")
		}
	}
	if ctx.Mode == "sentinel" {
		remainSentinel := 0
		for _, h := range ctx.Active {
			if !removeSet[h.HostID] {
				remainSentinel++
			}
		}
		if remainSentinel < 3 {
			return fmt.Errorf("哨兵模式缩容后至少保留 3 台主机（将剩余 %d 台，哨兵无法达成多数派仲裁，主节点故障时不能自动切换）", remainSentinel)
		}
	}
	if ctx.Mode == "cluster" && ctx.Remain < 3 {
		return fmt.Errorf("Redis 集群至少保留 3 个节点")
	}
	return nil
}

// DrainParams 实现 stackkit.ScaleInDrainer：仅集群模式需要软下线。
//
// 给出待移除节点的 ip:port 列表（脚本据此在集群内迁移哈希槽并 del-node）；
// 主从 / 哨兵返回 false —— 它们的缩容就是停掉实例，没有集群元数据需要收尾。
// 端口取各主机自己的 port 参数（集群模式允许逐主机覆盖），缺省 6379。
func (d *Driver) DrainParams(ctx stackkit.DrainCtx) (map[string]string, bool) {
	if ctx.Mode != "cluster" || len(ctx.Removed) == 0 {
		return nil, false
	}
	nodes := make([]string, 0, len(ctx.Removed))
	for i := range ctx.Removed {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(ctx.Removed[i].ParamsJSON), &p)
		port := stackkit.FirstNonEmpty(stackkit.TrimParam(p, "port"), "6379")
		nodes = append(nodes, ctx.Removed[i].HostIP+":"+port)
	}
	sort.Strings(nodes)
	return map[string]string{"remove_nodes": strings.Join(nodes, ",")}, true
}
