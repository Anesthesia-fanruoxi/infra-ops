# Elasticsearch 套件冷热温架构设计

> 文档类型：技术方案 · 版本：v1.0（评审中） · 日期：2026-09-11 · 范围：工具 → 业务套件 ES

ES 套件升级为**双模式部署**：保留原有「集群」模式，新增「冷热温」架构模式。冷热温模式沿用大数据底座的**主脑流水线**多阶段编排，支持节点角色自定义（master / 纯协调 / 数据-hot/warm/cold）、SSL 开关与官方缩容下线流程，并同步将默认镜像升级至 **Elasticsearch 9.5.3**。

## 1. 背景与目标

当前 ES 套件只提供单一「集群」模式：所有节点同规格，由平台注入 master/data 隐式划分，无法按数据热度做分层存储。当业务同时存在高频热数据、中频温数据和低频冷数据时，把所有节点一视同仁会浪费成本，或在冷数据回流时拖累整体查询。

本方案让 ES 套件在**部署时像 Redis 一样可选择模式**：既有「集群」模式原样保留（仅升级镜像），另新增一套面向分层的「冷热温」架构模式。两模式共享同一 ES 套件入口，用模式下拉区分。

**范围口径**：本次交付把「冷热温」做成一个独立的可选项，节点角色、流水线编排、SSL、JVM 调优、缩容流程均围绕该模式落地；LLM 插件、索引模板、ILM 生命周期等**延到大套件后续扩展**，不做为本次范围。

## 2. 部署模式总览

在套件部署向导第一步选模式，与 Redis 主从/哨兵/集群的模式选择同一交互。模式确定后，第三步参数与角色分配界面按模式动态展示。

| 维度 | 原「集群」模式（保留） | 新增「冷热温」架构模式（新） |
| --- | --- | --- |
| 定位 | 小规模、规格一致的 ES 集群 | 按数据热度分层的生产环境集群 |
| 节点角色 | 平台注入 master/data 隐式划分 | 每台可多选：master / 纯协调 / 数据-hot/warm/cold |
| 编排方式 | 单阶段 node.sh | 主脑流水线多阶段（reset→masters→coords→data→verify） |
| 协调节点 | 隐式 | 独立「纯协调」角色，默认 2 台 |
| SSL | 默认关闭 | 参数配置第 3 步提供开关，开启自动签发自签证书 |
| 标签分层 | 不支持 | 按 `data_hot/warm/cold` tier 分配索引 |
| 默认镜像 | `elasticsearch:9.5.3` | `elasticsearch:9.5.3` |

「集群」模式只做两处最小改动：默认镜像升到 9.5.3，其余脚本与编排保持不变。所有新增复杂度收敛在「冷热温」模式内。

## 3. 冷热温架构的角色模型

