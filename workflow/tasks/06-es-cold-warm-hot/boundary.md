# 任务 06 · Elasticsearch 套件冷热温双模式 — boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。

## 1. 允许修改的文件（白名单）

```
model/stack.go                        # 若需扩展 StackMode/StackVar（如 Modes 约束、角色字段），仅追加
store/builtin_stacks.go               # elasticsearch 蓝图：新增 cold_warm_hot 模式、模式级变量、Pipeline 阶段、镜像升 9.5.3
api/stack_engine.go                   # ES 冷热温多阶段流水线接线/角色→node.roles 生成/SSL 签发/JVM 注入/服务登记与探活路由
api/stack_instance.go                 # 角色校验（奇偶主节点/互斥）+ 缩容保护（禁直接拔停）
api/stack.go                          # 创建入口校验透传
api/stack_verify.go                   # 按 tier 分层的接入地址与探活（协调/数据 hot/warm/cold 角色路由）
script/check_es_tpl.py                # 新增：冷热温/集群 双形态渲染校验脚本
store/stacks/elasticsearch/**         # node.sh（RUN 分发器）、configs/**（新增 cold-warm-hot 分层配置、master/coordinating 配置）
template/static/pages/stacks-forms.js # 模式单选、角色多选、SSL/JVM 参数渲染、显隐联动
template/static/pages/stacks.js       # 实例展示 tier 徽标、成员角色标签、按 tier 接入地址
template/static/style.css             # 仅追加/扩展相关样式（走 CSS 变量）
docs/es-suite-cold-warm-hot.md        # 实施中同步修订设计文档
workflow/tasks/06-es-cold-warm-hot/** # 本任务执行文档进度记录
workflow/workflow.md                  # 仅勾选本任务步骤 [x]
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
store/migrations.go 及 store/db.go     # 本任务无 schema 变更（模式/角色随 ParamsJSON 持久化），禁止迁移
store/builtin_templates.go、store/builtin/**   # 模板安装体系不涉本任务
store/stacks/（elasticsearch 外的全部套件）   # redis/bigdata/kafka/rabbitmq/rocketmq/nacos/powerjob 一律不动
common/**、config/**、router/**        # 无新 HTTP 端点，路由与安全边界零改动
api/（白名单外的其他文件）              # orchestration/deploy/host 系列不涉
template/（白名单外的其他页面）          # 其他页面 UI 不动
workflow/tasks/（06 外的其他任务目录）
workflow/boundaries/**、workflow/docs/**
```

## 3. 接口与数据边界

1. 不新增 HTTP 端点；复用现有创建/重装/扩缩容/卸载接口，仅扩展 **冷热温模式** 下的参数语义
   （`ssl_enabled`、`coordinator_count`、角色行勾选、JVM 堆与 `jvm_opts`）。
2. 无数据库 schema 变更；模式、角色、SSL/JVM 全部随既有 ParamsJSON 持久化，不新建表。
3. 缩容走既有 scale_in 接口，仅冷热温模式在 compose down 前插入官方下线编排。
4. 角色互斥（master 与数据层）必须后端兜底校验，前端置灰仅是交互优化。
5. 安全红线：CA 私钥仅落盘在节点 `home_dir`，平台不持久化、日志脱敏；SSH/日志不输出密钥材料。

## 4. 技术边界

1. 套件脚本/配置为 `go:embed`——每次改动后必须 `go build` 重编译才生效，验证时注意。
2. 单文件 ≤300 行：`node.sh`/`stack_engine.go`/`stacks.js` 已接近上限，超出时拆分
   （脚本可拆 `run_<key>` 独立阶段文件、Go 可拆 `api/es_engine.go`、前端可拆 `stacks-es.js`，
   拆分文件落在白名单内无需再确认）。
3. LLM/索引模板/ILM 等**不在本次范围**（见设计文档 §1 范围口径），不得越界新增。
4. 「集群」模式仅做镜像升级，脚本行为保持现状，任何渲染差异即回归失败。
5. 并行会话约束：B2-B6 触及 `stack_engine.go`/`node.sh` 热区，开工前先 `git status`
   确认无未合并外部改动，发现冲突立即暂停上报。
6. 遵守 rules.md：占位符 SQL、日志脱敏、不提交密钥、写操作落审计。

## 5. 验收边界

1. 静态：`go build`、`go vet`、`bash -n`（改动脚本）、`node --check`（改动 JS）、
   `python script/check_es_tpl.py` 全绿。
2. `git diff --name-only` 全部落在第 1 节白名单内。
3. 真机：仅测试节点（≥4 台：1 master + 2 协调 + 2 数据层，standby 允许机器），禁止生产写操作（红线）。
4. 缩容验证必须包含官方下线流程：校验分片迁移完成、集群回归 green 后才允许停容器。
5. 非冷热温的「集群」模式零回归为硬性验收项；SSL 开关两种形态均须渲染校验通过。