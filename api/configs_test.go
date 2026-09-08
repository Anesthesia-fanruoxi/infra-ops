package api

import (
	"encoding/json"
	"strings"
	"testing"

	"infra-ops/model"
)

func TestMergeConfigs(t *testing.T) {
	raw := json.RawMessage(`[{"key":"nginx_conf","label":"nginx","file":"/x","required":false},{"key":"my_cnf","label":"mysql","file":"{{data_dir}}/my.cnf","required":true}]`)
	must := func(m map[string]string, err error) map[string]string { if err != nil { t.Fatal(err) }; return m }

	// 任务级 + 主机覆盖
	out := must(mergeConfigs(raw, map[string]string{"nginx_conf": "A", "my_cnf": "B"}, map[string]string{"nginx_conf": "A2"}))
	if out["nginx_conf"] != "A2" || out["my_cnf"] != "B" {
		t.Fatalf("got %v", out)
	}
	// required 缺失报错
	if _, err := mergeConfigs(raw, nil, nil); err == nil {
		t.Fatal("expected error for missing required config")
	}
	// 非 required 缺失可容忍
	r0 := json.RawMessage(`[{"key":"nginx_conf","label":"n","required":false}]`)
	if m := must(mergeConfigs(r0, nil, nil)); len(m) != 0 {
		t.Fatalf("expected empty, got %v", m)
	}
}

func TestApplyConfigOverrides(t *testing.T) {
	script := `#!/bin/bash
set -e
D=/data
# __DEPLOY_CONF__ my_cnf
cat > "$D/my.cnf" <<'EOF'
default
EOF
# __DEPLOY_CONF_END__ my_cnf
echo done
`
	tpl := &model.DeployTemplate{Configs: json.RawMessage(`[{"key":"my_cnf","label":"mysql my.cnf","file":"{{data_dir}}/my.cnf"}]`)}

	// 未提供内容 → 脚本原样保留
	if got := applyConfigOverrides(script, tpl, map[string]string{"data_dir": "/data"}); got != script {
		t.Fatalf("expected unchanged, got:\n%s", got)
	}

	// 提供内容 → 用 base64 覆盖写目标文件，去掉默认块
	rendered := applyConfigOverrides(script, tpl, map[string]string{"data_dir": "/data", "__cfg.my_cnf": "custom\n[mysqld]\nbuffer=1G\n"})
	if strings.Contains(rendered, "'default'") && !strings.Contains(rendered, "# __DEPLOY_CONF_END__ my_cnf") {
		t.Fatalf("suspicious: %s", rendered)
	}
	if !strings.Contains(rendered, "base64 -d)") {
		t.Fatalf("expected base64 write injected:\n%s", rendered)
	}
	if !strings.Contains(rendered, "'/data/my.cnf'") {
		t.Fatalf("expected rendered file path:\n%s", rendered)
	}
	if strings.Contains(rendered, "--\n'default'\n--") && strings.Contains(rendered, "cat > \"$D/my.cnf\"") {
		t.Fatalf("expected default block removed:\n%s", rendered)
	}
	// 目标路径 {{data_dir}} 渲染正确
	if !strings.Contains(rendered, "> '/data/my.cnf'") {
		t.Fatalf("file path not rendered: %s", rendered)
	}
}

func TestApplyConfigOverridesMissingAnchor(t *testing.T) {
	tpl := &model.DeployTemplate{Configs: json.RawMessage(`[{"key":"my_cnf","label":"mysql","file":"/x"}]`)}
	script := "no anchor here"
	out := applyConfigOverrides(script, tpl, map[string]string{"__cfg.my_cnf": "zzz"})
	if out != script {
		t.Fatalf("expected unchanged for missing anchor, got:\n%s", out)
	}
}