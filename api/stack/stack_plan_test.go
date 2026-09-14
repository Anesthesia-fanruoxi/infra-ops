package stack

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stacks/bigdata"
)

// planTestHosts 构造规划输入：ips[0] 为主节点；首主机参数携带 ha/components/masters。
func planTestHosts(t *testing.T, ips []string, ha bool, masters map[string]string) []model.StackRunHost {
	t.Helper()
	if len(ips) < 1 {
		t.Fatal("至少一台主机")
	}
	mb, _ := json.Marshal(masters)
	p0, _ := json.Marshal(map[string]string{
		"ha":         strconv.FormatBool(ha),
		"components": "hdfs,zookeeper,yarn,spark,flink,hbase,hive,metastore_db,trino",
		"masters":    string(mb),
	})
	hosts := []model.StackRunHost{{
		HostID: 1, HostName: ips[0], HostIP: ips[0], Role: "master", Seq: 1, ParamsJSON: string(p0),
	}}
	for i := 1; i < len(ips); i++ {
		p, _ := json.Marshal(map[string]string{"ha": strconv.FormatBool(ha), "components": "hdfs,zookeeper,yarn,spark,flink,hbase,hive,metastore_db,trino"})
		hosts = append(hosts, model.StackRunHost{
			HostID: int64(i + 1), HostName: ips[i], HostIP: ips[i], Role: "worker", Seq: i + 1, ParamsJSON: string(p),
		})
	}
	return hosts
}

