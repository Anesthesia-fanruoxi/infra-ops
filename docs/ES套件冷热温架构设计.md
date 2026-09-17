# Elasticsearch 套件冷热温架构设计

> 文档类型：技术方案 · 版本：v1.3 · 日期：2026-09-16（v1.1 落定三档规格预设，移除 SSL 与 JVM 自由参数；v1.2 修正内存口径为「数据层 2×堆、master/协调 堆+4G」；v1.3 拓扑改为「最小规划 + 按 n 计算」，删写死机器数的参考拓扑，容量全部改单 hot 节点口径） · 范围：工具 → 业务套件 ES

ES 套件升级为**双模式部署**：保留原有「集群」模式，新增「冷热温」架构模式。冷热温模式沿用大数据底座的**主脑流水线**多阶段编排，支持节点角色自定义（master / 纯协调 / 数据-hot/warm/cold）、**三档规格预设**与官方缩容下线流程，并同步将默认镜像升级至 **Elasticsearch 9.5.3**。

## 1. 背景与目标

当前 ES 套件只提供单一「集群」模式：所有节点同规格，由平台注入 master/data 隐式划分，无法按数据热度做分层存储。当业务同时存在高频热数据、中频温数据和低频冷数据时，把所有节点一视同仁会浪费成本，或在冷数据回流时拖累整体查询。

本方案让 ES 套件在**部署时像 Redis 一样可选择模式**：既有「集群」模式原样保留（仅升级镜像），另新增一套面向分层的「冷热温」架构模式。两模式共享同一 ES 套件入口，用模式下拉区分。

**范围口径**：本次交付把「冷热温」做成一个独立的可选项，节点角色、流水线编排、规格档位预设、缩容流程均围绕该模式落地；LLM 插件、索引模板、ILM 生命周期等**延到大套件后续扩展**，不做为本次范围。

## 2. 部署模式总览

在套件部署向导第一步选模式，与 Redis 主从/哨兵/集群的模式选择同一交互。模式确定后，第三步参数与角色分配界面按模式动态展示。

| 维度 | 原「集群」模式（保留） | 新增「冷热温」架构模式（新） |
| --- | --- | --- |
| 定位 | 小规模、规格一致的 ES 集群 | 按数据热度分层的生产环境集群 |
| 节点角色 | 平台注入 master/data 隐式划分 | 每台可多选：master / 纯协调 / 数据-hot/warm/cold |
| 编排方式 | 单阶段 node.sh | 主脑流水线多阶段（reset→masters→coords→data→verify） |
| 协调节点 | 隐式 | 独立「纯协调」角色，默认 2 台 |
| 规格档位 | 按档位的 data_hot 档给单节点堆 | 第 2 步选档（小额尝鲜/标准使用/大力出奇迹），逐角色给堆与内存建议 |
| 标签分层 | 不支持 | 按 `data_hot/warm/cold` tier 分配索引 |
| 默认镜像 | `elasticsearch:9.5.3` | `elasticsearch:9.5.3` |

「集群」模式保持单容器与单阶段编排不变，仅把默认镜像升到 9.5.3、并把 JVM 堆改为由规格档位给出（见 §6）。所有新增复杂度收敛在「冷热温」模式内。

## 3. 冷热温架构的角色模型

