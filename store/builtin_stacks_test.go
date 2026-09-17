package store

// 任务 09（套件目录化拆分）契约层自检。
//
// 覆盖四件事：
//  1. 装配表自检：每个套件的每个模式都必须能取到并读到 node 脚本（embed 收紧后的回归闸门）；
//  2. embed 范围：StackFS 只能含 scripts/ 与 configs/，绝不能含 .go 源码；
//  3. 议题④联动：部署模板中直接指向套件脚本的 3 条路径改走统一读取入口后仍可读；
//  4. P7（B10）契约冒烟：Blueprint 合法性 / Phase 可读 / 缺脚本文案 / PlanRoles 确定性，
//     以及 9 套件全部实现 stackkit.RolePlanner —— 引擎侧兼容壳删除后，驱动接口是唯一契约面。

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// TestStackRegistrySelfCheck 装配表自检（与启动期同一实现）。
func TestStackRegistrySelfCheck(t *testing.T) {
	if err := VerifyStackRegistry(); err != nil {
		t.Fatalf("套件装配表自检未通过: %v", err)
	}
}

// TestStackRegistryCoverage 装配表覆盖 9 个内置套件且顺序稳定（顺序即前端卡片顺序）。
func TestStackRegistryCoverage(t *testing.T) {
	want := []string{"redis", "bigdata", "kafka", "elasticsearch", "rabbitmq", "rocketmq", "nacos", "powerjob", "elfk"}
	got := ListStackKeys()
	if len(got) != len(want) {
		t.Fatalf("套件数量 %d，期望 %d（%v）", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个套件为 %s，期望 %s（顺序即对外展示顺序）", i+1, got[i], want[i])
		}
	}
	for _, key := range want {
		d := FindStackDriver(key)
		if d == nil {
			t.Fatalf("套件 %s 未登记", key)
		}
		if d.Key() != key {
			t.Fatalf("套件 %s 的驱动 key 为 %s", key, d.Key())
		}
	}
	if FindStackDriver("不存在的套件") != nil {
		t.Fatal("未知套件应返回 nil")
	}
	if FindBuiltinStack("不存在的套件") != nil {
		t.Fatal("未知套件应返回 nil")
	}
}

