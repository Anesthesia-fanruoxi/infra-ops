#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

# ============ 公共变量 ============
COMPONENTS="{{components}}"   # 逗号分隔子集: hdfs,spark,flink,hive（hdfs 必选）
ROLE="{{__role}}"
SELF_IP="{{__ip}}"
MASTER_IP="{{__master_ip}}"
NN_RPC_PORT="{{nn_rpc_port}}"
REPLICATION="{{replication}}"
SPARK_MASTER_PORT="{{master_port}}"
SPARK_WEBUI_PORT="{{webui_port}}"
SPARK_WORKER_CORES="{{worker_cores}}"
SPARK_WORKER_MEM="{{worker_mem}}"
JM_RPC_PORT="{{jm_rpc_port}}"
TM_SLOTS="{{tm_slots}}"
NM_MEM="{{nm_mem}}"
NM_VCORES="{{nm_vcores}}"
TRINO_HTTP_PORT="{{trino_http_port}}"
TRINO_MEM="{{trino_mem}}"
HOME_DIR="{{home_dir}}"

has() { case ",${COMPONENTS}," in *",$1,"*) return 0;; *) return 1;; esac; }

[ -n "${COMPONENTS}" ] || { echo "部署组件为空"; exit 1; }
has hdfs || { echo "HDFS 为必选组件（大数据底座的存储基座）"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${MASTER_IP}" ] || { echo "主节点 IP 为空（须指定主节点）"; exit 1; }
NN_RPC_PORT=${NN_RPC_PORT:-9000}
REPLICATION=${REPLICATION:-2}
SPARK_MASTER_PORT=${SPARK_MASTER_PORT:-7077}
SPARK_WEBUI_PORT=${SPARK_WEBUI_PORT:-8080}
SPARK_WORKER_CORES=${SPARK_WORKER_CORES:-4}
SPARK_WORKER_MEM=${SPARK_WORKER_MEM:-8g}
JM_RPC_PORT=${JM_RPC_PORT:-6123}
TM_SLOTS=${TM_SLOTS:-4}
NM_MEM=${NM_MEM:-8192}
NM_VCORES=${NM_VCORES:-4}
TRINO_HTTP_PORT=${TRINO_HTTP_PORT:-8080}
TRINO_MEM=${TRINO_MEM:-4G}
umask 022

# 同名旧容器迁移：非 compose 管理的先移除（幂等）
migrate_old() {
  local C="$1"
  if docker ps -a --format '{{.Names}}' | grep -qx "${C}"; then
    local PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${C}" 2>/dev/null || true)"
    if [ -z "${PROJ}" ]; then
      echo "检测到同名旧容器（非 compose 管理），移除后重建: ${C}"
      docker rm -f "${C}" &>/dev/null || true
    fi
  fi
}

wait_port() { # wait_port <端口> <次数> <容器名> <角色中文名>
  local PORT="$1" TIMES="$2" C="$3" NAME="$4" READY=0
  for _ in $(seq 1 "${TIMES}"); do
    if (echo > "/dev/tcp/127.0.0.1/${PORT}") >/dev/null 2>&1; then READY=1; break; fi
    sleep 3
  done
  if [ "${READY}" != "1" ]; then
    echo "${NAME} 端口 ${PORT} 未就绪，最近日志:"
    docker logs "${C}" --tail 30 2>&1 || true
    exit 1
  fi
}

echo "=== 大数据底座节点部署: 组件[${COMPONENTS}] 角色[${ROLE}] 本机 ${SELF_IP} ==="

# ============ ZooKeeper（可选，全部节点组建 ensemble） ============
if has zookeeper; then
  ZK_HOME="${HOME_DIR}/zookeeper"
  IMAGE_ZK="{{image_zookeeper}}"
  mkdir -p "${ZK_HOME}/data" "${ZK_HOME}/datalog"
  # 由全体节点 IP 生成 ZOO_SERVERS（server.N=ip:2888:3888），本机序号即 ZOO_MY_ID
  ZK_SERVERS="" ; ZK_MY_ID="{{__seq}}" ; ZK_ID=1
  IFS=',' read -ra ZK_IPS <<< "{{__node_ips}}"
  for zkip in "${ZK_IPS[@]}"; do
    ZK_SERVERS="${ZK_SERVERS}server.${ZK_ID}=${zkip}:2888:3888 "
    if [ "${zkip}" = "${SELF_IP}" ]; then ZK_MY_ID="${ZK_ID}"; fi
    ZK_ID=$((ZK_ID+1))
  done
  cat > "${ZK_HOME}/compose.yml" <<YAMLEOF
name: bigdata-zookeeper
services:
  zookeeper:
    image: {{image_zookeeper}}
    container_name: bigdata-zookeeper
    restart: always
    network_mode: host
    environment:
      ZOO_MY_ID: "${ZK_MY_ID}"
      ZOO_SERVERS: "${ZK_SERVERS}"
      ZOO_ADMINSERVER_ENABLED: "false"
    volumes:
      - {{home_dir}}/zookeeper/data:/data
      - {{home_dir}}/zookeeper/datalog:/datalog
YAMLEOF
  migrate_old "bigdata-zookeeper"
  echo "[ZooKeeper] 拉取镜像 ${IMAGE_ZK}"
  docker compose -f "${ZK_HOME}/compose.yml" pull
  docker compose -f "${ZK_HOME}/compose.yml" up -d
  wait_port 2181 30 "bigdata-zookeeper" "ZooKeeper"
  echo "[ZooKeeper] 已就绪（myid=${ZK_MY_ID}）"
fi

# ============ HDFS（必选） ============
if has hdfs; then
  HADOOP_HOME="${HOME_DIR}/hadoop"
  IMAGE_H="{{image}}"
  mkdir -p "${HADOOP_HOME}/conf" "${HADOOP_HOME}/data"
  cat > "${HADOOP_HOME}/conf/core-site.xml" <<'XMLEOF'
@@CORE_SITE@@
XMLEOF
  cat > "${HADOOP_HOME}/conf/hdfs-site.xml" <<'XMLEOF'
@@HDFS_SITE@@
XMLEOF

  if [ "${ROLE}" = "master" ]; then
    C_HDFS=hadoop-namenode; PORT_HDFS=9870; CN_HDFS=NameNode
    mkdir -p "${HADOOP_HOME}/data/namenode"
    cat > "${HADOOP_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-hadoop
services:
  namenode:
    image: {{image}}
    container_name: hadoop-namenode
    restart: always
    network_mode: host
    command: ["hdfs", "namenode"]
    environment:
      ENSURE_NAMENODE_DIR: /hadoop/dfs/name
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - {{home_dir}}/hadoop/data/namenode:/hadoop/dfs/name
YAMLEOF
  else
    C_HDFS=hadoop-datanode; PORT_HDFS=9866; CN_HDFS=DataNode
    mkdir -p "${HADOOP_HOME}/data/datanode"
    cat > "${HADOOP_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-hadoop
services:
  datanode:
    image: {{image}}
    container_name: hadoop-datanode
    restart: always
    network_mode: host
    command: ["hdfs", "datanode"]
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - {{home_dir}}/hadoop/data/datanode:/hadoop/dfs/data
YAMLEOF
  fi
  migrate_old "${C_HDFS}"
  echo "[HDFS] 拉取镜像 ${IMAGE_H}"
  docker compose -f "${HADOOP_HOME}/compose.yml" pull
  docker compose -f "${HADOOP_HOME}/compose.yml" up -d
  wait_port "${PORT_HDFS}" 30 "${C_HDFS}" "HDFS ${CN_HDFS}"
  echo "[HDFS] ${CN_HDFS} 已就绪"
fi

# ============ Spark（可选） ============
if has spark; then
  SPARK_HOME="${HOME_DIR}/spark"
  IMAGE_S="{{image_spark}}"
  mkdir -p "${SPARK_HOME}/data"
  if [ "${ROLE}" = "master" ]; then
    C_SPARK=spark-master; PORT_SPARK="${SPARK_WEBUI_PORT}"; CN_SPARK=Master
    cat > "${SPARK_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-spark
services:
  master:
    image: {{image_spark}}
    container_name: spark-master
    restart: always
    network_mode: host
    user: root
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.master.Master", "--host", "{{__ip}}", "--port", "{{master_port}}", "--webui-port", "{{webui_port}}"]
    volumes:
      - {{home_dir}}/spark/data:/opt/spark/work-dir
YAMLEOF
  else
    C_SPARK=spark-worker; PORT_SPARK=8081; CN_SPARK=Worker
    cat > "${SPARK_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-spark
services:
  worker:
    image: {{image_spark}}
    container_name: spark-worker
    restart: always
    network_mode: host
    user: root
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.worker.Worker", "spark://{{__master_ip}}:{{master_port}}", "--host", "{{__ip}}", "--webui-port", "8081"]
    environment:
      SPARK_WORKER_CORES: "{{worker_cores}}"
      SPARK_WORKER_MEMORY: "{{worker_mem}}"
    volumes:
      - {{home_dir}}/spark/data:/opt/spark/work-dir
YAMLEOF
  fi
  migrate_old "${C_SPARK}"
  echo "[Spark] 拉取镜像 ${IMAGE_S}"
  docker compose -f "${SPARK_HOME}/compose.yml" pull
  docker compose -f "${SPARK_HOME}/compose.yml" up -d
  wait_port "${PORT_SPARK}" 20 "${C_SPARK}" "Spark ${CN_SPARK}"
  if [ "${ROLE}" != "master" ]; then
    for _ in $(seq 1 10); do
      if docker logs "${C_SPARK}" 2>&1 | grep -q "Successfully registered with master"; then
        echo "[Spark] Worker 已注册到 spark://${MASTER_IP}:${SPARK_MASTER_PORT}"
        break
      fi
      sleep 3
    done
  fi
  echo "[Spark] ${CN_SPARK} 已就绪"
fi

# ============ Flink（可选） ============
if has flink; then
  FLINK_HOME="${HOME_DIR}/flink"
  IMAGE_F="{{image_flink}}"
  mkdir -p "${FLINK_HOME}"
  if [ "${ROLE}" = "master" ]; then
    C_FLINK=flink-jobmanager; CN_FLINK=JobManager
    cat > "${FLINK_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-flink
services:
  jobmanager:
    image: {{image_flink}}
    container_name: flink-jobmanager
    restart: always
    network_mode: host
    command: jobmanager
    environment:
      FLINK_PROPERTIES: |
        jobmanager.rpc.address: {{__ip}}
        jobmanager.rpc.port: {{jm_rpc_port}}
        jobmanager.bind-host: 0.0.0.0
        rest.address: {{__ip}}
        rest.bind-address: 0.0.0.0
        rest.port: 8081
YAMLEOF
  else
    C_FLINK=flink-taskmanager; CN_FLINK=TaskManager
    cat > "${FLINK_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-flink
services:
  taskmanager:
    image: {{image_flink}}
    container_name: flink-taskmanager
    restart: always
    network_mode: host
    command: taskmanager
    environment:
      FLINK_PROPERTIES: |
        jobmanager.rpc.address: {{__master_ip}}
        jobmanager.rpc.port: {{jm_rpc_port}}
        taskmanager.host: {{__ip}}
        taskmanager.bind-host: 0.0.0.0
        taskmanager.numberOfTaskSlots: {{tm_slots}}
YAMLEOF
  fi
  migrate_old "${C_FLINK}"
  echo "[Flink] 拉取镜像 ${IMAGE_F}"
  docker compose -f "${FLINK_HOME}/compose.yml" pull
  docker compose -f "${FLINK_HOME}/compose.yml" up -d
  if [ "${ROLE}" = "master" ]; then
    wait_port 8081 20 "${C_FLINK}" "Flink JobManager"
  else
    sleep 5
    for _ in $(seq 1 20); do
      if docker logs "${C_FLINK}" 2>&1 | grep -qi "successful registration\|registered at jobmanager"; then
        echo "[Flink] TaskManager 已注册到 JobManager ${MASTER_IP}:${JM_RPC_PORT}"
        break
      fi
      sleep 3
    done
  fi
  echo "[Flink] ${CN_FLINK} 已就绪"
fi

# ============ YARN（可选，主节点 RM / 工作节点 NM） ============
if has yarn; then
  YARN_HOME="${HOME_DIR}/yarn"
  IMAGE_Y="{{image}}"
  mkdir -p "${YARN_HOME}/conf"
  cat > "${YARN_HOME}/conf/yarn-site.xml" <<'XMLEOF'
@@YARN_SITE@@
XMLEOF
  if [ "${ROLE}" = "master" ]; then
    C_YARN=hadoop-resourcemanager; PORT_YARN=8088; CN_YARN=ResourceManager
    cat > "${YARN_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-yarn
services:
  resourcemanager:
    image: {{image}}
    container_name: hadoop-resourcemanager
    restart: always
    network_mode: host
    command: ["yarn", "resourcemanager"]
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/yarn/conf/yarn-site.xml:/opt/hadoop/etc/hadoop/yarn-site.xml
YAMLEOF
  else
    C_YARN=hadoop-nodemanager; PORT_YARN=8042; CN_YARN=NodeManager
    cat > "${YARN_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-yarn
services:
  nodemanager:
    image: {{image}}
    container_name: hadoop-nodemanager
    restart: always
    network_mode: host
    command: ["yarn", "nodemanager"]
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/yarn/conf/yarn-site.xml:/opt/hadoop/etc/hadoop/yarn-site.xml
YAMLEOF
  fi
  migrate_old "${C_YARN}"
  echo "[YARN] 拉取镜像 ${IMAGE_Y}"
  docker compose -f "${YARN_HOME}/compose.yml" pull
  docker compose -f "${YARN_HOME}/compose.yml" up -d
  wait_port "${PORT_YARN}" 20 "${C_YARN}" "YARN ${CN_YARN}"
  echo "[YARN] ${CN_YARN} 已就绪"
fi

# ============ Hive（可选，仅主节点） ============
if has hive; then
  if [ "${ROLE}" = "master" ]; then
    HIVE_HOME="${HOME_DIR}/hive"
    IMAGE_HV="{{image_hive}}"
    mkdir -p "${HIVE_HOME}/conf" "${HIVE_HOME}/data/derby" "${HIVE_HOME}/data/warehouse"
    cat > "${HIVE_HOME}/conf/hive-site.xml" <<'XMLEOF'
@@HIVE_SITE@@
XMLEOF
    cat > "${HIVE_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-hive
services:
  metastore:
    image: {{image_hive}}
    container_name: hive-metastore
    restart: always
    network_mode: host
    environment:
      SERVICE_NAME: metastore
    volumes:
      - {{home_dir}}/hive/conf/hive-site.xml:/opt/hive/conf/hive-site.xml
      - {{home_dir}}/hive/data/derby:/opt/hive/data/derby
  hiveserver2:
    image: {{image_hive}}
    container_name: hive-hiveserver2
    restart: always
    network_mode: host
    environment:
      SERVICE_NAME: hiveserver2
    depends_on:
      - metastore
    volumes:
      - {{home_dir}}/hive/conf/hive-site.xml:/opt/hive/conf/hive-site.xml
      - {{home_dir}}/hive/data/warehouse:/opt/hive/data/warehouse
YAMLEOF
    migrate_old "hive-metastore"
    migrate_old "hive-hiveserver2"
    echo "[Hive] 拉取镜像 ${IMAGE_HV}"
    docker compose -f "${HIVE_HOME}/compose.yml" pull
    docker compose -f "${HIVE_HOME}/compose.yml" up -d
    echo "[Hive] 等待 Metastore（首次启动需初始化 Derby 元数据库，最长 3 分钟）..."
    wait_port 9083 60 "hive-metastore" "Hive Metastore"
    echo "[Hive] 等待 HiveServer2（最长 4 分钟）..."
    wait_port 10000 80 "hive-hiveserver2" "HiveServer2"
    echo "[Hive] Metastore/HiveServer2 已就绪"
  else
    echo "[Hive] Metastore/HiveServer2 为单实例服务（内置 Derby），仅在主节点部署；本机提交作业用 beeline 连接 ${MASTER_IP}:10000"
  fi
fi

# ============ HBase（可选，主节点 HMaster / 工作节点 RegionServer） ============
if has hbase; then
  HBASE_HOME="${HOME_DIR}/hbase"
  IMAGE_HB="{{image_hbase}}"
  mkdir -p "${HBASE_HOME}/conf" "${HBASE_HOME}/data"
  cat > "${HBASE_HOME}/conf/hbase-site.xml" <<'XMLEOF'
@@HBASE_SITE@@
XMLEOF
  if [ "${ROLE}" = "master" ]; then
    C_HB=hbase-master; PORT_HB=16010; CN_HB=HMaster
    cat > "${HBASE_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-hbase
services:
  master:
    image: {{image_hbase}}
    container_name: hbase-master
    restart: always
    network_mode: host
    command: ["master"]
    volumes:
      - {{home_dir}}/hbase/conf/hbase-site.xml:/opt/hbase/conf/hbase-site.xml
      - {{home_dir}}/hbase/data:/opt/hbase/data
YAMLEOF
  else
    C_HB=hbase-regionserver; PORT_HB=16030; CN_HB=RegionServer
    cat > "${HBASE_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-hbase
services:
  regionserver:
    image: {{image_hbase}}
    container_name: hbase-regionserver
    restart: always
    network_mode: host
    command: ["regionserver"]
    volumes:
      - {{home_dir}}/hbase/conf/hbase-site.xml:/opt/hbase/conf/hbase-site.xml
      - {{home_dir}}/hbase/data:/opt/hbase/data
YAMLEOF
  fi
  migrate_old "${C_HB}"
  echo "[HBase] 拉取镜像 ${IMAGE_HB}"
  docker compose -f "${HBASE_HOME}/compose.yml" pull
  docker compose -f "${HBASE_HOME}/compose.yml" up -d
  wait_port "${PORT_HB}" 40 "${C_HB}" "HBase ${CN_HB}"
  echo "[HBase] ${CN_HB} 已就绪"
fi

# ============ Trino（可选，主节点 coordinator / 工作节点 worker） ============
if has trino; then
  TRINO_HOME="${HOME_DIR}/trino"
  IMAGE_T="{{image_trino}}"
  mkdir -p "${TRINO_HOME}/etc/catalog" "${TRINO_HOME}/data"
  # Trino 容器以 uid 1000 运行，数据目录需可写
  chown -R 1000:1000 "${TRINO_HOME}/data" 2>/dev/null || true
  cat > "${TRINO_HOME}/etc/node.properties" <<PROPSEOF
node.environment=bigdata
node.id=trino-${SELF_IP}
node.data-dir=/data
PROPSEOF
  cat > "${TRINO_HOME}/etc/jvm.config" <<JVMEOF
-server
-Xmx${TRINO_MEM}
-XX:+UseG1GC
-XX:G1HeapRegionSize=32M
-XX:+ExplicitGCInvokesConcurrent
-XX:+ExitOnOutOfMemoryError
-XX:-UseBiasedLocking
-XX:ReservedCodeCacheSize=512M
JVMEOF
  if [ "${ROLE}" = "master" ]; then
    cat > "${TRINO_HOME}/etc/config.properties" <<CFGEOF
coordinator=true
node-scheduler.include-coordinator=true
http-server.http.port=${TRINO_HTTP_PORT}
discovery-server.enabled=true
discovery.uri=http://${SELF_IP}:${TRINO_HTTP_PORT}
CFGEOF
    C_TRINO=trino-coordinator; CN_TRINO=Coordinator
  else
    cat > "${TRINO_HOME}/etc/config.properties" <<CFGEOF
coordinator=false
http-server.http.port=${TRINO_HTTP_PORT}
discovery.uri=http://${MASTER_IP}:${TRINO_HTTP_PORT}
CFGEOF
    C_TRINO=trino-worker; CN_TRINO=Worker
  fi
  cat > "${TRINO_HOME}/etc/catalog/hive.properties" <<CATEOF
connector.name=hive
hive.metastore.uri=thrift://${MASTER_IP}:9083
CATEOF
  cat > "${TRINO_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-trino
services:
  trino:
    image: {{image_trino}}
    container_name: trino-node
    restart: always
    network_mode: host
    user: "1000:1000"
    volumes:
      - {{home_dir}}/trino/etc:/etc/trino
      - {{home_dir}}/trino/data:/data
YAMLEOF
  migrate_old "trino-node"
  echo "[Trino] 拉取镜像 ${IMAGE_T}"
  docker compose -f "${TRINO_HOME}/compose.yml" pull
  docker compose -f "${TRINO_HOME}/compose.yml" up -d
  wait_port "${TRINO_HTTP_PORT}" 40 "trino-node" "Trino ${CN_TRINO}"
  echo "[Trino] ${CN_TRINO} 已就绪（hive catalog -> ${MASTER_IP}:9083）"
fi

# ============ 汇总 ============
echo "=== 节点部署完成: ${SELF_IP}（角色 ${ROLE}）==="
if [ -d "${HOME_DIR}/zookeeper" ]; then echo "  zk     : ${HOME_DIR}/zookeeper/compose.yml"; fi
if [ -d "${HOME_DIR}/hadoop" ]; then echo "  hadoop : ${HOME_DIR}/hadoop/compose.yml"; fi
if [ -d "${HOME_DIR}/yarn" ]; then echo "  yarn   : ${HOME_DIR}/yarn/compose.yml"; fi
if [ -d "${HOME_DIR}/spark" ]; then echo "  spark  : ${HOME_DIR}/spark/compose.yml"; fi
if [ -d "${HOME_DIR}/flink" ]; then echo "  flink  : ${HOME_DIR}/flink/compose.yml"; fi
if [ -d "${HOME_DIR}/hive" ]; then echo "  hive   : ${HOME_DIR}/hive/compose.yml"; fi
if [ -d "${HOME_DIR}/hbase" ]; then echo "  hbase  : ${HOME_DIR}/hbase/compose.yml"; fi
if [ -d "${HOME_DIR}/trino" ]; then echo "  trino  : ${HOME_DIR}/trino/compose.yml"; fi
if [ "${ROLE}" = "master" ]; then
  if has zookeeper; then echo "  ZooKeeper   : ${SELF_IP}:2181（ensemble 全体节点）"; fi
  if has hdfs; then echo "  HDFS WebUI  : http://${SELF_IP}:9870（fs.defaultFS=hdfs://${MASTER_IP}:${NN_RPC_PORT}）"; fi
  if has yarn; then echo "  YARN RM UI  : http://${SELF_IP}:8088（提交作业用 yarn jar）"; fi
  if has spark; then echo "  Spark UI    : http://${SELF_IP}:${SPARK_WEBUI_PORT}（接入 spark://${SELF_IP}:${SPARK_MASTER_PORT}）"; fi
  if has flink; then echo "  Flink UI    : http://${SELF_IP}:8081"; fi
  if has hive; then echo "  Hive        : jdbc:hive2://${SELF_IP}:10000（beeline -u ... -n hive）"; fi
  if has hbase; then echo "  HBase UI    : http://${SELF_IP}:16010（shell: docker exec -it hbase-master hbase shell）"; fi
  if has trino; then echo "  Trino UI    : http://${SELF_IP}:${TRINO_HTTP_PORT}（CLI: trino --server ${SELF_IP}:${TRINO_HTTP_PORT}）"; fi
fi
exit 0
