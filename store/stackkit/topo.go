package stackkit

import (
	"fmt"
	"strconv"
	"strings"

	"infra-ops/model"
)

// 本文件承载套件与引擎共用的**通用校验/取值助手**：两侧引用同一实现，
// 避免「引擎一份、套件一份」在搬迁中产生行为漂移（原文案与口径零变更）。

// MasterMustBeSelected 通用主节点校验：必须指定主节点，且主节点须在已选主机内。
// 原文案（api/stack/stack_topo.go 的 masterMustBeSelected）逐字保留。
func MasterMustBeSelected(masterID int64, hostIDs []int64) error {
	if masterID == 0 {
		return fmt.Errorf("请指定一台主节点")
	}
	for _, id := range hostIDs {
		if id == masterID {
			return nil
		}
	}
	return fmt.Errorf("主节点必须在已选主机中")
}

// ParseReplicas 解析副本数参数：空为 0，非法为 -1（调用方据此判定非法输入）。
func ParseReplicas(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// ModeDef 从蓝图取模式定义；不存在返回 nil。
//
// 与 (*PlanInput).ModeDef 同口径（按蓝图声明顺序取首个匹配），是引擎侧唯一的取模式入口：
// 引擎持有的是 stackkit.Driver，蓝图来自 Driver.Blueprint()，不再有 BuiltinStack.ModeDef 包装。
func ModeDef(bp model.StackBlueprint, mode string) *model.StackMode {
	for i := range bp.Modes {
		if bp.Modes[i].Key == mode {
			return &bp.Modes[i]
		}
	}
	return nil
}

// DeclaresVar 报告蓝图是否声明了名为 name 的参数（SharedVars ∪ HostVars）。
//
// 引擎用它做「声明式分流」，替代 `bp.Key == "bigdata"` 一类硬编码：例如
// 「声明了 components 参数 ⇒ 该套件以组件清单为部署单元」，与套件目录一一对应，
// 且新增同类套件时引擎无需改动。
func DeclaresVar(bp model.StackBlueprint, name string) bool {
	for _, v := range bp.SharedVars {
		if v.Name == name {
			return true
		}
	}
	for _, v := range bp.HostVars {
		if v.Name == name {
			return true
		}
	}
	return false
}
