# Kafka 套件方案（三张表基线 + KafkaUI 开关 + 扩缩容风险与规避）

> 方法依据：`docs/新增套件说明.md`（模式 / 角色 / 步骤三张解析表）与项目级 skill
> `.workbuddy/skills/new-stack/SKILL.md`（执行序 = 对齐三张表 → 后端 → 前端 → 基线门禁）。
>
> **前置结论**：kafka 是 9 个套件里**能力最少**的一个 —— 后端只有 `kafka.go` / `plan.go` /
> `register.go` / `defaults.go` 四个文件，前端 `index.js` 是空注册（`forms:{}` / `components:{}`）、
> `style.css` 全文只有注释。它没有 `probe.go` / `endpoint.go` / `scale.go` / `vars.go` / `validate.go`，
> 也**没有任何 bootstrap / scale_out / scale_in 脚本**。
>
> 本文 = ① 三张表现状 ② KafkaUI 开关方案对比 ③ 扩缩容风险矩阵（含实测证据）
> ④ 规避候选清单 K1–K6（含向其余单模式套件通用化）⑤ 验证手段。

---

## 1. 第一张表：模式表（`store/stacks/kafka/kafka.go:46-59`）

| Key | Label | MinHosts | AssignMaster | HasBootstrap | DefaultHomeDir | 拓扑语义 |
|---|---|---|---|---|---|---|
| `kraft` | KRaft 集群 | 1 | ✘ | ✘ | `/data/kafka-kraft` | 每台 1 容器，`broker,controller` 混部，**`controller.quorum.voters` 部署时按成员 IP 固化** |
| `zk` | ZooKeeper 集群 | 1 | ✘ | ✘ | `/data/kafka-zk` | 每台 2 容器（ZK + broker），**`zoo.cfg` 的 `server.N` 部署时按成员 IP 固化** |

- `RequiresDocker: true`（`kafka.go:45`）→ 运行步骤条首格自动加「Docker 环境」。
- **两个模式都是 `has_bootstrap=false`**：KRaft 的 `kafka-storage.sh format` 直接写在 `node` 脚本里，
  属于「一次成型」，没有独立的集群初始化阶段。
- 参数按模式裁剪（`SharedVars` 的 `Modes`）：`controller_port` / `cluster_id` 仅 `kraft`；
  `zk_image` / `zk_port` 仅 `zk`；`image` / `mem` / `enable_ui` / `ui_image` / `ui_port` 全模式。
- 逐主机参数 `HostVars`：`port`（默认 9092）+ **两条同名的 `home_dir`**（按 `Modes` 区分，
  分别是 `/data/kafka-kraft` 与 `/data/kafka-zk`）。
- `min_hosts` 都是 1：选 1 台 = 单节点集群（KRaft 单 voter / ZK 单节点 ensemble），
  脚本用 `RF=min(3,N)`、`MIN_ISR=min(2,N)` 自适应。`host_hint` 只做建议不拦截。

## 2. 第二张表：角色表（`store/stacks/kafka/plan.go:14-31`）

| 模式 | comp | role | label | 落点 | scope |
|---|---|---|---|---|---|
| `kraft` | `kafka` | `broker` | Broker + Controller（KRaft 仲裁） | `AddAll` | `all` |
| `zk` | `kafka` | `zk` | ZooKeeper | `AddAll` | `all` |
| `zk` | `kafka` | `broker` | Broker | `AddAll` | `all` |
| 两模式 +`enable_ui=yes` | `kafka` | `ui` | Kafka UI | `AddAt(..., hosts[0])` | **`aux`** |

- **所有角色都是 `AddAll`/`AddAt(aux)`，没有一条 `scope=""`** ⇒
  `api/stack/stack_instance_validate.go:40` 的 `planProtectedHosts` 命中为空
  ⇒ **kafka 目前没有任何主机受缩容保护**（唯一的兜底是引擎的 `remain < 1`）。
  对照：`AddAll` 落 `scope="all"`、`AddRest` 落 `scope="rest"`，只有显式
  `AddAt(..., "", ...)` 才是"落点角色（受保护）"—— nacos / powerjob 的 `boot`、
  rabbitmq 的 `seed`、rocketmq 的 `master`、redis 的 `master` 都是后者，
  **kafka 是 9 套件里唯一"零落点保护"的**。
- `assign_master=false` ⇒ 角色计划的 `masters` 恒为 `null`（`baseline/cases/kafka_*.json` 可证）
  ⇒ ① 接入地址卡片没有主/备区分 ② 缩容软下线**没有天然执行锚点**（redis 靠 leader、bigdata 靠 NameNode）。
- `ui` 与 `broker` 共用 `comp=kafka` ⇒ 主机视图里 UI 不会独立成片；
  且 `AddAt(..., in.Hosts[0])` 是「**所选主机集合里的第一台**」（按 Seq 升序，扩容时 = 存量首台）。

