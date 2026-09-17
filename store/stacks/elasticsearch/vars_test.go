package elasticsearch

import (
	"encoding/json"
	"strconv"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// vars_test.go：规格档位注入（ExtraVars）的行为断言。
//
// 这里是「注入值」的权威校验点：界面显示多少、容器就按多少定堆，
// 靠的就是 __es_heap* 这些注入键。渲染脚本的结构校验在 script/check_es_tpl.py，
// 档位表本身的不变量在 sizing_test.go，三者互补不重复。

// runHost 造一台运行主机（ParamsJSON 即共享参数 + 主机参数合并后的结果）。
func runHost(id int64, seq int, ip, role string, params map[string]string) model.StackRunHost {
	b, _ := json.Marshal(params)
	return model.StackRunHost{ID: id, HostID: id, HostName: "n" + strconv.Itoa(seq),
		HostIP: ip, Role: role, Seq: seq, ParamsJSON: string(b)}
}

// TestExtraVarsCWHInjectsPerRoleHeap 冷热温（替换形态）：逐角色堆按档位注入，且不改动通用变量。
func TestExtraVarsCWHInjectsPerRoleHeap(t *testing.T) {
	hosts := []model.StackRunHost{
		runHost(1, 1, "10.0.6.1", "master", map[string]string{"roles": "master,coordinator", "sizing": "full"}),
		runHost(2, 2, "10.0.6.2", "worker", map[string]string{"roles": "data_hot", "sizing": "full"}),
	}
	d := New()
	ctx := stackkit.VarsCtx{Mode: "cold_warm_hot", Op: "create", Hosts: hosts,
		Extra: stackkit.ClusterExtra(hosts)}
	out := d.ExtraVars(ctx)
	if out == nil {
		t.Fatal("冷热温应返回替换表（非 nil）")
	}
	m := out[1]
	if m["__es_sizing"] != "full" || m["__es_sizing_label"] != "大力出奇迹" {
		t.Fatalf("档位注入错误: %v", m)
	}
	// 大力出奇迹档：hot 31g（compressed oops 上限）；master/协调合并行取原纯协调值 16g
	want := map[string]string{
		"__es_heap_master":      "16g",
		"__es_heap_coordinator": "16g",
		"__es_heap_data_hot":    "31g",
		"__es_heap_data_warm":   "16g",
		"__es_heap_data_cold":   "8g",
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("%s = %q，期望 %q", k, m[k], v)
		}
	}
	// 通用变量仍然齐备（替换形态必须自己重建完整表）
	for _, k := range []string{"__seed_hosts", "__cluster_size", "__es_nodes", "__es_bootstrap"} {
		if m[k] == "" {
			t.Fatalf("替换表缺少通用变量 %s", k)
		}
	}
	// __cluster_size 是**容器**总数（主机1 两个容器 + 主机2 一个容器 = 3），不是主机数
	if m["__es_nodes"] != "master,coordinator" || m["__cluster_size"] != "3" {
		t.Fatalf("按 roles 重算失败: __es_nodes=%q __cluster_size=%q", m["__es_nodes"], m["__cluster_size"])
	}
}

// TestExtraVarsClusterIncremental 集群模式（增补形态）：返回 nil 不替换，但就地追加档位与单节点堆。
func TestExtraVarsClusterIncremental(t *testing.T) {
	hosts := []model.StackRunHost{
		runHost(1, 1, "10.0.6.1", "master", map[string]string{"sizing": "mini"}),
		runHost(2, 2, "10.0.6.2", "worker", map[string]string{"sizing": "mini"}),
	}
	d := New()
	extra := stackkit.ClusterExtra(hosts)
	before := extra[1]["__seed_hosts"]
	if out := d.ExtraVars(stackkit.VarsCtx{Mode: "cluster", Op: "create", Hosts: hosts, Extra: extra}); out != nil {
		t.Fatal("集群模式应返回 nil（就地增补，不替换引擎通用表）")
	}
	m := extra[1]
	if m["__es_heap"] != "6g" { // mini 档数据节点 = 6g
		t.Fatalf("集群模式 __es_heap = %q，期望 6g", m["__es_heap"])
	}
	if m["__es_sizing_label"] != "小额尝鲜" {
		t.Fatalf("档位展示名 = %q", m["__es_sizing_label"])
	}
	if m["__seed_hosts"] != before {
		t.Fatal("增补形态不应改动引擎通用变量")
	}
	// 集群模式不注入逐角色堆（单容器，没有多角色的取用场景）
	if _, ok := m["__es_heap_data_hot"]; ok {
		t.Fatal("集群模式不应注入 __es_heap_data_hot")
	}
}

// TestExtraVarsSizingFallback 历史实例（参数里没有 sizing）与非法档位一律回落默认档。
func TestExtraVarsSizingFallback(t *testing.T) {
	stdHot := esRoleHeap(esSizingStandard, "data_hot")
	cases := []map[string]string{
		{"roles": "data_hot"},                       // 本次改造前部署的实例
		{"roles": "data_hot", "sizing": ""},         // 显式空
		{"roles": "data_hot", "sizing": "  "},       // 空白
		{"roles": "data_hot", "sizing": "huge"},     // 未知档位
		{"roles": "data_hot", "sizing": "STANDARD"}, // 大小写归一
	}
	for i, params := range cases {
		hosts := []model.StackRunHost{runHost(1, 1, "10.0.6.1", "worker", params)}
		out := New().ExtraVars(stackkit.VarsCtx{Mode: "cold_warm_hot", Op: "create", Hosts: hosts,
			Extra: stackkit.ClusterExtra(hosts)})
		if got := out[1]["__es_heap_data_hot"]; got != stdHot {
			t.Fatalf("用例 %d %v: 堆 = %q，期望回落默认档 %q", i, params, got, stdHot)
		}
	}
}

// TestExtraVarsEmptyHosts 主机为空（异常路径）不得 panic，且按默认档给值。
func TestExtraVarsEmptyHosts(t *testing.T) {
	d := New()
	if out := d.ExtraVars(stackkit.VarsCtx{Mode: "cluster"}); out != nil {
		t.Fatal("无主机时集群模式应返回 nil")
	}
	if out := d.ExtraVars(stackkit.VarsCtx{Mode: "cold_warm_hot"}); out != nil {
		t.Fatalf("无主机时冷热温不应造出空表: %v", out)
	}
}
