# ================= 大数据底座 HA 模式扩展（ha.sh）=================
# 由 node.sh 的 HA_SH 占位注入其顶部（函数定义 + HA 变量），仅声明不执行；
# 非 HA 运行时 HA="false"，各 deploy_* 均不调用，对非 HA 零影响。
# 引用说明：SELF_IP/HOME_DIR/COMPONENTS/migrate_old/wait_port 由 node.sh 先定义。
HA="{{ha}}"
NN1="{{__nn1_ip}}"; NN2="{{__nn2_ip}}"; JNS="{{__jn_ips}}"
RM1="{{__rm1_ip}}"; RM2="{{__rm2_ip}}"
SPARK_M2="{{__spark_m2_ip}}"
FLINK_JM2="{{__flink_jm2_ip}}"
HMASTER2="{{__hmaster2_ip}}"
HS2S="{{__hs2_ips}}"; MSS="{{__ms_ips}}"; HIVE_DB="{{__hive_db_ip}}"
HDFS_ENTRY="{{__hdfs_entry}}"; ZK_IPS="{{__zk_ips}}"
NAMESPACE="{{hdfs_nameservice}}"
NN_RPC="{{nn_rpc_port}}"; JN_RPC="{{jn_rpc_port}}"; JN_HTTP="{{jn_http_port}}"
HIVE_DB_IMAGE="{{hive_db_image}}"; HIVE_DB_PW="{{hive_db_password}}"
NAMESPACE=${NAMESPACE:-ns1}; NN_RPC=${NN_RPC:-9000}; JN_RPC=${JN_RPC:-8485}; JN_HTTP=${JN_HTTP:-8484}
PASSWORD_PW="${HIVE_DB_PW:-HiveDb@123}"

ha_in() { case ",${1}," in *",${2},"*) return 0;; *) return 1;; esac; }

# 本机 HDFS 角色：nn1 / nn2 / jn / dn
hdfs_ha_role() {
  [ "${SELF_IP}" = "${NN1}" ] && { echo nn1; return; }
  [ "${SELF_IP}" = "${NN2}" ] && { echo nn2; return; }
  if ha_in "${JNS}" "${SELF_IP}"; then echo jn; else echo dn; fi
}
# 本机组件从角色判断
ha_is() { [ "${SELF_IP}" = "${1}" ]; }

wait_remote_port() { # wait_remote_port <ip> <port> <次数> <名>
  local IP="$1" P="$2" T="$3" N="$4" R=0
  for _ in $(seq 1 "${T}"); do
    if (echo > "/dev/tcp/${IP}/${P}") >/dev/null 2>&1; then R=1; break; fi
    sleep 3
  done
  [ "${R}" = "1" ] || { echo "${N} ${IP}:${P} 未就绪"; return 1; }
}
# ZK ensemble 任一可用（上限 120s）
wait_zk() {
  local I=1 R=0
  for _ in $(seq 1 40); do
    R=1
    IFS=',' read -ra _Z <<< "${ZK_IPS}"
    for z in "${_Z[@]}"; do
      (echo > "/dev/tcp/${z}/2181") >/dev/null 2>&1 && { R=0; break; }
    done
    [ "${R}" = "0" ] && return 0
    sleep 3
  done
  echo "[HA] ZooKeeper ensemble 未就绪（${ZK_IPS}）"
  return 1
}

# ---------------- HDFS ----------------
deploy_hdfs_ha() {
  HADOOP_HOME="${HOME_DIR}/hadoop"
  mkdir -p "${HADOOP_HOME}/conf" "${HADOOP_HOME}/data/journal" \
    "${HADOOP_HOME}/data/namenode" "${HADOOP_HOME}/data/datanode"
  ROLE_=$(hdfs_ha_role)
  echo "[HA-HDFS] 本机角色 ${ROLE_}（nn1=${NN1} nn2=${NN2} jns=${JNS}）"
  C_MARK="${HADOOP_HOME}/data/.formatted"
  VOL_CONF1="${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml"
  VOL_CONF2="${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml"
  case "${ROLE_}" in
    jn)
      cat > "${HADOOP_HOME}/compose.yml" <<'HAEOF'
