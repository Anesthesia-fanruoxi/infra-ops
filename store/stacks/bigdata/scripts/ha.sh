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

# ---------------- HDFS boot（先 JN + NN 格式化/启动，DN 留待 hdfs_dn 阶段，保证 DN 晚于 NN 就绪） ----------------
deploy_hdfs_ha_boot() {
  HADOOP_HOME="{{home_dir}}/hadoop"
  mkdir -p "{{home_dir}}/hadoop/conf" "{{home_dir}}/hadoop/data/journal" \
    "{{home_dir}}/hadoop/data/namenode" "{{home_dir}}/hadoop/data/datanode"
  ROLE_=$(hdfs_ha_role)
  echo "[HA-HDFS] boot 阶段，本机角色 ${ROLE_}（nn1=${NN1} nn2=${NN2} jns=${JNS}）"
  C_MARK="{{home_dir}}/hadoop/data/.formatted"
  VOL_CONF1="{{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml"
  VOL_CONF2="{{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml"
  IN_JNS=no; ha_in "${JNS}" "${SELF_IP}" && IN_JNS=yes
  IS_NN1=no; IS_NN2=no
  [ "${SELF_IP}" = "${NN1}" ] && IS_NN1=yes
  [ "${SELF_IP}" = "${NN2}" ] && IS_NN2=yes
  CMP="docker compose -f {{home_dir}}/hadoop/compose.yml"

  cat > "{{home_dir}}/hadoop/compose.yml" <<'HAEOF0'
name: bigdata-hadoop
services:
HAEOF0

  # —— JournalNode：JNS 内全部主机（含 NN1/NN2）都部署，保证 ≥2/3 quorum ——
  if [ "${IN_JNS}" = "yes" ]; then
    cat >> "{{home_dir}}/hadoop/compose.yml" <<'HAEOF'
  journalnode:
    image: {{image}}
    container_name: hadoop-journalnode
    restart: always
    network_mode: host
    command: ["hdfs", "journalnode"]
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - {{home_dir}}/hadoop/data/journal:/hadoop/dfs/journal
HAEOF
    chown -R 1000:1000 "{{home_dir}}/hadoop/data" 2>/dev/null || true
    migrate_old "hadoop-journalnode" 2>/dev/null || true
    pull_retry "{{home_dir}}/hadoop/compose.yml"
    ${CMP} up -d --remove-orphans
  fi

  # 等待 JournalNode quorum（≥2 台可达，上限约 200s）
  wait_jn_quorum() {
    local OK=0
    for _ in $(seq 1 66); do
      local N=0 J
      IFS=',' read -ra _JQ <<< "${JNS}"
      for J in "${_JQ[@]}"; do (echo > "/dev/tcp/${J}/${JN_RPC}") >/dev/null 2>&1 && N=$((N+1)); done
      [ "${N}" -ge 2 ] && { OK=1; break; }
      sleep 3
    done
    [ "${OK}" = "1" ] || { echo "[HA-HDFS] JournalNode quorum 未就绪（<2 台可达：${JNS}）"; return 1; }
    return 0
  }

  # —— NameNode1：等 quorum 后 format ——
  if [ "${IS_NN1}" = "yes" ]; then
    wait_jn_quorum || exit 1
    if [ ! -f "${C_MARK}" ]; then
      echo "[HA-HDFS] 首次格式化 NameNode（nn1=${NN1}）..."
      # format 的 STARTUP_MSG 会打印完整 classpath（数万字符），直接回显会撑爆主脑
      # SSH exec 输出缓冲触发 short write，故重定向到日志文件并仅回显尾部。
      FMTLOG="/tmp/nn1_format_$$.log"
      if docker run --network host --rm -v "${VOL_CONF1}" -v "${VOL_CONF2}" \
        -v "{{home_dir}}/hadoop/data/namenode:/hadoop/dfs/name" \
        {{image}} hdfs namenode -format -force -nonInteractive >"${FMTLOG}" 2>&1; then
        echo "[HA-HDFS] format 完成"
        grep -E "successfully formatted|Storage directory" "${FMTLOG}" | tail -n 5 || true
      else
        RC=$?
        echo "[HA-HDFS] format 失败（rc=${RC}），日志尾部："
        tail -n 40 "${FMTLOG}" || true
        exit 1
      fi
      touch "${C_MARK}"
    fi
    # 初始化 ZKFC 自动故障转移状态（幂等，重装也执行）：缺它会导致 ZKFC 无法选出 active
    # NameNode，bootstrap 的 dfsadmin/客户端连接将一直挂起
    echo "[HA-HDFS] 初始化 ZKFC 自动故障转移状态（formatZK）..."
    ZKLOG="/tmp/nn1_zkfc_$$.log"
    docker run --network host --rm -v "${VOL_CONF1}" -v "${VOL_CONF2}" \
      -e HADOOP_OPTS="-Ddfs.ha.namenode.id=nn1" \
      {{image}} hdfs zkfc -formatZK -force -nonInteractive >"${ZKLOG}" 2>&1 \
      && echo "[HA-HDFS] formatZK 完成" \
      || echo "[HA-HDFS] formatZK 未完全成功，继续（依赖后续选主）"
  fi
  # —— NameNode2：等 ZK + quorum + 等 NN1 RPC 就绪 后 bootstrap ——
  if [ "${IS_NN2}" = "yes" ]; then
    wait_zk || exit 1
    wait_jn_quorum || exit 1
    # NN1 需先完成 format 并启动 namenode 才会监听 NN_RPC；同阶段 NN1/NN2 并行，
    # bootstrapStandby 直接连 NN1:NN_RPC 会遭遇 Connection refused。故先有界等待 NN1 就绪。
    echo "[HA-HDFS] 等待主 NameNode ${NN1}:${NN_RPC} 就绪（供 bootstrapStandby 抓取命名空间）..."
    wait_remote_port "${NN1}" "${NN_RPC}" 70 "NameNode(nn1 RPC)" || exit 1
    # 注意：NN1 的 RPC(9000)/WebUI(9870) 端口是在 initialize() 早期就 bind 的，
    # 此时 NameNode 的 started 标记仍为 false；bootstrapStandby 打到该窗口会立刻抛
    # "NameNode still not started"（Hadoop 内部重试很短，实测 ~9s 即放弃）。
    # 故再等 WebUI 端口，缩小窗口，并对 bootstrapStandby 做有界重试兜底，避免重装偶发失败。
    wait_remote_port "${NN1}" 9870 70 "NameNode(nn1 WebUI)" || true
    if [ ! -f "${C_MARK}" ]; then
      echo "[HA-HDFS] bootstrap Standby（nn2=${NN2}）..."
      # --network host + 显式 nn id：bridge 下容器 hostname 为随机 ID，getLocalHost 无法匹配
      # rpc-address 判不出自身 NN，导致 "Could not determine own NN ID"。
      # bootstrapStandby 的 STARTUP_MSG 会打印完整 classpath（数万字符），直接回显
      # 会撑爆主脑 SSH exec 输出缓冲触发 short write，故重定向到日志文件并仅回显尾部。
      BOOTLOG="/tmp/nn2_bootstrap_$$.log"
      BOOT_OK=0
      for _TRY in $(seq 1 12); do
        if docker run --network host --rm -v "${VOL_CONF1}" -v "${VOL_CONF2}" \
          -v "{{home_dir}}/hadoop/data/namenode:/hadoop/dfs/name" \
          -e HADOOP_OPTS="-Ddfs.ha.namenode.id=nn2" \
          {{image}} hdfs namenode -bootstrapStandby -force -nonInteractive >"${BOOTLOG}" 2>&1; then
          BOOT_OK=1; break
        fi
        echo "[HA-HDFS] bootstrapStandby 第 ${_TRY}/12 次未成功（NN1 可能仍在加载镜像），20s 后重试..."
        sleep 20
      done
      if [ "${BOOT_OK}" = "1" ]; then
        echo "[HA-HDFS] bootstrapStandby 完成"
        tail -n 8 "${BOOTLOG}" || true
      else
        echo "[HA-HDFS] bootstrapStandby 连续 12 次失败，日志尾部："
        tail -n 40 "${BOOTLOG}" || true
        exit 1
      fi
      touch "${C_MARK}"
    fi
  fi

  # —— 追加 NameNode + ZKFC（仅 NN 主机）并启动 ——
  if [ "${IS_NN1}" = "yes" ] || [ "${IS_NN2}" = "yes" ]; then
    NN_SUF=""; [ "${IS_NN2}" = "yes" ] && NN_SUF="2"
    cat >> "{{home_dir}}/hadoop/compose.yml" <<HAEOF
  namenode:
    image: {{image}}
    container_name: hadoop-namenode${NN_SUF}
    restart: always
    network_mode: host
    command: ["hdfs", "namenode"]
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - {{home_dir}}/hadoop/data/namenode:/hadoop/dfs/name
  zkfc:
    image: {{image}}
    container_name: hadoop-zkfc${NN_SUF}
    restart: always
    network_mode: host
    command: ["hdfs", "zkfc"]
    depends_on:
      - namenode
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
HAEOF
    chmod -R a+rwx "{{home_dir}}/hadoop/data" 2>/dev/null || true
    migrate_old "hadoop-namenode${NN_SUF}" "hadoop-zkfc${NN_SUF}" 2>/dev/null || true
    ${CMP} up -d --remove-orphans
    C_=hadoop-namenode${NN_SUF}
    # NameNode 绑定在 $SELF_IP:9870，wait_port 探测 127.0.0.1 会误报未就绪，这里用 SELF_IP 直测
    P_=9870; READY_=0
    for _ in $(seq 1 40); do
      if (echo > "/dev/tcp/${SELF_IP}/${P_}") >/dev/null 2>&1; then READY_=1; break; fi
      sleep 3
    done
    [ "${READY_}" = "1" ] || { echo "[HA-HDFS] NameNode WebUI 未就绪（${SELF_IP}:${P_}）"; docker logs "${C_}" --tail 60 2>&1 || true; }
    echo "[HA-HDFS] NameNode${NN_SUF} 已启动（${SELF_IP}:${P_}）"
  else
    echo "[HA-HDFS] ${ROLE_} boot 完成（本机无 NameNode，JN 已就绪）"
  fi
}

