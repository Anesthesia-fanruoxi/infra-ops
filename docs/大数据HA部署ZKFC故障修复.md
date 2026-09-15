# 大数据底座 HA 部署：ZKFC 未初始化导致 NameNode 双双 standby

> 触发场景：2026-09-14 12:32 的 bigdata `cluster` 首次部署（`stack_runs.id=75`）失败。
> 现象是 3.40 上 `hive-hiveserver2` 的 10000 端口 7 分钟未就绪，真正的原因在 HDFS 选主。
> 本文记录根因链、修复与验证方式。

## 1. 现象与初步定位

| 观察点 | 事实 |
|---|---|
| 引擎侧 | run 75（bigdata/cluster/create）5 台主机中 seq=1（`192.168.3.40`）失败，`节点部署失败: exit: Process exited with status 1` |
| Hive 阶段 | `HiveServer2 端口 10000 未就绪`，`hive-hiveserver2: Up 7 minutes`，宿主机无 10000 监听 |
| 容器 stdout | `Starting HiveServer2` 之后**每 3–4 分钟**一个 `Hive Session ID`，一小时共 17 条，始终未监听 |
| 容器内 `/tmp/hive/hive.log` | `StandbyException: Operation category READ is not supported in state standby`，`invoking getFileInfo over /192.168.3.40:9000 after 8 failover attempts`、随后 `:9000 after 11 failover attempts` |

**方向**：不是 Hive 的问题，而是 HDFS 两个 NameNode **都停在 standby**，客户端在两侧来回 failover。

## 2. 根因链（每一环都有现场证据）

1. `hdfs zkfc -formatZK` 在 `12:33:06` 失败。留档日志 `/tmp/nn1_zkfc_1750405.log`：
   `ClientCnxn$SessionTimeoutException: Client session timed out, have not heard from server in 10177ms`，
   `session id = 0x0`（会话**从未建立**），`Connection timed out: couldn't connect to ZooKeeper in 10000 milliseconds`。
2. 脚本把这次失败**降级成告警**（旧 `ha.sh`）：
   `[HA-HDFS] formatZK 未完全成功，继续（依赖后续选主）`。
3. ZK 里 `/hadoop-ha` 从未创建：`zkCli ls /hadoop-ha` → `Node does not exist`。
4. `hadoop-zkfc` 容器因此崩溃循环：每次启动即
   `Unable to start failover controller. ... Parent znode does not exist. Run with -formatZK flag to initialize ZooKeeper.`，
   exit code `3`，`docker inspect` → `restarts=117`，状态 `Restarting`。
5. 两侧 NameNode 没有选主执行者 → 都停在 standby（第 1 节 Hive 日志即其下游表现）。

**为什么只有这一次撞上**：run 75 是 create + reset，reset 删掉了 ZK 数据目录，5 节点 ensemble 从零重建。
- ZK 容器 `12:32:38` Started，`12:32:42` 各节点报「就绪」，**`12:32:54` 就开跑 formatZK** —— 距选主完成仅 12 秒，
  且宿主机同时还在拉镜像、起 journalnode/namenode。
- 而旧 `wait_zk` 只探 `2181` 的 **TCP 连通**，不代表 ensemble 已形成 quorum、能建会话；
  `ha.zookeeper.session-timeout` 默认仅 10000ms，窗口极窄。
- 以往的 reinstall / 增量部署因为 ZK 早已稳定运行，从未撞上这个窗口 —— 所以它一直潜伏。

**对照组**（09-11 成功那次 `/tmp/nn1_zkfc_1178482.log`，同机同镜像）：
`Socket connection established ... Session establishment complete ... session id = 0x300098986230000`
→ `ActiveStandbyElector:385 Successfully created /hadoop-ha/ns1 in ZK.`，全部在同一秒完成。

**附带缺陷（同一次部署实测）**：镜像 entrypoint 对 `metastore` 与 `hiveserver2` **都会**跑 schematool，
3–4 个容器同时 `-initSchema` 打同一个 MySQL 会互抢，输的一方报
`Error: Table 'CTLGS' already exists (state=42S01,code=1050)` → `exit 1` → 靠 `restart: always` 恢复。
schema 最终正确，但白丢一轮重启。（此项**不是**本次失败的原因。）

## 3. 修复（`store/stacks/bigdata/scripts/{ha.sh,node.sh}`）