Elasticsearch 中**每个节点都隐式承担协调职责**——收到客户端请求的节点自动执行 scatter/gather 两阶段协调（[官方文档](https://www.elastic.co/guide/en/elasticsearch/reference/8.3/modules-node.html)）。因此「协调」拆成两种语义：`master` 候选节点（隐式协调但不存数据），以及 `node.roles=[]` 的**纯协调节点**（只转发/聚合）。

| 勾选角色 | `node.roles` | 说明 |
| --- | --- | --- |
| master（候选） | `["master"]` | 参与选举，隐式协调，不存数据 |
| 纯协调 coordinating | `[]` | 仅做负载均衡与聚合，默认 2 台 |
| 数据-热 data_hot | `[..., "data_hot"]` | 高 IOPS 层，存热数据 |
| 数据-温 data_warm | `[..., "data_warm"]` | 中频访问层 |
| 数据-冷 data_cold | `[..., "data_cold"]` | 低频归档层 |

### 组合与校验规则

- **master 不与数据层混部**：master 候选不承担任何 `data_*` 角色，保证选举节点轻量稳定。
- **数据层可多选叠加**：一台大服务器可同时勾选 hot/warm/cold，映射为单容器 `node.roles=["data_hot","data_warm","data_cold"]`。
- **热节点每台唯一**：套件模型是每台一个 ES 容器，天然满足「一台不出现 2 个 hot」。
- **≥1 个 master 候选**，建议奇数（生产 ≥3），避免脑裂。
- **纯协调默认 2 台**，可在参数中调整。

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
| `verify` | 校验集群状态 · leader · Always | 校验健康、各 tier、证书 |

**一图流**：`reset → masters → coords → data → verify`（每阶段全机成功才推进）。

- **扩容/加装复用**：沿用 `selectPipelinePhases` 裁剪，scale_out 只在新主机跑对应角色阶段，不触碰既有节点。
- **失败定位**：单阶段全机并行，失败精确到主机与阶段 Key。
- **配置全量覆盖**：重装对所有主机全覆盖生成 `elasticsearch.yml`，避免旧配置残留。

## 5. 参数配置与 SSL 开关

第三步参数配置新增变量，均走既有「声明式 + 锚点替换」机制。

- `ssl_enabled`（布尔开关）：关闭走明文；开启时 `node.sh` 用 `openssl` 自动生成 CA 与每节点自签证书，注入 `xpack.security.http.ssl.enabled=true` 及证书路径，前端可下载 CA 公钥。
- `cluster_name` / `image`：集群名与镜像（默认 `elasticsearch:9.5.3`）。
- `transport_port` / `http_port`：Transport（9300）与 HTTP（9200）端口，host 网络直连。
- `coordinator_count`：纯协调节点数量，默认 2。

## 6. JVM 参数调优

- `heap_xms / heap_xmx`：每台可设堆内存；数据层默认建议 hot > warm > cold。
- `jvm_opts`（追加）：GC 与其它参数透传（默认 `-XX:+UseG1GC`），合并进 `ES_JAVA_OPTS`。
- `sysctl / 内存锁`：检测 `vm.max_map_count` 不足给出提示；可选 `bootstrap.memory_lock`。

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

- **模式选择**：向导第一步 el-radio 呈现「集群」「冷热温」，选中后动态渲染下一步。
- **角色分配**：主机选择区每台角色多选，master 与数据层互斥置灰，纯协调默认勾选 2 台。
- **参数配置**：第三步展示 SSL 开关、端口、集群名、JVM 堆与 `jvm_opts`。
- **结果展示**：接入地址、各 tier 节点分布、健康状态；SSL 开启时提供 CA 下载。

## 9. 后续扩展（大套件）

LLM/向量搜索插件、索引模板、分词器、ILM 生命周期策略等延到大套件，复用本模式已铺好的节点接入与参数化配置；本期只交付可运行的冷热温部署能力。

## 10. 实施步骤与风险

### 实施步骤

1. 内置模板改造：升级 ES 9.5.3，新增「冷热温」模式及模式级变量。
2. `node.sh` 改造为 RUN 分发器（`run_reset/run_masters/run_coords/run_data/run_verify`）。
3. 接入 `pipelineByMode` 流水线与角色→`node.roles` 生成/校验。
4. 实现 SSL 自签签发 + 配置注入 + CA 下载。
5. 实现 JVM 堆与参数透传。
6. 实现自定义 `scale_in` 下线脚本。
7. 前端向导：模式、角色多选、SSL/JVM 参数。
8. 补充单测与三场景渲染校验。

### 主要风险

| 风险 | 应对 |
| --- | --- |
| master 候选为偶数 / 脑裂 | 校验奇数并给引导提示，生产建议 ≥3 |
| 缩容直接停容器导致分片重分配故障 | 强制先走官方下线流程再停容器 |
| 同机 hot/warm/cold 叠加 I/O 冲突 | 角色分配页提示，默认仅单数据层 |
| 首次引导选举失败 | `initial_master_nodes` 仅引导节点注入，verify 前有界等待 |

## 参考来源

1. Elasticsearch 官方文档 · Node（协调节点与 scatter/gather）：https://www.elastic.co/guide/en/elasticsearch/reference/8.3/modules-node.html
2. Elasticsearch 官方文档 · Add and remove Elasticsearch nodes：https://www.elastic.co/docs/deploy-manage/maintenance/add-and-remove-elasticsearch-nodes
3. Elasticsearch 官方文档 · Node shutdown API：https://www.elastic.co/guide/en/elasticsearch/reference/8.3/put-shutdown.html