package stackkit

import (
	"encoding/json"
	"strconv"
	"strings"

	"infra-ops/model"
)

// 本文件承载「通用集群变量注入」：由引擎与套件（如冷热温在通用变量之上叠加 roles 派生量）
// 共用同一实现，避免两侧各写一份而漂移。
//
// 变量名与取值口径即拆分前 api/stack/stack_vars.go 的 stackClusterExtra，
// 逐字段零变更（脚本消费、基线快照均已冻结）。

// ClusterExtra 生成 per-host 通用集群变量（种子节点/主节点/角色等），供脚本渲染。
func ClusterExtra(hosts []model.StackRunHost) map[int64]map[string]string {
	nodes := make([]string, 0, len(hosts))
	ips := make([]string, 0, len(hosts))
	masterIP, masterPort := "", ""
	hasMasterRole := false
	transport := "9300"
	ports := map[int64]int{}
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		port := strings.TrimSpace(p["port"])
		if port == "" {
			port = "6379"
		}
		n, _ := strconv.Atoi(port)
		ports[h.HostID] = n
		nodes = append(nodes, h.HostIP+":"+port)
		ips = append(ips, h.HostIP)
		if t := strings.TrimSpace(p["transport_port"]); t != "" {
			transport = t
		}
		if h.Role == "master" {
			hasMasterRole = true
			masterIP, masterPort = h.HostIP, port
		}
	}
	if masterIP == "" && len(hosts) > 0 {
		masterIP = hosts[0].HostIP
		masterPort = strconv.Itoa(ports[hosts[0].HostID])
		if masterPort == "0" {
			masterPort = "6379"
		}
	}
	seedParts := make([]string, 0, len(ips))
	for _, ip := range ips {
		seedParts = append(seedParts, strconv.Quote(ip+":"+transport))
	}
	seedHosts := strings.Join(seedParts, ", ")

	out := map[int64]map[string]string{}
	for _, h := range hosts {
		port := ports[h.HostID]
		if port == 0 {
			port = 6379
		}
		boot := h.Role == "master" || (!hasMasterRole && h.Seq == 1)
		bootStr, roles, brokerID := "false", "data", strconv.Itoa(h.Seq)
		if boot {
			bootStr, roles, brokerID = "true", "master, data", "0"
		}
		out[h.HostID] = map[string]string{
			"__nodes":         strings.Join(nodes, ","),
			"__node_ips":      strings.Join(ips, ","),
			"__cluster_size":  strconv.Itoa(len(hosts)),
			"__role":          h.Role,
			"__master_ip":     masterIP,
			"__master_port":   masterPort,
			"__bus_port":      strconv.Itoa(port + 10000),
			"__quorum":        strconv.Itoa(len(hosts)/2 + 1),
			"__is_bootstrap":  bootStr,
			"__join_host":     "rabbit@" + masterIP,
			"__node_name":     "n" + strconv.Itoa(h.Seq),
			"__seed_hosts":    seedHosts,
			"__roles":         roles,
			"__broker_id":     brokerID,
			"__process_roles": "broker,controller",
			"__voter_ips":     strings.Join(ips, ","),
			"__new_nodes":     "",
		}
	}
	return out
}

// ClusterExtraMerged 在扩容/加装时以存量+新增主机合并推导集群变量，并标记新节点不引导。
func ClusterExtraMerged(op string, existing, newHosts []model.StackRunHost) map[int64]map[string]string {
	all := MergedHosts(existing, newHosts)
	if len(all) == 0 {
		return map[int64]map[string]string{}
	}
	extra := ClusterExtra(all)
	if op != "scale_out" && op != "add_component" {
		return extra
	}
	newIDs := map[int64]bool{}
	newNodes := make([]string, 0, len(newHosts))
	for _, h := range newHosts {
		newIDs[h.HostID] = true
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		port := strings.TrimSpace(p["port"])
		if port == "" {
			port = "6379"
		}
		newNodes = append(newNodes, h.HostIP+":"+port)
	}
	existIPs := make([]string, 0, len(existing))
	for _, h := range existing {
		existIPs = append(existIPs, h.HostIP)
	}
	newNodesStr := strings.Join(newNodes, ",")
	voterIPs := strings.Join(existIPs, ",")
	for id, e := range extra {
		e["__new_nodes"] = newNodesStr
		if voterIPs != "" {
			e["__voter_ips"] = voterIPs
		}
		if newIDs[id] {
			e["__is_bootstrap"] = "false"
			e["__process_roles"] = "broker"
		}
		extra[id] = e
	}
	return extra
}

// MergedHosts 依次合并 existing 与 newHosts（按 HostID 去重，先出现的顺序优先）。
// 供扩容/加装场景在「存量 + 新增」全集上推导集群变量与角色。
func MergedHosts(existing, newHosts []model.StackRunHost) []model.StackRunHost {
	byID := map[int64]model.StackRunHost{}
	order := make([]int64, 0, len(existing)+len(newHosts))
	for _, h := range existing {
		if _, ok := byID[h.HostID]; !ok {
			order = append(order, h.HostID)
		}
		byID[h.HostID] = h
	}
	for _, h := range newHosts {
		if _, ok := byID[h.HostID]; !ok {
			order = append(order, h.HostID)
		}
		byID[h.HostID] = h
	}
	all := make([]model.StackRunHost, 0, len(order))
	for _, id := range order {
		all = append(all, byID[id])
	}
	return all
}
