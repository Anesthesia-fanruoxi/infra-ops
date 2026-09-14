package bigdata

import (
	"encoding/json"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// scale.go：大数据底座缩容保护（原 api/stack/stack_instance_validate.go 的 bigdata 分支）。

// ScaleGuard 大数据底座缩容保护：
//  1. 不得移除唯一主节点（与 redis/rocketmq 同侧）；
//  2. 承载 HA 落点角色的主机不可缩容——优先读已物化角色计划（引擎已先行拦截），
//     无计划时按 HA 角色矩阵现场推导（原 bigdataProtectedHosts）。
func (d *Driver) ScaleGuard(ctx stackkit.ScaleCtx) error {
	removeSet := stackkit.IDSet(ctx.RemoveIDs)
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
	// 已物化计划由引擎统一做落点保护（planProtectedHosts），此处只兜底无计划场景
	if ctx.Instance != nil && hasPlanHosts(ctx.Instance.RolePlanJSON) {
		return nil
	}
	label, ok := protectedHosts(ctx.Active)
	if !ok {
		return nil
	}
	for id := range removeSet {
		ip := ""
		for _, h := range ctx.Active {
			if h.HostID == id {
				ip = h.HostIP
				break
			}
		}
		if l, hit := label[ip]; hit {
			return fmt.Errorf("主机 %s（%s）承载落点角色，不可缩容；落点角色主机不在扩缩容范围内", ip, l)
		}
	}
	return nil
}

func hasPlanHosts(rolePlanJSON string) bool {
	s := strings.TrimSpace(rolePlanJSON)
	if s == "" {
		return false
	}
	var p model.RolePlan
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return false
	}
	return len(p.Hosts) > 0
}

// protectedHosts 返回 HA 实例的保护角色清单（map[ip]=角色标签）；非 HA 返回 ok=false。
func protectedHosts(active []model.StackInstanceHost) (map[string]string, bool) {
	runHosts := make([]model.StackRunHost, 0, len(active))
	for _, h := range active {
		if h.Status != "active" && h.Status != "" {
			continue
		}
		runHosts = append(runHosts, model.StackRunHost{
			HostID: h.HostID, HostName: h.HostName, HostIP: h.HostIP,
			Role: h.Role, Seq: h.Seq, ParamsJSON: h.ParamsJSON,
		})
	}
	r := ResolveRole(runHosts)
	if !r.Ha {
		return nil, false
	}
	label := map[string]string{}
	add := func(ip, name string) {
		if ip == "" {
			return
		}
		if label[ip] == "" {
			label[ip] = name
		} else {
			label[ip] += "+" + name
		}
	}
	add(r.Nn1, "NN1(Active)")
	add(r.Nn2, "NN2(Standby)")
	for _, jn := range r.JNs {
		add(jn, "JN")
	}
	add(r.Rm1, "RM1")
	add(r.Rm2, "RM2")
	add(r.SparkM1, "SparkM1")
	add(r.SparkM2, "SparkM2")
	add(r.FlinkJM1, "JM1")
	add(r.FlinkJm2, "JM2")
	add(r.HMaster1, "HMaster1")
	add(r.HMaster2, "HMaster2")
	for _, m := range r.MSs {
		add(m, "MS")
	}
	for _, s := range r.Hs2s {
		add(s, "HS2")
	}
	add(r.HiveDB, "MetaDB")
	for _, z := range filterEmpty(strings.Split(r.ZKIps, ",")) {
		add(z, "ZK")
	}
	return label, true
}
