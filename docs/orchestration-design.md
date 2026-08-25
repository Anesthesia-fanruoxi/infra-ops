# 任务编排（Orchestration）设计方案

> 目标：让「一台服务器要做的 N 件事」变成一个可保存、可复用、可定时的整体，
> 而不是每次手动按顺序点 N 个模板。

---

## 1. 背景与现状

当前部署中心的能力边界：

- 一次任务 = 一个模板 × 一批主机（并行执行）
- 已有可复用资产：模板变量渲染、`{{__seq}}` 等内置主机变量、流式 SSE 日志、
  自适应并发、超时控制、安装标记（host_installs）、审计、历史保留清理

缺口：**多个模板之间的顺序组合**没有承载。例如新机器初始化 =
时区/NTP → 基础软件 → 内核调优 → Docker → 业务组件，目前要手动排队点 5 次。

## 2. 概念模型

```
编排（Orchestration）
 ├─ 基本信息：名称 / 备注 / 执行模式
 └─ 步骤链（Step 1..N，严格有序）
      └─ 步骤 = 模板 + 参数 + （可选）适用范围
```

两种创建视角，本质是同一条数据的不同填法：

| | 主机触发（先选主机） | 任务触发（先选模板） |
|---|---|---|
| 交互顺序 | 勾选主机 → 为**每台主机**排各自的步骤链 | 选模板+填参数 → 追加为步骤 → 最后统一选主机 |
| 典型场景 | 各台服务器角色不同（A 要 docker，B 要 nginx） | 同一批机器走相同的标准化流程（新机初始化） |
| 存储形态 | 每台主机一条差异化链 | 一条共享链应用到全部所选主机 |

**核心抽象：一切编排最终都归约为「每台主机一个有序步骤队列」。**
任务触发的共享链 = 把同一条链复制给每台主机。引擎只需要实现一个原语：
*按队列逐个执行主机上的步骤*。

## 3. 执行模式与并发语义（重点）

### 3.1 两种执行模式

| 模式 | 代号 | 语义 | 适用 |
|------|------|------|------|
| 主机独立流水线 | `by_host`（默认） | 每台主机各自从头到尾跑完自己的链；主机之间完全并行 | 绝大多数情况：各主机链条互不相同、互不依赖 |
| 全局步骤栅栏 | `by_step` | 第 k 步在**所有**目标主机上完成后，才允许任何主机开始第 k+1 步 | 少数需要跨主机先后关系的场景（如先扩 DB 再发应用） |

> 你的观察完全正确：「绝大多数主机独一无二，极少数挤在一起」。
> 所以默认 `by_host`——一台主机卡住（如 dnf 锁等待）只影响它自己的链，
> 不拖累其他几十台。`by_step` 作为显式选项保留，不为默认。

### 3.2 主机内部的并发

- 同一台主机的步骤**严格串行**（SSH 会话本身也是串行的，无收益）
- 不同主机之间：沿用现有自适应并发
  `min(CPU×4, 主机数)`，上限 32

### 3.3 等待与慢步骤的处理

| 问题 | 方案 |
|------|------|
| 包管理器锁等待（apt/dnf lock） | 步骤级**重试**：`retry_count`(默认 0) + `retry_interval_sec`(默认 30)；脚本本身保持幂等 |
| 单步卡死 | 沿用全局执行超时 600s，步骤级可覆盖 `timeout_sec` |
| 某台主机某步失败 | 该主机链条**立即终止**标记 failed；其他主机不受影响 |
| 失败也要走完后续 | 步骤级开关 `continue_on_error`（默认关），如「清理磁盘失败无所谓，继续装」 |

## 4. 数据模型

