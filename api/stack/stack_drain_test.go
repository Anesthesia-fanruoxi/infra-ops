package stack

import (
	"regexp"
	"strings"
	"testing"

	"infra-ops/api/deploy"
	"infra-ops/store"
)

// 缩容软下线的运行期占位符必须真正注入：套件在 scale_in 脚本里写 {{remove_nodes}}，
// 该键不在蓝图变量声明内，只靠 RenderScript 会留下字面量，软下线静默失效
// （冷热温的 {{remove_nodes}} / {{remove_masters}} / {{self_ip}} 即此缺陷）。
func TestScaleInScriptRuntimeVarsInjected(t *testing.T) {
	d := store.FindBuiltinStack("redis")
	script, err := store.LoadStackPhase(d, "cluster", "scale_in")
	if err != nil {
		t.Fatal(err)
	}
	// 第一步：蓝图声明的变量（password / port）
	rendered, err := deploy.RenderScript(script, stackVarsJSON(d.Blueprint(), "cluster"),
		map[string]string{"password": "s3cret", "port": "6379"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "{{remove_nodes}}") {
		t.Fatal("前置条件不成立：remove_nodes 应由运行期注入，而非蓝图声明")
	}
	// 第二步：运行期注入（引擎在 stack_drain.go 里做的补齐）
	out := applyRuntimeVars(rendered, map[string]string{"self_ip": "10.0.0.9", "remove_nodes": "10.0.0.1:6379"})
	// 注意排除 docker 自己的 {{.Names}} 模板（{{ 后紧跟点号），只找未替换的 {{name}} 占位符
	if m := regexp.MustCompile(`\{\{[A-Za-z_]`).FindString(out); m != "" {
		t.Fatalf("仍有未替换占位符 %s:\n%s", m, out)
	}
	if !strings.Contains(out, `REMOVE_NODES="10.0.0.1:6379"`) {
		t.Fatalf("remove_nodes 未注入:\n%s", out)
	}
}

// 空键 / 空值不参与替换（避免把占位符静默换成空串）。
func TestApplyRuntimeVarsSkipsEmpty(t *testing.T) {
	got := applyRuntimeVars("a={{x}} b={{y}}", map[string]string{"x": "1", "y": "", "": "z"})
	if got != "a=1 b={{y}}" {
		t.Fatalf("got %q", got)
	}
	if applyRuntimeVars("k={{x}}", nil) != "k={{x}}" {
		t.Fatal("空表应原样返回")
	}
}
