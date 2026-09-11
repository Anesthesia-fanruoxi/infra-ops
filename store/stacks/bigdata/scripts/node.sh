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
# 各组件主角色所在主机（角色规划；引擎注入 __master_<comp>，未规划时回落主节点）
NN_IP="{{__master_hdfs}}"
RM_IP="{{__master_yarn}}"
SPARK_MASTER_IP="{{__master_spark}}"
JM_IP="{{__master_flink}}"
HIVE_IP="{{__master_hive}}"
HMASTER_IP="{{__master_hbase}}"
TRINO_COORD_IP="{{__master_trino}}"
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
NN_IP=${NN_IP:-${MASTER_IP}}
RM_IP=${RM_IP:-${MASTER_IP}}
SPARK_MASTER_IP=${SPARK_MASTER_IP:-${MASTER_IP}}
JM_IP=${JM_IP:-${MASTER_IP}}
HIVE_IP=${HIVE_IP:-${MASTER_IP}}
HMASTER_IP=${HMASTER_IP:-${MASTER_IP}}
TRINO_COORD_IP=${TRINO_COORD_IP:-${MASTER_IP}}
umask 022

is_master() { [ "${SELF_IP}" = "$1" ]; }

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

# 探测本机端口是否可连：先试 127.0.0.1，再试本机网卡 IP。
# 部分组件（如设置了 yarn.nodemanager.hostname 的 NodeManager）只把 Web 端口绑在具体网卡 IP 上，
# 仅探测 127.0.0.1 会误判为“未就绪”。
_port_ready() { # _port_ready <port>
  (echo > "/dev/tcp/127.0.0.1/$1") >/dev/null 2>&1 && return 0
  (echo > "/dev/tcp/{{__ip}}/$1") >/dev/null 2>&1 && return 0
  return 1
}

wait_port() { # wait_port <端口> <次数> <容器名> <角色中文名>
  local PORT="$1" TIMES="$2" C="$3" NAME="$4" READY=0
  for _ in $(seq 1 "${TIMES}"); do
    if _port_ready "${PORT}"; then READY=1; break; fi
    sleep 3
  done
  if [ "${READY}" != "1" ]; then
    echo "${NAME} 端口 ${PORT} 未就绪，诊断:"
    echo "--- 容器状态 ---"
    docker ps -a --filter "name=${C}" --format '{{.Names}}: {{.Status}}' || true
    echo "--- 宿主机 ${PORT} 监听 ---"
    (ss -ltn 2>/dev/null | grep ":${PORT} ") || echo "宿主机无进程监听 ${PORT}"
    echo "最近日志:"
    docker logs "${C}" --tail 60 2>&1 || true
    # return 而非 exit：函数内 exit 会绕过调用方的 `|| true`（ha.sh 依赖此语义）
    return 1
  fi
}


pull_retry() { # pull_retry <compose.yml>：镜像预拉，失败重试 5 次（递增等待）；仍失败仅告警，交由 up 阶段兜底
  local F="$1" i
  for i in 1 2 3 4 5; do
    if docker compose -f "$F" pull; then return 0; fi
    echo "[Pull] 拉取失败（$i/5），$((i*5))s 后重试..."
    sleep $((i*5))
  done
  echo "[Pull] 预拉仍失败，交由 up 阶段兜底拉取（wait_port 会做最终校验）"
  return 0
}

wait_host() { # wait_host <ip> <端口> <次数> <名>
  local IP="$1" P="$2" T="$3" N="$4" R=0
  for _ in $(seq 1 "${T}"); do
    if (echo > "/dev/tcp/${IP}/${P}") >/dev/null 2>&1; then R=1; break; fi
    sleep 3
  done
  [ "${R}" = "1" ] || { echo "${N} ${IP}:${P} 未就绪"; return 1; }
  return 0
}

