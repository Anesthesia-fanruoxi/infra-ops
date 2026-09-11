# 大数据底座 · 部署前角色物化（RolePlan）设计

> 状态：已实施（2026-09-11）· 单测 + 存量集群 API 实测通过
> 追加（同日）：全套件覆盖——通用套件（redis/kafka/elasticsearch/rabbitmq/rocketmq/nacos/powerjob）角色预览随「部署前置」展示（用户拍板：预览在参数填写后与 Docker 预检同屏，而非选完主机即弹）；planGenericRoles 按套件+模式展开角色目录；落点角色（scope=""）主机禁止缩容；预览接口 `stack_key/host_params` 扩展。
> 评审拍板：
> ① Plan **只保留最近一份**，不做历史 rev 留存（原 §九-5 开放问题关闭）；
> ② **扩容/缩容只允许操作全员角色节点（数据/工作节点）**——落点角色（主备/JN/ZK/Coordinator 等）部署后即冻结，不参与扩缩容，也无需在扩缩容时重规划落点（原 §五-3 的「重排」路径移除）。
> 关联：`api/stack_engine.go`、`api/stack_instance.go`、`api/stack_verify.go`、`api/stack.go`、`model/stack.go`、`template/static/pages/stacks.js`、`template/static/pages/stacks-forms.js`
> 关联文档：`docs/bigdata-ha-design.md`（§3.2 角色规划、§4.1 占位符、§4.2 分配算法）
> 目标：把角色分配从「执行期推导」改为「部署前物化」，让部署 / 探活 / 扩容 / 重装**共读同一份计划**，并支持「手动 + 自动」混合、可预览、可冻结、可审计。

---

## 一、背景与动机

### 1.1 现状：角色在**执行期**被反复推导

当前大数据底座的角色落点（NN1/NN2/RM1/RM2/JN/ZK…）没有独立归宿，只有两处间接信息：

1. **覆盖式参数** `params_json.masters`（用户在「角色规划」步骤填的组件→主机 IP 映射），只记住**被显式指定的**角色；
2. **主机级粗粒度角色** `stack_run_host.Role` / `stack_instance_host.Role`，取值仅 `master` / `worker` / `node` / `replica`。

真正的细粒度落点由 `stackBigdataRole(hosts)`（`api/stack_engine.go:1194`）**每次调用时现场计算**，规则见 §4.2：

```
primary(comp)   = masters 指定 ?? 主节点
secondary(comp) = masters 指定 ?? seq 最小的非 primary 主机
jn / zk         = masters 指定 ?? seq 前 3 台
hive_db_host    = masters 指定 ?? primary(hive)
```

该函数当前有 **5 处生产调用点**，各自独立推导：

| # | 调用点 | 用途 |
|---|--------|------|
| 1 | `stack_engine.go:1465` | 部署期生成注入变量（`bigdataMasterExtra` + `bigdataHAVarTable`） |
| 2 | `stack_engine.go:954` | `registerBigdataServices` 登记服务入口 |
| 3 | `stack_instance.go:481` | `bigdataProtectedHosts` 缩容保护清单 |
| 4 | `stack_instance.go:842` | `validateBigdataMasters` 主从互斥校验 |
| 5 | `stack_verify.go:134` | **探活**期望容器 / 端点矩阵 |

此外 `buildCreateHosts`（bootstrap 落点）、`buildAddComponentHosts`（masters 合并）、`buildReinstallHosts` 也各自重置或读取 masters 参数。

### 1.2 问题：多消费方推导 → 结果可能不一致

**探活 41 误判**即为典型：41 是 HA 备主（跑 namenode2 / zkfc2 / journalnode / rm2 / spark-master2 / flink-jobmanager2 / hbase-backup-master / hive-metastore2 / hive-hiveserver2-2 / trino 等 12 个容器），却被探活当作 worker 校验。

根因不是算法错，而是**输入源不统一**：探活走的是 `instanceHostsAsRunHosts(inst)`，而补丁前主机级 `params_json` 里残留的旧 `masters` 覆盖了引擎权威矩阵——两处对「谁是备主」得出不同答案。

同类隐患：