## 3. 第三张表：步骤表（`store/stacks/kafka/kafka.go:21-36`）

`Pipeline(mode)` 返回 `nil`；`Phase()` 只认 `stackkit.PhaseNode`。每个模式**一枚脚本**：

| 阶段 | kraft | zk |
|---|---|---|
| `node` | `stacks/kafka/scripts/kraft-node.sh`（7454 B） | `stacks/kafka/scripts/zk-node.sh`（8020 B） |
| `bootstrap` | NO_SCRIPT | NO_SCRIPT |
| `scale_out` | NO_SCRIPT | NO_SCRIPT |
| `scale_in` | NO_SCRIPT | NO_SCRIPT |

（渲染基线 `baseline/render/kafka__*` 逐项可查；`baseline/pipeline/kafka__*.json` 均为 `null`。）

**结论**：kafka 只能「整装」，**扩容 / 缩容 / 加装组件在服务端都没有专属脚本**。
全 9 套件里只有 redis 与 elasticsearch 声明了 `scale_in`，只有 redis 声明了 `scale_out`。

> 引擎行为（`api/stack/stack_engine.go:110-127`）：`scale_out` 的部署阶段永远跑 **node 脚本**
> （`runPhaseNode` 固定加载 `PhaseNode`），只有 `hasPhase(key, mode, "scale_out")` 为真时才额外
> 跑一个收尾脚本。所以 kafka 扩容 = 在新机上把 node 脚本再跑一遍 —— **是否安全完全取决于
> node 脚本在 `__process_roles` / `__voter_ips` / `cluster_id` 被替换成扩容口径后生成的配置**（见 §5）。

## 4. 现状能力矩阵（按 `store/stacks/*` 逐方法实测）

| 能力接口 | 方法 | kafka | nacos | powerjob | rabbitmq | rocketmq | redis | es | bigdata |
|---|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `RolePlanner` | `PlanRoles` | ✔ | ✔ | ✔ | ✔ | ✔ | ✔ | ✔ | ✔ |
| `ServiceRegistrar` | `RegisterServices` | ✔ | ✘ | ✘ | ✔ | ✘ | ✔ | ✔ | ✔ |
| `DefaultsProvider` | `Defaults` | ✔ | ✘ | ✘ | ✔ | ✘ | ✔ | ✘ | ✘ |
| `CreateValidator` | `ValidateCreate` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✔ |
| `TopologyBuilder` | `ValidateTopology` | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✘ | ✘ |
| `ScaleGuard` | `ScaleGuard` | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✘ | ✔ |
| `ScaleInGuard` | `ScaleInGuard` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✘ |
| `ScaleInDrainer` | `DrainParams` | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✔ | ✘ |
| `ProbePlugin` | `Containers` | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✘ | ✔ |
| `EndpointProvider` | `Endpoints` | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✔ | ✔ |
| `VarInjector` | `ExtraVars` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✔ |
| `InstanceVarInjector` | `InjectInstanceVars` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ |
| `SelfPlanning` | `SelfPlans` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ |
| `RoleNamer` | `HostRole` | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ | ✘ | ✘ |
| `BootstrapHostResolver` | `BootstrapHostIP` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ |
| `AssetProvisioner` | `RequiredAssets` | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✘ | ✔ |
| 前端 `selectComp` / `step2Comp` | — | 均 ✘ | — | — | — | — | `step2Comp` | `step2Comp` | 两个都有 |

- **kafka 只实现了 3 个能力**（`PlanRoles` / `RegisterServices` / `Defaults`），
  与 nacos、powerjob、rocketmq 同属"最薄一档"（后三者只有 `PlanRoles`）。
- 注意 `ScaleGuard` 与 `ScaleInGuard` 是**两个不同接口**：es 只实现后者（"不允许移除最后一台
  master 候选主机"），redis/bigdata 实现前者。kafka 两个都没有。
- 无 `EndpointProvider` 的代价（引擎兜底，`store/stackkit/probe.go:100`）：接入地址只有一条
  `tcp://<ip>`（名字取实例名「Kafka」、**不带端口、不带 component**），与 `register.go` 里
  真正登记的 `kafka://<ip>:<port>` / `zookeeper://<ip>:<port>` 并不一致。
  （缺该能力的还有 nacos / powerjob / rabbitmq / rocketmq / elfk。）

---

## 5. 扩缩容风险评估（含实测证据）

### 5.1 实测方法

一次性 Go 测试走**真实引擎路径**渲染 kafka 的 node 脚本并落盘比对
（`store.LoadStackPhase` → `deploy.RenderScript` → `applyStackVars`，`extra` 取
`stackkit.ClusterExtraMerged(op, existing, new)`），用完即删。场景：存量 3 台 `10.0.1.1-3`，
新增 `10.0.1.4`（`Seq=maxSeq+1=4`）。