# 主脑流程第 1 阶段：清理旧环境残留（容器 + 挂载/配置目录），保证重装不被上次失败污染
run_reset() {
  # 0) 主机名自解析保障：Java 服务（Hive/HS2/Flink entrypoint 等）启动时要 InetAddress.getLocalHost()，
  #    若 hostname 不在 /etc/hosts 会 UnknownHostException 反复重试（幂等追加）
  _HN="$(hostname)"
  if ! grep -Eq "[[:space:]]${_HN}([[:space:]]|$)" /etc/hosts 2>/dev/null; then
    echo "127.0.0.1 ${_HN}" >> /etc/hosts
    echo "[Reset] 已向 /etc/hosts 追加主机名解析（127.0.0.1 ${_HN}）"
  fi
  echo "[Reset] 清理旧环境：停止残留容器并清空挂载/配置目录（${HOME_DIR}）"
  # 1) down 各组件 compose 项目（含 -v 清数据卷；bind-mount 宿主目录由第 3 步删除）
  for _f in "${HOME_DIR}/compose.yml" "${HOME_DIR}/hadoop/compose.yml" "${HOME_DIR}/zookeeper/compose.yml" \
            "${HOME_DIR}/yarn/compose.yml" "${HOME_DIR}/spark/compose.yml" "${HOME_DIR}/flink/compose.yml" \
            "${HOME_DIR}/hive/compose.yml" "${HOME_DIR}/hive/metadb/compose.yml" \
            "${HOME_DIR}/hbase/compose.yml" "${HOME_DIR}/trino/compose.yml"; do
    if [ -f "${_f}" ]; then
      docker compose -f "${_f}" down --remove-orphans -v >/dev/null 2>&1 \
        || docker compose -f "${_f}" down --remove-orphans >/dev/null 2>&1 || true
      echo "[Reset] 已移除 ${_f}"
    fi
  done
  # 2) 兜底：删除上次失败遗留的所有大数据相关容器（含非 compose 管理）
  for _c in $(docker ps -a --format '{{.Names}}' | grep -E '^(bigdata|hadoop|spark|flink|hive|hbase|trino)[_-]' || true); do
    docker rm -f "${_c}" >/dev/null 2>&1 || true
  done
  # 3) 清空组件挂载/配置目录，交由本阶段后的安装逻辑重新生成
  for _d in hadoop zookeeper yarn spark flink hive hbase trino; do
    rm -rf "${HOME_DIR}/${_d}"
  done
  rm -rf "${HOME_DIR}/compose.yml" "${HOME_DIR}/.formatted" 2>/dev/null || true
  mkdir -p "${HOME_DIR}"
  echo "[Reset] 历史残存已清理完毕"
}

# NameNode 活跃后预建公共服务目录（DN 与上层组件启动前完成，保证 hive/hbase 根目录存在）
init_hdfs_dirs() {
  local NN="hadoop-namenode"
  docker exec "${NN}" hdfs dfs -mkdir -p /apps /data 2>/dev/null || true
  docker exec "${NN}" hdfs dfs -chmod -R 777 /apps 2>/dev/null || true
  if has hive; then docker exec "${NN}" hdfs dfs -mkdir -p /apps/hive/warehouse 2>/dev/null || true; fi
  if has hbase; then docker exec "${NN}" hdfs dfs -mkdir -p /hbase 2>/dev/null || true; fi
  if has hbase; then docker exec "${NN}" hdfs dfs -chmod 777 /hbase 2>/dev/null || true; fi
}