- **落点漂移不可见**：`secondary = seq 最小的非 primary`。缩容移除一台主机后，若它恰好是某组件备角色落点，落点会**静默漂移到另一台**，用户无感知。
- **手动/自动无法区分**：分不清某角色是「用户指定」还是「自动算的」，出问题无法定位是谁的决策。
- **无法预览**：提交前看不到最终落地拓扑，只能靠"应该会分到那台"。
- **扩缩容无重规划语义**：成员变化后不重新规划，却继续沿用旧推导，边界情况容易出错。

### 1.3 设计目标与非目标

**目标**

1. **单一事实源**：角色在部署前计算一次、持久化为 `RolePlan`，此后所有消费方**只读**。
2. **手动 + 自动混合**：支持逐角色标注来源（`manual` / `auto`），自动角色也在规划时**算出并冻结**。
3. **可预览可编辑**：提交前展示「主机 × 角色」矩阵，可微调、可一键自动填充。
4. **可审计**：记录 rev、生成时间、触发操作、来源标注。
5. **零回归**：非 HA 单机路径行为不变；存量实例自动兼容。

**非目标**

- 不改动 `stackBigdataRole` 的分配算法本身（该算法已是权威，仅将调用时机前移）。
- 不引入「单机 → HA 停机转换」（沿用 `bigdata-ha-design.md` §五 结论，本期不做）。
- 不覆盖非 bigdata 套件（redis 主从等沿用现有 `stackHostRole`）。

---

## 二、核心概念：RolePlan

### 2.1 定义

**RolePlan = 一次部署/变更操作执行前，对全部参与主机的角色落点的确定性、已冻结、带来源标注的完整描述。**

它同时是一份**可执行契约**：部署、探活、缩容保护、重装全部以它为准，不再现场推导。

### 2.2 数据结构

```jsonc
{
  "rev": 1,                          // 版本号，每次重规划 +1
  "op": "create",                    // create | scale_out | add_component | replan
  "generated_at": "2026-09-11T12:30:00+08:00",
  "generated_by": "auto",            // auto(引擎生成) | manual(用户编辑后提交)
  "ha": true,
  "hosts": [
    {
      "host_id": 3,
      "host_name": "node-40",
      "host_ip": "192.168.3.40",
      "seq": 1,
      "roles": [
        { "comp": "hdfs",      "role": "nn1",  "label": "NN1 (Active)", "source": "manual" },
        { "comp": "hdfs",      "role": "jn",   "label": "JournalNode",  "source": "auto"   },
        { "comp": "hdfs",      "role": "dn",   "label": "DataNode",     "source": "auto", "scope": "all" },
        { "comp": "zookeeper", "role": "zk",   "label": "ZK-1",         "source": "auto"   },
        { "comp": "yarn",      "role": "rm1",  "label": "RM1 (Active)", "source": "auto"   }
      ]
    }
  ],
  "injected": {                      // 物化后的注入变量（部署直接取用）
    "__zk_ips": "192.168.3.40,192.168.3.41,192.168.3.63",
    "__hdfs_entry": "hdfs://ns1",
    "__nn1_ip": "192.168.3.40",
    "__nn2_ip": "192.168.3.41",
    "__jn_ips": "192.168.3.40:8485,192.168.3.41:8485,192.168.3.63:8485",
    "__rm1_ip": "192.168.3.40",
    "__rm2_ip": "192.168.3.41"
  },
  "warnings": [
    "NN2 由自动分配落到 192.168.3.41（seq 最小非主）"
  ]
}
```

### 2.3 两类角色：落点角色 vs 全员角色

这是本设计的关键区分，直接决定「扩缩容与落点角色的边界」：

| 类别 | `scope` | 含义 | 成员变更时 | 示例 |
|------|---------|------|-----------|------|
| **落点角色** | `single`（默认） | 全局唯一或具体落点明确的角色 | **部署后冻结**：不随扩缩容变动；扩缩容不允许触及承载落点角色的主机（见 §3.3） | nn1 / nn2 / rm1 / rm2 / jn（前 3 台）/ spark_m1 / spark_m2 / jm1 / jm2 / hm1 / hm2 / ms1 / ms2 / hs2a / hs2b / db / zk |
| **全员角色** | `all` | 每台成员机都承担 | 自动跟随成员集合，**无需重规划** | dn / nm / spark-worker / flink-tm / rs / trino-worker |

> 现状把两类混在一起（全员角色隐式存在、落点角色靠 seq 推导），这正是「落点静默漂移」的温床。物化后，落点角色在部署时一次定格、终身冻结；扩缩容被硬性限制在全员角色节点上，从机制上杜绝漂移。

