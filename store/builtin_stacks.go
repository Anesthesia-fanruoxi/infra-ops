package store

import (
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// 本文件是套件目录化（任务 09）P7 收尾后的**薄壳**：只保留三件事 ——
//   - 蓝图列表（对外展示顺序 = 装配表登记顺序）；
//   - 按 key 查驱动（`FindBuiltinStack` 直接返回 stackkit.Driver，不再有兼容包装）；
//   - 阶段脚本渲染（读内嵌脚本 + @@ASSET@@ 资源注入，需访问 StackFS 故留在 store 包）。
//
// 单一事实源是各套件目录 store/stacks/<key>；蓝图与阶段表**没有第二份副本**。
// P1 的 BuiltinStack 兼容壳与 legacyStack 过渡驱动均已删除。

// DockerTemplateName 套件 Docker 前置所调用的内置模板名。
const DockerTemplateName = "安装 Docker"

// ListStackBlueprints 返回内置套件蓝图（不含脚本正文），顺序即装配表登记顺序。
func ListStackBlueprints() []model.StackBlueprint {
	ds := ListStackDrivers()
	out := make([]model.StackBlueprint, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Blueprint())
	}
	return out
}

// FindBuiltinStack 按 key 取内置套件驱动；不存在返回 nil。
// 返回类型即契约层 stackkit.Driver —— 引擎与套件目录共用同一个接口，无中间包装。
func FindBuiltinStack(key string) stackkit.Driver { return FindStackDriver(key) }

// LoadStackPhase 渲染某模式某阶段的脚本（读内嵌脚本 + @@ASSET@@ 资源注入）。
//
// 「未知阶段」与「该模式无此阶段脚本」的报错文案不同，且已被 P0 基线快照冻结，不得改动。
func LoadStackPhase(d stackkit.Driver, mode, phase string) (string, error) {
	if !stackkit.ValidPhase(phase) {
		return "", fmt.Errorf("未知阶段: %s", phase)
	}
	f, ok := d.Phase(mode, phase)
	if !ok {
		return "", fmt.Errorf("套件 %s 模式 %s 没有 %s 脚本", d.Key(), mode, phase)
	}
	b, err := StackFS.ReadFile(f.Path)
	if err != nil {
		return "", err
	}
	script := string(b)
	for key, asset := range f.Assets {
		c, err := StackFS.ReadFile(asset)
		if err != nil {
			return "", err
		}
		script = strings.ReplaceAll(script, "@@"+key+"@@", string(c))
	}
	return script, nil
}