# ---------------- HDFS dn（NN 已就绪后启动 DataNode，避免先于 NN 启动造成寻址/注册混乱） ----------------
deploy_hdfs_ha_dn() {
  HADOOP_HOME="{{home_dir}}/hadoop"
  ROLE_=$(hdfs_ha_role)
  CMP="docker compose -f {{home_dir}}/hadoop/compose.yml"
  # 等待任一 NameNode RPC 就绪（active 必定至少一侧监听 NN_RPC）
  echo "[HA-HDFS] dn 阶段，等待 NameNode（${NN1}/${NN2}:${NN_RPC}）就绪..."
  local _ok=0
  for _ in $(seq 1 40); do
    if (echo > "/dev/tcp/${NN1}/${NN_RPC}") >/dev/null 2>&1; then _ok=1; break; fi
    if [ -n "${NN2}" ] && (echo > "/dev/tcp/${NN2}/${NN_RPC}") >/dev/null 2>&1; then _ok=1; break; fi
    sleep 3
  done
  [ "${_ok}" = "1" ] || { echo "NameNode 未就绪（${NN1}/${NN2}:${NN_RPC}），无法启动本机 DataNode"; exit 1; }

  # 追加 datanode 服务（boot 阶段已在各主机生成 compose.yml）
  cat >> "{{home_dir}}/hadoop/compose.yml" <<'HAEOF'
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
HAEOF
  chown -R 1000:1000 "{{home_dir}}/hadoop/data" 2>/dev/null || true
  migrate_old "hadoop-datanode" 2>/dev/null || true
  pull_retry "{{home_dir}}/hadoop/compose.yml"
  ${CMP} up -d --remove-orphans
  wait_port 9866 30 "hadoop-datanode" "HDFS DataNode" || true
  echo "[HA-HDFS] ${ROLE_} DataNode 已启动并上报（${SELF_IP}:9866）"
}

