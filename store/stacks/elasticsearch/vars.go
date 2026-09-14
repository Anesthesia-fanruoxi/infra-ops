package elasticsearch

import (
	"encoding/json"
	"strconv"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// vars.go：冷热温模式的变量注入（原 api/stack/stack_vars_cwh.go 原样迁入）。
//
// 冷热温与「集群」模式的差别在于角色不是平台隐式给的，而是每台主机在界面上勾选的
// （主机参数 roles）。因此注入表必须**以 roles 为权威整体替换**通用表，派生
// node.roles 与「初识 master」等变量；其余通用变量（seed_hosts/node_name/cluster_size）
// 复用 stackkit.ClusterExtra，保证与其它套件同源。

// ExtraVars 实现 stackkit.VarInjector。
//
// 仅冷热温模式接管注入表（返回非 nil ⇒ 整体替换）；「集群」模式返回 nil，沿用引擎通用表
// ——与拆分前「仅冷热温走 stackCWHExtraMerged」的分流逐字一致。
func (d *Driver) ExtraVars(ctx stackkit.VarsCtx) map[int64]map[string]string {
	if ctx.Mode != "cold_warm_hot" {
		return nil
	}
	return cwhExtra(stackkit.MergedHosts(ctx.Existing, ctx.Hosts))
}

// cwhExtra 以每台主机 roles 参数为权威来源派生 node.roles 与初识 master 变量。
func cwhExtra(hosts []model.StackRunHost) map[int64]map[string]string {
	base := stackkit.ClusterExtra(hosts)
	bootName, bootSeq := "", 0
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		if strings.Contains(p["roles"], "master") && (bootSeq == 0 || h.Seq < bootSeq) {
			bootSeq, bootName = h.Seq, "n"+strconv.Itoa(h.Seq)
		}
	}
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		isMaster := strings.Contains(p["roles"], "master")
		name := "n" + strconv.Itoa(h.Seq)
		m := base[h.HostID]
		m["__es_roles"] = cwhNodeRoles(p["roles"])
		m["__es_is_master"] = strconv.FormatBool(isMaster)
		m["__es_bootstrap"] = strconv.FormatBool(bootName != "" && name == bootName)
		m["__es_bootstrap_name"] = bootName
		base[h.HostID] = m
	}
	return base
}

// cwhNodeRoles 将冷热温每台主机的 roles csv 映射为 ES node.roles 列表体（yml 用）。
// master→"master"；纯协调(coordinator)→空（node.roles: [] 为纯协调）；数据层→按勾选叠加 data_hot/warm/cold。
func cwhNodeRoles(rolesCSV string) string {
	var parts []string
	for _, r := range strings.Split(rolesCSV, ",") {
		r = strings.TrimSpace(r)
		switch r {
		case "master":
			return `"master"`
		case "coordinator":
			// 纯协调：不落 data，node.roles 为空数组
		default:
			if r != "" {
				parts = append(parts, strconv.Quote(r))
			}
		}
	}
	return strings.Join(parts, ",")
}