# ============ ZooKeeper（可选，全部节点组建 ensemble） ============
deploy_zookeeper() {
  ZK_HOME="${HOME_DIR}/zookeeper"
  IMAGE_ZK="{{image_zookeeper}}"
  mkdir -p "${ZK_HOME}/data" "${ZK_HOME}/datalog"
  # 由全体节点 IP 生成 ZOO_SERVERS。
  # 官方镜像格式：server.N=ip:2888:3888;2181（分号后为 clientPort）。
  # 缺 ;2181 时 quorum(2888/3888) 仍能选主，但永不监听客户端 2181。
  ZK_SERVERS="" ; ZK_MY_ID="{{__seq}}" ; ZK_ID=1
  IFS=',' read -ra ZK_IPS <<< "{{__node_ips}}"
  for zkip in "${ZK_IPS[@]}"; do
    ZK_SERVERS="${ZK_SERVERS}server.${ZK_ID}=${zkip}:2888:3888;2181 "
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
      ZOO_4LW_COMMANDS_WHITELIST: "srvr,ruok,mntr"
      ZOO_ADMINSERVER_ENABLED: "false"
      ZOO_STANDALONE_ENABLED: "false"
    volumes:
      - {{home_dir}}/zookeeper/data:/data
      - {{home_dir}}/zookeeper/datalog:/datalog
YAMLEOF
  migrate_old "bigdata-zookeeper"
  # myid 一致性保障：历史部署的主机序号残留会与本次 ZOO_SERVERS 不一致，导致 QuorumPeer 反复退出/选主闪断
  if [ "$(cat "${ZK_HOME}/data/myid" 2>/dev/null | tr -d '[:space:]')" != "${ZK_MY_ID}" ]; then
    echo "[ZooKeeper] 写入 myid=${ZK_MY_ID}（修正残留或缺失）"
    echo "${ZK_MY_ID}" > "${ZK_HOME}/data/myid"
  fi
  echo "[ZooKeeper] 拉取镜像 ${IMAGE_ZK}"
  pull_retry "${ZK_HOME}/compose.yml"
  # --force-recreate：确保按新 ZOO_SERVERS（含 ;2181）重生 zoo.cfg，避免沿用旧容器环境
  docker compose -f "${ZK_HOME}/compose.yml" up -d --force-recreate --remove-orphans
  # 5 节点 ensemble 选举+同步耗时更长，等待窗口约 180s
  wait_port 2181 60 "bigdata-zookeeper" "ZooKeeper"
  ZK_STATE="$(docker exec bigdata-zookeeper zkServer.sh status 2>/dev/null | grep -i 'Mode' | head -1 || true)"
  echo "[ZooKeeper] 已就绪（myid=${ZK_MY_ID} ${ZK_STATE}）"
}

# ============ HDFS boot（必选）：仅 NameNode/JournalNode 角色启动 + 格式化 ============
deploy_hdfs_boot() {
  HADOOP_HOME="${HOME_DIR}/hadoop"
  IMAGE_H="{{image}}"
  mkdir -p "${HADOOP_HOME}/conf" "${HADOOP_HOME}/data"
  cat > "${HADOOP_HOME}/conf/core-site.xml" <<'XMLEOF'
@@CORE_SITE@@
XMLEOF
  cat > "${HADOOP_HOME}/conf/hdfs-site.xml" <<'XMLEOF'
@@HDFS_SITE@@
XMLEOF
  # MapReduce on YARN：Hive 默认执行引擎是 MR，缺此文件则 INSERT/CTAS 会报
  # "No valid local directories in property: mapreduce.cluster.local.dir"
  cat > "${HADOOP_HOME}/conf/mapred-site.xml" <<'XMLEOF'
@@MAPRED_SITE@@
XMLEOF
  # YARN 客户端配置：HiveServer2/Trino 等以 HADOOP_CONF_DIR={{home_dir}}/hadoop/conf 运行。
  # 若此处缺 yarn-site.xml，客户端会退回 yarn-default（ha.enabled=false、hostname=0.0.0.0），
  # 并把这些默认值序列化进 MR 的 job.xml；AM 侧 job.xml 优先级高于站点配置，
  # 于是 HA 被“降级”成 DefaultNoHARMFailoverProxyProvider，AM 去连 0.0.0.0:8030 永久失败。
  cat > "${HADOOP_HOME}/conf/yarn-site.xml" <<'XMLEOF'
@@YARN_SITE@@
XMLEOF
  if [ "${HA}" = "true" ]; then
    deploy_hdfs_ha_boot
    return
  fi
  if is_master "${NN_IP}"; then
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
    # apache/hadoop 镜像容器内以 hadoop(UID 1000) 运行，宿主机数据目录需归其所有，否则 NameNode chmod 失败退出
    chown -R 1000:1000 "${HADOOP_HOME}/data" 2>/dev/null || true
    chmod -R a+rwx "${HADOOP_HOME}/data" 2>/dev/null || true
    migrate_old "${C_HDFS}"
    echo "[HDFS] 拉取镜像 ${IMAGE_H}"
    pull_retry "${HADOOP_HOME}/compose.yml"
    docker compose -f "${HADOOP_HOME}/compose.yml" up -d --remove-orphans
    # reset 已清空 namenode 数据目录，ENSURE_NAMENODE_DIR 会自动触发首次格式化
    wait_port "${PORT_HDFS}" 40 "${C_HDFS}" "HDFS ${CN_HDFS}"
    echo "[HDFS] NameNode 已就绪，预建公共服务目录"
    init_hdfs_dirs
  else
    echo "[HDFS] 本机非 NameNode，boot 阶段仅生成配置（DataNode 由 hdfs_dn 阶段启动）"
  fi
}

