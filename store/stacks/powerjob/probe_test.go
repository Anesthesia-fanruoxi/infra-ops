package powerjob

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// clusterRaw 一份真实形态的 PowerJob 探活输出（引导节点：server-1 + MySQL；
// 节点容器名带运行期序号，故必须由 ScriptTail 现取）。
const clusterRaw = `__IO_BEGIN__
__IO_COMPOSE_BEGIN__
powerjob-server-1=running
powerjob-mysql=running
__IO_COMPOSE_END__
__IO_CTR__powerjob-mysql__=running|1|healthy|mysql:8.0|2026-09-15T02:00:00Z
__IO_CTR__powerjob-server-1__=running|1||powerjob/powerjob-server:latest|2026-09-15T02:00:00Z
__IO_PJ_CONSOLE__=200
__IO_PJ_AKKA__=1
__IO_END__
`

// memberRaw 成员节点（无 MySQL 容器；控制台未登录跳登录页 → 3xx，同样算已响应）。
const memberRaw = `__IO_BEGIN__
__IO_COMPOSE_BEGIN__
powerjob-server-2=running
__IO_COMPOSE_END__
__IO_CTR__powerjob-server-2__=running|1||powerjob/powerjob-server:latest|2026-09-15T02:00:00Z
__IO_PJ_CONSOLE__=302
__IO_PJ_AKKA__=1
__IO_END__
`

