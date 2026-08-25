package api

import (
	"encoding/json"
	"testing"
)

func TestMergeParamsP0(t *testing.T) {
	raw := json.RawMessage(`[
		{"name":"msg","label":"消息","default":"base_default","required":false},
		{"name":"port","label":"端口","default":"8080","required":false}
	]`)

	// 任务级默认 + 主机级覆盖 msg，port 继承默认
	merged, err := mergeParams(raw, map[string]string{"msg": "taskval"}, map[string]string{"msg": "hostval"})
	if err != nil {
		t.Fatal(err)
	}
	if merged["msg"] != "hostval" {
		t.Fatalf("主机覆盖应优先，got %q", merged["msg"])
	}
	if merged["port"] != "8080" {
		t.Fatalf("应继承模板默认，got %q", merged["port"])
	}

	// 缺少必填项应在渲染时报错（required 且默认值与覆盖均为空）
	rawReq := json.RawMessage(`[{"name":"token","label":"令牌","default":"","required":true}]`)
	if m, _ := mergeParams(rawReq, nil, nil); m["token"] != "" {
		t.Fatalf("默认应为空，got %q", m["token"])
	}
	if _, err := renderScript("echo {{token}}", rawReq, map[string]string{}); err == nil {
		t.Fatal("必填项为空应报错")
	}

	// 渲染后占位符被替换
	merged2, _ := mergeParams(raw, map[string]string{"msg": "hi"}, nil)
	out, err := renderScript("echo {{msg}}", raw, merged2)
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo hi" {
		t.Fatalf("渲染结果错误: %q", out)
	}
}
