// vars.go：冷热温模式的变量注入（一机多容器）。
//
// 冷热温与「集群」模式的差别：角色矩阵的每个勾选格 = 该主机一个独立 ES 容器
// （master 与协调可同机叠加、数据层 hot/warm/cold 可叠加、数据层与 master/协调互斥）。
// 端口按角色固定偏移（master 9200 / 协调 9210 / hot 9220 / warm 9230 / cold 9240，
// transport = http + 100），数据节点端口仅供内部通信不对外登记。注入表以 roles 为权威
// 整体替换通用表，派生本机容器清单、种子地址与集群规模；其余通用变量由引擎渲染。
package elasticsearch

import (
	"encoding/json"
	"strconv"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// cwhDataRoles 未分配主机的兜底角色：自动归冷热温全数据层（行清空/存量空参数统一口径）。
var cwhDataRoles = []string{"data_hot", "data_warm", "data_cold"}

// cwhAllRoles 冷热温全部合法角色（与 validateCWHRoles 白名单、脚本 ALL_ROLES 同表）。
var cwhAllRoles = []string{"master", "coordinator", "data_hot", "data_warm", "data_cold"}

// cwhRolePort 冷热温多容器的 HTTP 端口（host 网络固定偏移，同机不冲突）。
func cwhRolePort(role string) string {
	switch role {
	case "master":
		return "9200"
	case "coordinator":
		return "9210"
	case "data_hot":
		return "9220"
	case "data_warm":
		return "9230"
	case "data_cold":
		return "9240"
	}
	return "9200"
}

// cwhRoleTransport 冷热温多容器的 transport 端口（HTTP + 100）。
func cwhRoleTransport(role string) string {
	switch role {
	case "master":
		return "9300"
	case "coordinator":
		return "9310"
	case "data_hot":
		return "9320"
	case "data_warm":
		return "9330"
	case "data_cold":
		return "9340"
	}
	return "9300"
}

// cwhRawHostRoles 解析主机参数中的 roles（不兜底：空返回 nil）。plan.go 用它区分
// 「自动模式」行（未勾选 → 预览为单条「自动分配（数据节点）」落点）。
func cwhRawHostRoles(p map[string]string) []string {
	var out []string
	for _, r := range strings.Split(p["roles"], ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// cwhHostRoles 解析主机参数中的 roles 为角色清单；未分配（空）兜底为冷热温全数据层。
func cwhHostRoles(p map[string]string) []string {
	if out := cwhRawHostRoles(p); len(out) > 0 {
		return out
	}
	return cwhDataRoles
}

// cwhRolesJSON 从主机 ParamsJSON 解析 roles 角色清单（两类主机结构体口径统一）。
func cwhRolesJSON(paramsJSON string) []string {
	p := map[string]string{}
	_ = json.Unmarshal([]byte(paramsJSON), &p)
	return cwhHostRoles(p)
}

// cwhRunHostRoles 运行时主机（StackRunHost）便捷包装（plan.go / DrainParams 用）。
func cwhRunHostRoles(h model.StackRunHost) []string { return cwhRolesJSON(h.ParamsJSON) }

// cwhRawRunHostRoles 运行时主机的裸 roles（不兜底：空返回 nil），plan.go 展开落点用。
func cwhRawRunHostRoles(h model.StackRunHost) []string {
	p := map[string]string{}
	_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
	return cwhRawHostRoles(p)
}

// cwhInstHostRoles 实例主机（StackInstanceHost）便捷包装（ScaleInGuard 用）。
func cwhInstHostRoles(h model.StackInstanceHost) []string { return cwhRolesJSON(h.ParamsJSON) }

// esSizingFromHosts 从主机参数里取规格档位。
//
// 共享变量 sizing 已被引擎合并进每台主机的参数表（api/stack/stack_create_hosts.go），
// 因此这里与 roles 同源读取：取首台非空值，缺失（历史实例）或未知一律回落默认档 standard。
func esSizingFromHosts(hosts []model.StackRunHost) string {
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		if v := strings.TrimSpace(p["sizing"]); v != "" {
			return esSizingNormalize(v)
		}
	}
	return esSizingStandard
}

// injectSizingVars 把档位与堆内存写入某台主机的注入表。
//
//   - 冷热温（一机多容器）：逐角色注入 __es_heap_<角色>，脚本按本机角色清单给每个容器取堆，
//     并据此做「本机容器堆合计 vs 宿主内存」的软校验；
//   - 集群模式（单容器，节点即数据节点）：注入单值 __es_heap。
func injectSizingVars(m map[string]string, mode, profile string) {
	m["__es_sizing"] = profile
	m["__es_sizing_label"] = esSizingLabel(profile)
	if mode != "cold_warm_hot" {
		m["__es_heap"] = esRoleHeap(profile, esRoleCluster)
		return
	}
	for _, role := range cwhAllRoles {
		m["__es_heap_"+role] = esRoleHeap(profile, role)
	}
}

// ExtraVars 实现 stackkit.VarInjector。
//
// 冷热温模式以每台主机勾选的 roles 为权威**整体替换**注入表（返回非 nil）；
// 集群模式**就地增补**（返回 nil，不替换引擎通用表），只追加档位与单节点堆内存。
func (d *Driver) ExtraVars(ctx stackkit.VarsCtx) map[int64]map[string]string {
	profile := esSizingFromHosts(ctx.Hosts)
	if ctx.Mode != "cold_warm_hot" {
		// 增补形态：在引擎已装配的通用表上追加（不替换，避免丢失 ClusterExtraMerged 的扩容语义）
		for _, h := range ctx.Hosts {
			m := ctx.Extra[h.HostID]
			if m == nil {
				continue
			}
			injectSizingVars(m, ctx.Mode, profile)
		}
		return nil
	}
	hosts := stackkit.MergedHosts(ctx.Existing, ctx.Hosts)
	if len(hosts) == 0 {
		return nil // 无主机（异常路径）：不造空表，避免把引擎通用表替换成空
	}
	base := stackkit.ClusterExtra(hosts)

	// 逐台解析角色清单；首个 master 容器（按 Seq 最小）作为集群初始引导节点。
	type hostRoles struct {
		host  model.StackRunHost
		roles []string
	}
	perHost := make([]hostRoles, 0, len(hosts))
	bootSeq, bootName := 0, ""
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		roles := cwhHostRoles(p)
		perHost = append(perHost, hostRoles{host: h, roles: roles})
		for _, r := range roles {
			if r == "master" && (bootSeq == 0 || h.Seq < bootSeq) {
				bootSeq, bootName = h.Seq, "n"+strconv.Itoa(h.Seq)+"-master"
			}
		}
	}

	// 种子地址只列 master 容器的 transport（固定 9300）；集群规模 = 全部容器数。
	var seed []string
	total := 0
	for _, it := range perHost {
		for _, r := range it.roles {
			total++
			if r == "master" {
				seed = append(seed, strconv.Quote(it.host.HostIP+":"+cwhRoleTransport(r)))
			}
		}
	}
	seedHosts := strings.Join(seed, ",")

	for _, it := range perHost {
		m := base[it.host.HostID]
		isMaster := false
		for _, r := range it.roles {
			if r == "master" {
				isMaster = true
			}
		}
		prefix := "n" + strconv.Itoa(it.host.Seq)
		m["__seed_hosts"] = seedHosts
		m["__cluster_size"] = strconv.Itoa(total)
		m["__es_nodes"] = strings.Join(it.roles, ",") // 本机容器角色清单，脚本按此循环拉起
		m["__es_is_master"] = strconv.FormatBool(isMaster)
		m["__es_bootstrap"] = strconv.FormatBool(bootName != "" && prefix+"-master" == bootName)
		m["__es_bootstrap_name"] = bootName
		// 规格档位与逐角色堆内存（sizing.go 预设表）：脚本据此给每个容器定堆，
		// 不再有 HEAP_XMS/HEAP_XMX/JVM_OPTS 之类的用户入参。
		injectSizingVars(m, ctx.Mode, profile)
		base[it.host.HostID] = m
	}
	return base
}