```sql
-- 编排定义
CREATE TABLE orchestrations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    description  TEXT NOT NULL DEFAULT '',
    exec_mode    TEXT NOT NULL DEFAULT 'by_host' CHECK (exec_mode IN ('by_host','by_step')),
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

-- 步骤定义（by_step 模式：host_scope 留空表示共享主机集）
CREATE TABLE orchestration_steps (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id  INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
    seq               INTEGER NOT NULL,            -- 1 起，链内唯一
    template_id       INTEGER NOT NULL REFERENCES deploy_templates(id),
    params_json       TEXT NOT NULL DEFAULT '{}',
    host_scope        TEXT NOT NULL DEFAULT '',    -- 空=全部；或 JSON 数组 [host_id...]（by_step 局部主机）
    continue_on_error INTEGER NOT NULL DEFAULT 0,
    retry_count       INTEGER NOT NULL DEFAULT 0,
    retry_interval_sec INTEGER NOT NULL DEFAULT 30,
    timeout_sec       INTEGER NOT NULL DEFAULT 0,  -- 0=沿用全局
    UNIQUE(orchestration_id, seq)
);

-- 主机触发模式的差异化链（by_host：每台主机自己的 seq 序列）
CREATE TABLE orchestration_host_chains (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
    host_id          INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    seq              INTEGER NOT NULL,
    template_id      INTEGER NOT NULL REFERENCES deploy_templates(id),
    params_json      TEXT NOT NULL DEFAULT '{}',
    continue_on_error INTEGER NOT NULL DEFAULT 0,
    retry_count      INTEGER NOT NULL DEFAULT 0,
    retry_interval_sec INTEGER NOT NULL DEFAULT 30,
    UNIQUE(orchestration_id, host_id, seq)
);

-- 运行实例
CREATE TABLE orchestration_runs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    orchestration_id INTEGER NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    exec_mode        TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'running'
                     CHECK (status IN ('running','success','partial','failed')),
    total_hosts      INTEGER NOT NULL DEFAULT 0,
    ok_hosts         INTEGER NOT NULL DEFAULT 0,
    fail_hosts       INTEGER NOT NULL DEFAULT 0,
    trigger_type     TEXT NOT NULL DEFAULT 'manual',  -- manual / schedule
    created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    finished_at      TEXT
);

-- 运行明细：每主机×每步骤一行（含实时输出，纳入保留清理）
CREATE TABLE orchestration_run_steps (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
    host_id     INTEGER NOT NULL,
    host_name   TEXT NOT NULL DEFAULT '',
    host_ip     TEXT NOT NULL DEFAULT '',
    seq         INTEGER NOT NULL,
    template_id INTEGER NOT NULL,
    template_name TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','running','success','failed','skipped')),
    attempt     INTEGER NOT NULL DEFAULT 1,
    output      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    started_at  TEXT,
    finished_at TEXT
);
CREATE INDEX idx_orun_steps_run ON orchestration_run_steps(run_id);
```

要点：
- **不**为每个微步骤生成 deploy_task，避免任务记录爆炸；
  编排运行有独立的 runs/run_steps 两级记录，粒度足够回放与排查
- `orchestration_run_steps.output` 纳入现有的按天保留清理
- 成功步骤照常写 `host_installs` 安装标记

## 5. 执行引擎

```
Run(orchestrationId)
  ├─ 1. 展开为每主机队列:  map[hostID] => []stepExec{seq, script, retry...}
  │      by_host : 直接取 orchestration_host_chains
  │      by_step : 取 orchestration_steps 共享链复制给所有 scope 内主机
  ├─ 2. 建 orchestration_runs + 批量插入 run_steps(pending)
  ├─ 3. 调度循环（单 goroutine 即可）:
  │      - 自适应信号量内，为每个"有空闲且未完结"的主机派发下一步
  │      - by_step 模式额外检查全局栅栏：seq=k 未全员终态前不发 k+1
  │      - 步骤失败: 尝试重试(retry_count) → 仍败则该主机链终止(除非 continue_on_error)
  └─ 4. 每步执行体 = 复用现有 execOnHost(渲染/解密/拨号) + streamTee 实时输出
         输出增量 → SSE TopicOrchestrationProgress
         输出全量 → 回写 run_steps.output（64KB 上限不变）
```

**复用清单**（几乎不需要新造轮子）：

| 现有能力 | 在编排中的角色 |
|----------|----------------|
| renderScript + applyHostVars | 步骤脚本渲染（含 {{__seq}} 等） |
| execOnHost / streamTee | 单主机执行 + 实时日志（建议抽成公共 service 供两处调用） |
| eventbus + SSE | 新增 TopicOrchestrationProgress，前端订阅方式与部署一致 |
| host_installs 标记 | 步骤成功即打标 |
| audit_logs | action=`orchestration.run` |
| 保留清理 loop | 扩展清理 orchestration_runs 超期数据 |
| 自适应并发 | 主机级并行度 |

## 6. SSE 与进度页

事件流：`GET /api/sse/orchestration?run_id=N`

