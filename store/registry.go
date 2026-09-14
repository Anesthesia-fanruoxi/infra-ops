// 套件装配表：把各套件驱动登记进 stackkit 注册表，并提供统一的查找 / 列出入口。
//
// 迁移期（P2 起）已目录化套件（store/stacks/<key>）与 builtin_stacks.go 的过渡驱动
// legacyStack 并存，行为零变更；每期用真驱动替换一条装配表行。
//
// 新套件接入（拆分完成后只需一行）：
//
//	mkdir store/stacks/<key>/{,scripts,configs}
//	写 <key>.go（Driver）+ 按需 plan.go / register.go / verify.go / vars.go
//	本文件 init() 增加一行：stackkit.Register(<key>.New())
package store

import (
	"fmt"

	"infra-ops/store/stackkit"
	"infra-ops/store/stacks/bigdata"
	"infra-ops/store/stacks/elasticsearch"
	"infra-ops/store/stacks/elfk"
	"infra-ops/store/stacks/kafka"
	"infra-ops/store/stacks/nacos"
	"infra-ops/store/stacks/powerjob"
	"infra-ops/store/stacks/rabbitmq"
	"infra-ops/store/stacks/redis"
	"infra-ops/store/stacks/rocketmq"
)

// stackDrivers 套件装配表：数组顺序即对外展示顺序（前端卡片顺序），已被 P0 快照
// workflow/tasks/09-stack-dir-split/baseline/blueprint/_order.json 冻结，增删行须同步刷新基线。
//
// 迁移期：已目录化的套件用真驱动（store/stacks/<key>），未迁移的仍用 builtin_stacks.go 的
// 过渡驱动 builtinLegacyStacks[<key>]。P2–P4 逐期把后者替换为真驱动，B10 后本表全是真驱动。
var stackDrivers = []stackkit.Driver{
	redis.New(),
	bigdata.New(),
	kafka.New(),
	elasticsearch.New(),
	rabbitmq.New(),
	rocketmq.New(),
	nacos.New(),
	powerjob.New(),
	elfk.New(),
}

func init() {
	for _, d := range stackDrivers {
		if err := stackkit.Register(d); err != nil {
			panic("套件装配表登记失败: " + err.Error())
		}
	}
	// 启动自检：embed 收紧后若某套件某模式的脚本读不到，必须在启动期暴露，而不是等部署期。
	if err := VerifyStackRegistry(); err != nil {
		panic("套件装配表自检失败: " + err.Error())
	}
}

// FindStackDriver 按 key 取套件驱动；不存在返回 nil。
func FindStackDriver(key string) stackkit.Driver { return stackkit.Find(key) }

// ListStackDrivers 按登记顺序返回全部套件驱动。
func ListStackDrivers() []stackkit.Driver { return stackkit.List() }

// ListStackKeys 按登记顺序返回全部套件 key。
func ListStackKeys() []string { return stackkit.Keys() }

// VerifyStackRegistry 套件装配表自检：
//   - 蓝图 key 与驱动 key 必须一致、必须声明模式；
//   - 每个套件的每个模式都必须能取到 node 脚本，且脚本与注入资源在 embed 内可读。
//
// 该检查把「embed 模式写错 / 资源漏迁」从部署期问题变成启动期问题（设计文档 §5.3 第 5 点）。
func VerifyStackRegistry() error {
	drivers := stackkit.List()
	if len(drivers) == 0 {
		return fmt.Errorf("套件装配表为空")
	}
	for _, d := range drivers {
		key := d.Key()
		bp := d.Blueprint()
		if bp.Key != key {
			return fmt.Errorf("套件 %s 的蓝图 key 为 %q，与驱动 key 不一致", key, bp.Key)
		}
		if len(bp.Modes) == 0 {
			return fmt.Errorf("套件 %s 未声明任何模式", key)
		}
		for _, m := range bp.Modes {
			f, ok := d.Phase(m.Key, stackkit.PhaseNode)
			if !ok || f.Path == "" {
				return fmt.Errorf("套件 %s 模式 %s 缺少 node 脚本", key, m.Key)
			}
			if _, err := StackFS.ReadFile(f.Path); err != nil {
				return fmt.Errorf("套件 %s 模式 %s 的 node 脚本不可读(%s): %w", key, m.Key, f.Path, err)
			}
			for ph, asset := range f.Assets {
				if _, err := StackFS.ReadFile(asset); err != nil {
					return fmt.Errorf("套件 %s 模式 %s 的注入资源 %s(%s) 不可读: %w", key, m.Key, ph, asset, err)
				}
			}
		}
	}
	return nil
}
