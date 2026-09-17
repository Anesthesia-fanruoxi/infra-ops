package nacos

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// clusterRaw 一份真实形态的 Nacos 探活输出（节点容器名带序号，故必须由 ScriptTail 现取）。
const clusterRaw = `__IO_BEGIN__
__IO_COMPOSE_BEGIN__
nacos-1=running
nacos-mysql=running
__IO_COMPOSE_END__
__IO_CTR__nacos-1__=running|1||nacos/nacos-server:v2.3.2|2026-09-15T02:00:00Z
__IO_CTR__nacos-mysql__=running|1|healthy|mysql:8.0|2026-09-15T02:00:00Z
__IO_NC_HEALTH__=200
__IO_END__
`

func probeCtx(mode string, params map[string]string) stackkit.ProbeCtx {
	if params == nil {
		params = map[string]string{"port": "8848", "home_dir": "/data/nacos"}
	}
	return stackkit.ProbeCtx{Mode: mode, Role: "master", HostIP: "10.0.0.1", Params: params}
}

func findCheck(checks []stackkit.Check, name, comp string) *stackkit.Check {
	for i := range checks {
		if checks[i].Name == name && (comp == "" || checks[i].Component == comp) {
			return &checks[i]
		}
	}
	return nil
}

// 容器名含序号 ⇒ Containers 必须为空（名字由 ScriptTail 现取）。
func TestContainersIsEmpty(t *testing.T) {
	if got := New().Containers(probeCtx("cluster", nil)); len(got) != 0 {
		t.Fatalf("容器名不可预知，应返回空: %v", got)
	}
}

