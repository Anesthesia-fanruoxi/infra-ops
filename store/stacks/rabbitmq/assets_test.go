// assets_test.go：插件资产推导与需求声明的契约测试。
package rabbitmq

import (
	"os"
	"strings"
	"testing"
)

// 版本矩阵：各系列的文件版本与 release 标签（3.10 / 3.11 无 v 前缀）。
func TestRbPluginNeed(t *testing.T) {
	cases := []struct {
		image string
		ver   string
		ptag  string
	}{
		{"rabbitmq:3.13-management", "3.13.0", "v3.13.0"},
		{"rabbitmq:3.12.7", "3.12.0", "v3.12.0"},
		{"rabbitmq:3.11-management", "3.11.1", "3.11.1"},
		{"rabbitmq:3.10", "3.10.2", "3.10.2"},
		{"rabbitmq:4.0.7", "4.0.7", "v4.0.7"},
		{"rabbitmq:4.1", "4.1.0", "v4.1.0"},
		{"rabbitmq:4.2-management", "4.2.0", "v4.2.0"},
		// 私有仓库前缀不影响标签解析
		{"192.168.7.13:5000/rabbitmq:3.13-management", "3.13.0", "v3.13.0"},
		// 未知系列走 `${series}.0`（与 node.sh 的 * 分支一致）
		{"rabbitmq:4.3-management", "4.3.0", "v4.3.0"},
	}
	for _, c := range cases {
		need, ok := rbPluginNeed(c.image)
		if !ok {
			t.Fatalf("%s 推导失败", c.image)
		}
		if need.FileName != rbPluginBase+"-"+c.ver+".ez" {
			t.Fatalf("%s 文件名 = %s", c.image, need.FileName)
		}
		if !strings.Contains(need.FetchURLs[0], "/"+c.ptag+"/") {
			t.Fatalf("%s 首选下载地址未含标签 %s: %s", c.image, c.ptag, need.FetchURLs[0])
		}
	}
}

// 无法解析的标签不声明资产（由 node.sh 现场下载兜底）。
func TestRbPluginNeedUnparsable(t *testing.T) {
	for _, image := range []string{"rabbitmq:latest", "rabbitmq", "", "myhost:5000/rabbitmq"} {
		if _, ok := rbPluginNeed(image); ok {
			t.Fatalf("%s 不应推导出资产", image)
		}
	}
}

// 下载源顺序：加速代理优先、GitHub 直连垫底。
// 实测（2026-09-16 本机）raw github.com 直连超时，而代理 0.6~2.2s 可达——直连排首位会让
// 服务端代下白等一个建连超时、甚至重试耗尽整体失败，用户看到的就是「点了下载没反应」。
func TestRbPluginFetchOrder(t *testing.T) {
	need, ok := rbPluginNeed("rabbitmq:3.13-management")
	if !ok {
		t.Fatal("推导失败")
	}
	if len(need.FetchURLs) < 2 {
		t.Fatalf("下载源过少，代理兜底链已退化: %+v", need.FetchURLs)
	}
	for i, u := range need.FetchURLs {
		if !strings.Contains(u, rbPluginRepoURL) {
			t.Fatalf("第 %d 个地址未指向官方 release 归档: %s", i+1, u)
		}
	}
	if strings.HasPrefix(need.FetchURLs[0], rbPluginRepoURL) {
		t.Fatalf("首选下载地址仍是 GitHub 直连（国内不可达，应先走加速代理）: %s", need.FetchURLs[0])
	}
	last := need.FetchURLs[len(need.FetchURLs)-1]
	if !strings.HasPrefix(last, rbPluginRepoURL) {
		t.Fatalf("末位应为 GitHub 直连兜底（代理全挂时仍可直连）: %s", last)
	}
}

// 脚本侧轮换顺序必须与 FetchURLs 同族：node.sh 的目标机现场下载用同一批源，
// 两边不一致会出现「平台代下走代理、目标机却先卡直连超时」的割裂。这是人工同步点，故上锁。
func TestRbPluginProxyOrderMatchesScript(t *testing.T) {
	src, err := os.ReadFile("scripts/node.sh")
	if err != nil {
		t.Fatalf("读取 scripts/node.sh 失败: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "PROXIES=(")
	if i < 0 {
		t.Fatal("node.sh 未找到 PROXIES 定义")
	}
	line := body[i:]
	if j := strings.IndexAny(line, ")\r\n"); j >= 0 {
		line = line[:j+1]
	}
	// 空串代表 GitHub 直连，出现在首位即回到「先等建连超时」的老问题
	if strings.HasPrefix(line, `PROXIES=(""`) {
		t.Fatalf("node.sh 的 PROXIES 首位是直连（空串），应与 assets.go 一样代理优先: %s", line)
	}
	for _, p := range []string{"https://gh-proxy.com", "https://gh.ddlc.top", "https://ghfast.top"} {
		if !strings.Contains(line, p) {
			t.Fatalf("node.sh 的 PROXIES 缺代理 %s: %s", p, line)
		}
	}
}

// 需求声明：仅集群模式 + 开关开启时需要插件资产。
func TestRequiredAssetsDelayedPlugin(t *testing.T) {
	d := &Driver{}
	if got := d.RequiredAssets("cluster", map[string]string{"delayed_plugin": "false"}); got != nil {
		t.Fatalf("开关关闭不应声明资产: %+v", got)
	}
	if got := d.RequiredAssets("cluster", map[string]string{"delayed_plugin": "true"}); len(got) != 1 {
		t.Fatalf("开关开启应声明 1 项资产: %+v", got)
	}
	// 开关开启 + image 为空：回落蓝图默认镜像推导
	got := d.RequiredAssets("cluster", map[string]string{"delayed_plugin": "yes"})
	if len(got) != 1 || got[0].Version != "3.13.0" {
		t.Fatalf("默认镜像推导 = %+v", got)
	}
	if got[0].RemoteDir != "{{home_dir}}/plugins" || got[0].Key != rbPluginBase {
		t.Fatalf("资产落点异常: %+v", got[0])
	}
	// 非集群模式不声明（当前套件仅 cluster 模式，防御性断言）
	if got := d.RequiredAssets("single", map[string]string{"delayed_plugin": "true"}); got != nil {
		t.Fatalf("非集群模式不应声明资产: %+v", got)
	}
}