name: bigdata-hadoop
services:
  journalnode:
    image: {{image}}
    container_name: hadoop-journalnode
    restart: always
    network_mode: host
    command: ["hdfs", "journalnode"]
    volumes:
      - ${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - ${HADOOP_HOME}/data/journal:/hadoop/dfs/journal
HAEOF
      C_=hadoop-journalnode; P_=${JN_RPC}
      ;;
    nn1)
      if [ ! -f "${C_MARK}" ]; then
        echo "[HA-HDFS] 首次格式化 NameNode（nn1=${NN1}）..."
        docker run --rm -v "${VOL_CONF1}" -v "${VOL_CONF2}" \
          -v "${HADOOP_HOME}/data/namenode:/hadoop/dfs/name" \
          {{image}} hdfs namenode -format -force -nonInteractive
        touch "${C_MARK}"
      fi
      cat > "${HADOOP_HOME}/compose.yml" <<'HAEOF'
name: bigdata-hadoop
services:
  namenode:
    image: {{image}}
    container_name: hadoop-namenode
    restart: always
    network_mode: host
    command: ["hdfs", "namenode"]
    volumes:
      - ${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - ${HADOOP_HOME}/data/namenode:/hadoop/dfs/name
  zkfc:
    image: {{image}}
    container_name: hadoop-zkfc
    restart: always
    network_mode: host
    command: ["hdfs", "zkfc"]
    depends_on:
      - namenode
    volumes:
      - ${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
HAEOF
      chmod -R a+rwx "${HADOOP_HOME}/data" 2>/dev/null || true
      docker compose -f "${HADOOP_HOME}/compose.yml" up -d --remove-orphans
      migrate_old "hadoop-namenode" || true
      docker compose -f "${HADOOP_HOME}/compose.yml" restart namenode 2>/dev/null || true
      C_=hadoop-namenode; P_=9870
      ;;
    nn2)
      wait_zk || exit 1
      # 等待 JournalNode quorum（上限 120s）：bootstrapStandby 依赖 edits 已可读
      for _ in $(seq 1 40); do
        R=1
        IFS=',' read -ra _J <<< "${JNS}"
        for j in "${_J[@]}"; do (echo > "/dev/tcp/${j}/${JN_RPC}") >/dev/null 2>&1 && R=2; done
        [ "${R}" = "2" ] && break
        sleep 3
      done
      for j in "${_J[@]}"; do wait_remote_port "${j}" "${JN_RPC}" 10 "JournalNode" || exit 1; done
      if [ ! -f "${C_MARK}" ]; then
        echo "[HA-HDFS] bootstrap Standby（nn2=${NN2}）..."
        docker run --rm -v "${VOL_CONF1}" -v "${VOL_CONF2}" \
          -v "${HADOOP_HOME}/data/namenode:/hadoop/dfs/name" \
          {{image}} hdfs namenode -bootstrapStandby -force -nonInteractive
        touch "${C_MARK}"
      fi
      cat > "${HADOOP_HOME}/compose.yml" <<'HAEOF'
name: bigdata-hadoop
services:
  namenode:
    image: {{image}}
    container_name: hadoop-namenode2
    restart: always
    network_mode: host
    command: ["hdfs", "namenode"]
    volumes:
      - ${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - ${HADOOP_HOME}/data/namenode:/hadoop/dfs/name
  zkfc:
    image: {{image}}
    container_name: hadoop-zkfc2
    restart: always
    network_mode: host
    command: ["hdfs", "zkfc"]
    depends_on:
      - namenode
    volumes:
      - ${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
HAEOF
      C_=hadoop-namenode2; P_=9870
      ;;
    *) # datanode
      cat > "${HADOOP_HOME}/compose.yml" <<'HAEOF'
name: bigdata-hadoop
services:
  datanode:
    image: {{image}}
    container_name: hadoop-datanode
    restart: always
    network_mode: host
    command: ["hdfs", "datanode"]
    volumes:
      - ${HADOOP_HOME}/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${HADOOP_HOME}/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - ${HADOOP_HOME}/data/datanode:/hadoop/dfs/data
HAEOF
      C_=hadoop-datanode; P_=9866
      ;;
  esac
  chown -R 1000:1000 "${HADOOP_HOME}/data" 2>/dev/null || true
  migrate_old "${C_}"
  docker compose -f "${HADOOP_HOME}/compose.yml" pull
  docker compose -f "${HADOOP_HOME}/compose.yml" up -d --remove-orphans
  wait_port "${P_}" 30 "${C_}" "HDFS ${ROLE_}" || true
  echo "[HA-HDFS] ${ROLE_} 部署完成（${C_}=${P_}）"
}

# ---------------- YARN ----------------
deploy_yarn_ha() {
  YARN_HOME="${HOME_DIR}/yarn"
  mkdir -p "${YARN_HOME}/conf"
  if ha_is "${RM1}"; then
    C_=hadoop-resourcemanager; PROJ_=bigdata-yarn; CN_="YARN RM1 (active)"
  elif ha_is "${RM2}"; then
    C_=hadoop-resourcemanager2; PROJ_=bigdata-yarn-rm2; CN_="YARN RM2 (standby)"
  else
    C_=hadoop-nodemanager; PROJ_=bigdata-yarn; CN_="YARN NodeManager"
  fi
  NM_NAME="nodemanager"
  [ "${CN_}" = "YARN RM1 (active)" ] || [ "${CN_}" = "YARN RM2 (standby)" ] && NM_NAME="resourcemanager"
  cat > "${YARN_HOME}/compose.yml" <<HAEOF
name: ${PROJ_}
services:
  ${NM_NAME}:
    image: {{image}}
    container_name: ${C_}
    restart: always
    network_mode: host
    command: ["yarn", "${NM_NAME}"]
    volumes:
      - ${HOME_DIR}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - ${YARN_HOME}/conf/yarn-site.xml:/opt/hadoop/etc/hadoop/yarn-site.xml
HAEOF
  migrate_old "${C_}"
  docker compose -f "${YARN_HOME}/compose.yml" pull
  docker compose -f "${YARN_HOME}/compose.yml" up -d --remove-orphans
  if [ "${NM_NAME}" = "resourcemanager" ]; then PORT_=8088; else PORT_=8042; fi
  wait_port "${PORT_}" 20 "${C_}" "YARN ${CN_}"
  echo "[HA-YARN] ${CN_} 已就绪"
}

# ---------------- Spark ----------------
deploy_spark_ha() {
  SPARK_HOME="${HOME_DIR}/spark"
  mkdir -p "${SPARK_HOME}/data"
  # 双 Master 共用 recovery 配置（ZK 选举 + HDFS recovery 目录）
  cat > "${SPARK_HOME}/spark-defaults.conf" <<SPEOFS
spark.deploy.recoveryMode=ZOOKEEPER
spark.deploy.zookeeper.url=${ZK_IPS}
spark.deploy.recoveryDirectory=${HDFS_ENTRY}/spark/ha
SPEOFS
  if ha_is "${SPARK_MASTER_IP}"; then
    C_=spark-master; CN_="Spark Master1 (active)"
  elif ha_is "${SPARK_M2}"; then
    C_=spark-master2; CN_="Spark Master2 (standby)"
  else
    C_=spark-worker; CN_="Spark Worker"
  fi
  HOST_ARG="--host ${SELF_IP} --port {{master_port}} --webui-port {{webui_port}}"
  if [ "${C_}" = "spark-worker" ]; then
    cat > "${SPARK_HOME}/compose.yml" <<'HAEOF'
name: bigdata-spark
services:
  worker:
    image: {{image_spark}}
    container_name: spark-worker
    restart: always
    network_mode: host
    user: root
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.worker.Worker", "spark://{{__master_spark}}:{{master_port}},${SPARK_M2}:{{master_port}}", "--host", "{{__ip}}", "--webui-port", "8081"]
    environment:
      SPARK_WORKER_CORES: "{{worker_cores}}"
      SPARK_WORKER_MEMORY: "{{worker_mem}}"
    volumes:
      - ${SPARK_HOME}/data:/opt/spark/work-dir
      - ${SPARK_HOME}/spark-defaults.conf:/opt/spark/conf/spark-defaults.conf
HAEOF
    PORT_=8081
  else
    cat > "${SPARK_HOME}/compose.yml" <<HAEOF
name: bigdata-spark
services:
  master:
    image: {{image_spark}}
    container_name: ${C_}
    restart: always
    network_mode: host
    user: root
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.master.Master", "--host", "${SELF_IP}", "--port", "{{master_port}}", "--webui-port", "{{webui_port}}"]
    volumes:
      - ${SPARK_HOME}/data:/opt/spark/work-dir
      - ${SPARK_HOME}/spark-defaults.conf:/opt/spark/conf/spark-defaults.conf
HAEOF
    PORT_="{{webui_port}}"
  fi
  migrate_old "${C_}"
  docker compose -f "${SPARK_HOME}/compose.yml" pull
  docker compose -f "${SPARK_HOME}/compose.yml" up -d --remove-orphans
  wait_port "${PORT_}" 20 "${C_}" "Spark ${CN_}" || true
  echo "[HA-Spark] ${CN_} 已就绪"
}

# ---------------- Flink ----------------
deploy_flink_ha() {
  FLINK_HOME="${HOME_DIR}/flink"
  mkdir -p "${FLINK_HOME}"
  if ha_is "${JM_IP}"; then C_=flink-jobmanager; CN_="Flink JM1 (active)"; ROLE_=jm
  elif ha_is "${FLINK_JM2}"; then C_=flink-jobmanager2; CN_="Flink JM2 (standby)"; ROLE_=jm
  else C_=flink-taskmanager; CN_="Flink TaskManager"; ROLE_=tm; fi
  if [ "${ROLE_}" = "tm" ]; then
    cat > "${FLINK_HOME}/compose.yml" <<HAEOF
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
        jobmanager.rpc.address: ${JM_IP}
        jobmanager.rpc.port: {{jm_rpc_port}}
        taskmanager.host: ${SELF_IP}
        taskmanager.bind-host: 0.0.0.0
        taskmanager.numberOfTaskSlots: {{tm_slots}}
HAEOF
    PORT_=8081
  else
    cat > "${FLINK_HOME}/compose.yml" <<HAEOF
name: bigdata-flink
services:
  jobmanager:
    image: {{image_flink}}
    container_name: ${C_}
    restart: always
    network_mode: host
    command: jobmanager
    environment:
      FLINK_PROPERTIES: |
        jobmanager.rpc.address: ${SELF_IP}
        jobmanager.rpc.port: {{jm_rpc_port}}
        jobmanager.bind-host: 0.0.0.0
        rest.address: ${SELF_IP}
        rest.bind-address: 0.0.0.0
        rest.port: 8081
        high-availability: zookeeper
        high-availability.zookeeper.quorum: ${ZK_IPS}
        high-availability.storageDir: ${HDFS_ENTRY}/flink/ha
        high-availability.cluster-id: bigdata-flink
HAEOF
    PORT_=8081
  fi
  migrate_old "${C_}"
  docker compose -f "${FLINK_HOME}/compose.yml" pull
  docker compose -f "${FLINK_HOME}/compose.yml" up -d --remove-orphans
  wait_port "${PORT_}" 20 "${C_}" "Flink ${CN_}" || true
  echo "[HA-Flink] ${CN_} 已就绪"
}

# ---------------- HBase ----------------
deploy_hbase_ha() {
  HBASE_HOME="${HOME_DIR}/hbase"
  mkdir -p "${HBASE_HOME}/conf" "${HBASE_HOME}/data"
  if ha_is "${HMASTER_IP}"; then
    C_=hbase-master; CN_="HMaster1 (active)"
  elif ha_is "${HMASTER2}"; then
    C_=hbase-backup-master; CN_="HMaster2 (backup)"
  else
    C_=hbase-regionserver; CN_="RegionServer"
  fi
  if [ "${C_}" = "hbase-regionserver" ]; then
    cat > "${HBASE_HOME}/compose.yml" <<'HAEOF'
name: bigdata-hbase
services:
  regionserver:
    image: {{image_hbase}}
    container_name: hbase-regionserver
    restart: always
    network_mode: host
    command: ["regionserver"]
    volumes:
      - ${HBASE_HOME}/conf/hbase-site.xml:/opt/hbase/conf/hbase-site.xml
      - ${HBASE_HOME}/data:/opt/hbase/data
HAEOF
    PORT_=16030
  else
    cat > "${HBASE_HOME}/compose.yml" <<HAEOF
name: bigdata-hbase
services:
  master:
    image: {{image_hbase}}
    container_name: ${C_}
    restart: always
    network_mode: host
    command: ["master"]
    volumes:
      - ${HBASE_HOME}/conf/hbase-site.xml:/opt/hbase/conf/hbase-site.xml
      - ${HBASE_HOME}/data:/opt/hbase/data
HAEOF
    PORT_=16010
  fi
  migrate_old "${C_}"
  docker compose -f "${HBASE_HOME}/compose.yml" pull
  docker compose -f "${HBASE_HOME}/compose.yml" up -d --remove-orphans
  wait_port "${PORT_}" 40 "${C_}" "HBase ${CN_}" || true
  echo "[HA-HBase] ${CN_} 已就绪"
}

# ---------------- Hive（双 MS/HS2 + metastore_db） ----------------
chunk_at() { # 取逗号列表第 N 项（1 起）
  echo "${1}" | cut -d, -f"${2}"
}
deploy_metastore_db() {
  [ "${SELF_IP}" = "${HIVE_DB}" ] || return 0
  DB_HOME="${HOME_DIR}/hive/metadb"
  mkdir -p "${DB_HOME}/data"
  cat > "${DB_HOME}/compose.yml" <<'DBEOF'
name: bigdata-hive-metadb
services:
  mysql:
    image: {{hive_db_image}}
    container_name: hive-metastore-db
    restart: always
    network_mode: host
    environment:
      MYSQL_ROOT_PASSWORD: "${HIVE_DB_PW}"
      MYSQL_DATABASE: "metastore"
    volumes:
      - ${DB_HOME}/data:/var/lib/mysql
DBEOF
  migrate_old "hive-metastore-db"
  docker compose -f "${DB_HOME}/compose.yml" pull
  docker compose -f "${DB_HOME}/compose.yml" up -d
  wait_port 3306 60 "hive-metastore-db" "Hive MetaDB"
  echo "[HA-Hive] metastore_db (MySQL) 已就绪 @${SELF_IP}:3306"
}
deploy_hive_ha() {
  # node.sh 已在本机写下 conf/hive-site.xml（HIVE_SITE 占位注入），此处仅起容器
  MS1=$(chunk_at "${MSS}" 1); MS2=$(chunk_at "${MSS}" 2)
  HS1=$(chunk_at "${HS2S}" 1); HS2=$(chunk_at "${HS2S}" 2)
  deploy_metastore_db
  HIVE_HOME="${HOME_DIR}/hive"
  mkdir -p "${HIVE_HOME}/conf" "${HIVE_HOME}/data"
  CONTAINER_ARR=()
  [ -n "${MS1}" ] && ha_is "${MS1}" && CONTAINER_ARR+=("hive-metastore|metastore|metastore")
  [ -n "${MS2}" ] && ha_is "${MS2}" && CONTAINER_ARR+=("hive-metastore2|metastore|metastore")
  [ -n "${HS1}" ] && ha_is "${HS1}" && CONTAINER_ARR+=("hive-hiveserver2|hiveserver2|hiveserver2")
  [ -n "${HS2}" ] && ha_is "${HS2}" && CONTAINER_ARR+=("hive-hiveserver2-2|hiveserver2|hiveserver2")
  if [ ${#CONTAINER_ARR[@]} -eq 0 ]; then
    echo "[HA-Hive] 本机 ${SELF_IP} 不承载 Hive 实例"
    return 0
  fi
  : > "${HIVE_HOME}/compose.yml"
  printf 'name: bigdata-hive\nservices:\n' >> "${HIVE_HOME}/compose.yml"
  for item in "${CONTAINER_ARR[@]}"; do
    IFS='|' read -r C SVC LBL <<< "${item}"
    cat >> "${HIVE_HOME}/compose.yml" <<HAEOF
  ${SVC}:
    image: {{image_hive}}
    container_name: ${C}
    restart: always
    network_mode: host
    environment:
      SERVICE_NAME: ${LBL}
    volumes:
      - ${HIVE_HOME}/conf/hive-site.xml:/opt/hive/conf/hive-site.xml
      - ${HIVE_HOME}/data/warehouse:/opt/hive/data/warehouse
HAEOF
  done
  migrate_old "${C}"
  docker compose -f "${HIVE_HOME}/compose.yml" pull
  docker compose -f "${HIVE_HOME}/compose.yml" up -d --remove-orphans
  if [ -n "${MS1}" ] && ha_is "${MS1}"; then
    wait_port 9083 60 "hive-metastore" "Hive Metastore" || true
  fi
  if [ -n "${HS1}" ] && ha_is "${HS1}"; then
    wait_port 10000 80 "hive-hiveserver2" "HiveServer2" || true
  fi
  # 首次初始化 metastore schema（幂等：MS1 主机执行）
  if [ -n "${MS1}" ] && ha_is "${MS1}"; then
    if docker exec hive-metastore-db mysql -uroot -p"${PASSWORD_PW}" -e "SELECT 1 FROM metastore.bucketing_cols LIMIT 1" >/dev/null 2>&1; then
      echo "[HA-Hive] metastore schema 已初始化"
    else
      echo "[HA-Hive] 初始化 metastore schema（schematool）..."
      docker run --rm -v "${HIVE_HOME}/conf/hive-site.xml:/opt/hive/conf/hive-site.xml" \
        {{image_hive}} schematool -dbType mysql -initSchema --verbose || true
    fi
  fi
  echo "[HA-Hive] 本机 Hive 容器已就绪：${C}"
}