### 5.2 风险矩阵

| 操作 | `kraft` | `zk` |
|---|---|---|
| create | ✅ 正常 | ✅ 正常 |
| **scale_out** | ⚠️ **可用但无收尾校验**（渲染已正确，见 5.3） | 🔴 **不安全**（配置无法对齐，见 5.4） |
| **scale_in** | 🔴 **无任何保护，可缩到失去仲裁**（见 5.5） | 🔴 同上，且存活 `zoo.cfg` 仍指向已下线成员 |
| uninstall / purge | ⚠️ **`kafka-ui` 容器不被停也不被删**（见 5.6） | 同左 |

### 5.3 `kraft` 扩容：脚本渲染是正确的（好消息）

实测新机渲染结果：

```
SELF_ID="4"                     → node.id=4       （续号，不与存量冲突）
NODE_IPS="10.0.1.1,...,10.0.1.4" → 仅用于 UI bootstrap 列表与回显
VOTER_IPS="10.0.1.1,10.0.1.2,10.0.1.3"  → controller.quorum.voters 只含存量 3 台 ★
PROCESS_ROLES="broker"          → process.roles=broker（broker-only）      ★
CLUSTER_ID="abc123"             → 沿用实例参数（见下）
```

这正是 **KRaft 加 broker 的标准姿势**：新节点以 broker-only 身份、用**存量 voters 列表**启动，
**不需要改动存量节点配置**。原因是引擎早已铺好语义：

- `store/stackkit/extra.go:118-127`（`ClusterExtraMerged`）对 `scale_out` 的新节点给
  `__voter_ips = 存量 IP`、`__process_roles = "broker"`；
- `api/stack/stack_create.go:78-86` 把 `inst.ParamsJSON` 合并进 `params` 兜底
  ⇒ **`cluster_id` 沿用创建期生成值**，新 broker 不会 format 出第二个集群；
- `api/stack/stack_instance_build.go:85` 给新机 `Seq = maxSeq+i+1` ⇒ `node.id` 不冲突。

唯一缺口：没有 `scale_out` 收尾脚本去**校验**「新 broker 已注册进集群元数据」。
不过 Kafka broker 启动后自行注册，实际可用；只是失败时没人兜底报错。

### 5.4 `zk` 扩容：不安全（会静默半成功）

实测新机渲染结果：

```
NODE_IPS="10.0.1.1,10.0.1.2,10.0.1.3,10.0.1.4"
ZK_SERVERS → server.1=10.0.1.1:2888:3888 … server.4=10.0.1.4:2888:3888
myid = 4
zookeeper.connect = 10.0.1.1:2181,…,10.0.1.4:2181
```

而存量 3 台的 `zoo.cfg` **只有 `server.1..3`**（`zoo.cfg` 只在部署时生成一次）。
ZooKeeper 要求 **ensemble 每个成员的服务器列表必须一致**（差异只在 `myid`），
因此新 ZK 不是合法成员，无法加入 ensemble。

更糟的是**探活会误报成功**：`zk-node.sh:173-176` 的就绪检查是
`echo ruok > /dev/tcp/127.0.0.1/${ZK_PORT}`，而一个处于 LOOKING、无法加入 ensemble 的
ZK 依然会回 `imok` ⇒ 脚本打印「ZooKeeper 节点已就绪」，实际它从未加入集群。

连带影响：新机 Kafka broker 的 `zookeeper.connect` 含 4 个地址，其中 1 个是坏成员，
broker 只要有一个可达就能工作，但会持续刷连接告警。

**结论：ZK 模式应明确禁止扩容**（除非引入 ZK 动态 reconfig + 改部署脚本）。

### 5.5 `scale_in`：现状可把集群缩到失去仲裁且不可自愈（最高危）

`validateScaleIn`（`api/stack/stack_instance_validate.go:11-51`）只做两件事：
`remain < 1` 直接拒绝、`scope==""` 的落点主机拒绝。kafka 两者都不命中（角色全是 `all`/`aux`）
⇒ **3 台可以缩到 1 台**。

而 `controller.quorum.voters` / `zoo.cfg` 的 `server.N` 是**部署时固化、缩容时不会更新**的：

| 初始化成员数 V | 缩后 Remain | 存活 voter/ensemble 成员 | 多数派要求 | 结果 |
|---|---|---|---|---|
| 3 | 2 | 2 / 3 | ⌊3/2⌋+1 = 2 | 能工作，但**仲裁冗余 = 0**（再挂 1 台即脑裂/失去仲裁） |
| 3 | **1** | 1 / 3 | 2 | 🔴 **1 < 2 → 仲裁永久丢失** |
| 5 | 2 | 2 / 5 | 3 | 🔴 直接失去仲裁 |

