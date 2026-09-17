# Redis 套件方案（三张表基线 + 改造路线）

> 方法依据：`docs/新增套件说明.md`（模式 / 角色 / 步骤三张解析表）与项目级 skill
> `.workbuddy/skills/new-stack/SKILL.md`（执行序 = 对齐三张表 → 后端 → 前端 → 基线门禁）。
>
> **前置结论**：redis **不是新增套件**，它是现有 9 套件的第一张卡片（`store/registry.go:34`
> 的 `stackDrivers` 首行 = 前端卡片顺序 = `baseline/blueprint/_order.json`）。三张表已全部落地，
> 基线快照齐备（1 blueprint + 3 pipeline + 12 render + 6 case）。
> 因此本方案 = **① 三张表的权威现状（可直接作为新增其它套件的填写样板）
> ② 能力实现矩阵（已实现 / 未实现及后果）③ 改造候选清单（待拍板）**。

---

## 1. 第一张表：模式表（`store/stacks/redis/redis.go:74-93`）

| Key | Label | MinHosts | AssignMaster | HasBootstrap | DefaultHomeDir | 拓扑语义 |
|---|---|---|---|---|---|---|
| `replication` | 主从 | 2 | ✔ | ✘ | `/data/redis-repl` | 1 主 N 从，用户指定主节点 |
| `sentinel` | 哨兵 | 3 | ✔ | ✘ | `/data/redis-sentinel` | 1 主 N 从，**每台同时跑 Redis 与 Sentinel**，自动故障切换 |
| `cluster` | 集群 | 3 | ✘ | ✔ | `/data/redis-cluster` | 全员 `node`，起来后自动 `CLUSTER CREATE`；三主或三主三从 |

- `RequiresDocker: true`（`redis.go:72`）→ 运行步骤条首格自动加「Docker 环境」，套件无需声明。
- **参数按模式裁剪**：`password` / `image` 全模式；`replicas` 仅 `cluster`；`sentinel_port` 仅 `sentinel`
  （`SharedVars` 的 `Modes` 字段，`redis.go:94-99`）。`replicas` 虽然声明在蓝图里，但前端第三步
  **不渲染成输入框**（`index.js:14` 的 `visibleSharedVars` 把它过滤掉），改由集群拓扑单选框驱动，
  提交时用 `extraParams` 回填（`index.js:15`）。
- 逐主机参数 `HostVars`：`port`（默认 6379）、`home_dir`（默认 `/data/redis`）。
- 静态 `MinHosts` 不足以表达集群约束，前端再收紧一次：`minHosts(ctx)` 在 `cluster + replicas=1`
  时返回 6（`index.js:9`）；服务端权威判定在角色表之后的拓扑校验里（见 §3.2）。

## 2. 第二张表：角色表（`store/stacks/redis/plan.go:14-33`）

| 模式 | comp | role | label | 落点 | scope | 备注 |
|---|---|---|---|---|---|---|
| `replication` | `redis` | `master` | Redis Master | `AddAt(..., Boot)` | 落点（受保护） | 来源标注 `in.Src()` = manual/auto |
| | `redis` | `replica` | Redis Replica | `AddRest` | 落点 | 非引导成员全为从 |
| `sentinel` | `redis` | `master` | Redis Master | `AddAt(..., Boot)` | 落点 | |
| | `redis` | `replica` | Redis Replica | `AddRest` | 落点 | |
| | `sentinel` | `sentinel` | Sentinel | `AddAll` | 落点 | **独立进程/端口 26379 ⇒ 独立 comp**，主机视图才各出一枚芯片 |
| `cluster` | `redis` | `node` | Redis Cluster 节点 | `AddAll` | 落点 | 附 `Warn`：主从与槽由 `CLUSTER CREATE` 决定，预览不指定 |

- 运行期改名：`roles.go:7-15`（`RoleNamer`）把非主节点命名为 `replica`，
  **仅 `replication` / `sentinel`**；`cluster` 返回空串 → 交还引擎通用 `master/worker/node` 命名。
- 契约纪律：`PlanRoles` 只做「往 `in.Roles` 累加 + `Warn`」，返回 `in.NewPlan()`（落点式）；
  `PlanHosts` / 警告驱动 / `AssignMaster` 兜底提示 / 来源标注全部由引擎统一收尾。

