// hub 镜像主机辅助函数单测：镜像域名剥离、任务参数解析、脚本关键行。
package deploy

import "testing"

func TestTrimRegistryDomain(t *testing.T) {
	cases := map[string]string{
		"mysql:8.0":                                       "mysql:8.0",                     // 无路径，原样
		"bitnami/kafka:3.6":                               "bitnami/kafka:3.6",             // 首段非域名，原样
		"quay.io/coreos/etcd:v3.5.10":                     "coreos/etcd:v3.5.10",           // 去域名
		"docker.io/library/mysql:8.0":                     "library/mysql:8.0",             // 去 docker.io
		"docker.elastic.co/elasticsearch/elasticsearch:8": "elasticsearch/elasticsearch:8", // 去 elastic 域名
		"localhost:5000/a/b:1":                            "a/b:1",                         // localhost 视为域名
		"registry.example.com:5000/x:2":                   "x:2",                           // 域名带端口
		"":                                                "",
	}
	for in, want := range cases {
		if got := trimRegistryDomain(in); got != want {
			t.Fatalf("trimRegistryDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHubImagesFromParams(t *testing.T) {
	js := `{"__hub_images":"mysql:8.0, bitnami/kafka:3.6 ,quay.io/coreos/etcd:v3.5.10","other":"x"}`
	got := hubImagesFromParams(js)
	if len(got) != 3 || got[0] != "mysql:8.0" || got[1] != "bitnami/kafka:3.6" || got[2] != "quay.io/coreos/etcd:v3.5.10" {
		t.Fatalf("hubImagesFromParams 解析错误: %v", got)
	}
	if hubImagesFromParams(`{}`) != nil || hubImagesFromParams(`bad json`) != nil {
		t.Fatalf("空/非法参数应返回 nil")
	}
}

func TestHubFlagFromParams(t *testing.T) {
	if !hubFlagFromParams(`{"__hub_auto_insecure":"1"}`) {
		t.Fatalf("开关为 1 应为 true")
	}
	if hubFlagFromParams(`{}`) || hubFlagFromParams(`{"__hub_auto_insecure":"0"}`) {
		t.Fatalf("缺省/0 应为 false")
	}
}

func TestHubScriptsContainAddress(t *testing.T) {
	// 健康检查必须打本机地址且 -f（401 也算失败）
	if s := hubHealthScript("127.0.0.1:5000"); !contains(s, "curl -sfo", "http://127.0.0.1:5000/v2/") {
		t.Fatalf("健康检查脚本异常: %s", s)
	}
	// 预热脚本：inspect 跳过 → pull → tag → push
	if s := hubPrewarmScript("mysql:8.0", "127.0.0.1:5000/mysql:8.0"); !contains(s,
		"docker image inspect '127.0.0.1:5000/mysql:8.0'",
		"docker pull 'mysql:8.0'",
		"docker tag 'mysql:8.0' '127.0.0.1:5000/mysql:8.0'",
		"docker push '127.0.0.1:5000/mysql:8.0'") {
		t.Fatalf("预热脚本异常: %s", s)
	}
	// 信任配置脚本：必须带重启与复检
	if s := insecureConfigScript("10.0.0.9:5000"); !contains(s, "systemctl restart docker", "insecure-registries") {
		t.Fatalf("自动配置脚本异常: %s", s)
	}
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if !stringContains(s, sub) {
			return false
		}
	}
	return true
}

func stringContains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