KRaft 侧的具体后果（**不是"性能下降"，是能力熔断**）：
仲裁丢失 ⇒ 没有 active controller ⇒
① **任何元数据变更都无法进行**（建 topic、分区选主、broker 上下线全部失败）；
② 已缓存元数据的 broker 仍能继续现有分区的读写，但**一旦有 broker 重启/退出，
其分区副本永久无法选出新 leader**；
③ **不自愈** —— voters 写死在每台的 `server.properties` 里，必须人工改回正确集合并重启。

ZK 侧更直接：ensemble 失去多数派 ⇒ 没有 leader ⇒ Kafka broker 与 ZK 断连，
超过 `zookeeper.session.timeout.ms`（默认 18s）后 **broker 自行退出** ⇒ 整个集群停摆。

**注意偏移方向**：由于 voters 只在创建时固化，扩容过的集群里 `len(active) > V`。
若用 `len(active)` 当 V 做保护，`majority` 会被算大 ⇒ **拦截更严**，属安全的偏差方向。

### 5.6 KafkaUI 的生命周期缺口

`kafka-ui` 的 compose 落在 **`${HOME_DIR}-ui/compose.yml`**（如 `/data/kafka-kraft-ui/compose.yml`），
与 kafka 自身的 `${HOME_DIR}/compose.yml` 是**两个独立的 compose 项目**。

而 `api/stack/stack_compose.go:9` 的 `stackComposeDownScript(home, comps, purge)` 只会
`down` `${HOME_DIR}/compose.yml` 与 `${HOME_DIR}/<组件>/compose.yml`（组件白名单是
bigdata 口径：hadoop/zookeeper/yarn/spark/flink/hive/hbase/trino）。

⇒ **缩容 / 卸载首台时 `kafka-ui` 容器不会被停**，`purge=true` 也不会删 `${HOME_DIR}-ui`：
残留孤儿容器 + 8080 端口占用 + UI 仍指向已失效的 bootstrap 列表。

同一条路径还暴露第二个问题：**`purge` 不删 `${HOME_DIR}/data`**
（KRaft 日志目录 / ZK 的 `zk-data` + `zk-datalog`），也不删 `${HOME_DIR}/server.properties`。
由于 `kraft-node.sh:143` 用 `kafka-storage.sh format --ignore-formatted`，
残留的旧元数据会让重装**沿用旧 `cluster_id` 的身份**（"清理残留"名不副实）。

第三个缺口：`AddAt(..., in.Hosts[0])` 依赖「首台」。若缩容移除了首台，
`planForGenericOp` 的 `scale_in` 分支会用剩余成员重算计划（`stack_plan_op.go:168-177`），
**角色预览会把 KafkaUI 标到新的首台上** —— 但没有任何脚本去那里部署它，
于是**预览与实际不一致**（预览"撒谎"）。
（扩容时反而是对的：`planFullMemberSet` 按 Seq 排序，存量首台仍在首位。）

### 5.7 口径不一致（小 bug，但会误导）

`enable_ui` 是**文本输入**（`Default: "no"`）。Go 侧 `plan.go:26` 用 `in.IsYes("enable_ui")`
（接受 `yes`/`true`/`1`），而两个脚本用 `[ "${ENABLE_UI}" = "yes" ]` 精确比较。
⇒ 用户填 `true` 时：**角色预览显示会部署 KafkaUI，脚本却不会部署**。

---

## 6. 规避方案候选清单

### K1 · 缩容仲裁保护（`ScaleGuard`，最高优先）

新建 `store/stacks/kafka/scale.go` 实现 `stackkit.ScaleGuard`，按模式给出仲裁规则：

| 规则 | 判据 | 动作 |
|---|---|---|
| 硬拦（立刻致死） | `Remain < ⌊V/2⌋+1`（V = 仲裁成员数，取 `len(ctx.Active)`） | 拒绝，文案说明"仲裁成员列表在部署时固化，缩到 X 台将失去多数派" |
| 建议拦（零冗余） | `Remain < 3` 且 `V ≥ 3` | 拒绝，文案引导「如需变更仲裁规模请重装」 |

- 只读 `ScaleCtx`（`Mode` / `Active` / `Remain`），不新增接口、不碰引擎、零套件分支。
- 附带把 `common/stackkit` 的仲裁判断抽成共用助手（见 K6），kafka 只声明自己的 V 与理由。
- **反向验证**：剥掉该实现跑 `TestKafkaScaleGuard` 应如期 FAIL。

### K2 · KafkaUI 开关（三选一）