# ============ HDFS dn（必选）：NameNode 就绪后启动 DataNode ============
deploy_hdfs_dn() {
  HADOOP_HOME="${HOME_DIR}/hadoop"
  IMAGE_H="{{image}}"
  if [ "${HA}" = "true" ]; then
    deploy_hdfs_ha_dn
    return
  fi
  if is_master "${NN_IP}"; then
    echo "[HDFS] 本机为 NameNode，无 DataNode 阶段"
    return
  fi
  echo "[HDFS] 等待 NameNode ${NN_IP}:${NN_RPC_PORT} 就绪..."
  wait_host "${NN_IP}" "${NN_RPC_PORT}" 40 "NameNode" || { echo "NameNode 未就绪，无法启动本机 DataNode"; exit 1; }
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
  chown -R 1000:1000 "${HADOOP_HOME}/data" 2>/dev/null || true
  chmod -R a+rwx "${HADOOP_HOME}/data" 2>/dev/null || true
  migrate_old "${C_HDFS}"
  echo "[HDFS] 拉取镜像 ${IMAGE_H}"
  pull_retry "${HADOOP_HOME}/compose.yml"
  docker compose -f "${HADOOP_HOME}/compose.yml" up -d --remove-orphans
  wait_port "${PORT_HDFS}" 30 "${C_HDFS}" "HDFS ${CN_HDFS}"
  echo "[HDFS] DataNode 已上报至 ${NN_IP}"
}

# ============ Spark（可选） ============
deploy_spark() {
  SPARK_HOME="${HOME_DIR}/spark"
  IMAGE_S="{{image_spark}}"
  mkdir -p "${SPARK_HOME}/data"
  if [ "${HA}" = "true" ]; then
    deploy_spark_ha
    return
  fi
  if is_master "${SPARK_MASTER_IP}"; then
    C_SPARK=spark-master; PORT_SPARK="${SPARK_WEBUI_PORT}"; CN_SPARK=Master
    cat > "${SPARK_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-spark
services:
  master:
    image: {{image_spark}}
    container_name: spark-master
    restart: always
    network_mode: host
    hostname: "{{__name}}"
    extra_hosts:
      - "{{__name}}:{{__ip}}"
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
    hostname: "{{__name}}"
    extra_hosts:
      - "{{__name}}:{{__ip}}"
    user: root
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.worker.Worker", "spark://{{__master_spark}}:{{master_port}}", "--host", "{{__ip}}", "--webui-port", "8081"]
    environment:
      SPARK_WORKER_CORES: "{{worker_cores}}"
      SPARK_WORKER_MEMORY: "{{worker_mem}}"
    volumes:
      - {{home_dir}}/spark/data:/opt/spark/work-dir
YAMLEOF
  fi
  migrate_old "${C_SPARK}"
  echo "[Spark] 拉取镜像 ${IMAGE_S}"
  pull_retry "${SPARK_HOME}/compose.yml"
  docker compose -f "${SPARK_HOME}/compose.yml" up -d
  wait_port "${PORT_SPARK}" 20 "${C_SPARK}" "Spark ${CN_SPARK}"
  if [ "${ROLE}" != "master" ]; then
    for _ in $(seq 1 10); do
      if docker logs "${C_SPARK}" 2>&1 | grep -q "Successfully registered with master"; then
        echo "[Spark] Worker 已注册到 spark://${SPARK_MASTER_IP}:${SPARK_MASTER_PORT}"
        break
      fi
      sleep 3
    done
  fi
  echo "[Spark] ${CN_SPARK} 已就绪"
}