# ---------------- YARN ----------------
deploy_yarn_ha() {
  YARN_HOME="{{home_dir}}/yarn"
  mkdir -p "{{home_dir}}/yarn/conf"
  if ha_is "${RM1}"; then
    C_=hadoop-resourcemanager; PROJ_=bigdata-yarn; CN_="YARN RM1 (active)"
  elif ha_is "${RM2}"; then
    C_=hadoop-resourcemanager2; PROJ_=bigdata-yarn-rm2; CN_="YARN RM2 (standby)"
  else
    C_=hadoop-nodemanager; PROJ_=bigdata-yarn; CN_="YARN NodeManager"
  fi
  NM_NAME="nodemanager"
  [ "${CN_}" = "YARN RM1 (active)" ] || [ "${CN_}" = "YARN RM2 (standby)" ] && NM_NAME="resourcemanager"
  # NodeManager 的 local-dirs 默认取 hadoop.tmp.dir 下的 nm-local-dir；镜像以 hadoop(UID1000) 运行、
  # 容器内 /hadoop 不可写 → 仅 NM 挂一块可写目录到 /hadoop/dfs/tmp 并 chown，否则 NM 上报 UNHEALTHY（RM 不需要）。
  # 同时预建 mapred 本地目录：Hive(MR 引擎) 提交作业时 AppMaster/Task 的 mapreduce.cluster.local.dir
  # 指向其下，LocalDirAllocator 要求顶层目录已存在且可写。
  NM_VOL_=""
  if [ "${NM_NAME}" = "nodemanager" ]; then
    mkdir -p "{{home_dir}}/yarn/tmp/mapred/local"
    chown -R 1000:1000 "{{home_dir}}/yarn/tmp" 2>/dev/null || true
    chmod -R a+rwx "{{home_dir}}/yarn/tmp" 2>/dev/null || true
    NM_VOL_="      - {{home_dir}}/yarn/tmp:/hadoop/dfs/tmp"
  fi
  cat > "{{home_dir}}/yarn/compose.yml" <<HAEOF
name: ${PROJ_}
services:
  ${NM_NAME}:
    image: {{image}}
    container_name: ${C_}
    restart: always
    network_mode: host
    command: ["yarn", "${NM_NAME}"]
    volumes:
      - {{home_dir}}/hadoop/conf/core-site.xml:/opt/hadoop/etc/hadoop/core-site.xml
      - {{home_dir}}/hadoop/conf/hdfs-site.xml:/opt/hadoop/etc/hadoop/hdfs-site.xml
      - {{home_dir}}/yarn/conf/yarn-site.xml:/opt/hadoop/etc/hadoop/yarn-site.xml
      - {{home_dir}}/hadoop/conf/mapred-site.xml:/opt/hadoop/etc/hadoop/mapred-site.xml
${NM_VOL_}
HAEOF
  migrate_old "${C_}"
  pull_retry "{{home_dir}}/yarn/compose.yml"
  docker compose -f "{{home_dir}}/yarn/compose.yml" up -d --remove-orphans
  if [ "${NM_NAME}" = "resourcemanager" ]; then PORT_=8088; else PORT_=8042; fi
  wait_port "${PORT_}" 20 "${C_}" "YARN ${CN_}"
  echo "[HA-YARN] ${CN_} 已就绪"
}

