#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }

COMPONENTS="{{components}}"
has() { case ",${COMPONENTS}," in *",$1,"*) return 0;; *) return 1;; esac; }
NN_RPC_PORT="{{nn_rpc_port}}"
NN_RPC_PORT=${NN_RPC_PORT:-9000}
MASTER_IP="{{__master_ip}}"
HA="{{ha}}"
# 各组件主角色所在主机（角色规划；未规划时与主节点同机）
NN_IP="{{__master_hdfs}}"
RM_IP="{{__master_yarn}}"
SPARK_MASTER_IP="{{__master_spark}}"
JM_IP="{{__master_flink}}"
HIVE_IP="{{__master_hive}}"
HMASTER_IP="{{__master_hbase}}"
TRINO_COORD_IP="{{__master_trino}}"

if ! docker ps --format '{{.Names}}' | grep -qx "hadoop-namenode"; then
  echo "未找到运行中的 hadoop-namenode 容器"; exit 1
fi

# HA 双 NameNode 场景下，若 active 尚未选出，dfsadmin/客户端经 nameservice 连接会长时间挂起，
# 进而拖垮整个 bootstrap 的 SSH 超时。先等待任一 NN 进入 active（有界），并给所有 hdfs 命令加 timeout。
wait_active_nn() {
  echo "等待 active NameNode 选出（最长 3 分钟）..."
  for _ in $(seq 1 60); do
    ST=$(timeout 20 docker exec hadoop-namenode hdfs haadmin -getAllServiceState 2>/dev/null || true)
    if echo "${ST}" | grep -qi active; then
      echo "active NameNode 已就绪：$(echo "${ST}" | tr '\n' ' ')"
      return 0
    fi
    sleep 3
  done
  echo "仍未见 active NameNode（当前：$(echo "${ST}" | tr '\n' ' ' | cut -c1-120)），继续尝试，可能影响后续操作"
  return 1
}
if [ "${HA}" = "true" ]; then
  wait_active_nn || true
fi

echo "等待 DataNode 注册（最长 2 分钟）..."
for _ in $(seq 1 40); do
  REPORT=$(timeout 30 docker exec hadoop-namenode hdfs dfsadmin -report 2>/dev/null || true)
  if echo "${REPORT}" | grep -q "Live datanodes ([1-9]"; then
    echo "DataNode 已注册"
    break
  fi
  sleep 3
done

echo "=== HDFS 集群报告 ==="
timeout 30 docker exec hadoop-namenode hdfs dfsadmin -report 2>/dev/null | head -30 || true

echo "初始化常用目录（幂等）..."
for d in /apps /data; do
  timeout 30 docker exec hadoop-namenode hdfs dfs -mkdir -p "${d}" 2>/dev/null || true
done

if has hive; then
  echo "初始化 Hive 数仓目录 /apps/hive/warehouse ..."
  timeout 30 docker exec hadoop-namenode hdfs dfs -mkdir -p /apps/hive/warehouse 2>/dev/null || true
  timeout 30 docker exec hadoop-namenode hdfs dfs -chmod -R 777 /apps/hive 2>/dev/null || true
fi

if has hbase; then
  echo "初始化 HBase 根目录 /hbase ..."
  timeout 30 docker exec hadoop-namenode hdfs dfs -mkdir -p /hbase 2>/dev/null || true
  timeout 30 docker exec hadoop-namenode hdfs dfs -chmod 777 /hbase 2>/dev/null || true
fi

timeout 30 docker exec hadoop-namenode hdfs dfs -ls / || true

# HA 模式：校验各组件主备状态（§4.4，非 HA 时跳过）
if [ "${HA}" = "true" ]; then
  echo "=== HDFS HA 状态（haadmin） ==="
  NN1_STATE=$(timeout 30 docker exec hadoop-namenode hdfs haadmin -getAllServiceState 2>/dev/null || true)
  echo "${NN1_STATE}" || true
  if echo "${NN1_STATE}" | grep -qi "standby\|active"; then
    echo "[OK] HDFS HA 双 NameNode 已就绪（nn1/nn2 自动选主中）"
  else
    echo "[WARN] 未检测到 haadmin 状态，请检查 NameNode/JournalNode/zkfc 是否就绪"
  fi
  if has yarn; then
    echo "=== YARN HA 状态（rmadmin） ==="
    timeout 30 docker exec hadoop-resourcemanager yarn rmadmin -getAllServiceState 2>/dev/null || true
  fi
  if has hive; then
    echo "=== Hive HA 服务状态 ==="
    echo "Metastore-1 ${MASTER_IP}:9083 / Metastore-2 及 HS2 双实例由节点脚本部署，接入 jdbc:hive2://zk1,zk2,zk3/;serviceDiscoveryMode=zooKeeper;zooKeeperNamespace=hiveserver2"
  fi
fi

echo "=== 大数据底座初始化完成 ==="
echo "组件: ${COMPONENTS}"
if [ "${HA}" = "true" ]; then
  echo "高可用: 已开启（HDFS QJM 双 NN / YARN 双 RM / Spark-Flink-HBase 双实例 / Hive 双 MS+HS2）"
  echo "HDFS 入口: ${NN_IP}:${NN_RPC_PORT}（nameservice 自动选主）"
else
  echo "fs.defaultFS: hdfs://${NN_IP}:${NN_RPC_PORT}"
fi
if has zookeeper; then echo "ZooKeeper : ensemble 全体节点（如 ${MASTER_IP}:2181）"; fi
if has yarn; then echo "YARN RM   : http://${RM_IP}:8088"; fi
if has spark; then echo "Spark 接入: spark://${SPARK_MASTER_IP}:{{master_port}}"; fi
if has flink; then echo "Flink UI  : http://${JM_IP}:8081"; fi
if has hive; then echo "Hive      : jdbc:hive2://${HIVE_IP}:10000（beeline -u ... -n hive）"; fi
if has hbase; then echo "HBase     : http://${HMASTER_IP}:16010（hbase shell 可用后建表）"; fi
if has trino; then echo "Trino     : http://${TRINO_COORD_IP}:{{trino_http_port}}（trino --server ${TRINO_COORD_IP}:{{trino_http_port}}，catalog=hive）"; fi
exit 0