// cluster 探活：节点 + MySQL 两个「实例」、镜像「版本」、readiness「就绪」，component 归属正确。
func TestParseSuiteCluster(t *testing.T) {
	out := New().ParseSuite(probeCtx("cluster", nil), clusterRaw, nil)
	if len(out.Checks) != 4 {
		t.Fatalf("期望 4 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	inst := findCheck(out.Checks, "实例", compNacos)
	if inst == nil || !inst.OK || !strings.Contains(inst.Detail, "nacos-1") {
		t.Fatalf("节点实例检查: %+v", out.Checks)
	}
	db := findCheck(out.Checks, "实例", compMySQL)
	if db == nil || !db.OK || !strings.Contains(db.Detail, "nacos-mysql") {
		t.Fatalf("MySQL 实例检查: %+v", out.Checks)
	}
	if ver := findCheck(out.Checks, "版本", compNacos); ver == nil || ver.Detail != "nacos/nacos-server:v2.3.2" {
		t.Fatalf("版本检查应取 nacos 镜像: %+v", out.Checks)
	}
	rd := findCheck(out.Checks, "就绪", compNacos)
	if rd == nil || !rd.OK || !strings.Contains(rd.Detail, "200") {
		t.Fatalf("readiness 200 应判通过: %+v", out.Checks)
	}
	// 未实测角色 ⇒ 交还引擎回落期望角色
	if out.LiveRoleFromOutput {
		t.Fatal("nacos 无实测角色，不应声明 LiveRoleFromOutput")
	}
}

// readiness 无响应（curl 输出 000）⇒ 就绪必须判失败，不能因为「没解析到」而静默通过。
func TestParseSuiteHealthDown(t *testing.T) {
	raw := strings.Replace(clusterRaw, "__IO_NC_HEALTH__=200", "__IO_NC_HEALTH__=000", 1)
	out := New().ParseSuite(probeCtx("cluster", nil), raw, nil)
	rd := findCheck(out.Checks, "就绪", compNacos)
	if rd == nil || rd.OK {
		t.Fatalf("000 应判失败: %+v", out.Checks)
	}
	if !strings.Contains(rd.Detail, "无响应") {
		t.Fatalf("明细应写明无响应: %q", rd.Detail)
	}
	if stackkit.ChecksOK(out.Checks) {
		t.Fatal("ChecksOK 应给出本机不通过")
	}
}

// 健康行缺失（容器在跑但脚本没探到）⇒ 同样按失败处理。
func TestParseSuiteHealthMissing(t *testing.T) {
	raw := strings.Replace(clusterRaw, "__IO_NC_HEALTH__=200\n", "", 1)
	out := New().ParseSuite(probeCtx("cluster", nil), raw, nil)
	rd := findCheck(out.Checks, "就绪", compNacos)
	if rd == nil || rd.OK || !strings.Contains(rd.Detail, "无响应") {
		t.Fatalf("缺失健康行应判失败: %+v", out.Checks)
	}
}

// 成员机（无 MySQL 容器、无节点）：不应产出任何实例检查，也不该凭空补「就绪」。
func TestParseSuiteEmpty(t *testing.T) {
	out := New().ParseSuite(probeCtx("cluster", nil), "__IO_BEGIN__\n__IO_END__\n", nil)
	if len(out.Checks) != 0 {
		t.Fatalf("无容器时不应有检查项: %+v", out.Checks)
	}
}

// Containers/ScriptTail 契约：tail 里必须现取容器名（含 MySQL）、复用 inspect_ctr、探 readiness。
func TestScriptTail(t *testing.T) {
	tail := New().ScriptTail(probeCtx("cluster", map[string]string{"port": "8848"}))
	for _, want := range []string{"docker ps", "inspect_ctr", "nacos-mysql", "readiness", "_NC_PORT='8848'"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("tail 缺 %q:\n%s", want, tail)
		}
	}
}

// Summary：给出存活计数的提示与 readiness 客户端命令。
func TestSummary(t *testing.T) {
	hosts := []stackkit.ProbeHostRow{
		{HostIP: "10.0.0.1", OK: true},
		{HostIP: "10.0.0.2", OK: false},
	}
	notes, hint := New().Summary(probeCtx("cluster", nil), hosts)
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "2 台成员中 1 台探活通过") {
		t.Fatalf("存活计数: %v", notes)
	}
	if !strings.Contains(hint, "10.0.0.1:8848") || !strings.Contains(hint, "readiness") {
		t.Fatalf("客户端命令: %q", hint)
	}
	// 汇总文案不得触发引擎的失败关键字（异常 / 未就绪 / 不符）
	for _, n := range notes {
		if strings.Contains(n, "异常") || strings.Contains(n, "未就绪") || strings.Contains(n, "不符") {
			t.Fatalf("正常汇总不应含失败关键字: %q", n)
		}
	}
}

// 端点：每台控制台；master 且无外部库时多一条 MySQL；worker 与外部库不含 MySQL。
func TestEndpoints(t *testing.T) {
	d := New()
	master := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.0.1", Role: "master"},
		Params: map[string]string{"port": "8848"},
	})
	if len(master) != 2 {
		t.Fatalf("master 应含控制台 + MySQL: %+v", master)
	}
	if master[0].URL != "http://10.0.0.1:8848/nacos/" || master[0].Component != compNacos || master[0].Role != "master" {
		t.Fatalf("控制台端点: %+v", master[0])
	}
	if master[1].URL != "jdbc:mysql://10.0.0.1:3306/nacos" || master[1].Component != compMySQL {
		t.Fatalf("MySQL 端点: %+v", master[1])
	}
	worker := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.0.2", Role: "worker"},
		Params: map[string]string{"port": "8848"},
	})
	if len(worker) != 1 || worker[0].URL != "http://10.0.0.2:8848/nacos/" {
		t.Fatalf("worker 只应含控制台: %+v", worker)
	}
	ext := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.0.1", Role: "master"},
		Params: map[string]string{"port": "8848", "db_host": "10.9.9.9:3306"},
	})
	if len(ext) != 1 {
		t.Fatalf("外部库时不应有 MySQL 端点: %+v", ext)
	}
}
