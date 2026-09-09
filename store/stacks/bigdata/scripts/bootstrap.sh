#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }

COMPONENTS="{{components}}"
has() { case ",${COMPONENTS}," in *",$1,"*) return 0;; *) return 1;; esac; }
NN_RPC_PORT="{{nn_rpc_port}}"
NN_RPC_PORT=${NN_RPC_PORT:-9000}
MASTER_IP="{{__master_ip}}"

if ! docker ps --format '{{.Names}}' | grep -qx "hadoop-namenode"; then
  echo "未找到运行中的 hadoop-namenode 容器"; exit 1
fi

echo "等待 DataNode 注册（最长 2 分钟）..."
for _ in $(seq 1 40); do
  REPORT=$(docker exec hadoop-namenode hdfs dfsadmin -report 2>/dev/null || true)
  if echo "${REPORT}" | grep -q "Live datanodes ([1-9]"; then
    echo "DataNode 已注册"
    break
  fi
  sleep 3
done

echo "=== HDFS 集群报告 ==="
docker exec hadoop-namenode hdfs dfsadmin -report 2>/dev/null | head -30 || true

echo "初始化常用目录（幂等）..."
for d in /apps /data; do
  docker exec hadoop-namenode hdfs dfs -mkdir -p "${d}" 2>/dev/null || true
done

if has hive; then
  echo "初始化 Hive 数仓目录 /apps/hive/warehouse ..."
  docker exec hadoop-namenode hdfs dfs -mkdir -p /apps/hive/warehouse 2>/dev/null || true
  docker exec hadoop-namenode hdfs dfs -chmod -R 777 /apps/hive 2>/dev/null || true
fi

if has hbase; then
  echo "初始化 HBase 根目录 /hbase ..."
  docker exec hadoop-namenode hdfs dfs -mkdir -p /hbase 2>/dev/null || true
  docker exec hadoop-namenode hdfs dfs -chmod 777 /hbase 2>/dev/null || true
fi

docker exec hadoop-namenode hdfs dfs -ls / || true

echo "=== 大数据底座初始化完成 ==="
echo "组件: ${COMPONENTS}"
echo "fs.defaultFS: hdfs://${MASTER_IP}:${NN_RPC_PORT}"
if has zookeeper; then echo "ZooKeeper : ${MASTER_IP}:2181（ensemble 全体节点）"; fi
if has yarn; then echo "YARN RM   : http://${MASTER_IP}:8088"; fi
if has spark; then echo "Spark 接入: spark://${MASTER_IP}:{{master_port}}"; fi
if has flink; then echo "Flink UI  : http://${MASTER_IP}:8081"; fi
if has hive; then echo "Hive      : jdbc:hive2://${MASTER_IP}:10000（beeline -u ... -n hive）"; fi
if has hbase; then echo "HBase     : http://${MASTER_IP}:16010（hbase shell 可用后建表）"; fi
if has trino; then echo "Trino     : http://${MASTER_IP}:{{trino_http_port}}（trino --server ${MASTER_IP}:{{trino_http_port}}，catalog=hive）"; fi
exit 0
