# 任务 05 · 大数据底座全组件高可用 — backend.md（后端执行）

> 设计依据：`docs/bigdata-ha-design.md`（评审稿，含组件级方案 §九）。
> 原则：套件级 `ha` bool 开关；非 HA 路径零回归；单文件 ≤300 行。
> 执行顺序即步骤编号；每步完成后按验收标准验收并在 workflow.md 标记 [x]。

## B1 模型与蓝图

- [ ] `model/stack.go`：`StackVar` 加 `Type`（string，"bool"）；`StackBlueprint` 加 `HaSupport bool`
- [ ] `store/builtin_stacks.go`：bigdata 蓝图加 `ha` bool 变量 + 辅助变量（`hdfs_nameservice`/`jn_http_port`/`jn_rpc_port`/`hive_db_image`/`hive_db_password`）+ `metastore_db` 组件声明 + `HaSupport: true`

验收：`go build`/`go vet` 通过；蓝图序列化含新字段；旧实例数据不受影响。

## B2 引擎占位符与分配算法

- [ ] `api/stack_engine.go`：`mergeBigdataMasterExtra` 扩展——`{{__zk_ips}}`、`{{__hdfs_entry}}`、`{{__nn1_ip}}/{{__nn2_ip}}/{{__jn_ips}}`、`{{__rm1_ip}}/{{__rm2_ip}}`、`{{__spark_m2_ip}}`、`{{__flink_jm2_ip}}`、`{{__hmaster2_ip}}`、`{{__hs2_ips}}/{{__ms_ips}}/{{__hive_db_ip}}`
- [ ] 从角色确定性分配算法（§4.2）：primary=角色矩阵指定??主节点；secondary/JN/ZK=矩阵指定??seq 规则
- [ ] masters 参数白名单扩展（`hdfs_nn2`/`yarn_rm2`/`spark_m2`/`flink_jm2`/`hbase_hm2`/`hive_ms2`/`hive_hs2b`/`hive_db`/`hdfs_jns`）

验收：`go build`/`go vet` 通过；占位符渲染单测/脚本校验覆盖 HA 全开与非 HA 两形态。

## B3 校验规则与保护

- [ ] `api/stack_instance.go`：`validateBigdataMasters` 扩展六条（§4.3：ZK 必勾、≥3 台、同组件角色互斥、metastore_db 强制、缩容保护、卸载依赖）
- [ ] `api/stack.go`：创建入口校验透传；`buildCreateHosts`/reinstall bootstrap 落点扩展到各组件 primary

验收：非法组合（无 ZK/<3 台/NN1=NN2）逐条返回 400 且文案明确；合法创建全链路通过。

## B4 HDFS HA 落地

- [ ] `configs/hdfs/core-site.xml`/`hdfs-site.xml`：`{{__hdfs_entry}}` + `{{__ha_core_props}}`/`{{__ha_hdfs_props}}` 条件块（QJM 属性见设计文档 §9.2）
- [ ] `scripts/node.sh`：HDFS 四分支——JN 主机（journalnode 容器）/NN1（format 幂等 + zkfc）/NN2（bootstrapStandby + zkfc）/其余 DN；`chown 1000:1000` 覆盖 journal 目录
- [ ] `scripts/bootstrap.sh`：NN1 上 `hdfs haadmin -getAllServiceState` 校验 + 汇总输出

验收：`bash -n` ×2；渲染校验 HA/非 HA 双形态属性正确；真机 3 台部署→stop NN1→30s 内 NN2 接管。

## B5 ZK 系组件 HA（YARN/Spark/Flink/HBase）

- [ ] `configs/yarn/yarn-site.xml`：`{{__ha_yarn_props}}`（ha.rm-ids/hostname×2/ZKRMStateStore/proxy），非 HA 保留单 hostname
- [ ] `scripts/node.sh`：YARN 双 RM、Spark 双 Master（spark-defaults: recoveryMode=ZOOKEEPER + recoveryDirectory）、Flink 双 JM（FLINK_PROPERTIES: ha.zookeeper/storageDir）、HBase backup-master 容器；`hbase.rootdir` 改 `{{__hdfs_entry}}`
- [ ] `scripts/bootstrap.sh`：`rmadmin -getAllServiceState`、Spark/Flink ZK leader、`hbase shell status` 校验

验收：`bash -n`；渲染校验双形态；真机各组件故障演练（stop 主实例→30s 接管→原实例回归 Standby）。

## B6 Hive HA

- [ ] `scripts/node.sh`：metastore_db（MySQL 8）部署 + `schematool -initSchema -dbType mysql` 幂等初始化 + 2×MS + 2×HS2（ZK 服务发现）
- [ ] `configs/hive/hive-site.xml`：外部 DB 连接变量化 + ZK 服务发现属性 + `{{__hdfs_entry}}`
- [ ] Trino Coordinator metastore 双 uri（`hive.metastore.thrift.uri.1/2`）

验收：`bash -n`；JDBC zkServiceDiscovery 串可连；Trino 查 Hive 表正常。

## B7 探活/服务登记/卸载清理

- [ ] `api/stack_engine.go`：`registerBigdataServices` 从角色登记（Standby/JN/backup 标签，供抽屉角色标签与探活路由）
- [ ] 探活 per-role 适配（§设计文档 6.4）：NN1 haadmin、JN 8485、RM1 rmadmin 等
- [ ] `scripts/node.sh` `migrate_old` 清单追加：journalnode/zkfc/rm 备实例/master 备实例/jm 备实例/hs2×2/ms×2/metastore_db

验收：抽屉角色标签正确；探活按角色路由；整体卸载无容器残留（`docker ps` 核对）。

## B8 渲染校验扩展与回归

- [ ] `script/check_bigdata_tpl.py`：HA 全开/单组件开/全关三场景 × 角色分离渲染校验
- [ ] 非 HA 全回归：8 组件组合创建→探活→卸载，行为与现状一致

验收：渲染校验全绿；`go build`/`go vet`/`bash -n` 全过；回归无行为差异。

## 进度记录

- 遇合顺序：ha.sh 先于 node.sh 的其他组件确定 HDFS/YARN/Spark/Flink/HBase 角色分支；Hive 双实例按 `metastore_db`/MS/HS 独立判定，非主节点也可能承载 DB。
- 《非 HA 回归坑》：BD 蓝图早期把 HA 专属变量（hdfs_nameservice/jn_*_port/hive_db_image/hive_db_password）标成 Required，导致 `components=hdfs,spark` 这类非 HA 组合在 mergeStackParams 阶段被强制校验失败。改为非必填（均带默认值；hive_db_password 空时引擎与 ha.sh 兜底 `HiveDb@123`），保持默认值渲染不变、阻断回归。
- 《@@ 残留坑》：ha.sh 注释中字面写了 `@@HA_SH@@`/`@@HIVE_SITE@@`，被注入 node.sh 后 `TestAllBuiltinStacksWired` 的 `strings.Contains(script,"@@")` 断言误报。措辞改为「HA_SH/HIVE_SITE 占位」规避。
- 《探活容器推断》：`bigdataExpectedContainers` 原仅区分 主/从，未覆盖 HA 从角色。扩为 HA 感知：镜像 ha.sh 分支（优先命中 NN2/JN/备用实例/DB 角色，再回落工作节点容器），新增单测覆盖 主（.1）/备（.2）/JN+DB（.3）三落点全过；NN1/NN2 主机不承载 journalnode 容器。
- 《容器不存在不等于失败》：journalnode 只部署在纯 JN 主机，探测须与 ha.sh 分支一一对齐，避免把 NN2 主机"少了 journalnode"误判为异常。