# ============ Flink（可选） ============
deploy_flink() {
  FLINK_HOME="${HOME_DIR}/flink"
  IMAGE_F="{{image_flink}}"
  mkdir -p "${FLINK_HOME}"
  if [ "${HA}" = "true" ]; then
    deploy_flink_ha
    return
  fi
  if is_master "${JM_IP}"; then
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
        jobmanager.rpc.address: {{__master_flink}}
        jobmanager.rpc.port: {{jm_rpc_port}}
        taskmanager.host: {{__ip}}
        taskmanager.bind-host: 0.0.0.0
        taskmanager.numberOfTaskSlots: {{tm_slots}}
YAMLEOF
  fi
  migrate_old "${C_FLINK}"
  echo "[Flink] 拉取镜像 ${IMAGE_F}"
  pull_retry "${FLINK_HOME}/compose.yml"
  docker compose -f "${FLINK_HOME}/compose.yml" up -d
  if [ "${ROLE}" = "master" ]; then
    wait_port 8081 20 "${C_FLINK}" "Flink JobManager"
  else
    sleep 5
    for _ in $(seq 1 20); do
      if docker logs "${C_FLINK}" 2>&1 | grep -qi "successful registration\|registered at jobmanager"; then
        echo "[Flink] TaskManager 已注册到 JobManager ${JM_IP}:${JM_RPC_PORT}"
        break
      fi
      sleep 3
    done
  fi
  echo "[Flink] ${CN_FLINK} 已就绪"
}

# ============ YARN（可选，主节点 RM / 工作节点 NM） ============
deploy_yarn() {
  YARN_HOME="${HOME_DIR}/yarn"
  IMAGE_Y="{{image}}"
  mkdir -p "${YARN_HOME}/conf"
  cat > "${YARN_HOME}/conf/yarn-site.xml" <<'XMLEOF'
@@YARN_SITE@@
XMLEOF
  if [ "${HA}" = "true" ]; then
    deploy_yarn_ha
    return
  fi
  if is_master "${RM_IP}"; then
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
    # NodeManager 的 local-dirs 默认取 hadoop.tmp.dir 下的 nm-local-dir；镜像以 hadoop(UID1000) 运行、
    # 容器内 /hadoop 不可写 → 必须挂一块可写目录到 /hadoop/dfs/tmp 并 chown 1000:1000，否则 NM 上报 UNHEALTHY。
    # 预先建好 mapred 本地目录：MR AppMaster/Task 的 mapreduce.cluster.local.dir 指向其下，
    # LocalDirAllocator 要求顶层目录已存在且可写。
    mkdir -p "${YARN_HOME}/tmp/mapred/local"
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
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - {{home_dir}}/yarn/conf/yarn-site.xml:/opt/hadoop/etc/hadoop/yarn-site.xml
      - {{home_dir}}/hadoop/conf/mapred-site.xml:/opt/hadoop/etc/hadoop/mapred-site.xml
      - {{home_dir}}/yarn/tmp:/hadoop/dfs/tmp
YAMLEOF
    chown -R 1000:1000 "${YARN_HOME}/tmp" 2>/dev/null || true
    chmod -R a+rwx "${YARN_HOME}/tmp" 2>/dev/null || true
  fi
  migrate_old "${C_YARN}"
  echo "[YARN] 拉取镜像 ${IMAGE_Y}"
  pull_retry "${YARN_HOME}/compose.yml"
  docker compose -f "${YARN_HOME}/compose.yml" up -d
  wait_port "${PORT_YARN}" 20 "${C_YARN}" "YARN ${CN_YARN}"
  echo "[YARN] ${CN_YARN} 已就绪"
}

# ============ Hive（可选） ============
deploy_hive() {
  if [ "${HA}" = "true" ]; then
    # HA：任意宿主都可能承载 MS1/MS2/HS1/HS2/metastore_db，deploy_hive_ha 内部按角色判定
    HIVE_HOME="${HOME_DIR}/hive"
    IMAGE_HV="{{image_hive}}"
    mkdir -p "${HIVE_HOME}/conf" "${HIVE_HOME}/data"
    cat > "${HIVE_HOME}/conf/hive-site.xml" <<'XMLEOF'
@@HIVE_SITE@@
XMLEOF
    deploy_hive_ha
    return
  fi
  if is_master "${HIVE_IP}"; then
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
    pull_retry "${HIVE_HOME}/compose.yml"
    docker compose -f "${HIVE_HOME}/compose.yml" up -d
    echo "[Hive] 等待 Metastore（首次启动需初始化 Derby 元数据库，最长 3 分钟）..."
    wait_port 9083 60 "hive-metastore" "Hive Metastore"
    echo "[Hive] 等待 HiveServer2（最长 4 分钟）..."
    wait_port 10000 80 "hive-hiveserver2" "HiveServer2"
    echo "[Hive] Metastore/HiveServer2 已就绪"
  else
    echo "[Hive] Metastore/HiveServer2 为单实例服务（内置 Derby），仅部署在 ${HIVE_IP}；本机提交作业用 beeline 连接 ${HIVE_IP}:10000"
  fi
}

