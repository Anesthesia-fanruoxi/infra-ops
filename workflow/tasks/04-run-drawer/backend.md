# 任务 04 后端执行文档 · 运行抽屉（三层结构 + 双 SSE + 步骤日志落库）

## 需求

点击「运行」后自动展开 50% 宽抽屉，三层只读监控：

1. **步骤层（L1）**：横向步骤条，展示每步进展（未开始/执行中/已完成）；完成后显示聚合色——全成功绿 / 部分失败黄 / 全失败红；可点击切换查看；无任何重跑能力
2. **主机层（L2）**：选中步骤下每台主机的运行状态（运行中/成功/失败/跳过），只有状态，错误信息只在日志
3. **日志层（L3）**：选中步骤的日志实时混排，行格式 = 时间 + IP + 内容 三维度

**双 SSE（两个独立端点）**：
- **步骤流** `/api/sse/orchestration/steps?run_id=N`：只推步骤生命周期（step started / step finished+聚合 / run done），不受主机结果细节影响；运行结束发 done 后关闭
- **详情流** `/api/sse/orchestration/detail?run_id=N&step=M`：推送该步骤的主机状态变化与日志行；连接时先发 init 快照（打底），再推增量；运行结束（或回溯已结束的运行）发 done 后关闭

**连接策略（前端负责，后端语义配合）**：运行中 = 步骤流 + 详情流两条连接；回溯/已结束 = 仅详情流（连一次拿一次快照）。

## 现状与差距

| 现状 | 差距 |
|------|------|
| 引擎只发布主机级事件（`orchProgress`），无步骤级事件 | 需新增步骤生命周期事件 |
| 增量日志仅走事件总线，不落库，刷新即断 | 需新表按步骤落库，回溯可重放 |
| `onLog` 的 output 块挂在 `TopicOrchestrationProgress` 上服务旧矩阵弹窗 | 旧矩阵弹窗移除，日志块改走独立 topic |
| 单端点 `/api/sse/orchestration` | 拆为步骤流 + 详情流两端点 |
| `api/orchestration.go` 已 539 行（超 300 行规约） | 借本次改造拆为 3 个文件 |

## 数据模型

### 迁移 V14：日志表

```sql
CREATE TABLE IF NOT EXISTS orchestration_run_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,               -- 步骤序号
    host_id    INTEGER NOT NULL,
    host_ip    TEXT NOT NULL DEFAULT '',
    text       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_orch_run_logs ON orchestration_run_logs(run_id, seq, id);
```

外键级联：`Delete`（删运行）与 `CleanupRunsBefore`（清超期/孤儿运行）删除 `orchestration_runs` 行时日志随删，**存储层清理逻辑零改动**。

### model.OrchestrationRunLog

`ID int64, RunID int64, Seq int, HostID int64, HostIP string, Text string, CreatedAt string`

## 事件总线

`common/eventbus/eventbus.go` 新增两个 topic：

- `TopicOrchestrationSteps`：步骤生命周期
- `TopicOrchestrationLogs`：日志行

`TopicOrchestrationProgress` 保留但只承载主机状态变化（running/success/failed/skipped/skipped-refresh）；**移除 output 块发布**（由 TopicOrchestrationLogs 取代）。

## 事件结构

```go
// 步骤流事件（TopicOrchestrationSteps）
type orchStepEvent struct {
    RunID     int64  `json:"run_id"`
    Seq       int    `json:"seq"`
    Name      string `json:"name,omitempty"`
    State     string `json:"state"`               // started / finished
    Aggregate string `json:"aggregate,omitempty"` // finished 时: success/partial/failed/skipped
}

// 日志行事件（TopicOrchestrationLogs）
type orchLogEvent struct {
    RunID  int64  `json:"run_id"`
    Seq    int    `json:"seq"`
    HostID int64  `json:"host_id"`
    HostIP string `json:"host_ip"`
    Text   string `json:"text"`
}
```

## SSE 协议

### 步骤流 `GET /api/sse/orchestration/steps?run_id=N`

| 事件 | 载荷 | 说明 |
|------|------|------|
| `init` | `{run_id, run_status, steps:[{seq,name,state,aggregate}]}` | 连接即发，从 run_steps 聚合当前态 |
| `step` | orchStepEvent | state=started / finished |
| `done` | `{run_status}` | 运行结束，发送后关闭连接；连接时已结束则 init→done 后立即关闭 |

步骤聚合规则（对 run_steps 该 seq 的全部行）：
- 任一行 running → `running`；全部 pending → `pending`
- 全部 skipped → `skipped`
- 否则统计非 skipped 行：failed=0 → `success`；ok(=0) 且 failed>0 → `failed`；混合 → `partial`

### 详情流 `GET /api/sse/orchestration/detail?run_id=N&step=M`

