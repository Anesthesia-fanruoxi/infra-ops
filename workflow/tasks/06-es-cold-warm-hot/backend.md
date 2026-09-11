# 任务 06 · Elasticsearch 套件冷热温双模式 — backend.md（后端执行）

> 设计依据：`docs/es-suite-cold-warm-hot.md`（评审稿）。
> 原则：双模式共享同一 ES 套件入口；「集群」模式仅升镜像零回归；冷热温模式沿用主脑流水线多阶段编排；
> 单文件 ≤300 行。执行顺序即步骤编号；每步完成后按验收标准验收并在 workflow.md 标记 [x]。

## B1 蓝图与模式接入

- [ ] `store/builtin_stacks.go`：elasticsearch 蓝图 `Modes` 追加 `cold_warm_hot` 模式
  （Label「冷热温」、说明、MinHosts ≥4、`AssignMaster:false`、角色由每行勾选而非隐式划分）
- [ ] 模式级变量：`ssl_enabled`（bool 开关）、`coordinator_count`（默认 2）、`heap_xms/heap_xmx`、
  `jvm_opts`（默认 `-XX:+UseG1GC`）；`image`/`cluster_name`/`transport_port`/`http_port` 沿用并升默认镜像
- [ ] 既有「集群」模式默认镜像升 `elasticsearch:9.5.3`（蓝图 + node.sh 兜底值同步），其余保持现状

验收：`go build`/`go vet` 通过；蓝图含新模式与变量；旧「集群」渲染输出除镜像外逐字节零差异。

## B2 角色模型与 node.roles 生成

- [ ] `api/stack_engine.go`：角色勾选 → `node.roles` 生成（master→`["master"]`、纯协调→`[]`、
  data_hot/warm/cold→按勾选叠加 `["data_hot","data_warm","data_cold"]`）
- [ ] master 与数据层互斥校验；热节点每台唯一；≥1 个 master 候选且生产建议奇数（≥3），偶数给引导提示
- [ ] `coordinator_count` 落地：不足时自动补勾纯协调默认主机

验收：`go build`/`go vet`；角色组合非法（master 混数据/无 master/热节点重复）逐条 400 且文案明确。

## B3 主脑流水线编排

- [ ] 冷热温模式注册 Pipeline 阶段：`reset → masters → coords → data → verify`（`Target`：前四个 all，
  `verify` 为 leader；`reset` 标 `FullOnly` 防加装/扩容清空既有数据）
- [ ] `store/stacks/elasticsearch/scripts/node.sh` 改造为 RUN 分发器：`run_reset`（清理残留容器+数据目录）/
  `run_masters`（首次含 `discovery.initial_master_nodes` 引导）/`run_coords`（`node.roles=[]`）/
  `run_data`（按主机 tier 启动）/`run_verify`（leader 校验健康、各 tier、证书）
- [ ] `runPipeline` 接入：确认 `selectPipelinePhases` 对 create/reinstall/add_component/scale_out 的裁剪行为
  （scale_out 只在新主机跑对应角色阶段，不触碰既有节点；配置全量覆盖防残留）

验收：`bash -n` node.sh；`go build`/`go vet`；渲染校验阶段序列正确。

## B4 SSL 自签签发与配置注入

- [ ] `ssl_enabled` 关闭走明文（现状）；开启时 `run_masters` 用 `openssl` 生成 CA + 每节点自签证书
- [ ] `elasticsearch.yml` 注入 `xpack.security.http.ssl.enabled=true` + 证书路径/CA；前端可下载 CA 公钥
- [ ] 证书/私钥仅落盘节点 `home_dir`，平台不持久化、日志脱敏

验收：`bash -n`；双形态（开/关）渲染校验通过；开启形态节点 HTTPS 就绪且 CA 可下载。

## B5 JVM 参数调优

- [ ] `heap_xms/heap_xmx` 每台可设；数据层默认建议 hot > warm > cold
- [ ] `jvm_opts`（追加）合并进 `ES_JAVA_OPTS`；`sysctl vm.max_map_count=262144` 沿用并在不足时提示

验收：`bash -n`；渲染校验 java_opts 拼装正确；默认参数无回归。

## B6 缩容 scale_in 官方下线流程

- [ ] 冷热温模式自定义 scale_in：compose down 前插入——① `PUT /_cluster/settings` 设
  `cluster.routing.allocation.exclude._name`（或 `_ip`）排空分片；② `PUT /_nodes/<id>/shutdown` `"type":"remove"`
  自动排空安全下线；③ 缩 master 候选先 `POST /_cluster/voting_config_exclusions`；④ 集群回 green 后再停容器
- [ ] 缩容保护：承载响应式角色（唯一 master/协调）的主机禁缩，前端提示 + 后端兜底

验收：单测/渲染校验通过；真机缩一台数据节点后分片迁移完成、集群回归 green、容器被安全移除。

## B7 服务登记 / 探活 / 接入地址

- [ ] `api/stack_engine.go`：冷热温模式登记服务（角色标签 master/协调/data-hot/warm/cold，供抽屉角色标签与探活路由）
- [ ] `api/stack_verify.go`：按 tier 分层接入地址（协调节点聚合入口、各数据层端点）；关闭 SSL 明文、开启时给出 HTTPS
- [ ] 探活按角色路由与 ES 健康接口校验集群整体状态

验收：抽屉角色标签正确；接入地址按 tier 分组语义正确；SSL 双形态端点无误。

## B8 渲染校验扩展与回归

- [ ] `script/check_es_tpl.py`（新增）：冷热温全角色 / 单一数据层 / 集群旧模式 三场景 × SSL 开/关渲染校验
- [ ] 「集群」模式全回归：建集群→探活→缩容→卸载，行为与现状一致（仅镜像变化）

验收：渲染校验全绿；`go build`/`go vet`/`bash -n` 全过；旧「集群」模式零行为差异。

## 进度记录

（执行中追加：踩坑与决策，参照任务 05《非 HA 回归坑》等条目格式）