### 2.4 角色目录（物化表的列）

| 组件 | 落点角色（`scope=single`） | 全员角色（`scope=all`） |
|------|--------------------------|----------------------|
| hdfs | nn1、nn2、jn（≤3，multi） | dn、zkfc（与 nn 同机） |
| zookeeper | zk（≤3，multi） | — |
| yarn | rm1、rm2 | nm |
| spark | spark_m1、spark_m2 | spark_worker |
| flink | jm1、jm2 | flink_tm |
| hbase | hm1、hm2 | rs |
| hive | ms1、ms2、hs2a、hs2b、db | — |
| trino | coordinator | trino_worker |

> 该目录是前端「主机 × 角色」矩阵的行定义来源，也是后端 planner 的展开依据。新增组件只需扩充此表。

### 2.5 与 `masters` 参数的关系

**`masters` 参数降级为 planner 的输入之一，不再是角色的存储形态。**

- 规划时：`masters` 中**有值**的键 → 对应角色 `source=manual`；**无值**的键 → 按 §4.2 算法落位，`source=auto`。
- 落库后：`masters` 可保留用于审计/回滚，但所有消费方**只读 RolePlan**，不再读 `masters`。
- 长期：`masters` 键与 RolePlan 角色键做映射收敛（见 §六-3）。

---

## 三、生成时机与生命周期

### 3.1 时机矩阵

| 操作 | 是否生成/刷新 Plan | 触发方式 | 说明 |
|------|-------------------|---------|------|
| 新建 create | **生成 rev 1** | 提交前预览（dry-run）→ 确认后随创建落库 | 前端可预览可编辑 |
| 加装 add_component | **重规划 → rev+1** | 提交前预览 | 组件集合变化，只为新增组件补落点；已冻结落点**原样继承** |
| 扩容 scale_out | **增量追加，不重排落点** | 提交前预览（只读展示） | 新机只追加全员角色（dn/nm/worker…），落点角色原样冻结；无「重排」入口 |
| 缩容 scale_in | **不刷新，前置校验** | 缩容前拦截 | 仅允许移除**不承载任何落点角色**的主机（见 §3.3） |
| 重装 reinstall | **不刷新，只读** | — | 沿用当前 Plan，保证重装后落点不变 |
| 探活 verify | **不刷新，只读** | — | 唯一事实源，杜绝 41 类不一致 |
| 卸载 uninstall | 不读 | — | 走现有容器清单 |

### 3.2 版本与重规划

- 每次生成写入 `rev`（自增）、`op`、`generated_at`、`generated_by`。
- **实例只保留最近一份 Plan（已拍板）**：覆盖式更新，不做历史 rev 留存；审计需求由 `generated_at` / `generated_by` / 操作日志覆盖，不建 `stack_role_plan_rev` 表。
- 「重规划」仅发生在**新建与加装**：以当前成员 + 参数为输入重新跑 planner；已有落点角色原样继承（含 manual 指定），只为新增组件补落点；新增角色不手动指定 → `auto`。

### 3.3 扩缩容边界与保护规则（迁移自 `bigdataProtectedHosts`，按拍板 ② 收紧）

扩缩容**只针对数据/工作节点**（承载全员角色的机器）。缩容/移除主机前校验：

1. 该主机是否承载**落点角色**？若是 → **直接拦截**（不提供「先重规划再缩」的出路），提示「主机 X 承载 NN2 / JN / RM-2…，不可缩容；落点角色主机不在扩缩容范围内」。
2. 该主机仅承载**全员角色** → 放行，全员角色随成员自然退出（沿用现有语义）。
3. 扩容侧同理：新机自动只承担全员角色，不提供落点分配入口；如确需调整主备布局，走「重装 + 手动指定 masters」路径，而非扩缩容。

---

## 四、后端改造

### 4.1 planner：`stackBigdataRole` 升格为规划器

**算法一行不改**，仅新增一层包装，把结果展开为可持久化结构：

```go
// planBigdataRoles 以现有 stackBigdataRole 为唯一算法，展开为完整 RolePlan。
// opts.Masters 为手动指定（来自 masters 参数或前端编辑）；缺省键按 §4.2 自动落位。
func planBigdataRoles(bp *store.BuiltinStack, hosts []model.StackRunHost,
    opts PlanOptions) (model.RolePlan, error)
```