Elasticsearch 中**每个节点都隐式承担协调职责**——收到客户端请求的节点自动执行 scatter/gather 两阶段协调（[官方文档](https://www.elastic.co/guide/en/elasticsearch/reference/8.3/modules-node.html)）。因此「协调」拆成两种语义：`master` 候选节点（隐式协调但不存数据），以及 `node.roles=[]` 的**纯协调节点**（只转发/聚合）。

| 勾选角色 | `node.roles` | 说明 |
| --- | --- | --- |
| master（候选） | `["master"]` | 参与选举，隐式协调，不存数据 |
| 纯协调 coordinating | `[]` | 仅做负载均衡与聚合，建议 ≥2 台 |
| 数据-热 data_hot | `[..., "data_hot"]` | 高 IOPS 层，存热数据 |
| 数据-温 data_warm | `[..., "data_warm"]` | 中频访问层 |
| 数据-冷 data_cold | `[..., "data_cold"]` | 低频归档层 |

### 组合与校验规则

- **master 不与数据层混部**：master 候选不承担任何 `data_*` 角色，保证选举节点轻量稳定。
- **数据层可多选叠加**：一台大服务器可同时勾选 hot/warm/cold，映射为单容器 `node.roles=["data_hot","data_warm","data_cold"]`。
- **热节点每台唯一**：套件模型是每台一个 ES 容器，天然满足「一台不出现 2 个 hot」。
- **≥1 个 master 候选**，建议奇数（生产 ≥3），避免脑裂。
- **纯协调建议 ≥2 台**，实际数量由矩阵勾选决定（每个勾选格 = 一个容器）。

### 节点角色在界面上的分配

向导第二步的主机选择区，每台主机呈现可多选的角色 checkbox；平台按并集生成该主机 `node.roles`。master 与数据层互斥由前端置灰 + 后端校验双保险拦截。

> 数据层叠加玩法在「服务器资源充足」前提下允许；规格一般时建议每台只承担单一数据层，避免 I/O 冲突。

## 4. 主脑流水线编排

沿用大数据底座 `pipelineByMode`：安装拆成有序阶段，**每阶段全机成功后才进入下一阶段**。`node.sh` 改造为 RUN 分发器，用 `__run` 决定执行哪个 `run_<key>` 分支。

| 阶段 Key | Label / 目标 | 作用 |
| --- | --- | --- |
| `reset` | 清理旧环境与残留容器 · all · FullOnly | 清掉上次失败残留的容器/数据目录 |
| `masters` | 拉起 master 候选 · all | 首次含 `discovery.initial_master_nodes` 引导，全局仅引导节点 init |
| `coords` | 拉起纯协调节点 · all | 启动 `node.roles=[]` |
| `data` | 拉起数据节点 · all | 按主机角色启动 hot/warm/cold tier |
| `verify` | 校验集群状态 · leader · Always | 校验健康与各 tier 分层 |

**一图流**：`reset → masters → coords → data → verify`（每阶段全机成功才推进）。

- **扩容/加装复用**：沿用 `selectPipelinePhases` 裁剪，scale_out 只在新主机跑对应角色阶段，不触碰既有节点。
- **失败定位**：单阶段全机并行，失败精确到主机与阶段 Key。
- **配置全量覆盖**：重装对所有主机全覆盖生成 `elasticsearch.yml`，避免旧配置残留。

## 5. 参数配置

参数按「声明式 + 锚点替换」落到脚本，分共享变量与主机变量两组；**窗口级共享参数由前端写入 `sharedParams`，后端在建实例时合并进每台主机的 `ParamsJSON`**，运行期再由套件的 `ExtraVars` 读回。

共享变量（`SharedVars`）：

- `cluster_name` / `image`：集群名与镜像（默认 `elasticsearch:9.5.3`）。
- `sizing`：**规格档位**，取值 `mini` / `standard` / `full`，默认 `standard`。第 2 步由套件自己的组件选卡（不出现在第 3 步参数页），空值或未知值一律回落 `standard`。
- `transport_port`：Transport 端口（默认 9300），**仅集群模式**可填；冷热温对外只登记协调/master 入口，端口按角色固定偏移。

主机变量（`HostVars`）：

- `port`（默认 9200，仅集群模式）、`home_dir`（服务主目录，默认 `/data/elasticsearch`）。
- `roles`（仅冷热温）：该主机的角色勾选，逗号分隔，每勾一个角色 = 该主机一个独立 ES 容器；留空表示「自动模式」，部署时兜底归冷热温数据层。

> 历史沿革：早期设计里的 `ssl_enabled`（自签证书）、`coordinator_count`、以及 `java_opts / heap_xms / heap_xmx / jvm_opts` 四个 JVM 自由文本变量均已移除——xpack 安全按设计保持关闭，协调节点数由「勾选即部署」的矩阵天然决定，堆内存改由规格档位给出（见 §6）。

## 6. 规格档位与堆内存（三档内部预设）

### 6.1 为什么不再暴露 JVM 参数

ES 8.7+ 会按「节点角色 + 容器可见内存」**自动**推算 JVM 堆。冷热温是一机多容器：每个容器看到的都是宿主全量内存，自动定堆会让同机各容器堆之和高出宿主实际内存，直接触发 OOM 或换页。因此堆必须由平台按「档位 × 角色」显式指定，且必须同时满足官方两条硬约束：

- **堆 ≤ 宿主内存的 50%**（**仅数据节点**——其余留给 Lucene 的文件系统缓存，否则查询性能断崖；master / 纯协调不存分片，见 §6.4）；
- **堆 ≤ 31 GB**，越过 HotSpot 的 compressed oops 上限后指针变宽、有效内存反而变少。

GC 固定 `-XX:+UseG1GC`（ES 默认且官方推荐），不再透传其它 JVM 参数。

### 6.2 唯一事实源

`store/stacks/elasticsearch/sizing.go` 定义三档 × 五角色的配置表，是全平台唯一的堆内存来源，同时供两条链路消费：

- **部署期**：`ExtraVars` 按主机读回档位，注入 `__es_sizing` / `__es_sizing_label`；集群模式注入 `__es_heap`（取该档 `data_hot` 档，节点即数据节点），冷热温注入 `__es_heap_master` / `__es_heap_coordinator` / `__es_heap_data_hot` / `__es_heap_data_warm` / `__es_heap_data_cold`。
- **界面**：同一份表经蓝图新增的 `Extras` 字段原样下发，第 2 步档位卡、逐角色规格表与容量芯片都读它——**界面数字与实际部署数字同源**，不会漂移。

`Extras` 是「套件自定义只读元数据」的通用出口：骨架不认识其中任何键、不做任何分支，套件写什么、前端读什么。

### 6.3 三档规格（每角色 JVM 参数 + 基本数量 + 单 hot 性能基准，v1.4）

**档位定义「每个角色的 JVM 参数 + 基本数量 + 单 hot 节点性能基准」**。性能基准是**单 hot 节点口径**（集群能力 ≈ n × 该值，n 由用户按手上的数据节点自行换算，界面不做推算——按数据量推台数没有准数，做过 n 换算芯片又撤掉）。机器数量与硬件规格最终由用户决定，档位数值只是参考不是要求：

| 档位 | JVM 堆（master/协调 · hot · warm · cold） | 基本数量 | 单 hot 日增 | 单 hot 写入 | 查询 QPS |
| --- | --- | --- | --- | --- | --- |
| `mini` 小额尝鲜 | 2g · 6g · 4g · 2g | 每角色各 ×1 | 5–20 GB / 节点 | 10–20 MB/s（约 1–2 万条/秒） | 30–100 起 |
| `standard` 标准使用 | 8g · 16g · 8g · 4g | master ×3（奇数）· 协调 ×2，数据层按需 n | 30–80 GB / 节点 | 20–40 MB/s（约 2–4 万条/秒） | 每 hot 节点约 100–400 |
| `full` 大力出奇迹 | 16g · 31g · 16g · 8g | master ×3 · 协调 ×2，数据层按需 n | 80–230 GB / 节点 | 40–80 MB/s（约 4–8 万条/秒） | 每 hot 节点约 150–650 |

> **master 与协调合并为一行**：master 候选本身隐式协调，两角色规格取原纯协调（较重）的一档，取大不取小；部署注入时 coordinator 复用该行堆（`roleHeaps` 映射），脚本侧 `HEAP_COORDINATOR` 变量不变。

> **「数量一致、规格不一致」是合法形态**：官方对三层数量没有任何对齐要求，各层独立伸缩。一机多容器（如 5 台同构大机、每台跑 hot+warm+cold 三个容器）就是 `n_hot = n_warm = n_cold` 的特例——机器维度同构便于采购运维，容器维度按角色取各自规格；此时宿主内存按三容器建议内存之和取（角色矩阵带 I/O 争用提示）。

逐角色的堆与内存建议在 `sizing.go` 里逐条写死，三档之间**逐角色单调不减**，并由单元测试自证（含「数据层 Ref 必须为按需 n」的反写死断言）；档位选择后界面上会直接列出这张表，用户不需要自己折算。

### 6.4 内存口径按角色分两类（不是一律 2 × 堆）

初版把「堆 ≤ 宿主内存 50%」这条官方口径套到了**所有**角色上，结果满载档的纯协调节点成了「16g 堆配 64G 内存」。这条口径的来源是 Lucene 页缓存——它只对**存分片的节点**成立：

| 角色类别 | 内存口径 | 依据 |
| --- | --- | --- |
| 数据节点（hot / warm / cold） | **内存 = 堆 × 2** | 官方 heap-size 口径：另一半留给 Lucene 文件系统缓存，段文件读全走它，直接决定查询延迟 |
| master / 纯协调 | **内存 = 堆 + 4 GB** | 两类节点都不存分片、无 Lucene 页缓存需求，只需覆盖 JVM 堆外（metaspace / 线程栈 / netty direct buffer）与系统开销 |

按此修正后（堆与磁盘规格一律不动，只降内存），「一机一角色」的合计内存：

| 档位 | 修正前 | 修正后 |
| --- | --- | --- |
| `mini` | 56 GB | 38 GB |
| `standard` | 160 GB | 76 GB |
| `full` | 256 GB | 144 GB |

界面上内存列会带口径短标注（`堆 × 2` / `堆 + 4G`），表下一行给出整句说明——数字与解释来自同一份 `sizing.go`（`mem_rule` 逐行、`mem_note` 整句，由 `esSizingTable` 统一补齐后随 `Extras` 下发），不存在界面一套说法、实现另一套的可能。

> **同机混部**：宿主内存按该机上各容器的**建议内存之和**取，而不是按堆之和。单测里 `sizingMemViolation` 对两类角色分别校验（数据层不得低于 2 × 堆，master/协调不得低于堆 + 4G 且不得高于堆 + 12G——后者是为了拦住「又按 2 × 堆给」的虚胖回潮），并配了反向验证用例。

> 容量口径：以上为**量级参考而非承诺**，前提是副本数 1、hot 层保留约 7 天、单文档约 1 KB JSON、单 hot 节点持续写入 10–80 MB/s（随档位递增）。实际吞吐受分片数、文档大小、查询复杂度与磁盘类型影响，上表用于选型沟通，不作为 SLA。

### 6.5 脚本侧

- 冷热温 `node.sh`：`heap_of <role>` 取本角色堆，写进 compose 的 `ES_JAVA_OPTS=-Xms… -Xmx… -XX:+UseG1GC`；
- 集群 `node.sh`：同样固定为 `-Xms{{__es_heap}} -Xmx{{__es_heap}} -XX:+UseG1GC`，并在结束时回显档位与堆；
- `check_host_capacity`：一机多容器时读 `/proc/meminfo` **软校验**「同机各容器堆之和 ≤ 宿主内存 50%」，超了给告警但不阻断部署（用户可能确实只想先跑起来）；
- `sysctl / 内存锁`：检测 `vm.max_map_count` 不足给出提示；可选 `bootstrap.memory_lock`。

门禁 `script/check_es_tpl.py` 对三场景渲染做断言，并用带负向回头的正则**反向校验两个脚本里不存在任何自由文本 JVM 变量**（排除合法的 `ES_JAVA_OPTS`），确保「去掉 JVM 参数」这件事不会在后续改动里悄悄回退。

## 7. 扩容与缩容

### 扩容（scale_out）

新增主机**只在其自身执行**对应角色分支，归属 master/协调/数据层由勾选角色决定，**不改动既有节点配置**。

### 缩容（scale_in）

不能直接拔停容器，必须先走 ES 官方**下线**流程：

1. **排空分片**：`PUT /_cluster/settings` 设置 `cluster.routing.allocation.exclude._name`（或 `_ip`），把该节点分片迁走（[官方文档](https://www.elastic.co/docs/deploy-manage/maintenance/add-and-remove-elasticsearch-nodes)）。
2. **（推荐）节点停机 API**：`PUT /_nodes/<node_id>/shutdown` 且 `"type":"remove"`，由 ES 自动排空再安全下线（[官方文档](https://www.elastic.co/guide/en/elasticsearch/reference/8.3/put-shutdown.html)）。
3. **master 候选专用**：删 master 候选需先 `POST /_cluster/voting_config_exclusions` 更新投票配置。
4. **确认健康后停容器**：分片迁移完成、集群恢复 green，再对该主机 `docker compose down`。

冷热温模式需要一套**自定义 `scale_in` 脚本**，在通用 compose down 前插入下线流程。

## 8. 前端交互

- **模式选择**：向导第一步网格卡片呈现「集群」「冷热温」，选中后动态渲染下一步。
- **规格档位与角色分配**（第 2 步，冷热温为「倒品字」两栏布局：上左主机勾选、上右套件表单）：套件自己的组件渲染档位选卡（小额尝鲜/标准使用/大力出奇迹）与逐角色规格表（角色 / JVM 堆 / 内存建议 / 参考数量）、同机叠放提示；主机勾选区每台多选角色，master 与数据层互斥置灰，行留空即自动模式（预览显示灰色「自动分配（数据节点）」）。
- **参数配置**（第 3 步）：集群名、镜像、端口等；档位已在第 2 步选定，由套件的 `visibleSharedVars` 过滤掉不再重复展示。
- **结果展示**：接入地址（协调/master 入口）、各 tier 节点分布、健康状态。

## 9. 后续扩展（大套件）

LLM/向量搜索插件、索引模板、分词器、ILM 生命周期策略等延到大套件，复用本模式已铺好的节点接入与参数化配置；本期只交付可运行的冷热温部署能力。

## 10. 实施步骤与风险

### 实施步骤

1. 内置模板改造：升级 ES 9.5.3，新增「冷热温」模式及模式级变量。
2. `node.sh` 改造为 RUN 分发器（`run_reset/run_masters/run_coords/run_data/run_verify`）。
3. 接入 `pipelineByMode` 流水线与角色→`node.roles` 生成/校验。
4. 实现三档规格预设：`sizing.go` 逐角色堆表 + `ExtraVars` 注入 + 脚本消费（取代早期设想的 SSL 自签与 JVM 参数透传）。
5. 实现自定义 `scale_in` 下线脚本。
6. 前端向导：模式、档位选卡、角色多选。
7. 补充单测与三场景渲染校验。

### 主要风险

| 风险 | 应对 |
| --- | --- |
| master 候选为偶数 / 脑裂 | 校验奇数并给引导提示，生产建议 ≥3 |
| 缩容直接停容器导致分片重分配故障 | 强制先走官方下线流程再停容器 |
| 同机 hot/warm/cold 叠加 I/O 冲突 | 角色分配页提示，默认仅单数据层 |
| 一机多容器堆超卖（ES 自动定堆按可见内存算，容器看到的是宿主全量） | 堆一律由档位显式给出，脚本再加同机堆之和 ≤ 宿主 50% 的软校验告警 |
| 首次引导选举失败 | `initial_master_nodes` 仅引导节点注入，verify 前有界等待 |

## 参考来源

1. Elasticsearch 官方文档 · Node（协调节点与 scatter/gather）：https://www.elastic.co/guide/en/elasticsearch/reference/8.3/modules-node.html
2. Elasticsearch 官方文档 · Add and remove Elasticsearch nodes：https://www.elastic.co/docs/deploy-manage/maintenance/add-and-remove-elasticsearch-nodes
3. Elasticsearch 官方文档 · Node shutdown API：https://www.elastic.co/guide/en/elasticsearch/reference/8.3/put-shutdown.html
4. Elasticsearch 官方文档 · 堆内存与 JVM 设置（不超过宿主内存 50%、不超过 31GB、G1GC）：https://www.elastic.co/guide/en/elasticsearch/reference/current/advanced-configuration.html
5. Elasticsearch 官方文档 · 自动定堆（8.7+ 按节点角色与可见内存推算，一机多容器须显式指定）：https://www.elastic.co/guide/en/elasticsearch/reference/current/docker.html