## 3. 第三张表：步骤表（`store/stacks/redis/redis.go:22-64`）

### 3.1 形态选择

`Pipeline(mode)` 返回 **nil** ⇒ 走传统两步：**node（全员）→ bootstrap（仅该模式 `HasBootstrap`）**。
redis 是「简单套件」的标准样板，只有 `cluster` 有引导阶段。

### 3.2 脚本矩阵（含资源注入 `@@KEY@@`）

| 阶段 | replication | sentinel | cluster |
|---|---|---|---|
| `node` | `repl-node.sh` + `repl-master.conf` / `repl-replica.conf` | `sentinel-node.sh` + `sentinel-master.conf` / `sentinel-replica.conf` / `sentinel.conf` | `cluster-node.sh` + `cluster.conf` |
| `bootstrap` | — | — | `cluster-bootstrap.sh`（CLUSTER CREATE） |
| `scale_out` | — | — | `cluster-add.sh`（新节点加入 + 加槽） |
| `scale_in` | — | — | **无脚本**（基线 `render/redis__cluster__scale_in.txt:1` = `NO_SCRIPT`） |

脚本落盘目录：`store/stacks/redis/scripts/`（5 枚）与 `configs/`（6 份 conf），
经 `store/embed.go` 的 `stacks/*/scripts stacks/*/configs` 递归 embed；
**改脚本必须重建 exe**，并刷新渲染基线（`SNAPSHOT_WRITE=1 go test ./store/ -run TestSnapshotRenderScripts`）。

---

## 4. 能力实现矩阵（对照 `store/stackkit/capability.go`）

### 4.1 已实现（8 项）

| 能力 | 落点 | 承担的业务规则 |
|---|---|---|
| `RolePlanner` | `plan.go` | 三模式落点（第二张表） |
| `TopologyBuilder` | `topo.go` | 核心约束：主从/哨兵必须指定主节点；集群副本数只能 0/1、三主三从须 ≥6 且偶数、主机数需被 `replicas+1` 整除、至少 3 个主节点。**文案被测试与用户依赖，逐字不可改** |
| `DefaultsProvider` | `defaults.go` | 仅运行期兜底 `cluster.replicas`（create 期不动实例参数） |
| `RoleNamer` | `roles.go` | worker → replica（仅主从/哨兵） |
| `ServiceRegistrar` | `register.go` | 入口登记：`Redis 主/从`、集群 `Redis Cluster`、哨兵模式额外登记 `redis-sentinel://ip:sentinel_port` |
| `ProbePlugin` | `probe.go` | 期望容器 + 脚本尾片段（PING/INFO replication/INFO server/CLUSTER/SENTINEL）+ 输出解析 + 汇总与客户端命令 |
| `EndpointProvider` | `probe.go:239` | `redis://` 与哨兵的 `redis-sentinel://` |
| `ScaleGuard` | `probe.go:261` | 主从/哨兵不得移除唯一主节点；集群至少保留 3 节点 |

### 4.2 未实现（8 项，均走引擎通用默认）

| 能力 | 未实现的后果（redis 语境） |
|---|---|
| `SelfPlanning` | 不需要：落点式计划足以表达三模式（bigdata 才需要清单式） |
| `CreateValidator` | 创建期无套件专属校验；集群的整除规则在 `TopologyBuilder`，奇数主机拦截在前端 `canStep3` —— **同一规则两处实现，口径可能漂移** |
| `VarInjector` | 通用注入表已够用（per-host 只有 port / home_dir / roles） |
| `ScaleInGuard` | 见 §5-R2：哨兵模式的**仲裁多数派**没有套件级保护 |
| `ScaleInDrainer` | **见 §5-R1**：集群缩容不软下线 |
| `BootstrapHostResolver` | 通用规则（主节点即引导机）对三模式都成立 |
| `InstanceVarInjector` | 无 HA 落点矩阵，不需要 |
| `AssetProvisioner` | 见 §5-R4：镜像以外无离线物料需求 |

---

## 5. 改造候选清单（按价值 / 风险排序，待拍板）