职责：

1. 调用 `stackBigdataRole(hosts)` 得到权威落点；
2. 展开**全员角色**到每台主机；
3. 逐角色标注 `source`（`opts.Masters` 命中 → `manual`，否则 `auto`）；
4. 生成 `injected`（复用 `bigdataMasterExtra` + `bigdataHAVarTable` 的产物）；
5. 执行 §4.3 校验，产出 `warnings` / 返回错误。

> 关键：planner 是**唯一**角色计算入口；`stackBigdataRole` 保持为内部算法，禁止其他文件直接调用（否则又制造多源）。可通过「仅 planner 同文件可调用」的约定 + 单测防线保障。

### 4.2 存储：实例新增 `RolePlanJSON`

`model/stack.go` → `StackInstance` 增加字段：

```go
RolePlanJSON string `json:"role_plan_json"`
```

- 存最近一份 `RolePlan`。
- 无需新建独立表；与 `ParamsJSON` / `ComponentsJSON` 同级。
- SQLite 增列：新增迁移 `migrateV24`（`store/migrations.go`，当前最高为 V23），复用现有 `addColumnIfMissing(db, "stack_instances", "role_plan_json", ...)`：

  ```sql
  ALTER TABLE stack_instances ADD COLUMN role_plan_json TEXT NOT NULL DEFAULT ''
  ```

- `store/repo/stack.go`：`stackInstCols` 列清单、`CreateInstance` INSERT、`UpdateInstance` UPDATE 与 `scanStackInstance` 同步带上新列。

### 4.3 消费方收敛为只读

新增统一读取入口：

```go
// loadRolePlan 读取实例已物化的 RolePlan；缺失时惰性生成并落库（存量兼容）。
func (h *stackHandler) loadRolePlan(inst *model.StackInstance) (model.RolePlan, error)
```

5 处调用点改造：

| # | 原位置 | 改为 |
|---|--------|------|
| 1 | `stack_engine.go:1465` 注入变量 | 读 `plan.Injected` |
| 2 | `stack_engine.go:954` 服务登记 | 读 `plan` 按主机取角色 |
| 3 | `stack_instance.go:481` 缩容保护 | 读 `plan.Hosts[].Roles` |
| 4 | `stack_instance.go:842` 校验 | 校验移至 planner（§4.4） |
| 5 | `stack_verify.go:134` 探活 | 读 `plan` 生成期望矩阵 |

`buildCreateHosts` / `buildAddComponentHosts` / `buildReinstallHosts` 中的 `stackBigdataMasters` 调用同步改为读 plan。

### 4.4 校验前移

`validateBigdataMasters`（`stack_instance.go:776`）的规则前移到 planner：

1. HA → components 含 zookeeper；
2. HA → 主机数 ≥ 3；
3. HA + hive → 含 metastore_db；
4. 同组件主从互斥（nn1≠nn2、rm1≠rm2、spark_m1≠spark_m2、jm1≠jm2、hm1≠hm2、ms1≠ms2）；
5. **新增**：落点角色必须落在成员主机上（IP ⊆ 所选主机集）；
6. **新增**：`scope=all` 角色不得被单独指定（防止误配）。

校验通过才产出 Plan；不通过返回具体冲突角色对，前端标红。

### 4.5 新增 API

| 方法 | 路径 | 用途 |
|------|------|------|
| `POST` | `/api/stacks/plan/preview` | **dry-run**：给定 host_ids + params + 可选用户编辑 → 返回 RolePlan（不落库），供前端预览 |
| `POST` | `/api/stacks/instances/:id/replan` | 对已有实例重规划 → 持久化 rev+1，返回新 Plan |
| `GET` | `/api/stacks/instances/:id/plan` | 读取当前 Plan |

创建/加装请求体新增可选 `role_plan`（扩容无角色入参，新机自动追加全员角色）：

- **缺省** → 后端 planner 自动生成（最短路径，行为兼容）；
- **携带** → 后端以 `role_plan` 中的 `manual` 项为准，其余重算，校验后落库。

---

## 五、前端改造

### 5.1 从「下拉覆盖」到「计划预览表」

现状（`stacks-forms.js` `haRoles`）：按组件分组的角色行 + 每行一个主机下拉，默认「自动分配」。**下拉仍是好的输入控件，但产出物应是一张物化后的矩阵表。**

