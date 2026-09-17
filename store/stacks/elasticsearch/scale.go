package elasticsearch

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

// scale.go：冷热温模式的缩容保护与软下线（一机多容器口径）。
//
// 冷热温的缩容是两步：
//  1. ScaleInGuard —— 装配缩容主机集合后拦截「移除最后一台含 master 容器的主机」，集群不能失去选举能力；
//  2. DrainParams —— 在存活 leader 上执行官方软下线（排空分片 + 安全退场），集群回绿后才停容器。
//     节点名按容器粒度展开（n<seq>-<角色>，一台主机可能多个容器）。
//
// 只有冷热温需要这两步：集群模式返回 nil/false，走引擎通用「直接停服」路径。

// ScaleInGuard 实现 stackkit.ScaleInGuard：移除后剩余 master 容器为 0 时拒绝。
func (d *Driver) ScaleInGuard(ctx stackkit.ScaleCtx) error {
	if ctx.Mode != "cold_warm_hot" {
		return nil
	}
	removedIDs := map[int64]bool{}
	for _, id := range ctx.RemoveIDs {
		removedIDs[id] = true
	}
	remainMasters := 0
	if ctx.Instance != nil {
		for _, h := range ctx.Instance.Hosts {
			if removedIDs[h.HostID] {
				continue
			}
			if stackkit.Contains(cwhInstHostRoles(h), "master") {
				remainMasters++
			}
		}
	}
	if remainMasters == 0 {
		return fmt.Errorf("不允许移除最后一台含 master 容器的主机：集群将失去选举与仲裁能力")
	}
	return nil
}

// DrainParams 实现 stackkit.ScaleInDrainer：给出待排空的容器节点名与其中的 master 容器节点名
// （节点名 = n<seq>-<角色>，脚本据此在集群内先摘除分片再退场）。leader 选取与脚本执行由引擎负责。
func (d *Driver) DrainParams(ctx stackkit.DrainCtx) (map[string]string, bool) {
	if ctx.Mode != "cold_warm_hot" {
		return nil, false
	}
	var removeNodes, removeMasters []string
	for i := range ctx.Removed {
		h := ctx.Removed[i]
		prefix := "n" + strconv.Itoa(h.Seq)
		for _, r := range cwhRunHostRoles(h) {
			name := prefix + "-" + r
			removeNodes = append(removeNodes, name)
			if r == "master" {
				removeMasters = append(removeMasters, name)
			}
		}
	}
	sort.Slice(removeNodes, func(a, b int) bool { return removeNodes[a] < removeNodes[b] })
	sort.Slice(removeMasters, func(a, b int) bool { return removeMasters[a] < removeMasters[b] })
	return map[string]string{
		"remove_nodes":   strings.Join(removeNodes, ","),
		"remove_masters": strings.Join(removeMasters, ","),
	}, true
}
