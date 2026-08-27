# 任务 04 前端执行文档 · 运行抽屉（三层结构 + 双 SSE）

## 交互总览

点击「运行」确认后：创建运行 → **自动打开 50% 宽抽屉**进入实时监控。运行中的「进度」按钮与已结束的「详情」按钮（含行点击）打开**同一个抽屉**：运行中走双 SSE 实时流；回溯/已结束只连详情流拿快照。纯只读，抽屉内无任何操作按钮。

## 抽屉结构（自上而下）

```
┌─────────────────────────────────────────────┐
│ 任务名 · run #12        [运行中] 成功0·失败0·共3台 │ ← 头部：run 元信息
├─────────────────────────────────────────────┤
│ L1 [1 NTP同步✓]→[2 基础软件●]→[3 内核调优]→[4 Docker] │ ← 步骤条：可点击
├─────────────────────────────────────────────┤
│ L2 主机层：IP · 主机名 · 状态徽章（运行中/成功/失败/跳过）│
├─────────────────────────────────────────────┤
│ L3 日志层：等宽字体流，行格式 [HH:MM:SS] IP 内容，     │
│    多主机混排，跟随滚动                                       │
└─────────────────────────────────────────────┘
```

### L1 步骤条状态

| 状态 | 样式 |
|------|------|
| 未开始 | 灰、虚化 |
| 执行中 | 品牌色高亮 + 脉动点 |
| 已完成·全成功 | 绿（勾） |
| 已完成·部分失败 | 黄（勾+警示） |
| 已完成·全失败 | 红（叉） |
| 全跳过 | 灰 |

### L2 主机行

仅三/四态徽章：运行中（蓝脉动）/ 成功（绿）/ 失败（红）/ 跳过（灰）。**无错误信息列**——错误只出现在 L3。

### L3 日志行

- 格式：`HH:MM:SS  10.0.1.11  日志内容`（时间取后端 created_at，IP 取后端 host_ip，内容 text）
- 多主机混排，不做主机过滤
- 新行到达自动滚到底部；用户向上滚动时暂停自动滚动，回到底部后恢复
- 引擎状态行（开始执行/重试/成功/失败）与 SSH 输出行同样渲染

## 自动追踪（核心交互）

`followMode` 布尔开关：

1. **抽屉打开**：运行中 → `selectedSeq` = 当前执行中的步骤（run_steps 中首个含 running 单元格的 seq），`followMode=true`；已结束 → `selectedSeq` = 第 1 步，`followMode=false`
2. **自动切换**：`followMode=true` 时步骤流收到 `step started(seq)` → `selectedSeq=seq`，重连详情流 `?step=seq`
3. **手动切换**：点击步骤条 → `selectedSeq=点击值`；若点击的是**当前执行中的步骤**则 `followMode=true`（恢复自动追踪），否则 `followMode=false`（暂停自动追踪）
4. 步骤条上给当前执行中的步骤加「追踪中」视觉标记，手动模式且未追踪时给提示（如标题栏小字「已暂停自动追踪，点击执行中步骤恢复」）

## SSE 连接管理

| 场景 | 步骤流 | 详情流 |
|------|--------|--------|
| 运行中打开 | ✅ 连接至 done | ✅ 连接 `?step=selectedSeq`，随切换重连 |
| 回溯/已结束打开 | ❌ 不连接 | ✅ 连一次（init 快照 → done 关闭），切步骤时重连 |

- 详情流重连：关闭旧 EventSource → 新开 `?step=M`（init 快照覆盖 L2/L3，天然补齐断连间隙）
- 抽屉关闭：两条连接全部关闭
- 步骤流 `done`：关闭连接 + 刷新 run 元信息（终态/统计）+ `loadList()`（列表状态翻转）
- 详情流 `done`：关闭连接（run 元信息由步骤流 done 或主动刷新兜底）

## 事件处理

- 步骤流 `init`：重建 L1 全量骨架（seq/name/state/aggregate）
- 步骤流 `step`：`started` → 更新 L1 该步为执行中；`finished` → 更新聚合色
- 详情流 `init`：`hosts` 覆盖 L2；`logs` 覆盖 L3（重置滚动到底）
- 详情流 `host`：更新 L2 对应主机徽章
- 详情流 `log`：追加 L3 行 + 自动滚动

## 数据打底

打开抽屉先 `GET /api/orchestration/runs/:id`（run + 全部明细行），前端按 seq 分组构建 L1 骨架与 L2 初始主机列表，再连 SSE 增量。

## 移除项（旧矩阵弹窗）

