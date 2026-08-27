# 任务 04 边界文档 · 运行抽屉

## 文件白名单（改动仅限于此）

| 文件 | 改动 |
|------|------|
| `store/migrations.go` | 新增 migrateV14（orchestration_run_logs 表 + 索引）并注册 |
| `store/orchestration_log_repo.go` | 新增：日志 repo（AppendRunLogs / RunStepLogs） |
| `model/orchestration.go` | 新增 OrchestrationRunLog 结构 |
| `common/eventbus/eventbus.go` | 新增 TopicOrchestrationSteps / TopicOrchestrationLogs 常量 |
| `api/orchestration.go` | 拆分瘦身：保留 CRUD + Run/createRun + RunsDetail；引擎与 SSE 逻辑外移 |
| `api/orchestration_engine.go` | 新增：executeRun + execCell + 事件结构与发布 |
| `api/orchestration_sse.go` | 新增：SSESteps / SSEDetail 处理器 |
| `router/router.go` | 新增两条 SSE 路由；移除旧 `/sse/orchestration` 路由；NewOrchHandler 注入日志 repo |
| `template/static/pages/orchestration_drawer.js` | 新增：抽屉 mixin |
| `template/static/pages/orchestrations.js` | 移除矩阵弹窗，合并抽屉 mixin |
| `template/static/style.css` | 追加抽屉三层样式 |
| `template/index.html` | 引入 orchestration_drawer.js |
| `workflow/workflow.md` | 登记任务 04 并标记进度 |

## 明确不做（scope 外）

- 不改引擎执行语义：by_step 栅栏、重试、变量合并、自适应并发、MarkHostInstalled 全部保持原样
- 不改 `orchestrations` 列表接口与三态派生（任务 03 成果）
- 不改部署中心（deploy）的 SSE 与日志逻辑，仅复用 `execHostWith` 现状
- 不做抽屉内任何操作能力（无重跑/无停止/无跳过执行）
- 不做日志导出/搜索/过滤（时间+IP+内容三列展示即止）
- 不清理存量运行数据；旧运行无日志行属预期（回溯显示「无日志」空态）
- 不动 `workflow/tasks/01~03` 既有文档

## 接口契约（对前端的边界）

- 移除：`GET /api/sse/orchestration?run_id=N`（旧端点）
- 新增：`GET /api/sse/orchestration/steps?run_id=N`
- 新增：`GET /api/sse/orchestration/detail?run_id=N&step=M`
- 保留不动：`GET /api/orchestration/runs/:id`、`POST /api/orchestrations/:id/run`、任务记录 CRUD
- 事件载荷字段以后端 backend.md 协议表为准，前端不自行扩展字段含义

## 风险与回退

- SSE 双连接并发推送：事件总线订阅为每连接独立 goroutine，无锁竞争热点；旧端点下线后无其他消费方（已核对前端唯一引用为 orchestrations.js）
- 日志写入频率：随 SSH 输出 chunk 逐批事务写库，量级为运维脚本输出，SQLite WAL 足够；若实测有压力，回退方案为攒批（500ms 窗口）落库
- 回退：git revert 本任务提交即可，V14 建表语句幂等（IF NOT EXISTS），对旧数据无破坏