| 事件 | 载荷 | 说明 |
|------|------|------|
| `init` | `{step, run_status, hosts:[{host_id,host_ip,host_name,status}], logs:[{id,ts,ip,text}]}` | 快照：该步骤主机态 + 该步骤已落库日志（最近 2000 行） |
| `host` | `{seq, host_id, status, attempt}` | 该步骤主机状态变化（服务端按 step=M 过滤） |
| `log` | `{id, seq, ts, ip, text}` | 该步骤日志行（服务端按 step=M 过滤） |
| `done` | `{run_status}` | 运行结束发送后关闭；连接时已结束则 init→done 立即关闭（回溯语义） |

## 引擎改造（api/orchestration_engine.go）

1. **步骤事件**：每步主机 goroutine 发起前发布 `started`（带模板名）；`wg.Wait()` 后重查 `RunSteps(runID)` 该 seq 行计算聚合，发布 `finished`
2. **日志落库**：`onLog` 回调将 chunk 按 `\n` 拆行（trim `\r`，跳过空行），**先写库再发布**（保证重连 init 快照不丢行）
3. **引擎生成状态行**（同样落库+发布，让日志自解释）：`开始执行（第 N 次）`、`第 N 次执行失败，X 秒后重试`、`执行成功`、`执行失败：{err}`
4. **跳过推送**：主机失败且未开「失败继续」时，除现有 `skipped-refresh` 外，对该主机后续 pending 步骤逐行发布 `host` 事件（status=skipped），详情流按 step 过滤后 L2 实时置灰
5. **收尾**：`FinishRun` 后发布步骤流 `run done`（带终态）

## 文件拆分（300 行规约）

| 文件 | 内容 | 预估行数 |
|------|------|---------|
| `api/orchestration.go` | handler 定义 + CRUD + Run/createRun + RunsDetail | ~300 |
| `api/orchestration_engine.go` | executeRun + execCell + 事件结构 + 聚合计算 | ~260 |
| `api/orchestration_sse.go` | SSESteps + SSEDetail + 快照构建 | ~230 |
| `store/orchestration_log_repo.go` | AppendRunLogs（批量事务）/ RunStepLogs（快照，cap 2000 行） | ~80 |

路由（router/router.go）：
- 新增 `protected.GET("/sse/orchestration/steps", ...)`、`protected.GET("/sse/orchestration/detail", ...)`
- 移除 `protected.GET("/sse/orchestration", ...)`（旧端点唯一消费方是旧矩阵弹窗，随本任务一并移除）
- `NewOrchHandler` 注入 `*store.OrchestrationLogRepo`

## 执行步骤

1. [ ] 迁移 V14 + `model.OrchestrationRunLog` + `store/orchestration_log_repo.go`（AppendRunLogs / RunStepLogs）—— 验收：旧库启动自动建表，`go build` 通过
2. [ ] 事件总线新增 2 topic + 引擎改造（步骤事件 / 日志落库 / 状态行日志 / 跳过推送 / output 块下线）+ 文件拆分 —— 验收：编译通过，引擎事件按协议发布，日志边执行边入库（库内可见进度行）
3. [ ] 双 SSE 端点（init 快照 + 过滤推送 + done 关闭）+ 路由更新 + 移除旧端点 —— 验收：`go build` + `go vet` + `go test` 通过；`curl` 连接步骤流可见 init/step/done 事件序列
4. [ ] 交叉编译验证（`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`）—— 验收：产物生成无报错

## 验收标准（联调，与前端任务合并验收）

- 运行中连接步骤流：init → step started/finished 序列与实际步骤推进一致；done 后连接自动关闭
- 详情流 init 快照含主机态与已产生日志；`host`/`log` 增量实时到达；切换 step 参数重连可拿到目标步骤快照
- 已结束运行连详情流：init → done 后关闭（回溯语义）
- 运行结束后 `orchestration_run_logs` 内含全部步骤日志行（含引擎状态行），时间+IP+内容三列可查
- 删除任务记录后日志随运行级联删除，无孤儿行

## 进度记录

- 2026-08-27：任务立项，三件套编写完成，待确认后执行。
- 2026-08-27：步骤 1-3 执行完成。migrateV14 幂等验证 + 日志 repo 落库/读取/级联删除测试通过；引擎改造完成（步骤 started/finished 事件、日志按行拆分先落库再发布、状态行、跳过主机逐行推送 skipped）；文件拆分完成（orchestration.go 293 / engine 299 / sse 274 行，均 ≤300）；双 SSE 端点 httptest 协议测试通过（init/step/host/log/done/回溯/步骤过滤）；旧 /sse/orchestration 路由已移除；go build/vet/test 全绿；步骤 4 交叉编译（CGO_ENABLED=0 linux/amd64）通过。
- 待办：联调验收（运行→抽屉三层实时联动→回溯）由用户启动页面执行。
