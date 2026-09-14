// Package stackkit 定义套件的契约层：最小必选驱动接口 + 可选能力接口 + 无业务共享工具。
//
// 依赖方向（不可打破）：
//
//	model ← stackkit ← { store/registry.go, store/stacks/<key> } ← api/stack
//
// 本包只放契约与工具，禁止 import store 或 api/**；套件目录之间也禁止互相 import
// （ELFK 复用 ES 的 yml 素材走资源路径，不走代码依赖）。
package stackkit

import "infra-ops/model"

// 阶段常量：取值即既有引擎口径，不得改名（下游脚本与基线快照均按此冻结）。
const (
	PhaseNode      = "node"
	PhaseBootstrap = "bootstrap"
	PhaseScaleOut  = "scale_out"
	PhaseScaleIn   = "scale_in"
)

// Phases 全部合法阶段，顺序即引擎的判定顺序。
var Phases = []string{PhaseNode, PhaseBootstrap, PhaseScaleOut, PhaseScaleIn}

// ValidPhase 报告 phase 是否为已知阶段。
// 引擎据此把「未知阶段」与「该模式无此阶段脚本」分开报错，两者文案不同，不可合并。
func ValidPhase(phase string) bool {
	for _, p := range Phases {
		if p == phase {
			return true
		}
	}
	return false
}

// PhaseFile 一个阶段的可渲染描述：脚本路径 + 其引用的资源注入表。
type PhaseFile struct {
	// Path 相对 store 包的嵌入路径，如 stacks/redis/scripts/repl-node.sh。
	Path string
	// Assets 占位符注入表：@@KEY@@ → stacks/<key>/configs/xxx（同样相对 store 包）。
	Assets map[string]string
}

// Driver 套件最小必选契约（每个套件都实现，约 40 行样板）。
// 套件专属的可选行为通过 capability.go 的能力接口按需实现，未实现即走引擎通用默认。
type Driver interface {
	// Key 套件唯一键，与 model.StackBlueprint.Key 一致。
	Key() string
	// Blueprint 套件蓝图（模式 / 变量 / 分类），只读。
	Blueprint() model.StackBlueprint
	// Phase 返回某模式下某阶段的脚本描述；ok=false 表示该模式没有此阶段脚本。
	// phase 取 PhaseNode / PhaseBootstrap / PhaseScaleOut / PhaseScaleIn。
	Phase(mode, phase string) (PhaseFile, bool)
	// Pipeline 返回某模式的有序流水线阶段；返回空表示走传统 node→bootstrap 单步流程。
	Pipeline(mode string) []model.StackPhase
}
