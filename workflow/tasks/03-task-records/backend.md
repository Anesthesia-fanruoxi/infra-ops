# 任务 03 · 任务编排重构为「任务记录」— backend.md（后端执行）

> 语义变更：编排从「可反复运行的定义」改为「一次性任务记录」。
> 新建记录 = 未开始；执行一次 = 运行中 → 已结束（终态，含成功/部分成功/失败）。
> 不做 schema 迁移：三态由最近一次运行记录派生（无运行→未开始；
> 最近运行 status=running→运行中；否则→已结束）。
> 基线：工作区未提交的 state 派生改动（runStates 等）为本任务起点，重构替换之。

## 1. 列表接口（GET /api/orchestrations）

1. orchCols 联查最近一次运行（LEFT JOIN r.id = 子查询 MAX(id)），一次查询返回：
   - state：not_started / running / finished（由 completed 更名 finished）
   - last_run_id（0=未运行）、result（success/partial/failed，空=无）
   - ok_hosts / fail_hosts / total_hosts（最近运行统计）
2. 删除 runStates() 两段查询与内存过滤（废弃，过滤在 SQL 或联查结果上做）。
3. state 过滤参数沿用（前端 tabs 需要）。
4. model.Orchestration 追加 LastRunID / Result / OkHosts / FailHosts / TotalHosts。

## 2. 一次性语义守卫

1. createRun：编排已存在任何运行记录（HasRun：`SELECT 1 FROM
   orchestration_runs WHERE orchestration_id=?`）→ 返回错误
   「该任务记录已执行过，不可重复执行」。同一守卫天然覆盖运行中/已结束两态。
   enabled 检查保留（存量停用数据兜底）。
2. Save 更新分支（id>0）：HasRun → 拒绝「已开始执行的任务记录不可编辑」；
   新建分支不受影响。

## 3. 删除语义

Delete 改事务：删 orchestrations（级联 steps / step_host_vars）+ 显式删
orchestration_runs（该表无外键，run_steps 随 run 级联删除）。
任务记录与其运行明细同生共死，不再保留孤儿运行。

## 4. 运行历史接口下线

1. 删除 RunsList handler 与路由 `GET /api/orchestration/runs`（router.go 一行）。
2. 保留 `GET /api/orchestration/runs/:id`（详情与 SSE 回读仍依赖）。
3. 运行详情入口改为前端用列表返回的 last_run_id 直开，不再依赖历史列表。

## 5. 保留清理调整（CleanupRunsBefore）

派生状态下只删运行会让已结束记录「复活」为未开始，必须连带删记录：

```
收集：finished_at 超期的已结束运行 → orchestration_id 去重集合
排除：存在 status='running' 运行的编排（防误杀进行中任务，存量多运行数据兜底）
事务：DELETE FROM orchestration_runs WHERE orchestration_id IN 集合
      DELETE FROM orchestrations     WHERE id IN 集合
附带：清扫孤儿运行（orchestration_id 不在 orchestrations 表中的行）
```

未开始记录无运行行，不受保留清理影响，仅手动删除。
main.go 清理日志文案同步为「任务记录」口径。

## 6. 不变项

- executeRun 引擎、SSE 协议（/api/sse/orchestration?run_id=）、
  运行明细矩阵查询，全部零改动。
- 审计动作 orchestration.create / update / delete / run 沿用。

## 7. 验收自检

- [ ] go build / go vet 通过
- [ ] 新建记录无运行行，state=not_started
- [ ] 运行后 state=running；结束后 state=finished 且 result / 统计正确
- [ ] 已结束记录再次 POST /run 返回 400；PUT 保存被拒
- [ ] 删除记录后 orchestration_runs / orchestration_run_steps 无残留
- [ ] GET /api/orchestration/runs 返回 404；/runs/:id 正常
- [ ] 构造超期已结束运行后触发清理：记录整条消失、运行中记录不受影响

## 8. 进度记录

- 2026-08-26：任务立项，三件套编写完成，待确认后执行。
- 2026-08-26：步骤 1-3 执行完成并通过经验性验证（store 包测试覆盖 List 三态/结果统计/state 过滤、Delete 无孤儿、CleanupRunsBefore 连带删；api 包测试覆盖重跑守卫 + 编辑守卫）。Get 详情改用 orchBaseCols（不联查运行），修复 orchCols 多列导致的扫描错位。go build/vet 通过，历史列表路由已下线。