// TestStackFSExcludesGoSource embed 收紧：StackFS 只能含脚本与配置。
func TestStackFSExcludesGoSource(t *testing.T) {
	n := 0
	err := fs.WalkDir(StackFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		n++
		// 套件目录内的 .go 实现绝不能进二进制
		if strings.HasSuffix(path, ".go") {
			t.Errorf("StackFS 内出现 Go 源码: %s", path)
		}
		if !strings.Contains(path, "/scripts/") && !strings.Contains(path, "/configs/") {
			t.Errorf("StackFS 内出现非 scripts/configs 资源: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("StackFS 为空，embed 模式可能写错")
	}
}

// TestBuiltinTemplateSuiteScriptsReadable 部署模板复用套件脚本的 3 条路径必须可读。
func TestBuiltinTemplateSuiteScriptsReadable(t *testing.T) {
	for _, p := range []string{
		"stacks/rocketmq/scripts/namesrv.sh",
		"stacks/rocketmq/scripts/broker.sh",
		"stacks/rabbitmq/scripts/node.sh",
	} {
		if _, err := readAsset(p); err != nil {
			t.Fatalf("套件脚本 %s 不可读（embed 收紧后读取入口未接好）: %v", p, err)
		}
	}
	if _, err := readAsset("builtin/install-docker.sh"); err != nil {
		t.Fatalf("builtin/ 前缀未路由到 BuiltinFS: %v", err)
	}
}

// TestBuiltinTemplatesLoadable 全部内置模板（含复用套件脚本的 3 条）都必须能装载。
func TestBuiltinTemplatesLoadable(t *testing.T) {
	if len(builtinTemplates) == 0 {
		t.Fatal("内置模板列表为空")
	}
	for _, tpl := range builtinTemplates {
		if _, err := loadBuiltinScript(tpl); err != nil {
			t.Fatalf("模板 %s 装载失败: %v", tpl.name, err)
		}
	}
}

// TestStackDriverBlueprintContract 蓝图契约（registry 全覆盖）：
// key / 名称 / 分类 / 模式 / 脚本路径 / 流水线阶段都必须自洽，且脚本在 embed 内可读。
//
// 这是 P7 删壳后的核心闸门：引擎不再持有 BuiltinStack 字段面，任何套件蓝图写错都会在这里暴露，
// 而不是等到部署期。
func TestStackDriverBlueprintContract(t *testing.T) {
	validTargets := map[string]bool{"": true, "all": true, "leader": true}
	for _, d := range ListStackDrivers() {
		key := d.Key()
		bp := d.Blueprint()
		if bp.Key != key {
			t.Fatalf("套件 %s 的蓝图 key 为 %q", key, bp.Key)
		}
		if strings.TrimSpace(bp.Name) == "" {
			t.Fatalf("套件 %s 缺少名称", key)
		}
		if bp.Category != "service" && bp.Category != "platform" {
			t.Fatalf("套件 %s 分类非法: %q", key, bp.Category)
		}
		if len(bp.Modes) == 0 {
			t.Fatalf("套件 %s 未声明模式", key)
		}
		for _, m := range bp.Modes {
			if m.Key == "" || m.Label == "" {
				t.Fatalf("套件 %s 存在 key/label 为空的模式: %+v", key, m)
			}
			if m.MinHosts < 1 {
				t.Fatalf("套件 %s 模式 %s 的最小主机数非法: %d", key, m.Key, m.MinHosts)
			}
			// 每个模式都必须能取到 node 脚本，且脚本归本套件目录所有
			f, ok := d.Phase(m.Key, stackkit.PhaseNode)
			if !ok || f.Path == "" {
				t.Fatalf("套件 %s 模式 %s 缺少 node 脚本", key, m.Key)
			}
			if !strings.HasPrefix(f.Path, "stacks/"+key+"/") {
				t.Fatalf("套件 %s 模式 %s 的 node 脚本路径越出套件目录: %s", key, m.Key, f.Path)
			}
			if _, err := StackFS.ReadFile(f.Path); err != nil {
				t.Fatalf("套件 %s 模式 %s 的 node 脚本不可读(%s): %v", key, m.Key, f.Path, err)
			}
			for ph, asset := range f.Assets {
				if _, err := StackFS.ReadFile(asset); err != nil {
					t.Fatalf("套件 %s 模式 %s 的注入资源 %s(%s) 不可读: %v", key, m.Key, ph, asset, err)
				}
			}
			// 未知阶段一律 ok=false（引擎据 ValidPhase 把两种报错分开，驱动不得兜底返回脚本）
			if _, ok := d.Phase(m.Key, "不存在的阶段"); ok {
				t.Fatalf("套件 %s 模式 %s 对未知阶段返回了脚本", key, m.Key)
			}
			// 流水线阶段自洽：key/label 非空、key 不重复、Target 取值合法
			seen := map[string]bool{}
			for _, ph := range d.Pipeline(m.Key) {
				if ph.Key == "" || ph.Label == "" {
					t.Fatalf("套件 %s 模式 %s 存在 key/label 为空的流水线阶段: %+v", key, m.Key, ph)
				}
				if seen[ph.Key] {
					t.Fatalf("套件 %s 模式 %s 的流水线阶段 %s 重复", key, m.Key, ph.Key)
				}
				seen[ph.Key] = true
				if !validTargets[ph.Target] {
					t.Fatalf("套件 %s 模式 %s 的阶段 %s Target 非法: %q", key, m.Key, ph.Key, ph.Target)
				}
			}
		}
	}
}

// TestLoadStackPhaseErrorContract 冻结两种报错文案：
//   - 「未知阶段」由 ValidPhase 判定，与套件无关；
//   - 「该模式无此阶段脚本」必须带套件 key / 模式 / 阶段三段信息（P0 渲染基线逐字节依赖）。
func TestLoadStackPhaseErrorContract(t *testing.T) {
	if _, err := LoadStackPhase(FindBuiltinStack("redis"), "replication", "不存在的阶段"); err == nil ||
		err.Error() != "未知阶段: 不存在的阶段" {
		t.Fatalf("「未知阶段」文案被改动: %v", err)
	}
	missing := 0
	for _, d := range ListStackDrivers() {
		for _, m := range d.Blueprint().Modes {
			for _, ph := range stackkit.Phases {
				if _, ok := d.Phase(m.Key, ph); ok {
					continue
				}
				missing++
				_, err := LoadStackPhase(d, m.Key, ph)
				want := fmt.Sprintf("套件 %s 模式 %s 没有 %s 脚本", d.Key(), m.Key, ph)
				if err == nil || err.Error() != want {
					t.Fatalf("%s/%s/%s 的缺脚本文案被改动: got=%v want=%q", d.Key(), m.Key, ph, err, want)
				}
			}
		}
	}
	if missing == 0 {
		t.Fatal("未找到任何「该模式无此阶段脚本」样本，断言已失效")
	}
}

// TestStackDriverPlanRolesDeterministic 9 套件全部实现 stackkit.RolePlanner，且角色规划是确定性函数
// （同输入两次调用结果逐字节相同）。
//
// 引擎侧 planRolesInEngine 只剩通用兜底，套件分支已归 0；本测试保证「规划在套件侧」这一点不回退，
// 同时保证物化到库里的 RolePlan 可复现（增量刷新依赖它）。
func TestStackDriverPlanRolesDeterministic(t *testing.T) {
	hosts := []model.StackRunHost{
		{HostID: 1, HostName: "n1", HostIP: "10.0.0.1", Seq: 1},
		{HostID: 2, HostName: "n2", HostIP: "10.0.0.2", Seq: 2},
		{HostID: 3, HostName: "n3", HostIP: "10.0.0.3", Seq: 3},
	}
	planned := 0
	for _, d := range ListStackDrivers() {
		p, ok := d.(stackkit.RolePlanner)
		if !ok {
			t.Fatalf("套件 %s 未实现 stackkit.RolePlanner：角色规划仍在引擎侧", d.Key())
		}
		for _, m := range d.Blueprint().Modes {
			in := func() stackkit.PlanInput {
				return stackkit.PlanInput{
					Blueprint: d.Blueprint(),
					Op:        "create",
					Mode:      m.Key,
					Params:    map[string]string{"components": "hdfs", "ha": "false"},
					Hosts:     hosts,
					Roles:     stackkit.NewRoles(hosts),
				}
			}
			in1 := in()
			p1, e1 := p.PlanRoles(in1)
			in2 := in()
			p2, e2 := p.PlanRoles(in2)
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("套件 %s 模式 %s 两次规划成败不一致: %v / %v", d.Key(), m.Key, e1, e2)
			}
			if e1 != nil {
				continue
			}
			p1.GeneratedAt, p2.GeneratedAt = "", ""
			b1, _ := json.Marshal(p1)
			b2, _ := json.Marshal(p2)
			if string(b1) != string(b2) {
				t.Fatalf("套件 %s 模式 %s 角色规划不确定:\n%s\n%s", d.Key(), m.Key, b1, b2)
			}
			// 驱动必须二选一：自建主机行（清单式，如 bigdata），或用落点助手填角色（引擎展开）。
			// 两者皆空说明该模式既没规划也没兜底。
			if len(p1.Hosts) == 0 && len(in1.Roles.PlanHosts(hosts)) == 0 {
				t.Fatalf("套件 %s 模式 %s 既未产出主机行也未填落点助手: %+v", d.Key(), m.Key, p1)
			}
			planned++
		}
	}
	if planned == 0 {
		t.Fatal("没有任何套件产出角色计划，断言已失效")
	}
}

// TestDockerStacksDeclareRegistryPicker 所有 Docker 套件（RequiresDocker=true）都必须声明
// 自建仓库选择器变量 image_registry（Type=registry），且恰好声明一次、出现在共享变量里。
//
// 口径（2026-09-16 拍板）：套件选仓库的逻辑必须与基础建设部署 Docker 完全一致——前端第 3 步
// 渲染为「自建仓库」下拉（候选=已登记 Docker Registry 的在线主机，与部署中心同源），提交
// hub_host_id；后端 injectHubParams 把仓库地址写入每台主机参数，渲染期 privatizeImages 统一
// 改写全部镜像参数（isImageParam：image / image_* / *_image），并接入 hub 预热与 insecure 预检。
//
// 变量名固定为 image_registry 的原因：它就是引擎注入与消费的键，套件无需自带前缀拼接逻辑。
// 新增 Docker 套件漏声明时在此失败，而不是等部署期镜像前缀静默不生效。
func TestDockerStacksDeclareRegistryPicker(t *testing.T) {
	const regVar = "image_registry"
	dockerStacks := 0
	for _, d := range ListStackDrivers() {
		bp := d.Blueprint()
		if !bp.RequiresDocker {
			continue
		}
		dockerStacks++
		declared := 0
		for _, v := range bp.SharedVars {
			if v.Name != regVar {
				continue
			}
			declared++
			if v.Type != "registry" {
				t.Fatalf("套件 %s 的 %s 类型为 %q，期望 registry（前端据此渲染自建仓库下拉）", bp.Key, regVar, v.Type)
			}
			if v.Required {
				t.Fatalf("套件 %s 的 %s 不应为必填：仓库是选填项，留空即直连官方源", bp.Key, regVar)
			}
		}
		if declared == 0 {
			t.Fatalf("Docker 套件 %s 未声明自建仓库选择器 %s（需 Docker 的套件必须能选自建仓库）", bp.Key, regVar)
		}
		if declared > 1 {
			t.Fatalf("套件 %s 重复声明了 %d 次 %s", bp.Key, declared, regVar)
		}
		// 主机级变量不得占用该名：它是全局注入键（injectHubParams 写入每台主机参数），
		// 主机级同名声明会与注入值互相覆盖。
		for _, v := range bp.HostVars {
			if v.Name == regVar {
				t.Fatalf("套件 %s 在主机级变量里声明了 %s，会与 hub 注入值冲突", bp.Key, regVar)
			}
		}
	}
	if dockerStacks == 0 {
		t.Fatal("没有任何 RequiresDocker 的套件，断言已失效")
	}
}
