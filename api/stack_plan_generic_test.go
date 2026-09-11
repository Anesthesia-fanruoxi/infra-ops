package api

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store"
)

func gh(id int64, ip, role string, seq int) model.StackRunHost {
	return model.StackRunHost{HostID: id, HostIP: ip, HostName: "h" + ip, Role: role, Seq: seq}
}

// 通用套件：redis 主从——master 手动指定（manual），其余 replica（rest）。
func TestPlanGenericRedisReplication(t *testing.T) {
	bp := store.FindBuiltinStack("redis")
	hosts := []model.StackRunHost{gh(1, "10.0.0.1", "master", 1), gh(2, "10.0.0.2", "worker", 2), gh(3, "10.0.0.3", "worker", 3)}
	plan, err := planGenericRoles(bp, hosts, PlanOptions{Op: "create", Mode: "replication", ManualMaster: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Hosts) != 3 {
		t.Fatalf("hosts = %d", len(plan.Hosts))
	}
	r1 := plan.Hosts[0].Roles
	if len(r1) != 1 || r1[0].Label != "Redis Master" || r1[0].Source != "manual" || r1[0].Scope != "" {
		t.Fatalf("host1 roles = %+v", r1)
	}
	for i := 1; i < 3; i++ {
		rr := plan.Hosts[i].Roles
		if len(rr) != 1 || rr[0].Label != "Redis Replica" || rr[0].Scope != "rest" || rr[0].Source != "auto" {
			t.Fatalf("host%d roles = %+v", i+1, rr)
		}
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("manual master 不应有警告: %v", plan.Warnings)
	}
	// 落点保护：仅 master 不可缩容
	protected, ok := planProtectedHosts(&plan)
	if !ok || len(protected) != 1 || !strings.Contains(protected["10.0.0.1"], "Redis Master") {
		t.Fatalf("protected = %v ok=%v", protected, ok)
	}
}

// 未显式指定主节点 → 自动落首台并出警告。
func TestPlanGenericAutoMasterWarning(t *testing.T) {
	bp := store.FindBuiltinStack("redis")
	hosts := []model.StackRunHost{gh(1, "10.0.0.1", "worker", 1), gh(2, "10.0.0.2", "worker", 2)}
	plan, err := planGenericRoles(bp, hosts, PlanOptions{Op: "create", Mode: "replication"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Hosts[0].Roles[0].Source != "auto" {
		t.Fatalf("auto master source = %s", plan.Hosts[0].Roles[0].Source)
	}
	found := false
	for _, w := range plan.Warnings {
		if strings.Contains(w, "10.0.0.1") && strings.Contains(w, "自动") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少自动落点警告: %v", plan.Warnings)
	}
}

// redis cluster：全员节点角色 + 自动分配提示；min hosts 校验。
func TestPlanGenericRedisCluster(t *testing.T) {
	bp := store.FindBuiltinStack("redis")
	hosts := []model.StackRunHost{gh(1, "10.0.0.1", "node", 1), gh(2, "10.0.0.2", "node", 2), gh(3, "10.0.0.3", "node", 3)}
	plan, err := planGenericRoles(bp, hosts, PlanOptions{Op: "create", Mode: "cluster", Params: map[string]string{"replicas": "1"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, h := range plan.Hosts {
		if len(h.Roles) != 1 || h.Roles[0].Scope != "all" || h.Roles[0].Label != "Redis Cluster 节点" {
			t.Fatalf("%s roles = %+v", h.HostIP, h.Roles)
		}
	}
	if len(plan.Warnings) == 0 || !strings.Contains(plan.Warnings[0], "replicas") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	if _, ok := planProtectedHosts(&plan); ok {
		t.Fatalf("redis cluster 全员角色不应有落点保护")
	}
	// sentinel 模式至少 3 台
	_, err = planGenericRoles(bp, hosts[:2], PlanOptions{Op: "create", Mode: "sentinel"})
	if err == nil || !strings.Contains(err.Error(), "至少需要 3 台") {
		t.Fatalf("sentinel min hosts err = %v", err)
	}
}

// kafka kraft：全员 broker+controller；enable_ui 时 UI 落首台（aux，不保护）。
func TestPlanGenericKafkaKraft(t *testing.T) {
	bp := store.FindBuiltinStack("kafka")
	hosts := []model.StackRunHost{gh(1, "10.0.0.1", "node", 1), gh(2, "10.0.0.2", "node", 2), gh(3, "10.0.0.3", "node", 3)}
	plan, err := planGenericRoles(bp, hosts, PlanOptions{Op: "create", Mode: "kraft", Params: map[string]string{"enable_ui": "yes"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for i, h := range plan.Hosts {
		want := 2
		if i > 0 {
			want = 1
		}
		if len(h.Roles) != want {
			t.Fatalf("%s roles = %+v", h.HostIP, h.Roles)
		}
	}
	if plan.Hosts[0].Roles[1].Label != "Kafka UI" || plan.Hosts[0].Roles[1].Scope != "aux" {
		t.Fatalf("UI role = %+v", plan.Hosts[0].Roles[1])
	}
	if _, ok := planProtectedHosts(&plan); ok {
		t.Fatalf("kraft 全员角色不应有落点保护")
	}
}

// elasticsearch：引导主节点（master,data）落点保护，其余 data 节点随成员伸缩。
func TestPlanGenericElasticsearch(t *testing.T) {
	bp := store.FindBuiltinStack("elasticsearch")
	hosts := []model.StackRunHost{gh(1, "10.0.0.2", "worker", 1), gh(2, "10.0.0.1", "master", 2)}
	plan, err := planGenericRoles(bp, hosts, PlanOptions{Op: "create", Mode: "cluster", ManualMaster: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// master 角色主机（10.0.0.1）才是引导节点，与引擎 stackClusterExtra 一致
	var bootHost *model.RolePlanHost
	for i := range plan.Hosts {
		if plan.Hosts[i].HostIP == "10.0.0.1" {
			bootHost = &plan.Hosts[i]
		}
	}
	if bootHost == nil || len(bootHost.Roles) != 1 || !strings.Contains(bootHost.Roles[0].Label, "引导主节点") {
		t.Fatalf("boot host roles = %+v", bootHost)
	}
	protected, ok := planProtectedHosts(&plan)
	if !ok || len(protected) != 1 || protected["10.0.0.1"] == "" {
		t.Fatalf("protected = %v", protected)
	}
}

// 扩容刷新：既有 master 保持引导落点，新机自动获得 rest 角色（redis replication）。
func TestPlanGenericScaleOutRefresh(t *testing.T) {
	bp := store.FindBuiltinStack("redis")
	existing := []model.StackRunHost{gh(1, "10.0.0.1", "master", 1), gh(2, "10.0.0.2", "worker", 2)}
	fresh := []model.StackRunHost{gh(3, "10.0.0.3", "worker", 3)}
	all := planFullMemberSet(
		[]model.StackInstanceHost{
			{HostID: 1, HostIP: "10.0.0.1", Role: "master", Seq: 1, Status: "active"},
			{HostID: 2, HostIP: "10.0.0.2", Role: "worker", Seq: 2, Status: "active"},
		}, fresh)
	plan, err := planGenericRoles(bp, all, PlanOptions{Op: "scale_out", Mode: "replication", ManualMaster: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	_ = existing
	if len(plan.Hosts) != 3 {
		t.Fatalf("hosts = %d", len(plan.Hosts))
	}
	if plan.Hosts[0].Roles[0].Label != "Redis Master" || plan.Hosts[2].Roles[0].Label != "Redis Replica" {
		t.Fatalf("scale_out roles: %+v / %+v", plan.Hosts[0].Roles, plan.Hosts[2].Roles)
	}
}