# ---------------- Spark ----------------
deploy_spark_ha() {
  SPARK_HOME="{{home_dir}}/spark"
  mkdir -p "{{home_dir}}/spark/data"
  # 双 Master 共用 recovery 配置（ZK 选举 + HDFS recovery 目录）
  cat > "{{home_dir}}/spark/spark-defaults.conf" <<SPEOFS
spark.deploy.recoveryMode=ZOOKEEPER
spark.deploy.zookeeper.url=${ZK_IPS}
spark.deploy.recoveryDirectory=${HDFS_ENTRY}/spark/ha
SPEOFS
  if ha_is "${SPARK_MASTER_IP}"; then
    C_=spark-master; CN_="Spark Master1 (active)"
  elif ha_is "{{__spark_m2_ip}}"; then
    C_=spark-master2; CN_="Spark Master2 (standby)"
  else
    C_=spark-worker; CN_="Spark Worker"
  fi
  HOST_ARG="--host ${SELF_IP} --port {{master_port}} --webui-port {{webui_port}}"
  if [ "${C_}" = "spark-worker" ]; then
    cat > "{{home_dir}}/spark/compose.yml" <<'HAEOF'
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
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.worker.Worker", "spark://{{__master_spark}}:{{master_port}},{{__spark_m2_ip}}:{{master_port}}", "--host", "{{__ip}}", "--webui-port", "8081"]
    environment:
      SPARK_WORKER_CORES: "{{worker_cores}}"
      SPARK_WORKER_MEMORY: "{{worker_mem}}"
    volumes:
      - {{home_dir}}/spark/data:/opt/spark/work-dir
      - {{home_dir}}/spark/spark-defaults.conf:/opt/spark/conf/spark-defaults.conf
HAEOF
    PORT_=8081
  else
    cat > "{{home_dir}}/spark/compose.yml" <<HAEOF
name: bigdata-spark
services:
  master:
    image: {{image_spark}}
    container_name: ${C_}
    restart: always
    network_mode: host
    hostname: "{{__name}}"
    extra_hosts:
      - "{{__name}}:{{__ip}}"
    user: root
    command: ["/opt/spark/bin/spark-class", "org.apache.spark.deploy.master.Master", "--host", "${SELF_IP}", "--port", "{{master_port}}", "--webui-port", "{{webui_port}}"]
    volumes:
      - {{home_dir}}/spark/data:/opt/spark/work-dir
      - {{home_dir}}/spark/spark-defaults.conf:/opt/spark/conf/spark-defaults.conf
HAEOF
    PORT_="{{webui_port}}"
  fi
  migrate_old "${C_}"
  pull_retry "{{home_dir}}/spark/compose.yml"
  docker compose -f "{{home_dir}}/spark/compose.yml" up -d --remove-orphans
  wait_port "${PORT_}" 20 "${C_}" "Spark ${CN_}" || true
  echo "[HA-Spark] ${CN_} 已就绪"
}