| # | 位置 | 改动 |
|---|---|---|
| 1 | `ha.sh` `wait_zk` | 判据从「2181 TCP 通」改为「**多数派参与 + 已选出 leader**」：新增 `zk_state()` 用 4lw `mntr` 逐节点读 `zk_server_state`（compose 已把 `mntr` 放进 `ZOO_4LW_COMMANDS_WHITELIST`），上限 180s |
| 2 | `ha.sh` NN1 分支 | formatZK 前**先 `wait_zk`**（原来 NN1 完全没等过 ZK，只有 NN2 等） |
| 3 | `ha.sh` 新增 `ensure_hadoop_ha_znode()` | formatZK 有界重试 **12×20s**，以日志含 `Successfully created /hadoop-ha` 为**成功判据**，连续失败即 `exit 1` **中止部署**（不再降级告警） |
| 4 | `ha.sh` NN/ZKFC 启动段 | 启动后校验 ZKFC 处于 `running` 且 `RestartCount < 3`，否则打印其日志并中止 —— 把「7 分钟后在 Hive 阶段暴露」提前到 HDFS boot 阶段 |
| 5 | `node.sh` `wait_port` 失败诊断 | 额外打印**容器内** `/tmp/hive/hive.log` 尾部（stdout 只有 STARTUP_MSG 与周期 Session ID，根因在内部日志） |
| 6 | `ha.sh` `deploy_hive_ha` | 只保留 MS1 的 metastore 作为唯一 schema 初始化入口（其余容器 `SKIP_SCHEMA_INIT: "true"`）；MS2 主机先等 MS1 的 `9083` 就绪（并对 MS1/MS2 同机的退化配置加了 `! ha_is "${MS1}"` 防自等） |

## 4. 验证

| 项 | 方式 | 结果 |
|---|---|---|
| 语法 | `bash -n`（ha.sh / bootstrap.sh / 注入后的合并形态） | 三份均 rc=0 |
| `wait_zk` 行为 | 本地假 ZK 端点 4 场景 | 健康（leader）→ 通过；仅 follower（无 leader）→ 失败；**TCP 可连但不回数据**→ 失败；无监听 → 失败 |
| **反向验证** | 同一「TCP 可连、会话建不起来」场景下对比新旧实现 | 旧实现 `exit=0`（**误判为就绪**，放行 formatZK，正是 run 75 的成因）；新实现 `exit=1`（拦住） |
| `ensure_hadoop_ha_znode` | 打桩 `docker`/`sleep` | 一次成功 → rc=0、docker 调用 1 次；前两次失败 → rc=0、调用 3 次（重试生效）；始终失败 → rc=1、调用 12 次且调用方 `\|\| exit 1` 真的中止 |
| 门禁 | `go build/vet/test ./... -count=1` + `check_stack_lines.py --targets` | 全绿（9 包 ok，0 FAIL） |
| 渲染基线 | 仅 `baseline/render/bigdata__cluster__node.txt` 变化（+102/−22，1754→1834 行），已按修复内容刷新 | 差异逐条对应上表 6 处改动，无其它文件变动 |

## 5. 影响面

- 只影响 bigdata 套件 `ha=true` 的部署（`ha.sh` 仅注入 `node.sh`）；`bootstrap.sh`、其余 8 个套件、前端均未触碰。
- 脚本经 `go:embed` 进 `infra-ops-server.exe`，**必须重建二进制**才生效（已重建：旧包内 `formatZK 未完全成功` 计数 1 → 新包 0，`ensure_hadoop_ha_znode` 计数 0 → 3）。

## 6. 遗留

1. **需重跑一次 bigdata create 才能真机验证**（真机部署按约定由页面发起）。
2. 现有集群仍处于坏状态（`hadoop-zkfc` 崩溃循环、两侧 NN standby）。即使不改脚本，在 `192.168.3.40` 上补跑一次 formatZK 即可恢复（幂等、`-force` 会重建 `/hadoop-ha`）：

   ```bash
   docker run --network host --rm \
     -v /data/bigdata/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml \
     -v /data/bigdata/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml \
     -e HADOOP_OPTS="-Ddfs.ha.namenode.id=nn1" \
     192.168.7.13:5000/apache/hadoop:3.3.6 hdfs zkfc -formatZK -force -nonInteractive
   ```

   随后 `docker restart hadoop-zkfc hadoop-zkfc2`（在 3.40 / 3.41 各自执行），ZKFC 即可选出 active。