| 方案 | 落点 | 骨架改动 | 基线重录 | 评价 |
|---|---|---|---|---|
| **A（推荐）** | `kafka.go` 把 `enable_ui` 改 `Type:"bool"` + 两脚本判断改兼容 `yes\|true\|1` | **零**（骨架 `tpl-wizard.js:170` 已支持 `v.type==='bool'` 渲染 `el-switch`，先例：bigdata `ha`、ES `ssl_enabled`） | 无 | 一行后端 + 两处脚本；顺带修掉 §5.7 的口径不一致 |
| **B（"卡片上"）** | kafka 自己的 `forms.selectComp`（模板 `tpl-wizard.js:38` 已有插槽，先例：bigdata）—— 在第一步「部署模式」卡片区内渲染开关条 | **零** | 无（套件 js 只被沙箱求值） | 更醒目、贴合"放到卡片上"，且完全不动骨架 |
| C | 套件卡片右上角 `el-switch`（与 HA 并排，`tpl-wizard.js:27`） | **需要**新增通用分发点（如 `cardSwitches`） | `_fe_smoke` 模板基线重录 | 成本最高，且卡片位置语义是"套件级"（HA），KafkaUI 是"本次部署的可选附加物" |

A 与 B 可同时做：A 定语义（`true`/`false`），B 定呈现（开关条直接读写 `ctx.sharedParams.enable_ui`）。

### K3 · KafkaUI 生命周期收口

新增**通用可选能力**（如 `stackkit.SideComposeProvider`，返回额外 compose 目录 / purge 目录），
`stackComposeDownScript` 与 purge 路径据此追加。kafka 声明 `${home_dir}-ui` 即可。

- 零套件分支（引擎只做类型断言）、其他套件（如将来带 side-car 的）同样受益。
- 收益：缩容/卸载首台时 UI 被正常停掉；purge 真正清干净。

### K4 · `purge` 覆盖 kafka 的数据与配置目录

同上机制声明 `${home_dir}/data`、`${home_dir}/zk-data`、`${home_dir}/zk-datalog`、
`${home_dir}/server.properties|zoo.cfg`、`${home_dir}-ui` 为 purge 目标。
**否则重装会沿用旧 `cluster_id` 的元数据**（`--ignore-formatted`）。

### K5 · 探活与接入地址（`ProbePlugin` + `EndpointProvider`）

- `ProbePlugin`：期望容器清单（kraft `kafka-N`；zk `kafka-zk-N` + `kafka-N`；UI `kafka-ui`）
  + 套件专属检查：`kafka-broker-api-versions.sh`、`kafka-metadata-quorum.sh describe --status`
  （**直接暴露 voters 数 vs 存活成员数是否一致** —— 这正是 §5.5 风险的唯一可观测入口）；
  zk 模式补 `ruok` / `srvr`（并修正 §5.4 探活误报）。
- `EndpointProvider`：`kafka://ip:port`、zk 模式加 `zookeeper://ip:zk_port`、UI 加 `http://ip:ui_port`，
  逐个打 `Component` 标（`kafka` / `zookeeper` / `kafka_ui`）⇒ 接入地址卡片与探活页签才有内容。
- 顺带把 `ui` 拆成独立 `comp`（`kafka_ui`），主机视图芯片才能区分 UI 与 broker。

### K6 · 向其余单模式套件通用化

4 个单模式 `cluster` 套件的风险画像并不相同，共用的是**能力缺口**（都缺 `ScaleGuard` /
`ProbePlugin` / `EndpointProvider`）：

| 套件 | 角色（scope） | 仲裁/一致性模型 | 缩容风险 | 建议 |
|---|---|---|---|---|
| **kafka** | `broker`(all) / `zk`(all) / `ui`(aux) —— **零落点保护** | KRaft voters；ZK ensemble | 🔴 高（§5.5，可缩到失去仲裁） | 本轮 |
| `nacos` | `boot`(**落点**) + `member`(rest) | JRaft（配置/元数据） | 🔴 高（同类多数派问题，但 boot 主机已被保护） | 与 rabbitmq 合并一轮 |
| `rabbitmq` | `seed`(**落点**) + `member`(rest) | quorum queue | ⚠️ 中（队列需先 shrink/排空） | 与 nacos 合并一轮 |
| `rocketmq` | `master`(**落点**, AssignMaster) + `slave`(rest) | 主从，**无自动切换** | ⚠️ 中（master 已受保护，但缩掉 slave 后无副本） | 单独一轮（语义不同） |
| `powerjob` | `boot`(**落点**) + `server`(rest) | 无状态（元数据在 MySQL） | ✅ 低 | 只补探活/端点 |

通用化的落点：在 `store/stackkit` 提供**仲裁判断助手**（如
`GuardRemainMajority(label string, configured, remain int) error`），
各套件用一行调用声明自己的 N 与理由 ⇒ 引擎零分支、规则各自持有。

**不建议一轮内全做**：redis 那轮已经证明"每个套件的脚本与风险模型都必须单独验证"。

### 明确不做

- **`zk` 模式的扩容**：现有静态 `zoo.cfg` 架构下无法对齐配置（§5.4），
  除非引入 `reconfigEnabled` + `dynamicConfigFile` 并改部署脚本 —— 属于独立一期。