```
event: step      data={"run_id":1,"host_id":3,"seq":2,"status":"running"}
event: output    data={"run_id":1,"host_id":3,"seq":2,"output":"..."}   ← 增量日志
event: step      data={"run_id":1,"host_id":3,"seq":2,"status":"success","attempt":1}
event: done      data={"status":"partial","ok_hosts":8,"fail_hosts":1}
```

进度页布局（复用部署进度页风格）：
- 顶部：运行状态 + 主机完成度汇总条
- 主表格：每主机一行，列 = 步骤 1..N 的状态徽章（成功绿/失败红/进行中蓝/等待灰）
- 点击任一格子展开该步骤的实时日志抽屉

## 7. UI 交互（文字线框）

### 7.1 编排列表页（新增导航项：部署中心 → 任务编排）
```
[新建编排]                                [搜索____]
┌──────────────────────────────────────────────┐
│ 名称        模式     步骤数  最近运行   操作      │
│ 新机初始化   by_step   5     2h前 成功  运行 编辑 ⋯ │
│ k8s-node    by_host   4     1d前 失败  运行 编辑 ⋯ │
└──────────────────────────────────────────────┘
```

### 7.2 编排编辑器
- **模式切换**（顶部 radio）：按主机编排 / 按模板编排
- **按模板编排**：
  ```
  步骤链:  [≡ 1. 时区与NTP      参数:默认      范围:全部  ✕]
          [≡ 2. 基础软件安装    参数:默认      范围:全部  ✕]
          [+ 从模板库添加步骤]
  底部: 选择目标主机（现有多选组件 + 排序控件复用）
  ```
  步骤卡片可拖拽排序、点击展开参数表单（由模板 variables 动态生成，复用现有渲染）
- **按主机编排**：
  ```
  左栏: 已选主机列表（web-01 ● 当前编辑 / db-01 / node-03）
  右栏: 当前主机的链
        [≡ 1. 内核调优] [≡ 2. 安装Docker] [+ 添加步骤]
  快捷: [将此链复制到 → 全部/勾选主机]   ← 90% 场景一键铺开再微调
  ```

### 7.3 定时触发
现有 `deploy_schedules` 扩展一列 `target_type`(template/orchestration) +
`orchestration_id`，cron 到点跑编排；UI 在定时任务新建表单加类型切换。

## 8. 配套内置模板补充

配合编排上线，补齐「新机初始化」全家桶：

| 模板 | 内容要点 |
|------|----------|
| 基础软件安装（新增） | vim/curl/wget/tar/unzip/htop/lsof/tcpdump/bash-completion 等；自动识别 yum/dnf/apt；幂等 |
| 内核参数调优（增强现有） | 在 BBR 基础上补：文件句柄 655350、conntrack 上限、TCP 缓冲区、somaxconn、swappiness=10、arp 忽略异常通告等；分文件落盘便于回滚 |
| 时间同步（已有） | 不动 |

## 9. 分期实施

| 阶段 | 内容 | 预估工作量 |
|------|------|-----------|
| **P1** | 数据表 + 编排 CRUD API + **按模板编排**（by_step）手动执行 + 进度页（表格+日志抽屉）+ SSE | 引擎改造为主，约 60% |
| **P2** | **按主机差异化链**（by_host）+ 编辑器双模式交互 + 重试/continue_on_error + 安装标记打通 | 约 30% |
| **P3** | 定时触发编排 + 保留清理接入 + 运行历史对比视图 | 约 10% |

P1 结束即可覆盖「新机初始化」这条最高频路径。

## 10. 开放问题（需要你拍板）

1. **跨主机顺序**（先 DB 后 Web 这种）v1 用 `by_step` 模式覆盖够不够？
   更细粒度的「阶段分组」建议放 P3 之后看真实需求。
2. **编排内引用变量**：步骤参数要不要支持引用前序步骤的输出（如取到 IP 写配置）？
   实现成本高，v1 建议**不做**，用 `{{__ip}}` 等内置变量先顶。
3. **权限**：编排属于高危聚合操作（一键改 N 台），v1 沿用单一管理员账号体系不做细分，未来若有多用户再加。
4. **运行中编辑**：编排正在跑时禁止修改定义（编辑器锁定），避免语义混乱——默认这么做，OK？

---

*评审通过后按 P1 → P2 → P3 顺序实施，每阶段独立提交。*