| 编号 | 目标 | 事实依据 | 影响面 | 风险 |
|---|---|---|---|---|
| **R1** | **集群缩容软下线**：新增 `PhaseScaleIn` 的 `cluster-del.sh` + 实现 `ScaleInDrainer`，缩容前先 `CLUSTER FORGET` / 迁移槽 / `del-node` 再停容器 | 套件侧 `scale_in` 无脚本（§3.2）；`ScaleInDrainer` 未实现（§4.2） | `redis.go` + 新脚本 + 渲染基线重录 | 中：涉及集群数据面，需真机验证；脚本必须幂等 |
| **R2** | **哨兵模式仲裁保护**：`ScaleGuard`/`ScaleInGuard` 增加「缩容后哨兵个数 ≥ 多数派（≥3 且为奇数）」校验 | `ScaleGuard` 现只挡「唯一主节点」（`probe.go:261-281`）；引擎通用下限是否覆盖需实测确认 | 仅 `probe.go` 一处 + 单测 | 低：纯校验，失败即报错不会破坏存量 |
| **R3** | **创建校验口径收敛**：把集群整除/奇数规则收进 `CreateValidator`，前端 `canStep3` 只留交互提示 | §4.2 首条：同一规则前端 + 后端两处 | 新增 `validate.go` + `index.js` 微调 | 低：需保持报错文案与现有测试一致 |
| **R4** | **镜像资产化**：实现 `AssetProvisioner` 声明 redis 镜像 tar / 私有 registry 物料，复用已有的上传→SFTP 分发链路 | 现有 `image` 默认 `redis:7` 依赖目标机拉取；离线/内网环境会失败 | `assets.go` + 前端资产检查条 | 低：机制已在 bigdata 验证过 |
| **R5** | **探活页签化**：`verifyTabbed` + `verifyComponents` 让 `sentinel` 模式把「哨兵」与「Redis 本体」分成两个页签 | 现在哨兵模式的「哨兵」检查项与 redis 本体混在同一张节点表 | `hooks.js` 新增 2 个 hook | 低：骨架侧过滤规则已统一实现 |
| **R6** | **多实例隔离**：哨兵主名固定 `mymaster`、`home_dir` 默认固定，同主机部署第二个实例会冲突 | `probe.go:64` 写死 `mymaster`；`redis.go:102` 默认目录固定 | 参数化 `master_name` | 低-中：改动面小但牵动脚本与 conf 模板 |

---

## 6. 选定候选后的执行序（skill §6 / §7）

1. **冻结**：redis 快照已齐备（`baseline/cases/redis_*.json` 6 份、`render/redis__*__*.txt` 12 份、
   `_order.json`），无需补录；若改动脚本渲染内容，先跑一次不写盘的比对作为「改前基线」。
2. **改代码**：只动 `store/stacks/redis/` 与 `template/static/stacks/redis/`；
   禁止 import `store`、禁止套件间互 import；能力靠类型断言覆盖。
3. **门禁**（缺一不可）：
   ```
   go build ./... && go vet ./... && go test ./store/ ./api/stack/
   node --check template/static/stacks/redis/*.js
   node script/_fe_smoke.js                       # 57 脚本装载 + 模板逐字节 + 裸引用
   python script/check_stack_lines.py --targets   # 行数 + 套件分支 0 + 专属选择器 0
   ```
   有意变更才允许 `SNAPSHOT_WRITE=1` / `--record`，且必须在 `metrics.json` 的 `_note` 写明差异来源。
4. **重建重启**：前端与脚本都走 `go:embed` —— 停进程 → `go build -ldflags "-s -w" -o infra-ops-server.exe .`
   → 后台起服务 → `GET /api/healthz` 200；再从服务下发核对改动的资源字节。
5. **真机验证**：R1 / R6 涉及数据面与脚本，必须在真实集群上跑一次完整 create → scale_in 闭环，
   并检查 `cluster_state` 与哨兵多数派。

---

## 7. 风险与纪律

- `topo.go` / `probe.go` 的**报错与检查项文案被快照与测试冻结**，改造时应保持逐字一致，
  否则会在 `go test ./store/` 阶段暴露为差异而非回归。
- 三张表任何一格改动都要同步刷新受其驱动的产物：`baseline/blueprint/redis.json`、
  `baseline/pipeline/redis__<mode>.json`、对应 render 基线。
- 前端「探活组件页签」的过滤规则由骨架统一实现，套件只需在 `EndpointProvider`
  或检查项上打 `component=<key>` 即可被页签识别（`docs/新增套件说明.md` §5.1）。