- **`scale_in` 真软下线脚本**（改 voters / 摘 controller / 滚动重启）：
  风险面大、需要停机窗口与真机验证，**先做 K1 拦住，二期再评估**。

---

## 7. 验证手段

1. **渲染落盘比对**（本轮已用于取证，可固化为测试）：一次性 Go 测试走
   `LoadStackPhase → RenderScript → applyStackVars`，断言扩容口径下
   `PROCESS_ROLES=broker`、`VOTER_IPS=存量`、`node.id` 续号；脚本用完即删（`script/_*`）。
2. **`ScaleGuard` 单测 + 反向验证**：`store/stacks/kafka/scale_test.go`，
   剥掉实现应如期 FAIL。
3. **快照基线**：K2-A / K5 会改蓝图（`enable_ui` 的 `Type`、`ui` 的 comp 名）⇒
   `SNAPSHOT_WRITE=1 go test ./store/ ./api/stack/` 重录；K5 加 `endpoint.go` 后
   `baseline/cases/kafka_*.json` 的 `endpoints` 会从 `tcp://<ip>` 变成带端口的列表。
4. **门禁**：`go build/vet/test`、`node script/_fe_smoke.js`（K2-B 改套件 js 不碰模板基线）、
   `python script/check_stack_lines.py --targets`（新增文件需 `--record` 并在 `_note` 写明来源）。
5. **真机验证**（K1 之外都需要）：扩容后 `kafka-metadata-quorum.sh describe --status`
   应为 4 个 broker、3 个 voter；缩容被拦；UI 容器随缩容停掉。

---

## 8. 待拍板

| 编号 | 内容 | 落点 | 预估改动面 |
|---|---|---|---|
| K1 | 缩容仲裁保护 | `store/stacks/kafka/scale.go` + `scale_test.go` | 新文件 2 个，零骨架 |
| K2-A | KafkaUI 改 bool 开关 | `kafka.go` + 两个脚本 | 3 处 |
| K2-B | KafkaUI 开关移到第一步卡片区 | `template/static/stacks/kafka/index.js` | 1 文件 |
| K3 | UI 容器随缩容/卸载收口 | `stackkit` 新能力 + `stack_compose.go` | 通用层（需门禁重录） |
| K4 | purge 覆盖数据/配置目录 | 同 K3 | 复用 K3 机制 |
| K5 | 探活 + 接入地址 + `ui` 独立 comp | `probe.go` / `endpoint.go` / `plan.go` | 新文件 2 个 + 快照重录 |
| K6 | 仲裁助手通用化 + 其余套件排期 | `stackkit` + 各套件 | 跨套件，需分批 |

---

## 9. 本轮实施记录（K1 + K2(A+B) + K5）

按「每个套件的专属逻辑放在自己目录里、通用层零套件分支、套件之间零影响」的约束，
落地 **K1 / K2-A / K2-B / K5**，全部文件落在 `store/stacks/kafka/` 与
`template/static/stacks/kafka/`（另有 `template/index.html` 两行 script 与两处测试/门禁）。

### 9.1 K1 · 缩容仲裁保护 —— `store/stacks/kafka/scale.go`（新）

`ScaleGuard` 实现两道闸，两种模式文案各自点明后果：

| 闸 | 判据 | 理由 |
|---|---|---|
| 多数派 | `Remain ≥ ⌊在役台数/2⌋+1` | `voters` / `zoo.cfg` 部署时固化、缩容不同步收缩（§5.5） |
| 生产下限 | `Remain ≥ 3` | 2 台时任一节点故障即刻失去仲裁 |

多数派按**当前在役台数**算（而非创建时的 voters）——扩容后的集群里 broker-only 成员不是
voter，真实 voters 更小 ⇒ 算出的多数派偏大 ⇒ **拦得更严**，偏差方向安全（不需要访问主机即可判定）。
`ctx.Active` 为空（实例已无在役成员）时不判定，交回引擎的通用下限。

### 9.2 K2-A · `enable_ui` 改布尔开关（顺带修口径 bug）

- `kafka.go`：`{Name:"enable_ui", Type:"bool", Default:"false"}` —— 骨架
  `tpl-wizard.js:170` 早就支持 `v.type==='bool'` 渲染 `el-switch`（先例：bigdata `ha`、ES `ssl_enabled`）。
- **修掉一个真 bug**：前端提交 `true`/`false`，而两个 node 脚本按 `= "yes"` 精确比较 ⇒
  用户填 `true` 时**预览显示有 KafkaUI、实际不部署**。现在脚本里加「归一化」把
  `yes/true/1/on` 折算成 `yes`、其余折算为空，与后端 `stackkit.IsYes` 同口径，
  **历史实例里的 `yes`/`no` 同样兼容**。

