package redis

import (
	"testing"

	"infra-ops/store/stackkit"
)

// topo_test.go：Redis 拓扑校验用例（原 api/stack/stack_validate_test.go 的
// TestValidateRedisTopology 原样迁入，断言逐条保留）。
func TestValidateTopology(t *testing.T) {
	d := New()
	ids := []int64{1, 2, 3, 4, 5, 6}
	check := func(mode string, hosts, replicas int, masterID int64, hostIDs []int64) error {
		return d.ValidateTopology(stackkit.TopoCtx{
			Mode: mode, Hosts: hosts, Replicas: replicas, MasterID: masterID, HostIDs: hostIDs,
		})
	}

	if err := check("replication", 1, 0, 1, ids[:1]); err == nil {
		t.Fatal("expected replication min 2")
	}
	if err := check("replication", 2, 0, 0, ids[:2]); err == nil {
		t.Fatal("expected master required")
	}
	if err := check("replication", 2, 0, 9, ids[:2]); err == nil {
		t.Fatal("expected master in selection")
	}
	if err := check("replication", 2, 0, 1, ids[:2]); err != nil {
		t.Fatal(err)
	}
	if err := check("cluster", 2, 0, 0, ids[:2]); err == nil {
		t.Fatal("expected cluster min 3")
	}
	if err := check("cluster", 3, 0, 0, ids[:3]); err != nil {
		t.Fatal(err)
	}
	if err := check("cluster", 5, 1, 0, ids[:5]); err == nil {
		t.Fatal("expected even hosts for replicas=1")
	}
	if err := check("cluster", 6, 1, 0, ids); err != nil {
		t.Fatal(err)
	}
	if err := check("cluster", 4, 2, 0, ids[:4]); err == nil {
		t.Fatal("expected replicas 0 or 1")
	}
	if err := check("sentinel", 2, 0, 1, ids[:2]); err == nil {
		t.Fatal("expected sentinel min 3")
	}
	if err := check("sentinel", 3, 0, 0, ids[:3]); err == nil {
		t.Fatal("expected sentinel master")
	}
	if err := check("sentinel", 3, 0, 1, ids[:3]); err != nil {
		t.Fatal(err)
	}
}

// TestDefaultsClusterReplicas 运行期（bootstrap/scale_out）按 0（三主）补全缺省的副本倍数，
// 创建期不改写、显式取值不覆盖、非集群模式不补全。
func TestDefaultsClusterReplicas(t *testing.T) {
	d := New()
	params := map[string]string{}
	if err := d.Defaults("bootstrap", "cluster", params); err != nil {
		t.Fatal(err)
	}
	if params["replicas"] != "0" {
		t.Fatalf("cluster replicas=%q", params["replicas"])
	}
	params["replicas"] = "1"
	if err := d.Defaults("bootstrap", "cluster", params); err != nil {
		t.Fatal(err)
	}
	if params["replicas"] != "1" {
		t.Fatalf("显式取值不应被覆盖: %q", params["replicas"])
	}
	create := map[string]string{}
	if err := d.Defaults("create", "cluster", create); err != nil {
		t.Fatal(err)
	}
	if _, ok := create["replicas"]; ok {
		t.Fatal("创建期不应改写实例参数")
	}
	other := map[string]string{}
	if err := d.Defaults("bootstrap", "replication", other); err != nil {
		t.Fatal(err)
	}
	if _, ok := other["replicas"]; ok {
		t.Fatal("非集群模式不应补全 replicas")
	}
}