改造为**两段式**：

1. **输入**（保留现状）：组件分组 + 角色行下拉，选「自动分配 / 指定主机」；
2. **预览**（新增）：点「预览角色计划」→ 调 `POST /plan/preview` → 渲染**主机 × 角色**矩阵：

```
              | NN1 | NN2 | JN | ZK | DN | RM1 | RM2 | NM | ... | MetaDB
192.168.3.40  |  ●  |     | ●  | ●  | ●  |  ●  |     | ●  |     |
192.168.3.41  |     |  ●  | ●  | ●  | ●  |     |  ●  | ●  |     |
192.168.3.63  |     |     | ●  | ●  | ●  |     |     | ●  |     |
```

每格徽章标注来源：`手`（manual 蓝）/ `自`（auto 灰）。

### 5.2 手动 / 自动标注与冲突

- 每格 hover 显示「手动指定 / 自动分配（seq 最小非主）」；
- 用户点击格子可改为手动指定到其它主机 → 前端即时重算（本地 planner 逻辑或再调 preview）；
- 同组件两落点撞机 → 标红拦截，禁用「下一步」；
- 底部摘要：`落点角色 12 个 · 手动 2 · 自动 10 · 全员角色 5 个/机`。

### 5.3 扩容 / 缩容的交互（按拍板 ② 简化）

- 扩容向导**不再提供角色规划步骤**：新机自动承担全员角色（dn/nm/spark_worker/flink_tm/rs/trino_worker），提交前以只读预览展示「新机将承担：全员角色 × N」；
- 需在向导中明确提示：「主备角色（NN/RM/JM…）已随集群部署冻结，扩容不改变主备布局」；
- 缩容侧：主机选择列表中，承载落点角色的主机直接置灰并标注原因（如「NN2 落点」），不可勾选——与加装/卸载面板同源的交互语言。

### 5.4 实例抽屉展示

- 成员行角色标签改为读 `plan`（`NN1·Active` `NN2·Standby` `JN` `RM2`…），与探活同源；
- 新增「角色计划」页签：展示当前 Plan（rev / 生成时间 / 来源统计 / 矩阵表）。

---

## 六、兼容与迁移

### 6.1 存量实例（含当前 5 台 HA 集群）

- 实例 `role_plan_json` 为空 → `loadRolePlan` **惰性生成**：以当前成员 + `params_json`（含旧 `masters`）为输入跑 planner；
- `masters` 命中的角色标 `manual`，其余标 `auto`；
- 生成后落库，此后走新路径。**存量实例零手工操作**。

### 6.2 无 Plan 兜底

任何消费方读不到 Plan 时（异常/数据损坏），回退到「惰性生成」而非旧推导路径，保证**永远只有一条计算链**。

### 6.3 `masters` 参数收敛

- 短期：`masters` 保留为 planner 输入 + 审计字段，消费方不再直接读；
- 中期：前端提交时**直接提交 RolePlan 的 manual 项**，`masters` 仅作兼容读取；
- 长期（可选）：移除 `masters` 键，统一为 `role_overrides`。

### 6.4 回滚

- `role_plan_json` 为新增列，回滚版本忽略该列即可，旧路径（现场推导）仍可用；
- 但注意：回滚后 §1.2 的不一致隐患复现，建议不做长窗口回滚。

---

## 七、涉及文件清单

### 后端（Go）

| 文件 | 改动 |
|------|------|
| `model/stack.go` | `StackInstance` 增 `RolePlanJSON`；新增 `RolePlan` / `RolePlanHost` / `RolePlanRole` / `PlanOptions` 类型 |
| `api/stack_engine.go` | 新增 `planBigdataRoles`（§4.1）；`stackBigdataRole` 保持算法不变、限定内部调用；注入变量改读 `plan.Injected` |
| `api/stack_instance.go` | 新增 `loadRolePlan`（§4.3）；`buildCreateHosts` / `buildAddComponentHosts` / `buildReinstallHosts` / `bigdataProtectedHosts` 改读 Plan；`validateBigdataMasters` 规则前移 |
| `api/stack_verify.go` | 探活期望矩阵改读 `plan`（消除 41 类不一致） |
| `api/stack.go` | 创建/加装入口支持 `role_plan` 入参；扩容走「增量追加全员角色」；新增 `PlanPreview` / `Replan` / `GetPlan` handler |
| `api/stack_plan_test.go`（新增） | planner 单测：手动/自动混合、落点互斥、全员角色展开、存量惰性生成、rev 递增 |
| `store/migrations.go` | 新增 `migrateV24`：`stack_instances` 增 `role_plan_json` 列（`addColumnIfMissing`） |
| `store/repo/stack.go` | `stackInstCols` / INSERT / UPDATE / `scanStackInstance` 同步新列 |
| `router/router.go` | 注册 `/stacks/plan/preview`、`/stacks/instances/:id/plan`、`/stacks/instances/:id/replan` |

