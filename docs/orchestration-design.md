# 任务编排（Orchestration）设计方案 v2

> 目标：让「一台服务器要做的 N 件事」变成一个可保存、可复用、可定时的整体，
> 而不是每次手动按顺序点 N 个模板。
>
> **v2 关键修订**：变量不再是「一套参数共享给所有主机」，而是**逐主机设置**。
> 模板的参数只作为默认值，每台主机可在运行时单独覆盖。
> 因此「批量修改主机名」不再需要特殊模板——它只是某台主机上填了 `new_name` 变量的一个普通步骤。

---

## 1. 背景与现状

- 一次任务 = 一个模板 × 一批主机（并行执行），当前参数只有「任务级一套」，所有主机共用同一份变量
- 缺口 A：**多模板顺序组合**无承载（新机初始化 = NTP → 基础软件 → 内核调优 → Docker → 业务，要手动点 5 次）
- 缺口 B：**同一模板对不同主机要用不同变量**（如每台 Web 的 `server_name`、每台 DB 的 `buffer_size` 不同），当前做不到

v2 同时补两个缺口：① 变量逐主机；② 模板可编排成链。

## 2. 核心抽象

```
一次执行 = 一个模板 + 一批主机
  每个主机用自己的「变量合集」渲染同一个模板脚本
  变量合集 = 模板默认值  ◁  任务级默认  ◁  主机级覆盖  ◁  内置变量(__ip/__name...)
```

**变量解析优先级（后者覆盖前者）**：
```
模板 variables 默认值
   < 任务/步骤级默认 params
   < 主机级覆盖 host_params
   < 引擎内置变量 {{__ip}} {{__seq}} {{__ip_last}} {{__name}}
```

编排（Orchestration）只是把「多个模板」按顺序排成链，再套用同一套逐主机变量机制：
- 链上每个步骤 = 一个模板 + （可选）步骤级默认参数
- 逐主机覆盖在主机维度始终生效

## 3. 基础建设（部署中心）新流程

```
① 选择模板
    → 右侧展示该模板的变量表单（作为默认值，可先填通用部分）
② 勾选主机
    → 多选 + 排序控件（复用现有组件）
③ 展示已勾选主机，逐台自定义变量
    ┌──────────┬──────────────┬──────────┐
    │ 主机      │ 变量(覆盖)    │ 操作      │
    │ web-01    │ server_name=w1 ✎      │ 重置为默认 │
    │ web-02    │ server_name=w2 ✎      │ 重置为默认 │
    │ db-01     │ buffer_size=4G ✎      │ 重置为默认 │
    └──────────┴──────────────┴──────────┘
    [应用到全部] [复制到选中主机]  ← 大多数相同、个别微调时一键铺开
④ 确认执行 → 逐主机用各自变量渲染运行
```

修改主机名就是这条流程的一个实例：选 `set_hostname` 模板 → 勾主机 → 给每台填 `new_name` → 执行。
模板执行后自报 `infra-ops:set-name={{new_name}}`，台账名自动同步，无需单独做"批量改名"入口。

## 4. 执行模式与并发语义

| 模式 | 代号 | 语义 | 适用 |
|------|------|------|------|
| 主机独立流水线 | `by_host`（默认） | 每台主机各自跑完自己的链；主机间完全并行 | 绝大多数：各主机链不同、互不依赖 |
| 全局步骤栅栏 | `by_step` | 第 k 步在**所有**目标主机完成后，才允许任何主机开始 k+1 | 少数跨主机先后（先 DB 后 Web） |

- 同一主机步骤严格串行；不同主机沿用自适应并发 `min(CPU×4, 主机数)`，上限 32
- 等待/慢步骤：步骤级重试（retry_count / retry_interval_sec，专治 apt/dnf 锁）+ 全局超时 600s（步骤可覆盖）+ 失败终止该主机链（可开 continue_on_error）

## 5. 数据模型