# ---------------- Flink ----------------
deploy_flink_ha() {
  FLINK_HOME="{{home_dir}}/flink"
  mkdir -p "${FLINK_HOME}"
  # 官方 apache/flink 镜像不含 Hadoop 客户端，无法解析 hdfs:// storageDir。
  # 从已就绪的 hadoop 镜像导出客户端 jar 到宿主机目录，Flink 启动前再拷贝进其 /opt/flink/lib。
  # 用 docker cp（daemon 以 root 写宿主目录）而非 bind-mount+touch：hadoop 镜像默认非 root 用户，
  # 无法向 bind-mount 目录写 .prepared，会触发 set -e 中断。
  # 注意：只补 hadoop 核心 jar + 两个 shaded jar，绝不拷贝 hadoop 的 common/lib、hdfs/lib 通用
  # 依赖（旧版 commons-cli、netty、slf4j 等会覆盖 flink 自带版本，导致 NoSuchMethodError）。
  # flink 官方镜像 lib 已自带 jackson/zookeeper/curator/guava/netty 等绝大多数 hadoop 运行时依赖。
  HADOOP_CLIENT_DIR="${FLINK_HOME}/hadoop-client"
  mkdir -p "${HADOOP_CLIENT_DIR}"
  # v2：改用 hadoop-client-api + hadoop-client-runtime（官方 shaded uber jar，自包含 HDFS 客户端全部依赖）。
  # 旧方案零拷 hadoop-common 等散件 jar，缺类导致 HadoopUtils 初始化失败，.prepared-v2 强制重新导出。
  if [ ! -f "${HADOOP_CLIENT_DIR}/.prepared-v2" ]; then
    echo "[HA-Flink] 从 {{image}} 导出 Hadoop shaded client jar（client-api + client-runtime）..."
    rm -f "${HADOOP_CLIENT_DIR}"/*.jar
    rm -f "${HADOOP_CLIENT_DIR}/.prepared"
    _HC="$(docker create "{{image}}" bash 2>/dev/null)"
    if [ -n "${_HC}" ]; then
      docker cp "${_HC}:/opt/hadoop/share/hadoop/client/." "${HADOOP_CLIENT_DIR}/" 2>/dev/null || true
      # commons-logging 未被 client-runtime shade，Hadoop 初始化必需，单独从 common/lib 导出
      for _cl in $(docker run --rm "{{image}}" sh -c 'ls /opt/hadoop/share/hadoop/common/lib/commons-logging-*.jar 2>/dev/null' 2>/dev/null); do
        docker cp "${_HC}:${_cl}" "${HADOOP_CLIENT_DIR}/" 2>/dev/null || true
      done
      # 只保留 client-api / client-runtime / commons-logging，剔除可能存在的 minicluster/tests 大 jar
      for _f in "${HADOOP_CLIENT_DIR}"/*.jar; do
        case "${_f}" in
          *hadoop-client-api-*.jar|*hadoop-client-runtime-*.jar|*commons-logging-*.jar) ;;
          *) rm -f "${_f}" ;;
        esac
      done
      docker rm -f "${_HC}" >/dev/null 2>&1 || true
    fi
    if [ "$(ls "${HADOOP_CLIENT_DIR}"/hadoop-client-*.jar "${HADOOP_CLIENT_DIR}"/commons-logging-*.jar 2>/dev/null | wc -l)" -lt 3 ]; then
      echo "[HA-Flink] Hadoop client jar 导出失败（需 client-api + client-runtime + commons-logging 三个），中止"
      exit 1
    fi
    : > "${HADOOP_CLIENT_DIR}/.prepared-v2"
    echo "[HA-Flink] Hadoop client jar 已导出（$(ls "${HADOOP_CLIENT_DIR}"/*.jar 2>/dev/null | wc -l) 个：client-api + client-runtime + commons-logging）"
  fi
  if ha_is "${JM_IP}"; then C_=flink-jobmanager; CN_="Flink JM1 (active)"; ROLE_=jm
  elif ha_is "${FLINK_JM2}"; then C_=flink-jobmanager2; CN_="Flink JM2 (standby)"; ROLE_=jm
  else C_=flink-taskmanager; CN_="Flink TaskManager"; ROLE_=tm; fi
  if [ "${ROLE_}" = "tm" ]; then
    # HA 模式下 JM 的 RPC 端口是随机的（真实地址经 ZK leader 发布），TM 必须同样开启 HA 才能解析到 JM；
    # 否则 TM 会一直连 jobmanager.rpc.address:port（6123）被拒、永远注册不上。
    cat > "${FLINK_HOME}/compose.yml" <<HAEOF
name: bigdata-flink
services:
  taskmanager:
    image: {{image_flink}}
    container_name: flink-taskmanager
    restart: always
    network_mode: host
    command: sh -c 'cp -f /hadoop-client/*.jar /opt/flink/lib/ 2>/dev/null || true; exec /docker-entrypoint.sh taskmanager'
    volumes:
      - ${HADOOP_CLIENT_DIR}:/hadoop-client:ro
      - {{home_dir}}/hadoop/conf:/opt/flink-hadoop-conf:ro
    environment:
      HADOOP_CONF_DIR: /opt/flink-hadoop-conf
      FLINK_PROPERTIES: |
        jobmanager.rpc.address: ${JM_IP}
        jobmanager.rpc.port: {{jm_rpc_port}}
        taskmanager.host: ${SELF_IP}
        taskmanager.bind-host: 0.0.0.0
        taskmanager.numberOfTaskSlots: {{tm_slots}}
        high-availability.type: zookeeper
        high-availability.zookeeper.quorum: ${ZK_IPS}
        high-availability.storageDir: ${HDFS_ENTRY}/flink/ha
        high-availability.cluster-id: bigdata-flink
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
    command: sh -c 'cp -f /hadoop-client/*.jar /opt/flink/lib/ 2>/dev/null || true; exec /docker-entrypoint.sh jobmanager'
    volumes:
      - ${HADOOP_CLIENT_DIR}:/hadoop-client:ro
      - {{home_dir}}/hadoop/conf:/opt/flink-hadoop-conf:ro
    environment:
      HADOOP_CONF_DIR: /opt/flink-hadoop-conf
      FLINK_PROPERTIES: |
        jobmanager.rpc.address: ${SELF_IP}
        jobmanager.rpc.port: {{jm_rpc_port}}
        jobmanager.bind-host: 0.0.0.0
        rest.address: ${SELF_IP}
        rest.bind-address: 0.0.0.0
        rest.port: 8081
        high-availability.type: zookeeper
        high-availability.zookeeper.quorum: ${ZK_IPS}
        high-availability.storageDir: ${HDFS_ENTRY}/flink/ha
        high-availability.cluster-id: bigdata-flink
HAEOF
    PORT_=8081
  fi
  migrate_old "${C_}"
  pull_retry "${FLINK_HOME}/compose.yml"
  docker compose -f "${FLINK_HOME}/compose.yml" up -d --remove-orphans
  wait_port "${PORT_}" 20 "${C_}" "Flink ${CN_}" || true
  echo "[HA-Flink] ${CN_} 已就绪"
}

# ---------------- HBase ----------------
deploy_hbase_ha() {
  HBASE_HOME="{{home_dir}}/hbase"
  mkdir -p "{{home_dir}}/hbase/conf" "{{home_dir}}/hbase/data"
  if ha_is "${HMASTER_IP}"; then
    C_=hbase-master; CN_="HMaster1 (active)"
  elif ha_is "${HMASTER2}"; then
    C_=hbase-backup-master; CN_="HMaster2 (backup)"
  else
    C_=hbase-regionserver; CN_="RegionServer"
  fi
  if [ "${C_}" = "hbase-regionserver" ]; then
    cat > "{{home_dir}}/hbase/compose.yml" <<'HAEOF'
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
HAEOF
    PORT_=16030
  else
    cat > "{{home_dir}}/hbase/compose.yml" <<HAEOF
name: bigdata-hbase
services:
  master:
    image: {{image_hbase}}
    container_name: ${C_}
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
HAEOF
    PORT_=16010
  fi
  migrate_old "${C_}"
  pull_retry "{{home_dir}}/hbase/compose.yml"
  docker compose -f "{{home_dir}}/hbase/compose.yml" up -d --remove-orphans
  wait_port "${PORT_}" 40 "${C_}" "HBase ${CN_}"
  echo "[HA-HBase] ${CN_} 已就绪"
}

# ---------------- Hive（双 MS/HS2 + metastore_db） ----------------
chunk_at() { # 取逗号列表第 N 项（1 起）
  echo "${1}" | cut -d, -f"${2}"
}
deploy_metastore_db() {
  [ "${SELF_IP}" = "${HIVE_DB}" ] || return 0
  DB_HOME="{{home_dir}}/hive/metadb"
  mkdir -p "{{home_dir}}/hive/metadb/data"
  cat > "{{home_dir}}/hive/metadb/compose.yml" <<'DBEOF'
name: bigdata-hive-metadb
services:
  mysql:
    image: {{hive_db_image}}
    container_name: hive-metastore-db
    restart: always
    network_mode: host
    environment:
      MYSQL_ROOT_PASSWORD: "{{hive_db_password}}"
      MYSQL_DATABASE: "metastore"
    volumes:
      - {{home_dir}}/hive/metadb/data:/var/lib/mysql
DBEOF
  migrate_old "hive-metastore-db"
  pull_retry "{{home_dir}}/hive/metadb/compose.yml"
  docker compose -f "{{home_dir}}/hive/metadb/compose.yml" up -d
  wait_port 3306 60 "hive-metastore-db" "Hive MetaDB"
  echo "[HA-Hive] metastore_db (MySQL) 已就绪 @${SELF_IP}:3306"
}
deploy_hive_ha() {
  # node.sh 已在本机写下 conf/hive-site.xml（HIVE_SITE 占位注入），此处仅起容器
  MS1=$(chunk_at "${MSS}" 1); MS2=$(chunk_at "${MSS}" 2)
  HS1=$(chunk_at "${HS2S}" 1); HS2=$(chunk_at "${HS2S}" 2)
  deploy_metastore_db
  HIVE_HOME="{{home_dir}}/hive"
  mkdir -p "{{home_dir}}/hive/conf" "{{home_dir}}/hive/data"
  # apache/hive:4.0.0 官方镜像不含 MySQL Connector/J，metastore 连 MySQL 必需；
  # 仅承载 Hive 实例的机器需要（下载判定在 CONTAINER_ARR 之后），且必须以单文件挂载——
  # 若把宿主机 lib 目录整体挂到 /opt/hive/lib，会遮盖镜像自带的 hive-exec 等全部 jar
  MYSQL_JAR_NAME="mysql-connector-j-8.4.0.jar"
  CONTAINER_ARR=()
  [ -n "${MS1}" ] && ha_is "${MS1}" && CONTAINER_ARR+=("hive-metastore|metastore|metastore")
  [ -n "${MS2}" ] && ha_is "${MS2}" && CONTAINER_ARR+=("hive-metastore2|metastore|metastore")
  [ -n "${HS1}" ] && ha_is "${HS1}" && CONTAINER_ARR+=("hive-hiveserver2|hiveserver2|hiveserver2")
  [ -n "${HS2}" ] && ha_is "${HS2}" && CONTAINER_ARR+=("hive-hiveserver2-2|hiveserver2|hiveserver2")
  if [ ${#CONTAINER_ARR[@]} -eq 0 ]; then
    echo "[HA-Hive] 本机 ${SELF_IP} 不承载 Hive 实例"
    return 0
  fi
  mkdir -p "{{home_dir}}/hive/lib"
  if [ ! -f "{{home_dir}}/hive/lib/${MYSQL_JAR_NAME}" ]; then
    echo "[HA-Hive] 下载 MySQL Connector/J ..."
    if ! curl -fsSL -o "{{home_dir}}/hive/lib/${MYSQL_JAR_NAME}" \
        "https://maven.aliyun.com/repository/public/com/mysql/mysql-connector-j/8.4.0/${MYSQL_JAR_NAME}"; then
      if ! curl -fsSL -o "{{home_dir}}/hive/lib/${MYSQL_JAR_NAME}" \
          "https://repo1.maven.org/maven2/com/mysql/mysql-connector-j/8.4.0/${MYSQL_JAR_NAME}"; then
        echo "[HA-Hive] MySQL 驱动下载失败（aliyun/repo1 均不可达），无法连接 metastore_db"
        return 1
      fi
    fi
  fi
  : > "{{home_dir}}/hive/compose.yml"
  printf 'name: bigdata-hive\nservices:\n' >> "{{home_dir}}/hive/compose.yml"
  for item in "${CONTAINER_ARR[@]}"; do
    IFS='|' read -r C SVC LBL <<< "${item}"
    cat >> "{{home_dir}}/hive/compose.yml" <<HAEOF
  ${SVC}:
    image: {{image_hive}}
    container_name: ${C}
    restart: always
    network_mode: host
    environment:
      SERVICE_NAME: ${LBL}
      DB_DRIVER: mysql
      # Metastore/HS2 需访问 HDFS（warehouse），ns1 的 HA 映射在 Hadoop 配置里，仅 hive-site 不够
      HADOOP_CONF_DIR: /opt/hadoop-conf
    volumes:
      - {{home_dir}}/hadoop/conf:/opt/hadoop-conf:ro
      - {{home_dir}}/hive/conf/hive-site.xml:/opt/hive/conf/hive-site.xml
      - {{home_dir}}/hive/lib/${MYSQL_JAR_NAME}:/opt/hive/lib/${MYSQL_JAR_NAME}:ro
      - {{home_dir}}/hive/data/warehouse:/opt/hive/data/warehouse
HAEOF
  done
  migrate_old "${C}"
  pull_retry "{{home_dir}}/hive/compose.yml"
  docker compose -f "{{home_dir}}/hive/compose.yml" up -d --remove-orphans
  # 探活失败必须中止（node.sh set -e），否则端口未就绪会被静默吞掉、部署误报成功
  if [ -n "${MS1}" ] && ha_is "${MS1}"; then
    wait_port 9083 60 "hive-metastore" "Hive Metastore"
  fi
  if [ -n "${HS1}" ] && ha_is "${HS1}"; then
    wait_port 10000 140 "hive-hiveserver2" "HiveServer2"
  fi
  # 首次初始化 metastore schema（幂等：MS1 主机执行）
  if [ -n "${MS1}" ] && ha_is "${MS1}"; then
    if docker exec hive-metastore-db mysql -uroot -p"${PASSWORD_PW}" -e "SELECT 1 FROM metastore.bucketing_cols LIMIT 1" >/dev/null 2>&1; then
      echo "[HA-Hive] metastore schema 已初始化"
    else
      echo "[HA-Hive] 初始化 metastore schema（schematool）..."
      docker run --rm \
        -v "{{home_dir}}/hive/conf/hive-site.xml:/opt/hive/conf/hive-site.xml" \
        -v "{{home_dir}}/hive/lib/${MYSQL_JAR_NAME}:/opt/hive/lib/${MYSQL_JAR_NAME}:ro" \
        -v "{{home_dir}}/hadoop/conf:/opt/hadoop-conf:ro" \
        -e "HADOOP_CONF_DIR=/opt/hadoop-conf" \
        {{image_hive}} schematool -dbType mysql -initSchema --verbose || true
    fi
  fi
  echo "[HA-Hive] 本机 Hive 容器已就绪：${C}"
}