### 前端（Vue/JS）

| 文件 | 改动 |
|------|------|
| `template/static/pages/stacks-forms.js` | 角色规划步骤：保留输入下拉，新增「预览角色计划」与矩阵表渲染；手动/自动徽章；冲突标红；摘要统计 |
| `template/static/pages/stacks.js` | 扩容向导提示「主备已冻结」并只读预览新机角色；缩容主机列表置灰落点角色机；实例抽屉成员标签改读 Plan；新增「角色计划」页签 |
| `template/static/style.css` | 角色矩阵表样式（主机×角色网格、手动/自动徽章、冲突标红、diff 高亮） |

### 文档与校验

| 文件 | 改动 |
|------|------|
| `docs/bigdata-ha-design.md` | §3.2 / §4.2 / §4.3 同步指向本设计（角色规划升级为 RolePlan） |
| `docs/role-plan-design.md` | 本文档随实施同步更新 |
| `script/check_stacks_op_panel.js` | 扩展断言：矩阵表渲染、手动/自动标注、冲突禁用 |
| `script/check_bigdata_tpl.py` | 渲染校验保持（注入变量改读 Plan 后需回归） |

---

## 八、实施计划（四阶段）

| 阶段 | 内容 | 验证 |
|------|------|------|
| **P1 数据模型 + planner** | `RolePlan` 类型、`StackInstance.RolePlanJSON`、迁移脚本、`planBigdataRoles`、单测 | `go build/vet` + planner 单测全绿 |
| **P2 消费方收敛** | 5 处调用点 + build* 系列改读 `loadRolePlan`；惰性生成兼容；探活改造 | Go 单测 + 存量实例（41 集群）探活回归，确认与部署脚本一致 |
| **P3 前端预览表** | `/plan/preview` 接入、矩阵表渲染、手动/自动徽章、冲突拦截、摘要 | `node --check` + Node 自检断言 + 浏览器实机走查（HA 3 台 / 5 台） |
| **P4 扩缩容边界 + 文档** | 扩容「增量追加全员角色」、缩容拦截落点角色机；抽屉页签；docs 更新；全量回归 | 全量 check + 真实集群扩容（新机只见 dn/nm/worker）→缩容拦截→探活全链路验收 |

**验收标准**：同一集群在「部署 / 探活 / 缩容保护 / 重装」四处得出的角色落点**逐字节一致**（读同一份 Plan）；对同一 Plan 重放探活，41 类误判不再出现。

---

## 九、风险与开放问题

1. **热区冲突**：`stack_engine.go` / `stack_instance.go` / `stack_verify.go` 是高频改动文件，开工前需确认无并行会话在改同一文件（同 `bigdata-ha-design.md` §八-7 约定）。
2. **`masters` 与 RolePlan 双写窗口**：过渡期两者并存，须以 Plan 为唯一读源，避免又造多源；建议 P2 完成后即冻结 `masters` 的读路径。
3. **前端本地预览 vs 后端权威**：矩阵表编辑若走本地重算，可能与后端 planner 逻辑漂移；建议**每次编辑都回后端 preview**（增量式 dry-run），前端不实现第二套算法。
4. **扩缩容被限定在数据节点后的用户心智**：用户若想「换一台备主」只能走重装路径；需在 UI 与文档中写明，避免误以为扩缩容能调整主备。
5. ~~Plan 历史留存~~ **已拍板（2026-09-11）**：只保留最近一份，不做历史 rev 留存（§3.2）。
6. **非 bigdata 套件**：redis 主从 / 其他套件的角色仍走 `stackHostRole`；本设计暂不统一，后续可评估泛化。
7. **回滚语义**：回滚旧版本后 Plan 列被忽略，隐患复现（§6.4），需在发布说明中标注。