---

## 8. 本轮实施记录（2026-09-15）

按「每个套件的专属逻辑放在自己目录里、通用层零套件分支、套件之间零影响」的约束，
落地 R1 / R2 / R4 / R5 / R6 五项（R3 暂缓，理由见 §8.3；**R4 经用户复核后已回退，见 §8.5**）。

### 8.1 已落地

| 编号 | 落地位置 | 关键实现 |
|---|---|---|
| **R2** | `store/stacks/redis/scale.go`（新） | `ScaleGuard` 增加哨兵多数派校验：缩容后哨兵个数（= 剩余主机数）< 3 时拒绝 —— 引擎的 quorum 是 `len(hosts)/2+1`，2 台时一台故障就无法仲裁。原有「不得移除唯一主节点」「集群 ≥ 3 节点」保留 |
| **R5** | `probe.go` + `template/static/stacks/redis/hooks.js` | 端点与检查项打 `component`（`redis` / `sentinel`）；`hooks.js` 提供 `verifyTabbed`/`verifyComponents` 等 7 个页签挂点，仅哨兵模式启用；骨架 `probeAttributed` 自动剔除空壳页签 |
| **R1** | `scripts/cluster-del.sh`（新）+ `redis.go` 的 `PhaseScaleIn` + `scale.go` 的 `DrainParams` | 锚点选存活主节点 → 孤儿从重挂 → 按持有槽数 `--cluster reshard` 迁槽 → 先摘从后摘主 `del-node` → 校验 `cluster_state` 才放行停容器；全程幂等 |
| **R6** | `configs/sentinel.conf` + `probe.go` | `master_name` 参数化（默认 `mymaster`，Blueprint 按 `Modes: [sentinel]` 裁剪）：conf 模板、探活脚本文案、接入提示三处同步 |
| **R4** | ~~`store/stacks/redis/assets.go`（新）+ 三个 node 脚本~~ **已回退** | 原方案：声明离线镜像包（版本跟随 `image` 的 tag），复用上传 → SFTP 分发链路；脚本先 `docker load`，再用 `docker image inspect` 决定是否 `pull`。**复核结论：不必要** —— 第三步参数里的 `image` 本身就是镜像仓库入口（可填私有 registry），离线镜像 tar 属于重复手段，反而在第二步多出一条提示条。已整体删除，脚本回到原有 `docker compose pull`。通用资产机制（bigdata 的 Hive MySQL 驱动）与 `/api/stacks/assets` 接口**保留不动** |

### 8.2 顺带修掉的引擎级缺口（R1 的前提）

`remove_nodes` 这类**运行期参数不在蓝图变量声明里**，而 `stack_drain.go` 只调用
`deploy.RenderScript`（该函数仅替换蓝图里声明的名字），因此缩容脚本里的
`{{remove_nodes}}` **一直是字面量**：

- redis 场景：脚本拿不到待下线节点，软下线空转；
- ES 冷热温场景（既存缺陷）：`{{remove_nodes}}` / `{{remove_masters}}` / `{{self_ip}}` 三个都残留，
  实际会朝 `http://{{self_ip}}:9200` 发请求、排空名为 `{{remove_nodes}}` 的节点，
  等回绿超时后**中止缩容**。

已补 `applyRuntimeVars`（`api/stack/stack_vars.go`）并在 `stack_drain.go` 渲染后注入
`self_ip` 与套件给出的 drainParams —— 无套件分支、不改任何套件的注入表。
防回归用例：`api/stack/stack_drain_test.go`。

### 8.3 暂缓

- **R3 创建校验口径收敛**：`TopologyBuilder` 的文案被 `topo_test.go` 与快照冻结，
  把整除规则搬进 `CreateValidator` 会形成「两处都报错」或需要改动既有断言，
  收益（消除前端/后端双实现）与风险不匹配，留待后续单独评估。

### 8.4 验证证据

- `go test ./store/ ./api/stack/ ./store/stacks/redis/` 全绿；新增 redis 用例 6 个 + 引擎注入用例 2 个。
- 前端：`_fe_smoke.js` 57 脚本装载 + 模板逐字节一致 + 裸引用门禁通过；
  另用真实快照（`baseline/cases/redis_sentinel.json`）在沙箱里跑通 12 项页签行为断言（含反向验证）。
