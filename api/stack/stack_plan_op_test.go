package stack

// create 路径回归闸门（2026-09-14 修复「开始部署」500 后补）。
//
// 背景：planForStackOp 的 create 分支曾直接 `decodeRolePlan(inst.RolePlanJSON)`，
// 而 create 时实例尚未落库（createAndRun 只在 op != "create" 时才装配 inst），
// 该表达式必然 nil 解引用 → 被 Recovery 兜成 500，前端表现为全套件「开始部署」报错。
// 同一写法在 planForStackOp（bigdata 分支）与 planForGenericOp（通用分支）各有一处。
//
// 为什么此前没有暴露：既有单测都是**直接调 planGenericRoles**，绕过了 planForStackOp
// 这个真实入口；而 UI 的 create 首次部署才会命中。本文件补上入口级覆盖。
//
// 用 snapCases 的合法入参逐个套件走真实入口，钉住两件事：
//  1. create 传 nil inst 不得 panic，且计划从 rev 1 起算；
//  2. 判空不能把增量语义一起吃掉——add_component 在既有计划上仍须 rev+1。

import (
	"testing"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// planForCreate 用快照用例的参数调真实入口 planForStackOp(op="create", inst=nil)。
func planForCreate(t *testing.T, h *stackHandler, caseIdx int, c snapCase) *model.RolePlan {
	t.Helper()
	d := store.FindBuiltinStack(c.Stack)
	if d == nil {
		t.Fatalf("%s：套件不存在 %s", c.Name, c.Stack)
	}
	mode := stackkit.ModeDef(d.Blueprint(), c.Mode)
	if mode == nil {
		t.Fatalf("%s：未知模式 %s/%s", c.Name, c.Stack, c.Mode)
	}
	params := map[string]string{}
	for k, v := range c.Params {
		params[k] = v
	}
	masterHostID := int64(0)
	if c.ManualMaster {
		masterHostID = 1
	}
	plan, err := h.planForStackOp("create", d, mode, nil, params, snapHosts(caseIdx, c), nil, masterHostID)
	if err != nil {
		t.Fatalf("%s：create 规划失败: %v", c.Name, err)
	}
	if plan == nil {
		t.Fatalf("%s：create 必须产出角色计划", c.Name)
	}
	return plan
}

// TestPlanForCreateNilInstance 全部套件：create 且 inst == nil 必须正常出计划。
func TestPlanForCreateNilInstance(t *testing.T) {
	h := &stackHandler{}
	covered := map[string]bool{}
	for i, c := range snapCases {
		if c.Op != "" && c.Op != "create" {
			continue // 只针对 create；其余 op 传入的 inst 非空
		}
		if c.PlanOnly {
			continue // 负例：期望 planner 直接报错，与 nil 解引用无关
		}
		plan := planForCreate(t, h, i, c)
		if plan.Rev != 1 {
			t.Fatalf("%s：create 无既有计划，rev 应为 1，实际 %d", c.Name, plan.Rev)
		}
		if len(plan.Hosts) == 0 {
			t.Fatalf("%s：计划主机行为空", c.Name)
		}
		covered[c.Stack] = true
	}
	// 覆盖度自检：装配表里的套件必须全部走到，漏一个即断言失效（防「测试假过」）
	for _, d := range store.ListStackDrivers() {
		if !covered[d.Key()] {
			t.Fatalf("套件 %s 未纳入本测试（快照用例缺失或过滤过宽）", d.Key())
		}
	}
}

// TestPlanForAddComponentBumpsRev 加装（inst 非空、已有计划）必须 rev+1。
// 判空只应挡住 create，不能把增量重规划语义一起挡掉。
func TestPlanForAddComponentBumpsRev(t *testing.T) {
	var idx = -1
	var c snapCase
	for i, sc := range snapCases {
		if sc.Stack == "bigdata" && !sc.PlanOnly {
			idx, c = i, sc
			break
		}
	}
	if idx < 0 {
		t.Fatal("快照用例中没有可用的大数据用例")
	}
	h := &stackHandler{}
	hosts := snapHosts(idx, c)

	d := store.FindBuiltinStack(c.Stack)
	mode := stackkit.ModeDef(d.Blueprint(), c.Mode)
	if mode == nil {
		t.Fatalf("未知模式 %s/%s", c.Stack, c.Mode)
	}
	params := map[string]string{}
	for k, v := range c.Params {
		params[k] = v
	}

	base := planForCreate(t, h, idx, c)
	base.Rev = 3 // 模拟实例上已物化的第 3 版计划
	inst := &model.StackInstance{
		ID: 9001, StackKey: c.Stack, Mode: c.Mode, RolePlanJSON: encodeRolePlan(base),
	}
	if inst.RolePlanJSON == "" {
		t.Fatal("encodeRolePlan 应能序列化既有计划")
	}

	plan, err := h.planForStackOp("add_component", d, mode, inst, params, hosts, nil, 0)
	if err != nil {
		t.Fatalf("add_component 规划失败: %v", err)
	}
	if plan == nil {
		t.Fatal("add_component 必须产出角色计划")
	}
	if plan.Rev != 4 {
		t.Fatalf("add_component 应在既有计划上 rev+1（3 → 4），实际 %d", plan.Rev)
	}
}