### 9.3 K2-B · 开关放到第一步卡片区 —— `template/static/stacks/kafka/select.js`（新）

`forms.selectComp` 插槽（`tpl-wizard.js:38`）会被渲染在第一步「部署模式」区内。
由于该插槽是**整段接管**（`<template v-else>` 里的模式网格会被替换），套件组件连模式网格
一起渲染，从而该区域只有一处定义；骨架的 `stk-hint`（requires_docker 提示）与
`step1Hints` 在组件外侧，仍然保留。

`index.js` 的 `visibleSharedVars` 负责不重复展示：过滤掉 `enable_ui`（已在前一步确定），
并在未启用时隐藏 `ui_image` / `ui_port`。**零骨架改动**（模板基线 43201 字符逐字节一致）。

> 版式在 **§9.7** 按用户反馈调整为「与两个模式卡**同排**的第三张卡」（初版是在模式网格下方
> 另挂的一条独立提示条，用户认为与「部署模式」割裂）。

### 9.4 K5 · 探活 + 接入地址 —— `probe.go` / `endpoint.go`（新）

**一个结构性差异**：kafka 容器名带运行期序号（`kafka-<seq>` / `kafka-zk-<seq>`），
而 `stackkit.ProbeCtx` 只有主机 IP 与参数，**拿不到 seq** ⇒ 无法预知容器名。
解法（避免为它改动通用层）：

1. `Containers` 返回空 ⇒ 引擎先做通用 compose 状态检查（「容器」检查项已含每个容器名）；
2. `ScriptTail` 用 `docker ps` **现取**本机 kafka 相关容器名，复用引擎脚本里已定义的
   `inspect_ctr` 逐个输出（格式与引擎一致，含镜像与启动时间）；
3. `ParseSuite` 按真实名字分配组件归属，产出「实例 / 版本 / 仲裁 / 集群」检查项；
4. 好处：将来容器命名规则再变，探活依然正确。

组件与检查项：

| 模式 | 组件 | 检查项 |
|---|---|---|
| `kraft` | `kafka` | 实例（容器）、版本（镜像）、**仲裁**（`kafka-metadata-quorum.sh describe --status`：leader / voters / 最大落后） |
| `zk` | `kafka` + `zookeeper` | 实例 ×2、版本、**集群**（`srvr` 四字命令的 `Mode: leader/follower`） |
| 开了 UI | `ui` | 实例（`kafka-ui`，容器不存在时不产生检查项） |

`Summary` 给出 K1 风险的**可观测入口**：把 `voters` 数与探活通过台数对照 ——
低于多数派时输出「仲裁异常」（骨架据此把整体判为不通过）；ZK 侧要求 leader 唯一；
开了 `enable_ui` 却探不到 `kafka-ui` 也点出来（否则用户以为 UI 装好了）。

`endpoint.go` 补上引擎兜底缺失的信息（原本只有一条 `tcp://<ip>`，与 `register.go`
登记的 `kafka://` 并不一致）：`kafka://<ip>:<port>`、zk 模式追加 `zookeeper://<ip>:<zk_port>`、
首台且开 UI 时追加 `http://<ip>:<ui_port>`，均带 `component` —— 组件归属驱动探活页签。

### 9.5 未做的部分

- **K3 / K4**（UI 容器随缩容/卸载收口、`purge` 覆盖数据目录）：涉及通用层
  （`stackComposeDownScript` 只 `down` `${home_dir}/compose.yml`），需门禁重录，单独一轮。
  **现状风险仍在**：缩容/卸载首台后 `kafka-ui` 容器成为孤儿并占着 8080。
- **K6**（仲裁助手通用化到 nacos / rabbitmq / rocketmq）：按 §6 分批。
- **`zk` 模式扩容**与 **`scale_in` 真软下线**：维持「明确不做」的结论（§6 末）。

### 9.6 验证

- `go build ./...` / `go vet ./...` / `go test ./store/... ./api/stack/`（kafka 包新增 13 个用例）。
- **反向验证**：临时拆掉 `guardQuorum` 的下限闸，`TestScaleGuardRejectsBelowThree` 如期 FAIL。
- `node script/_fe_smoke.js`：脚本 59/59（新增 2 个）、9 套件注册齐备、**模板 43201 字符逐字节一致**
  （通用骨架零改动），并新增 kafka 插槽断言（`selectComp` / `visibleSharedVars` / 局部与全局组件）。
- `python script/check_stack_lines.py --targets`：**套件分支 0 处、套件专属选择器 0 处**；
  受检文件 163 个 / 20265 行（新增文件已 `--record`，`_note` 写明来源）。
- 快照三类：`blueprint/kafka.json`（`enable_ui` 变 bool）、`render/kafka__*__node.txt`（开关归一化块）、
  `cases/kafka_*.json`（endpoints 由 `tcp://<ip>` 变为 `kafka://<ip>:9092` 等）。