// HA 自动分配：3 台主机全角色展开，全量 masters 投影完整。
func TestPlanBigdataRolesAutoHA(t *testing.T) {
	hosts := planTestHosts(t, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, true, nil)
	plan, err := planBigdataRoles(hosts, PlanOptions{Op: "create"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Ha {
		t.Fatal("应为 HA")
	}
	if got := plan.Masters["hdfs"]; got != "10.0.0.1" {
		t.Fatalf("nn1 = %q, want 10.0.0.1", got)
	}
	if got := plan.Masters["hdfs_nn2"]; got != "10.0.0.2" {
		t.Fatalf("nn2 = %q, want 10.0.0.2（seq 最小非主）", got)
	}
	if got := plan.Masters["yarn_rm2"]; got != "10.0.0.2" {
		t.Fatalf("rm2 = %q, want 10.0.0.2", got)
	}
	if got := plan.Masters["hdfs_jns"]; got != "10.0.0.1,10.0.0.2,10.0.0.3" {
		t.Fatalf("jns = %q", got)
	}
	if got := plan.Masters["hive_db"]; got != "10.0.0.1" {
		t.Fatalf("hive_db = %q", got)
	}
	// 全员角色：dn/nm 每台都有
	for _, h := range plan.Hosts {
		hasDN, hasNM := false, false
		for _, rr := range h.Roles {
			if rr.Role == "dn" && rr.Scope == "all" {
				hasDN = true
			}
			if rr.Role == "nm" && rr.Scope == "all" {
				hasNM = true
			}
		}
		if !hasDN || !hasNM {
			t.Fatalf("主机 %s 缺全员角色 dn/nm", h.HostIP)
		}
	}
	// 落点角色保护：nn1/nn2/jn 所在主机不可缩容
	protected, isHA := planProtectedHosts(&plan)
	if !isHA {
		t.Fatal("应为 HA 保护")
	}
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if protected[ip] == "" {
			t.Fatalf("主机 %s 应为受保护落点", ip)
		}
	}
	// 注入变量物化
	if plan.Injected["__nn2_ip"] != "10.0.0.2" {
		t.Fatalf("injected __nn2_ip = %q", plan.Injected["__nn2_ip"])
	}
	if plan.Injected["__hdfs_entry"] != "hdfs://ns1" {
		t.Fatalf("injected __hdfs_entry = %q", plan.Injected["__hdfs_entry"])
	}
}

// 手动 + 自动混合：手动项标注 manual，其余 auto；GeneratedBy=manual。
func TestPlanBigdataRolesManualMix(t *testing.T) {
	masters := map[string]string{"hdfs": "10.0.0.2", "hdfs_nn2": "10.0.0.1", "yarn_rm2": "10.0.0.3"}
	hosts := planTestHosts(t, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, true, masters)
	plan, err := planBigdataRoles(hosts, PlanOptions{Op: "create", Masters: masters,
		ManualKeys: []string{"hdfs", "hdfs_nn2", "yarn_rm2"}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.GeneratedBy != "manual" {
		t.Fatalf("GeneratedBy = %q", plan.GeneratedBy)
	}
	src := map[string]string{} // role -> source
	for _, h := range plan.Hosts {
		for _, rr := range h.Roles {
			src[rr.Role] = rr.Source
		}
	}
	if src["nn1"] != "manual" || src["nn2"] != "manual" || src["rm2"] != "manual" {
		t.Fatalf("手动项来源错误: %v", src)
	}
	if src["spark_m2"] != "auto" || src["zk"] != "auto" {
		t.Fatalf("自动项来源错误: %v", src)
	}
	// 手动指定的 nn2 不产生自动分配提示
	for _, w := range plan.Warnings {
		if strings.Contains(w, "NameNode (Standby)") {
			t.Fatalf("手动指定不应产生 Standby 自动分配提示: %s", w)
		}
	}
}

// 落点互斥：主备同机必须在规划期拦截。
func TestPlanBigdataRolesConflict(t *testing.T) {
	masters := map[string]string{"hdfs": "10.0.0.1", "hdfs_nn2": "10.0.0.1"}
	hosts := planTestHosts(t, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, true, masters)
	if _, err := planBigdataRoles(hosts, PlanOptions{Op: "create", Masters: masters,
		ManualKeys: []string{"hdfs", "hdfs_nn2"}}); err == nil {
		t.Fatal("nn1==nn2 应报错")
	}
	// IP 越界
	bad := map[string]string{"hdfs": "10.9.9.9"}
	hosts2 := planTestHosts(t, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, true, bad)
	if _, err := planBigdataRoles(hosts2, PlanOptions{Op: "create", Masters: bad,
		ManualKeys: []string{"hdfs"}}); err == nil {
		t.Fatal("IP 不在成员中应报错")
	}
	// HA 缺 ZooKeeper
	p0, _ := json.Marshal(map[string]string{"ha": "true", "components": "hdfs", "masters": "{}"})
	hosts3 := planTestHosts(t, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, true, nil)
	hosts3[0].ParamsJSON = string(p0)
	if _, err := planBigdataRoles(hosts3, PlanOptions{Op: "create"}); err == nil {
		t.Fatal("HA 缺 ZooKeeper 应报错")
	}
}

// 核心不变量（41 类误判的根治机制）：以全量 masters 投影为输入，对任意主机子集
// 运行 bigdata.ResolveRole，其落点与全量 Plan 逐字节一致。
func TestPlanProjectionSubsetDeterministic(t *testing.T) {
	ips := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5"}
	hosts := planTestHosts(t, ips, true, nil)
	plan, err := planBigdataRoles(hosts, PlanOptions{Op: "create"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// 任意子集（模拟扩容时运行主机集合只有新机 / 探活单机推导）
	subset := hosts[3:] // 仅 4、5 两台"新机"
	mb, _ := json.Marshal(plan.Masters)
	for i := range subset {
		p := map[string]string{"ha": "true", "masters": string(mb),
			"components": "hdfs,zookeeper,yarn,spark,flink,hbase,hive,metastore_db,trino"}
		b, _ := json.Marshal(p)
		subset[i].ParamsJSON = string(b)
	}
	r := bigdata.ResolveRole(subset)
	if r.Nn1 != plan.Masters["hdfs"] || r.Nn2 != plan.Masters["hdfs_nn2"] {
		t.Fatalf("子集推导 nn 漂移: nn1=%q nn2=%q", r.Nn1, r.Nn2)
	}
	if r.Rm1 != plan.Masters["yarn"] || r.Rm2 != plan.Masters["yarn_rm2"] {
		t.Fatalf("子集推导 rm 漂移: rm1=%q rm2=%q", r.Rm1, r.Rm2)
	}
	if strings.Join(r.JNs, ",") != plan.Masters["hdfs_jns"] {
		t.Fatalf("子集推导 jn 漂移: %q", strings.Join(r.JNs, ","))
	}
	if r.ZKIps != plan.Masters["zookeeper_ips"] {
		t.Fatalf("子集推导 zk 漂移: %q", r.ZKIps)
	}
}