删除：`detailVisible`、`matrixRows`、`stepSeqs`、`logCell`、`logRow`、`showLog`、`cellText`、`logText`、`seqLabel`、旧 `connectSse`/`onProgress`/`closeSse`、矩阵 `el-dialog` 模板块。

## 文件组织（300 行规约）

| 文件 | 内容 |
|------|------|
| `template/static/pages/orchestration_drawer.js`（新增） | `window.OrchDrawerMixin`：抽屉 data/computed/methods（openRunDrawer / selectStep / 双 SSE 管理 / L1-L3 渲染） |
| `template/static/pages/orchestrations.js`（瘦身） | 页面骨架 + 列表/编辑器逻辑 + `mixins: [window.OrchDrawerMixin]`；移除矩阵弹窗相关 |
| `template/static/style.css` | 追加步骤条 / 日志流 / 追踪标记样式（挂 `orch-` 前缀） |
| `template/index.html` | 在 orchestrations.js 之前引入 orchestration_drawer.js |

## 执行步骤

1. [ ] `orchestration_drawer.js` mixin 骨架：抽屉模板（头部/L1/L2/L3）+ 打开/关闭流程 + GET 打底分组 —— 验收：`node --check` 通过，打开抽屉（已结束任务）可见静态三层结构与步骤聚合色
2. [ ] 双 SSE 接入与自动追踪：步骤流/详情流连接管理、事件处理、followMode 切换逻辑 —— 验收：运行中实时刷新，手动点历史步骤暂停追踪，点回执行中步骤恢复
3. [ ] 旧矩阵弹窗移除 + orchestrations.js 瘦身合并 mixin + style.css 样式 —— 验收：控制台零报错，无残留死代码引用
4. [ ] 联调验收：运行→自动开抽屉→三层实时联动→结束翻转列表状态；回溯已结束任务逐步骤点开日志 —— 验收：全流程无断流/无丢行，控制台零报错

## 验收标准

- 点「运行」确认后抽屉自动滑出，L1 随引擎推进自动高亮前进，完成后聚合色正确（全绿/部分黄/全红）
- L2 徽章三态实时变化；失败主机后续步骤自动置「跳过」
- L3 混排日志实时追加，格式 时间+IP+内容；向上滚动不强制拉底，回底恢复跟随
- 手动点历史步骤：L2/L3 切换为该步骤数据且不再自动切换；点回执行中步骤恢复自动追踪
- 已结束任务打开抽屉：仅详情流一次快照，逐步骤点开可看全部日志（含状态行）
- 旧矩阵弹窗代码全部移除，控制台零报错

## 进度记录

- 2026-08-27：任务立项，三件套编写完成，待确认后执行。
- 2026-08-27：步骤 1-3 执行完成。orchestration_drawer.js mixin（三层数据/双 SSE/自动追踪/日志滚动）+ orchestrations.js 模板抽屉 + 旧矩阵弹窗移除（matrixRows/stepSeqs/logCell/connectSse/onProgress 等全部清除，grep 无陈旧引用）；index.html 引入顺序调整；style.css 追加 orch- 前缀样式；node --check 通过、模板标签配平。
- 2026-08-27：联调验收反馈修复（3 项）——① L2 状态徽章远离主机：`.orch-rd-host-name` 的 `flex:1` 把徽章推到行尾，去掉后徽章紧贴主机名，IP 列保留 min-width 对齐；② L3 新增主机过滤：sec-title 改 flex 布局挂 el-select（选项为本步骤主机，按 IP 过滤），filteredLogs computed 过滤，切换步骤时随 applyStepLocal 重置，空态区分「无日志/过滤无结果」；③ 自动追踪恢复失效：selectStep 的判重守卫（`selectedSeq===seq` 提前 return）挡在 followMode 赋值之前——用户点击已选中的执行中步骤无法恢复追踪。修复为先算 liveSeq（首个未完成步骤）并赋 followMode 再判重；顺带 onStepsEvent 自动切换在同步号时跳过重连。node --check 通过。
- 2026-08-27：验收反馈修复（4 项）——④ 追踪状态标记仅运行中显示：追踪指示（自动追踪中/已暂停）用 `<template v-if="runStatus === 'running'">` 守卫，已结束运行不再显示「已暂停自动追踪」提示（无执行中步骤可点击，提示无意义）；运行中结束瞬间随 runStatus 翻转自动隐藏。node --check 通过。
- 待办：联调验收（运行→自动开抽屉→三层实时联动→回溯逐步骤）由用户启动页面执行。
