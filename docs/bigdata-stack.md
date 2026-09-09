# 大数据底座套件设计（bigdata：组件多选组合）

> 套件位置：`store/stacks/bigdata/`（单目录），蓝图注册在 `store/builtin_stacks.go`（Key=`bigdata`，Category=`platform`）。
> 单模式「组合部署」：一组机器统一主/从角色，勾选组件一次装齐。HDFS 必选打底。

## 一、基础设施版本基线（Java 兼容矩阵）

| 组件 | 默认镜像 | Java 兼容 | 说明 |
|---|---|---|---|
| Hadoop HDFS（必选） | `apache/hadoop:3.3.6` | Java 8 / 11 | 镜像内置 JRE，主机零 JDK 依赖 |
| ZooKeeper | `zookeeper:3.9` | Java 8/11 | ensemble 跑全部节点 |
| YARN | `apache/hadoop:3.3.6`（同 HDFS 镜像） | Java 8 / 11 | RM 在主节点，NM 在工作节点 |
| Spark | `apache/spark:3.5.1` | Java 8 / 11 / 17 | 内置 JDK 11 |
| Flink | `flink:1.19.1` | Java 8 / 11 / 17 | 内置 JDK 11 |
| Hive | `apache/hive:4.0.0` | Java 8 | 内置 JRE；兼容 Hadoop 3.x |
| HBase | `apache/hbase:2.5.10` | Java 8 / 11 | 内置 JRE；依赖 ZooKeeper |
| Trino | `trinodb/trino:435` | 自带运行时 | 依赖 Hive Metastore（thrift 9083） |

**原则：版本兼容问题在镜像层锁定，目标主机不需要安装任何 JDK。**
原生（非容器）部署的最大公约数是 **JDK 11**（可用「基础建设 → 安装 JDK」模板统一铺设）。

## 二、组件依赖与勾选规则

| 规则 | 实现 |
|---|---|
| HDFS 必选 | 前端勾选框锁定 + 后端 `validateBigdataComponents` 拒绝 |
| HBase → ZooKeeper | 前端勾 HBase 自动带上 ZK；后端依赖校验拒绝缺 ZK 组合 |
| Trino → Hive | 前端勾 Trino 自动带上 Hive；后端依赖校验拒绝缺 Hive 组合 |
| 被依赖组件不可取消 | 前端提示"正被其他已选组件依赖" |

## 三、拓扑与角色分派

| 组件 | 主节点 | 工作节点 |
|---|---|---|
| HDFS | NameNode | DataNode |
| ZooKeeper | ensemble 全体节点（ZOO_MY_ID 按节点序号） | — |
| YARN | ResourceManager（8088 UI） | NodeManager（8042 UI） |
| Spark | Master（7077 / 8080 UI） | Worker 自动注册 |
| Flink | JobManager | TaskManager 自动注册 |
| Hive | Metastore + HiveServer2（仅主节点） | — |
| HBase | HMaster（16010 UI） | RegionServer（16030 UI） |
| Trino | Coordinator（8080 UI） | Worker（discovery 自动注册） |

- 全部 **host 网络**；compose 幂等（同名非 compose 旧容器先迁移）；`/dev/tcp` 宿主侧健康检查；
- 脚本内部署顺序：ZooKeeper → HDFS → YARN → Spark → Flink → Hive → HBase → Trino；
- bootstrap 在主节点执行：等 DataNode 注册 → 集群报告 → 建 `/apps /data`（勾 Hive 建 `/apps/hive/warehouse`，勾 HBase 建 `/hbase`）。

## 四、目录与端口约定

| 项 | 约定 |
|---|---|
| 服务主目录 | `{home_dir}`（默认 `/data/bigdata`），下按组件分子目录：`zookeeper/ hadoop/ yarn/ spark/ flink/ hive/ hbase/ trino/` |
| hadoop | `conf/*.xml`（core-site/hdfs-site）、`data/namenode|datanode` |
| zookeeper | `data/`（myid+快照）、`datalog/` |
| yarn | `conf/yarn-site.xml`（复用 hadoop 的 core-site） |
| hive | `conf/hive-site.xml`、`data/derby`（元数据）、`data/warehouse` |
| hbase | `conf/hbase-site.xml`、`data/` |
| trino | `etc/`（node/jvm/config/catalog 全套）、`data/`（uid 1000 可写） |
| 端口 | NN 9000/9870、ZK 2181/2888/3888、YARN 8032/8088/8042、Spark 7077/8080/8081、Flink 6123/8081、Hive 9083/10000/10002、HBase 16000-16030、Trino 8080 |

端口冲突提醒：Trino HTTP 默认 8080 与 Spark Master UI 默认 8080 相同，若同时勾选两者请在参数里改开一个。

## 五、关键实现点

1. **ZooKeeper**：官方镜像 `ZOO_MY_ID`（取自 `{{__seq}}` 与 `{{__node_ips}}` 匹配）+ `ZOO_SERVERS`（`server.N=ip:2888:3888`）组建 ensemble；`ZOO_ADMINSERVER_ENABLED=false` 避开 8080。
2. **YARN**：同 hadoop 镜像，`command: ["yarn","resourcemanager"/"nodemanager"]`；`yarn-site.xml` 资产注入（RM hostname、NM 可用内存/核数可调 `nm_mem/nm_vcores`）；挂 core-site 保证作业能找到 HDFS。
3. **HBase**：`apache/hbase` 官方镜像入口为角色参数 `["master"]/["regionserver"]`；`hbase-site.xml` 注入 `hbase.rootdir=hdfs://{{__master_ip}}:{{nn_rpc_port}}/hbase`、`hbase.zookeeper.quorum={{__node_ips}}`、`hbase.*.hostname={{__ip}}`（host 网络下强制广播真实 IP）。
4. **Trino**：每节点生成 `etc/` 全套（node.id=trino-{IP}、jvm `-Xmx{{trino_mem}}`、coordinator/worker 两套 config.properties）；`catalog/hive.properties` 指向 `thrift://{{__master_ip}}:9083`，直接查 Hive/HDFS 数据；data 目录 chown 1000:1000。
5. **服务注册**：引擎 `registerBigdataServices` 按 components + role 自动登记全部入口（NameNode UI / ZK / RM UI / Spark UI / Flink UI / HiveServer2+Metastore / HMaster / Trino）。

## 六、验证

`bash -n` ×2 脚本；`script/check_bigdata_tpl.py` 渲染校验（4 种组合 × master/worker 的全部 heredoc、依赖链组合、bootstrap 3 变体、5 个 XML、8 组件分支覆盖）；`node --check`；`go build` + `go vet`。