3. `ha.sh` 里 `migrate_old "${C}"` 在 `for` 循环**之后**，只对最后一个容器生效（历史遗留）；
   本次未改动，重装路径如需更严格可在后续修复中移入循环。

## 7. 第二轮故障（run 76，同日）：wait_zk 探测集死锁 —— 第一轮修复引入的回归

### 7.1 现象

run 76（14:54 reinstall，5 台）：reset、ZooKeeper 阶段全部正常（5 台 myid=1..5 各自报 Mode），但 3.40（nn1）与 3.41（nn2）在 hdfs boot 阶段双双中止：

```
[HA] 等待 ZooKeeper ensemble（192.168.3.40,192.168.3.41,192.168.3.42，需 ≥2/3 参与且已选出 leader）...
[HA] ZooKeeper ensemble 未就绪（参与 3/3，需 ≥2 且含 leader；...）
[HA-HDFS] ZooKeeper 未就绪，中止（formatZK 无法建立会话）
```

### 7.2 根因：部署拓扑与 HA 判据的探测集不一致

- **部署侧（node.sh deploy_zookeeper）**：ZOO_SERVERS 由 `{{__node_ips}}`（**全体节点**）生成 —— zookeeper 在**全部 5 台**组建 ensemble（设计如此，node.sh:177 注释「全部节点组建 ensemble」）。
- **HA 侧（ha.sh）**：`ZK_IPS={{__zk_ips}}` 只是用户勾选的 **客户端连接串**（≥3 台子集，本例 40/41/42）。
- 第一轮修复给 wait_zk 加了「必须观测到 leader」判据，但探测集用的是 ZK_IPS。本次 ensemble 的 leader 落在 **myid=5（192.168.3.44）**，不在 ZK_IPS 内 → 40/41/42 恒为 follower → 「见 leader」永假 → 180s 超时死锁。
- 「参与 3/3」反而证明 mntr 工作正常（三台都返回了可解析的 zk_server_state）—— 与第一轮的 mntr 白名单疑虑无关。

### 7.3 修复（ha.sh）

1. 新增 `ZK_ALL_IPS={{__node_ips}}`（ensemble 全集，stackkit.ClusterExtra 通用变量，全体主机 IP）；
2. `wait_zk` 探测集改用 ZK_ALL_IPS：NEED=全集多数派，判据「≥NEED 完成选主（leader/follower）且可见 leader」—— 全集必然覆盖 leader；若未来部署侧改为「只装勾选的 ZK」，探测失败节点不计入，判据依然自洽（双向兼容）；
3. 客户端连接串 ZK_IPS 保持不变（spark.deploy.zookeeper.url / high-availability.zookeeper.quorum 继续用子集，合法）。

### 7.4 验证

- bash -n：ha.sh 与 @@HA_SH@@ 注入后的合并形态均通过；
- 行为级双向 harness（本地假 ZK 复刻 run 76 拓扑：5 端点、leader 在第 5 个、连接串仅前 3 个）：新 wait_zk PASS（5/5 完成选主、leader 可见），旧实现 FAIL（如期复现死锁）；
- go test ./store/ ./api/stack/ 全绿；渲染基线 bigdata__cluster__node.txt 已按 SNAPSHOT_WRITE 重录。

### 7.5 连带的前端交互修正

角色计划预览从「第三步点开始部署的弹框」前移到「向导第二步」：通用套件在主机列表下新增预览块（勾选后防抖自动刷新，带「生成预览/刷新」按钮），Docker 前置检查弹框回归纯 Docker 检查（删除内嵌预览块）；bigdata 第二步的预览按钮保持原位。模板基线 _expected_template.txt 与行数基线 metrics.json 已同步重录（差异来源见 _note）。

## 8. 遗留（更新）

- 第 6 节遗留 1 仍有效：需再发起一次 bigdata 部署做真机验证（本轮修复后 run 76 的两台失败机会在 reset 后重走全流程）。
- 第 6 节遗留 2/3 不变。
- 参数语义提示：前端「Ensemble 节点」勾选的 zookeeper_ips 实际只用作**客户端连接串**，ensemble 实际按全体节点组建 —— 语义不一致已向用户报告，暂不改产品行为。