- 脚本：6 枚 redis 脚本 `bash -n` 全过。
- 基线：蓝图 / 渲染 / 用例三类快照按 `SNAPSHOT_WRITE=1` 重录（差异仅为本轮有意变更），
  `check_stack_lines --targets` 通过并在 `_note` 写明来源。
- **待真机验证**：R1 的迁槽/摘除链路（需实际集群）。R4 已回退，不再需要镜像 tar。

### 8.5 用户复核后的两项调整

#### (1) 撤销 R4：Redis 不再做离线镜像资产

判断依据：第三步「集群共享参数」里的 `image` 字段本身就是镜像入口（可填私有 registry 或代理地址），
再叠一套「上传 tar → SFTP 分发 → 脚本 `docker load`」属于重复手段，且在第二步多出一条常驻提示条。

回退清单（全部还原到改动前状态，通用资产机制零改动）：

- 删除 `store/stacks/redis/assets.go`（`AssetProvisioner` 声明）；
- `template/static/stacks/redis/topo.js` 77 → 19（第二步资产检查条及其 methods / computed / mounted）；
- `template/static/stacks/redis/style.css` 28 → 4（`.stk-redis-asset*` 全部样式）；
- `repl-node.sh` / `sentinel-node.sh` / `cluster-node.sh`：`docker load` + `docker image inspect` 分支
  回退为原有两行 `docker compose pull`；
- `store/stacks_test.go` 140 → 136（去掉离线镜像兜底断言，R1 的 `del-node` 断言保留）；
- `store/stacks/redis/scale_test.go` 去掉 `TestRequiredAssetsImage`。

`bigdata` 的资产声明、`/api/stacks/assets` 接口、`stack_asset_push.go` 分发链路、`wizard.js` 的
`assetCheck` 响应式字段**均未改动**（仍有其它套件在用）。

#### (2) 第三步「填写参数」版式：主机列表向下铺满 + 页脚虚线

原状：`.deploy-host-vars-list` 固定 `max-height: 380px`，主机少时列表下方留出大片空白
（2560×1279 实测留白 **270px**，正是用户截图里的观感）。

改法（**通用骨架，零套件分支**，所有套件同步受益）：

- `template/static/stacks/common/tpl-wizard.js`：第三步根节点加 `class="stk-param-step"`（行数不变）；
- `template/static/stacks/common/common.css`：
  - `.stk-wizard-dialog .el-dialog__body` 改为 `flex` 列；
  - `.el-dialog__body > *` 默认 `flex: 0 0 auto`（第一/二步沿用「内容高于 body 就整页滚动」的旧行为）；
  - `.el-dialog__body > .stk-param-step` 为 `flex: 1 1 auto`，吸收弹框剩余高度；
  - `.stk-param-step .deploy-host-vars-list` 为 `flex: 1 1 auto; max-height: none`，在内部滚动；
  - **安全阀**：列表 `min-height: 160px` 不被压扁 + 该步骤 `overflow-y: auto`，
    保证弹框过矮时改由步骤整体滚动，不会把列表挤到 0 高而溢到页脚上；
  - `.stk-wizard-dialog .el-dialog__footer` 加 `border-top: 1px dashed`，在按钮上方标出内容末尾。

实测（`script/measure_wizard_layout.html`，2560×1279，5 台主机）：

| | 列表高度 | 列表→页脚留白 | 虚线→按钮 |
|---|---|---|---|
| 改前 | 380px（被 max-height 截断） | **270px** | 无虚线 |
| 改后 | 613px（铺满） | 20px（= body 内边距） | 15px |

3 台主机时列表 613px 且不出现内部滚动条（内容恰好放下）；12 台主机时仍为 613px + 内部滚动。

> ⚠️ **动 `.stk-param-step` / `.deploy-host-vars-list` / `.stk-wizard-dialog` 之前先跑测量台**，
> 不要再凭算术估算（该版式此前已因估算返工多次）：
> `chrome --headless=new --window-size=2576,1374 --virtual-time-budget=3000 --dump-dom
> "file:///D:/project/go/infra-ops/script/measure_wizard_layout.html?n=5"`（`&old=1` 还原改前对照）。