func probeCtx(mode string, params map[string]string) stackkit.ProbeCtx {
	if params == nil {
		params = map[string]string{"server_port": "7700", "akka_port": "10086", "home_dir": "/data/powerjob"}
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

// 引导节点探活：server + MySQL 两个「实例」、镜像「版本」、控制台 + 通信，component 归属正确。
func TestParseSuiteBoot(t *testing.T) {
	out := New().ParseSuite(probeCtx("cluster", nil), clusterRaw, nil)
	if len(out.Checks) != 5 {
		t.Fatalf("期望 5 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	if srv := findCheck(out.Checks, "实例", compPowerjob); srv == nil || !srv.OK || !strings.Contains(srv.Detail, "powerjob-server-1") {
		t.Fatalf("server 实例检查: %+v", out.Checks)
	}
	if db := findCheck(out.Checks, "实例", compMySQL); db == nil || !db.OK || !strings.Contains(db.Detail, "powerjob-mysql") {
		t.Fatalf("MySQL 实例检查: %+v", out.Checks)
	}
	if ver := findCheck(out.Checks, "版本", compPowerjob); ver == nil || ver.Detail != "powerjob/powerjob-server:latest" {
		t.Fatalf("版本检查应取 server 镜像: %+v", out.Checks)
	}
	if c := findCheck(out.Checks, "控制台", compPowerjob); c == nil || !c.OK || !strings.Contains(c.Detail, "200") {
		t.Fatalf("控制台 200 应判通过: %+v", out.Checks)
	}
	if a := findCheck(out.Checks, "通信", compPowerjob); a == nil || !a.OK || !strings.Contains(a.Detail, "10086") {
		t.Fatalf("Akka 可达应判通过: %+v", out.Checks)
	}
	// 未实测角色 ⇒ 交还引擎回落期望角色
	if out.LiveRoleFromOutput {
		t.Fatal("powerjob 无实测角色，不应声明 LiveRoleFromOutput")
	}
}

// 成员节点探活：只有 server 实例（无 MySQL），3xx 登录跳转同样判通过。
func TestParseSuiteMember(t *testing.T) {
	out := New().ParseSuite(probeCtx("cluster", nil), memberRaw, nil)
	if len(out.Checks) != 4 {
		t.Fatalf("期望 4 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	if db := findCheck(out.Checks, "实例", compMySQL); db != nil {
		t.Fatalf("成员机不应有 MySQL 检查: %+v", db)
	}
	if c := findCheck(out.Checks, "控制台", compPowerjob); c == nil || !c.OK || !strings.Contains(c.Detail, "302") {
		t.Fatalf("3xx 登录跳转应判通过: %+v", out.Checks)
	}
}

// 控制台无响应（curl 输出 000）⇒ 必须判失败，不能因为「没解析到」而静默通过。
func TestParseSuiteConsoleDown(t *testing.T) {
	raw := strings.Replace(clusterRaw, "__IO_PJ_CONSOLE__=200", "__IO_PJ_CONSOLE__=000", 1)
	out := New().ParseSuite(probeCtx("cluster", nil), raw, nil)
	c := findCheck(out.Checks, "控制台", compPowerjob)
	if c == nil || c.OK {
		t.Fatalf("000 应判失败: %+v", out.Checks)
	}
	if !strings.Contains(c.Detail, "无响应") {
		t.Fatalf("明细应写明无响应: %q", c.Detail)
	}
	if stackkit.ChecksOK(out.Checks) {
		t.Fatal("ChecksOK 应给出本机不通过")
	}
}

// 控制台行缺失（容器在跑但脚本没探到，如宿主机无 curl）⇒ 同样按失败处理。
func TestParseSuiteConsoleMissing(t *testing.T) {
	raw := strings.Replace(clusterRaw, "__IO_PJ_CONSOLE__=200\n", "", 1)
	out := New().ParseSuite(probeCtx("cluster", nil), raw, nil)
	if c := findCheck(out.Checks, "控制台", compPowerjob); c == nil || c.OK || !strings.Contains(c.Detail, "无响应") {
		t.Fatalf("缺失控制台行应判失败: %+v", out.Checks)
	}
}

// Akka 端口不可达（worker 接不进来，控制台却照常打开）⇒ 必须判失败。
func TestParseSuiteAkkaDown(t *testing.T) {
	raw := strings.Replace(clusterRaw, "__IO_PJ_AKKA__=1", "__IO_PJ_AKKA__=0", 1)
	out := New().ParseSuite(probeCtx("cluster", nil), raw, nil)
	a := findCheck(out.Checks, "通信", compPowerjob)
	if a == nil || a.OK || !strings.Contains(a.Detail, "不可达") {
		t.Fatalf("Akka 0 应判失败: %+v", out.Checks)
	}
	if stackkit.ChecksOK(out.Checks) {
		t.Fatal("ChecksOK 应给出本机不通过")
	}
}

// 空输出（无容器）：不应产出任何检查项，也不该凭空补「控制台 / 通信」。
func TestParseSuiteEmpty(t *testing.T) {
	out := New().ParseSuite(probeCtx("cluster", nil), "__IO_BEGIN__\n__IO_END__\n", nil)
	if len(out.Checks) != 0 {
		t.Fatalf("无容器时不应有检查项: %+v", out.Checks)
	}
}

// Containers/ScriptTail 契约：tail 里必须现取容器名（含 MySQL）、复用 inspect_ctr、
// 探控制台 HTTP 与 Akka 端口，端口取主机参数。
func TestScriptTail(t *testing.T) {
	tail := New().ScriptTail(probeCtx("cluster", map[string]string{"server_port": "8080", "akka_port": "10087"}))
	for _, want := range []string{"docker ps", "inspect_ctr", "powerjob-mysql", "__IO_PJ_CONSOLE__", "__IO_PJ_AKKA__", "_PJ_PORT='8080'", "_PJ_AKKA='10087'"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("tail 缺 %q:\n%s", want, tail)
		}
	}
}

// Summary：存活计数 + 控制台入口，且不得触发引擎的失败关键字（异常 / 未就绪 / 不符）。
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
	if hint != "http://10.0.0.1:7700/" {
		t.Fatalf("控制台入口: %q", hint)
	}
	for _, n := range notes {
		if strings.Contains(n, "异常") || strings.Contains(n, "未就绪") || strings.Contains(n, "不符") {
			t.Fatalf("正常汇总不应含失败关键字: %q", n)
		}
	}
}

// 端点：每台一条控制台入口，端口取主机参数，角色随主机。
func TestEndpoints(t *testing.T) {
	d := New()
	master := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.0.1", Role: "master"},
		Params: map[string]string{"server_port": "7700"},
	})
	if len(master) != 1 {
		t.Fatalf("应含且仅含控制台端点: %+v", master)
	}
	if master[0].URL != "http://10.0.0.1:7700/" || master[0].Component != compPowerjob || master[0].Role != "master" {
		t.Fatalf("控制台端点: %+v", master[0])
	}
	worker := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.0.2", Role: "worker"},
		Params: map[string]string{"server_port": "8080"},
	})
	if len(worker) != 1 || worker[0].URL != "http://10.0.0.2:8080/" || worker[0].Role != "worker" {
		t.Fatalf("成员端点: %+v", worker)
	}
}