- **待真机验证**：`kafka-metadata-quorum.sh` 的字段名与 `srvr` 输出（两者在真实集群上的格式）、
  UI 容器巡检、缩容拦截的实际用户体验。

### 9.7 复核反馈：开关改成同排第三张卡 + 修「点击不生效」

用户反馈两件事：① 那条独立的「可选组件 / KafkaUI 控制台」提示条**点了没反应**；
② 需求是「**放到部署模式的卡片里面**加一个开启 UI 的开关」。两条都已处理。

**① 根因：骨架用了 Vue 3 里不存在的 `$set`（真 bug，且是通用层）**

- 本项目跑的是 Vue 3（`vendor/vue.global.prod.js`），该文件里**没有** `$set`
  （`element-plus.min.js` 里那个 `$set` 是 dayjs 的日期方法，不是全局 API）。
  而 `template/static/stacks/common/wizard.js` 的 `onBoolVar` 写的是
  `this.$set(this.sharedParams, name, ...)` ⇒ 调用即抛 `TypeError`。
- **为什么之前没暴露**：`onBoolVar` 只被两处调用 —— 第三步的 `v.type==='bool'` 开关，
  以及套件第一步组件。而全仓只有两个 bool 变量（bigdata `ha`、kafka `enable_ui`），
  bigdata 的 HA 走卡片上的 `onHaToggle`（直接赋值），且 `enable_ui` 刚被
  `visibleSharedVars` 从第三步过滤掉 ⇒ **这条路径此前是死代码**，直到 kafka 第一步
  开关接上才第一次被执行到。
- **为什么是「静默」失败**：Vue 3 的 prod 构建会吞掉事件处理器里的异常，
  `window.onerror` 收不到 ⇒ 点击后界面毫无变化、控制台也干净。
- 修法：改直接赋值（`onBoolVar` 与相邻的 `onHaToggle` 统一口径；Vue 3 的 Proxy 响应式
  对新增键天然生效，本就不需要 `$set`）。**属通用层缺陷修复，零套件分支。**
- 防复发：`script/_fe_smoke.js` 新增**第 10 项门禁**——扫描全部已求值脚本，命中
  `this.$set(` / `this.$delete(` / `this.$on(` / `this.$off(` / `Vue.set(` / `Vue.delete(` /
  `Vue.prototype` / `beforeDestroy` 即 FAIL（全仓现有命中数为 0，无历史误报）。
- 新增常驻测量台 `script/measure_kafka_ui_switch.html`：真实 Vue 3 + Element Plus 挂载真实
  `select.js`，并**取用 `wizard.js` 里真实的 `onBoolVar`**（不复制粘贴）后真实派发点击事件。

**② 版式：KafkaUI 由「独立提示条」改为「与两个模式卡同排的第三张卡」**

`select.js` 里那张卡直接复用骨架的 `.stk-mode-grid` / `.stk-mode`，开启态用
`.stk-mode--sel`（与模式卡的选中态同一套视觉），卡内结构也与模式卡对齐
（首行「控件 + 名称」、次行说明 `<p>`、末行 `.faint` 提示），因此**天然与模式卡等高、同风格**；
`style.css` 因此从 18 行缩到 8 行（边框/圆角/内边距/选中态全由 `.stk-mode*` 提供）。
整张卡可点（`toggleUI`），开关自身 `@click.stop` 拦截冒泡后由 `@change` 驱动，
两条路径都落到 `onBoolVar`，不会双重翻转。

**验证（真实浏览器实测，非推断）**

| 检查 | 修复前（`$set`） | 修复后 |
|---|---|---|
| `typeof vm.$set` | `undefined` | `undefined`（根因） |
| 点开关后 `sharedParams.enable_ui` | `"false"`（**没变** = 用户看到的现象） | `"true"` |
| 点开关后 `el-switch` `is-checked` | `false` | `true` |
| 点开关后卡片 `stk-mode--sel` | `false` | `true` |
| 点卡片正文再切回 | `"false"` | `"false"`（正确翻转） |
| 卡片总数（`.stk-mode-grid > .stk-mode`） | 3 | 3（2 模式 + 1 UI） |

两轮都 `errors: none`，正是「静默失败」的特征。
其余：`go test ./store/... ./api/stack/` 全绿；`_fe_smoke.js` 59/59 + 模板 **43201 字符仍逐字节一致**
（通用骨架模板未动）；`check_stack_lines --targets` 通过（163 文件 / **20264 行**、套件分支 0、
专属选择器 0，本轮有意增行：`wizard.js` 309→312 为 3 行原因注释、`kafka/select.js` 45→51 为卡片
markup 与 `toggleUI`，`kafka/style.css` 18→8 为复用 `.stk-mode` 后的缩减）。

