# 任务 03 · 任务编排重构为「任务记录」— boundary.md（边界）

> 本文件限定执行本任务时**允许修改的文件范围与操作边界**。
> 清单之外的任何文件一律只读；确需越界时，停止并上报，不得自行修改。

## 1. 允许修改的文件（白名单）

```
api/orchestration.go                    # 列表联查/守卫/删除语义/RunsList 移除
model/orchestration.go                  # Orchestration 追加最近运行字段
store/orchestration_repo.go             # List 重写/Delete 事务化/HasRun/清理调整
router/router.go                        # 仅删 GET /orchestration/runs 一行
main.go                                 # 仅保留清理日志文案一行
template/static/pages/orchestrations.js # 任务记录语义改造
template/static/style.css               # 仅追加/调整既有类（走 CSS 变量）
```

## 2. 禁止修改的文件（黑名单 · 只读）

```
store/migrations.go                     # 本任务无 schema 变更，禁止加迁移
api/deploy_*.go、store/deploy_*.go      # 部署中心模块只读
common/**                               # sshx/eventbus/crypto 等只复用不改
template/static/app.js                  # 菜单名「任务编排」保留不改
template/static/pages/ 其余页面          # 其他页面只读
docs/**、README.md、script/**
```

## 3. 接口与数据边界

1. 路由仅删 `GET /api/orchestration/runs` 一行；不新增任何路由
   （运行详情沿用 /orchestration/runs/:id）。
2. 不新增表、不加列、不写迁移；三态全部派生自既有 orchestration_runs。
3. executeRun 执行引擎与 SSE 事件结构零改动；仅改入口守卫。
4. 审计动作名沿用 orchestration.*，不新增动作。

## 4. 技术边界

1. 守卫（一次性/编辑锁）在 api 层实现，repo 只提供 HasRun 等原语。
2. 前端不引入新依赖、不新增外网请求。
3. 遵守 rules.md：单文件 ≤300 行、占位符 SQL。

## 5. 验收边界

1. `go build`、`go vet` 全绿；浏览器控制台零报错。
2. `git diff --name-only` 全部落在白名单内（工作区既有未提交编排改动
   为本任务基线，提交时一并归入）。
3. 联调仅对测试机操作；生产红线沿用 rules.md。