# ============ HBase（可选，主节点 HMaster / 工作节点 RegionServer） ============
deploy_hbase() {
  HBASE_HOME="${HOME_DIR}/hbase"
  IMAGE_HB="{{image_hbase}}"
  mkdir -p "${HBASE_HOME}/conf" "${HBASE_HOME}/data"
  cat > "${HBASE_HOME}/conf/hbase-site.xml" <<'XMLEOF'
@@HBASE_SITE@@
XMLEOF
  if [ "${HA}" = "true" ]; then
    deploy_hbase_ha
    return
  fi
  if is_master "${HMASTER_IP}"; then
    C_HB=hbase-master; PORT_HB=16010; CN_HB=HMaster
    cat > "${HBASE_HOME}/compose.yml" <<'YAMLEOF'
name: bigdata-hbase
services:
  master:
    image: {{image_hbase}}
    container_name: hbase-master
    restart: always
    network_mode: host
    entrypoint: ["hbase"]
    command: ["master", "start"]
    # 注意：HBase 2.6.x 的 hbase 启动脚本不会把 HADOOP_CONF_DIR 加入 classpath，
    # 必须把 core-site/hdfs-site 直接挂进 HBASE_CONF_DIR(/usr/local/hbase/conf)，否则解析 hdfs://ns1 抛 UnknownHostException
    volumes:
      - {{home_dir}}/hbase/conf/hbase-site.xml:/usr/local/hbase/conf/hbase-site.xml
      - {{home_dir}}/hadoop/conf/core-site.xml:/usr/local/hbase/conf/core-site.xml:ro
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/usr/local/hbase/conf/hdfs-site.xml:ro
      - {{home_dir}}/hbase/data:/usr/local/hbase/data
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
    entrypoint: ["hbase"]
    command: ["regionserver", "start"]
    # 注意：HBase 2.6.x 的 hbase 启动脚本不会把 HADOOP_CONF_DIR 加入 classpath，
    # 必须把 core-site/hdfs-site 直接挂进 HBASE_CONF_DIR(/usr/local/hbase/conf)，否则解析 hdfs://ns1 抛 UnknownHostException
    volumes:
      - {{home_dir}}/hbase/conf/hbase-site.xml:/usr/local/hbase/conf/hbase-site.xml
      - {{home_dir}}/hadoop/conf/core-site.xml:/usr/local/hbase/conf/core-site.xml:ro
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/usr/local/hbase/conf/hdfs-site.xml:ro
      - {{home_dir}}/hbase/data:/usr/local/hbase/data
YAMLEOF
  fi
  migrate_old "${C_HB}"
  echo "[HBase] 拉取镜像 ${IMAGE_HB}"
  pull_retry "${HBASE_HOME}/compose.yml"
  docker compose -f "${HBASE_HOME}/compose.yml" up -d
  wait_port "${PORT_HB}" 40 "${C_HB}" "HBase ${CN_HB}"
  echo "[HBase] ${CN_HB} 已就绪"
}