```sql
-- 编排定义（不变）
CREATE TABLE orchestrations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    description  TEXT NOT NULL DEFAULT '',
    exec_mode    TEXT NOT NULL DEFAULT 'by_host' CHECK (exec_mode IN ('by_host','by_step')),
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

-- 步骤定义（params_json = 步骤级默认，可被主机覆盖）
CREATE TABLE orchestration_steps (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id  INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
    seq               INTEGER NOT NULL,
    template_id       INTEGER NOT NULL REFERENCES deploy_templates(id),
    params_json       TEXT NOT NULL DEFAULT '{}',   -- 步骤级默认
    host_scope        TEXT NOT NULL DEFAULT '',     -- 空=全部；JSON 数组[host_id] 局部
    continue_on_error INTEGER NOT NULL DEFAULT 0,
    retry_count       INTEGER NOT NULL DEFAULT 0,
    retry_interval_sec INTEGER NOT NULL DEFAULT 30,
    timeout_sec       INTEGER NOT NULL DEFAULT 0,   -- 0=全局
    UNIQUE(orchestration_id, seq)
);

-- 主机触发模式：每台主机自己的链（params 天然逐主机）
CREATE TABLE orchestration_host_chains (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
    host_id          INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    seq              INTEGER NOT NULL,
    template_id      INTEGER NOT NULL REFERENCES deploy_templates(id),
    params_json      TEXT NOT NULL DEFAULT '{}',    -- 该主机的变量覆盖
    continue_on_error INTEGER NOT NULL DEFAULT 0,
    retry_count      INTEGER NOT NULL DEFAULT 0,
    retry_interval_sec INTEGER NOT NULL DEFAULT 30,
    UNIQUE(orchestration_id, host_id, seq)
);

-- by_step 模式下，主机级变量覆盖（by_host 模式已在 host_chains 内）
CREATE TABLE orchestration_step_host_vars (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
    seq              INTEGER NOT NULL,
    host_id          INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    params_json      TEXT NOT NULL DEFAULT '{}',
    UNIQUE(orchestration_id, seq, host_id)
);

-- 运行实例（不变）
CREATE TABLE orchestration_runs (
    id, orchestration_id, name, exec_mode, status,
    total_hosts, ok_hosts, fail_hosts, trigger_type, created_at, finished_at
);

-- 运行明细
CREATE TABLE orchestration_run_steps (
    id, run_id, host_id, host_name, host_ip, seq,
    template_id, template_name, status, attempt, output, error,
    started_at, finished_at
);
```

**对现有 `deploy_tasks` / `deploy_task_hosts` 的调整**（支持逐主机变量）：
- `deploy_tasks.params_json` 语义改为 **任务级默认**（可为空）
- `deploy_task_hosts` 增加列 `params_json TEXT NOT NULL DEFAULT '{}'`（该主机的变量覆盖）
- 渲染：`renderScript(script, mergeVars(templateVars, taskDefault, hostOverride))`
- 旧数据 `deploy_task_hosts.params_json` 为空 → 回退用 `deploy_tasks.params_json`，行为不变（兼容）

## 6. 执行引擎

```
Run(orchestrationId)
  ├─ 展开为每主机队列 map[hostID] => []stepExec{seq, template, retry...}
  │    by_host : orchestration_host_chains（params 已在行内）
  │    by_step : orchestration_steps 默认链
  │              + 每台主机叠加 orchestration_step_host_vars 覆盖
  ├─ 建 orchestration_runs + run_steps(pending)
  ├─ 调度循环（单 goroutine）:
  │    - 自适应信号量内，为每个未完结主机派发下一步
  │    - by_step 额外检查全局栅栏
  │    - 步骤失败: 重试 → 仍败则终止该主机链(除非 continue_on_error)
  └─ 每步 = execOnHost(逐主机 mergeVars 渲染 + 解密 + 拨号) + streamTee
        增量 → SSE TopicOrchestrationProgress；全量 → run_steps.output
```

