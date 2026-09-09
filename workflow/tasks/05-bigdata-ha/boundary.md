# 任务 05 · 大数据底座全组件高可用 — boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。

## 1. 允许修改的文件（白名单）

```
model/stack.go                        # StackVar.Type + StackBlueprint.HaSupport
store/builtin_stacks.go               # bigdata 蓝图：ha/辅助变量、metastore_db、HaSupport
api/stack_engine.go                   # 占位符/分配算法/服务登记/探活适配
api/stack_instance.go                 # 校验扩展/bootstrap 落点/缩容保护
api/stack.go                          # 创建入口校验透传
template/static/pages/stacks.js       # 角色矩阵/徽标/抽屉标签/参数组装
template/static/pages/stacks-forms.js # 套件卡片开关/bool 渲染/显隐联动
template/static/style.css             # 仅追加/扩展相关样式（走 CSS 变量）
store/stacks/bigdata/**               # node.sh、bootstrap.sh、configs/**（仅此套件）
script/check_bigdata_tpl.py           # 渲染校验扩展
docs/bigdata-stack.md                 # 用户侧使用文档
docs/bigdata-ha-design.md             # 实施中同步修订设计文档
workflow/tasks/05-bigdata-ha/**       # 本任务执行文档进度记录
workflow/workflow.md                  # 仅勾选本任务步骤 [x]
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
store/migrations.go 及 store/db.go     # 本任务无 schema 变更，禁止迁移
store/builtin_templates.go、store/builtin/**   # 模板安装体系不涉本任务
store/stacks/（bigdata 外的全部套件）   # redis/es/kafka/rabbitmq/rocketmq/nacos/powerjob 不动
common/**、config/**、router/**        # 无新 HTTP 端点，路由零改动
api/（白名单外的其他文件）              # orchestration/deploy/host 系列不涉
template/（白名单外的其他页面）          # 其他页面 UI 不动
workflow/tasks/（05 外的其他任务目录）
workflow/boundaries/**、workflow/docs/**
```

## 3. 接口与数据边界

1. 不新增 HTTP 端点；复用现有创建/重装/扩缩容/卸载接口，仅扩展参数语义
   （`ha`、辅助变量、masters JSON 从角色 key）。
2. 无数据库 schema 变更；`ha` 与从角色信息全部随既有 ParamsJSON 持久化。
3. masters 白名单校验必须后端兜底，前端标红仅是交互优化。

## 4. 技术边界

1. 套件脚本/配置为 `go:embed`——每次改动后必须 `go build` 重编译才生效，验证时注意。
2. 单文件 ≤300 行：`stacks.js`/`node.sh` 已接近上限，超出时拆分新文件
   （前端可拆 `stacks-ha.js`，脚本可拆 `scripts/ha.sh` source 引入，拆分文件进白名单无需再确认）。
3. 并行会话约束：B2-B6 触及 `stack_engine.go`/`node.sh` 热区，各步骤开工前先
   `git status` 确认无未合并的外部改动；发现冲突立即暂停上报。
4. Flink/Spark HA 配置以现有注入方式内嵌（FLINK_PROPERTIES/挂载文件），不引入新配置体系。
5. 遵守 rules.md：日志脱敏、占位符 SQL、不提交密钥（hive_db_password 随机生成不入库明文日志）。

## 5. 验收边界

1. 静态：`go build`、`go vet`、`bash -n`（改动脚本）、`node --check`（改动 JS）、
   `python script/check_bigdata_tpl.py` 全绿。
2. `git diff --name-only` 全部落在第 1 节白名单内。
3. 真机：使用测试节点（≥3 台，standby-01 等允许的机器），禁止对生产节点写操作（红线）。
4. 故障演练标准：逐组件 stop 主实例容器 → 30s 内 Standby 接管 → 客户端读写不中断
   → 原实例重启后自动回归 Standby；Trino 明确不做演练（单点）。
5. 非 HA 回归为硬性验收项：任一回归差异即视为不通过。