# ============ Trino（可选） ============
deploy_trino() {
  TRINO_HOME="${HOME_DIR}/trino"
  IMAGE_T="{{image_trino}}"
  mkdir -p "${TRINO_HOME}/etc/catalog" "${TRINO_HOME}/data"
  # Trino 容器以 uid 1000 运行，数据目录需可写
  chown -R 1000:1000 "${TRINO_HOME}/data" 2>/dev/null || true
  # node.id 必须匹配 [A-Za-z0-9_-]+，不能含点（Trino 会以 "node.id is malformed" 拒绝启动），
  # 故把 IP 中的点替换为连字符，保持"每节点唯一且可复现"。
  cat > "${TRINO_HOME}/etc/node.properties" <<PROPSEOF
node.environment=bigdata
node.id=trino-${SELF_IP//./-}
node.data-dir=/data
PROPSEOF
  cat > "${TRINO_HOME}/etc/jvm.config" <<JVMEOF
-server
-Xmx${TRINO_MEM}
-XX:+UseG1GC
-XX:G1HeapRegionSize=32M
-XX:+ExplicitGCInvokesConcurrent
-XX:+ExitOnOutOfMemoryError
-XX:ReservedCodeCacheSize=512M
JVMEOF
  if is_master "${TRINO_COORD_IP}"; then
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
discovery.uri=http://${TRINO_COORD_IP}:${TRINO_HTTP_PORT}
CFGEOF
    C_TRINO=trino-worker; CN_TRINO=Worker
  fi
  # Trino 的 Hive 连接器是通过 HDFS 读写数据文件的，必须拿到 Hadoop 配置才能解析 hdfs://ns1（HA nameservice）。
  # 缺 hive.config.resources 时：建表报 "Failed checking path: hdfs://ns1/apps/hive/warehouse"，
  # 写入报 "Error creating ORC file"，根因都是 java.net.UnknownHostException: ns1。
  # 注意写任务在 worker 上执行，故每个节点都要拷贝配置（deploy_trino 逐节点执行）。
  HADOOP_CONF_LINE=""
  if [ -f "{{home_dir}}/hadoop/conf/core-site.xml" ]; then
    cp -f "{{home_dir}}/hadoop/conf/core-site.xml" "${TRINO_HOME}/etc/core-site.xml"
    cp -f "{{home_dir}}/hadoop/conf/hdfs-site.xml" "${TRINO_HOME}/etc/hdfs-site.xml"
    HADOOP_CONF_LINE="hive.config.resources=/etc/trino/core-site.xml,/etc/trino/hdfs-site.xml"
  fi
  cat > "${TRINO_HOME}/etc/catalog/hive.properties" <<CATEOF
connector.name=hive
# {{__hive_ms_uris}}：HA=双 Metastore uri（逗号分隔自动 failover），非 HA=单 uri（thrift://HIVE_IP:9083）
hive.metastore.uri={{__hive_ms_uris}}
${HADOOP_CONF_LINE}
# Hive 4 起 CREATE TABLE 默认建 EXTERNAL 表（HIVE-26175），而 Trino 默认拒绝对非托管表写入/删除：
# 不放开会分别报 "Cannot write to non-managed Hive table" 与 "Access Denied: Cannot drop table"
hive.non-managed-table-writes-enabled=true
hive.allow-drop-table=true
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
  pull_retry "${TRINO_HOME}/compose.yml"
  docker compose -f "${TRINO_HOME}/compose.yml" up -d
  wait_port "${TRINO_HTTP_PORT}" 40 "trino-node" "Trino ${CN_TRINO}"
  echo "[Trino] ${CN_TRINO} 已就绪（hive catalog -> {{__hive_ms_uris}}）"
}

# HA 模式扩展（ha.sh：HA 变量 + 各组件双实例部署函数；非 HA 时 HA=false，deploy_*_ha 不调用）
@@HA_SH@@

# ============ RUN 分发器（主脑按阶段注入 {{__run}}，子线程只执行自己负责的阶段） ============
RUN="${RUN:-{{__run}}}"
case "${RUN}" in
  reset)     run_reset;;
  zookeeper) has zookeeper && deploy_zookeeper;;
  hdfs_boot) has hdfs && deploy_hdfs_boot;;
  hdfs_dn)   has hdfs && deploy_hdfs_dn;;
  yarn)      has yarn && deploy_yarn;;
  spark)     has spark && deploy_spark;;
  flink)     has flink && deploy_flink;;
  hive)      has hive && deploy_hive;;
  hbase)     has hbase && deploy_hbase;;
  trino)     has trino && deploy_trino;;
  verify)    echo "[Verify] 校验阶段由主节点 bootstrap 脚本执行";;
  *)         echo "未知阶段或无需本机执行: ${RUN}";;
esac
echo "=== ${SELF_IP} ${RUN} 阶段完成 ==="