复用清单（不变）：renderScript/applyHostVars、execOnHost/streamTee、eventbus+SSE、
host_installs、audit_logs、保留清理、自适应并发。

## 7. SSE 与进度页

`GET /api/sse/orchestration?run_id=N`
```
event: step   data={"run_id":1,"host_id":3,"seq":2,"status":"running"}
event: output data={"run_id":1,"host_id":3,"seq":2,"output":"..."}
event: step   data={"run_id":1,"host_id":3,"seq":2,"status":"success","attempt":1}
event: done   data={"status":"partial","ok_hosts":8,"fail_hosts":1}
```
进度页：每主机一行，列=步骤状态徽章；点格子展开该步骤实时日志。

## 8. UI 交互线框

### 8.1 编排列表页（部署中心 → 任务编排）
```
[新建编排]                       [搜索____]
名称        模式     步骤数  最近运行   操作
新机初始化   by_step   5     2h前 成功  运行 编辑 ⋯
k8s-node    by_host   4     1d前 失败  运行 编辑 ⋯
```

### 8.2 编排编辑器
- 顶部 radio：按主机编排 / 按模板编排
- **按模板编排**：
  ```
  步骤链: [≡1. NTP] [≡2. 基础软件] [≡3. 内核调优] [+ 添加步骤]
  底部: 勾选目标主机 → 主机表格逐台填变量(可"应用到全部")
  ```
- **按主机编排**：
  ```
  左栏: 已选主机(web-01 ●编辑 / db-01 / node-03)
  右栏: 当前主机链 [≡1. 内核调优 参数:本机] [≡2. Docker 参数:本机]
  快捷: [复制此链 → 全部/选中]   ← 90% 场景一键铺开再微调
  ```

### 8.3 部署中心（基础建设）按 §3 三步流改造
重点在第三步的「逐主机变量表格」，这是 v2 的核心交互。

### 8.4 定时触发
`deploy_schedules` 扩展 `target_type`(template/orchestration) + `orchestration_id`，
cron 跑编排；定时任务的变量同样支持逐主机（保存时按主机固化快照）。

## 9. 配套内置模板

| 模板 | 内容 | 关键变量（逐主机） |
|------|------|--------------------|
| 设置主机名（原"批量修改主机名"回归为普通模板） | `hostnamectl set-hostname {{new_name}}` + 自报同步台账 | `new_name` |
| 基础软件安装（新增） | vim/curl/wget/htop... 自动识别 yum/dnf/apt，幂等 | 可选开关 |
| 内核参数调优（增强） | 句柄数/conntrack/TCP缓冲/swappiness... 分文件落盘 | — |
| 时间同步（已有） | 不动 | — |

## 10. 分期实施

| 阶段 | 内容 |
|------|------|
| **P0（前置，建议先做）** | 部署中心支持**逐主机变量**（deploy_task_hosts.params_json + 第三步变量表格 + 渲染合并） |
| **P1** | 编排数据表 + CRUD + **按模板编排(by_step)** 手动执行 + 进度页 + SSE |
| **P2** | **按主机差异化链(by_host)** + 双模式编辑器 + 重试/continue_on_error + 安装标记 |
| **P3** | 定时触发 + 保留清理 + 历史对比 |

> P0 是 P1/P2 的基础：逐主机变量机制先落地，编排直接复用。

## 11. 开放问题（需拍板）

1. **跨主机顺序** v1 用 `by_step` 覆盖够否？更细「阶段分组」放 P3 后。
2. **步骤间变量传递**（前序输出喂后续）v1 不做，用内置变量先顶。
3. **权限**：编排高危聚合操作，v1 单一管理员账号不过细。
4. **运行中锁定编辑**：编排执行中禁止改定义。
5. **（新增）逐主机变量 UI 密度**：主机很多时（如 50 台）第三步表格要不要支持
   分组/筛选 + 「批量填某变量」？v1 先做基础表格 + 应用到全部，复杂交互放 P2。

---

*评审通过按 P0 → P1 → P2 → P3 实施，每阶段独立提交。*
