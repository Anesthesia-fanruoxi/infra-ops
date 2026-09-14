#!/bin/bash
set -e

# Kafka 传统模式节点部署脚本：每台部署 ZooKeeper 节点（ensemble）+ Kafka broker，共 2 容器
# 模板参数:  {{image}} {{zk_image}} {{mem}} {{port}} {{zk_port}} {{home_dir}}
# 内置变量:  {{__ip}} {{__seq}} {{__node_ips}} {{__nodes}} {{__cluster_size}}
# 行为:     ZK 的 server.N 与 myid 按 __node_ips 顺序自动生成（N = 序号）；2888/3888 为
#           ensemble 内部端口（宿主机直通）；kafka 的 zookeeper.connect 自动指向全部 ZK 成员；
#           advertised.listeners 指向本机宿主机 IP；内部 topic 副本数自动取 min(3,N)。

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

# ==== 模板插值参数 ====
IMAGE="{{image}}"
ZK_IMAGE="{{zk_image}}"
MEM="{{mem}}"
PORT="{{port}}"
ZK_PORT="{{zk_port}}"
HOME_DIR="{{home_dir}}"
ENABLE_UI="{{enable_ui}}"
UI_IMAGE="{{ui_image}}"
UI_PORT="{{ui_port}}"
SELF_IP="{{__ip}}"
SELF_ID="{{__seq}}"
NODE_IPS="{{__node_ips}}"
CLUSTER_SIZE="{{__cluster_size}}"
ZK_CONTAINER="kafka-zk-${SELF_ID}"
CONTAINER="kafka-${SELF_ID}"
NET="middleware_net"

[ -n "${IMAGE}" ] || IMAGE="apache/kafka:3.7.0"
[ -n "${ZK_IMAGE}" ] || ZK_IMAGE="zookeeper:3.9"
[ -n "${MEM}" ] || MEM="1g"
[ -n "${PORT}" ] || PORT=9092
[ -n "${ZK_PORT}" ] || ZK_PORT=2181
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${NODE_IPS}" ] || { echo "成员 IP 列表为空"; exit 1; }
[ -n "${CLUSTER_SIZE}" ] || CLUSTER_SIZE=1

# ==== 内部 topic 副本数：min(3, N) / min(2, N) ====
if [ "${CLUSTER_SIZE}" -ge 3 ]; then RF=3; else RF=${CLUSTER_SIZE}; fi
if [ "${CLUSTER_SIZE}" -ge 2 ]; then MIN_ISR=2; else MIN_ISR=1; fi

# ==== 生成 ZK 成员列表与 kafka 连接串 ====
ZK_SERVERS=""
ZK_CONNECT=""
i=1
IFS=',' read -ra IPS <<< "${NODE_IPS}"
for ip in "${IPS[@]}"; do
  ip_trim=$(echo "${ip}" | xargs)
  [ -n "${ip_trim}" ] || continue
  if [ -n "${ZK_SERVERS}" ]; then ZK_SERVERS="${ZK_SERVERS}
"; fi
  ZK_SERVERS="${ZK_SERVERS}server.${i}=${ip_trim}:2888:3888"
  if [ -n "${ZK_CONNECT}" ]; then ZK_CONNECT="${ZK_CONNECT},"; fi
  ZK_CONNECT="${ZK_CONNECT}${ip_trim}:${ZK_PORT}"
  i=$((i+1))
done
[ -n "${ZK_SERVERS}" ] || { echo "生成 ZK 成员列表失败"; exit 1; }

# ==== KafkaUI bootstrap 列表：所有节点 ip:PORT（首节点可选部署） ====
BOOTSTRAP_LIST=""
if [ "${ENABLE_UI}" = "yes" ]; then
  BOOTSTRAP_LIST="$(echo "${NODE_IPS}" | sed "s/,/:${PORT},/g")"
  BOOTSTRAP_LIST="${BOOTSTRAP_LIST}:${PORT}"
fi

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 目录与权限（两个镜像内均 uid=1000） ====
mkdir -p "${HOME_DIR}/zk-data" "${HOME_DIR}/zk-datalog" "${HOME_DIR}/data"
echo "${SELF_ID}" > "${HOME_DIR}/zk-data/myid"
chown -R 1000:0 "${HOME_DIR}/zk-data" "${HOME_DIR}/zk-datalog" "${HOME_DIR}/data" 2>/dev/null || \
  chmod -R 777 "${HOME_DIR}/zk-data" "${HOME_DIR}/zk-datalog" "${HOME_DIR}/data"

# ==== 生成 zoo.cfg ====
umask 077
cat > "${HOME_DIR}/zoo.cfg" <<EOF
clientPort=2181
dataDir=/data
dataLogDir=/datalog
tickTime=2000
initLimit=10
syncLimit=5
${ZK_SERVERS}
EOF

# ==== 生成 kafka server.properties ====
cat > "${HOME_DIR}/server.properties" <<EOF
broker.id=${SELF_ID}
listeners=PLAINTEXT://0.0.0.0:9092
advertised.listeners=PLAINTEXT://${SELF_IP}:${PORT}
listener.security.protocol.map=PLAINTEXT:PLAINTEXT
zookeeper.connect=${ZK_CONNECT}
log.dirs=/var/lib/kafka/data
num.partitions=3
offsets.topic.replication.factor=${RF}
transaction.state.log.replication.factor=${RF}
transaction.state.log.min.isr=${MIN_ISR}
group.initial.rebalance.delay.ms=0
auto.create.topics.enable=true
EOF
umask 022
# zookeeper / apache/kafka 镜像均以 uid 1000 运行
chown 1000:0 "${HOME_DIR}/zoo.cfg" "${HOME_DIR}/server.properties" 2>/dev/null || true
chmod 644 "${HOME_DIR}/zoo.cfg" "${HOME_DIR}/server.properties"

# ==== 迁移旧版 docker run 容器（数据保留） ====
for cname in "${ZK_CONTAINER}" "${CONTAINER}"; do
  if docker ps -a --format '{{.Names}}' | grep -qx "${cname}"; then
    PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${cname}" 2>/dev/null || true)"
    if [ -z "${PROJ}" ]; then
      echo "检测到旧版 docker run 容器 ${cname}，迁移为 compose 管理（数据保留）"
      docker rm -f "${cname}" &>/dev/null || true
    fi
  fi
done

# ==== 生成 compose.yml ====
cat > "${HOME_DIR}/compose.yml" <<YAMLEOF
name: kafka-zk
services:
  zookeeper:
    image: ${ZK_IMAGE}
    container_name: ${ZK_CONTAINER}
    restart: always
    ports:
      - "${ZK_PORT}:2181"
      - "2888:2888"
      - "3888:3888"
    environment:
      - JVMFLAGS=-Xmx512m
    volumes:
      - ${HOME_DIR}/zoo.cfg:/conf/zoo.cfg:ro
      - ${HOME_DIR}/zk-data:/data
      - ${HOME_DIR}/zk-datalog:/datalog
    networks:
      - ${NET}
  kafka:
    image: ${IMAGE}
    container_name: ${CONTAINER}
    restart: always
    command: ["/opt/kafka/bin/kafka-server-start.sh", "/etc/kafka/server.properties"]
    ports:
      - "${PORT}:9092"
    environment:
      - KAFKA_HEAP_OPTS=-Xms${MEM} -Xmx${MEM}
    volumes:
      - ${HOME_DIR}/server.properties:/etc/kafka/server.properties:ro
      - ${HOME_DIR}/data:/var/lib/kafka/data
    depends_on:
      - zookeeper
    networks:
      - ${NET}

networks:
  ${NET}:
    external: true
YAMLEOF

# ==== 拉取镜像 ====
echo "拉取镜像 ${ZK_IMAGE} / ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull

# ==== 启动（compose 内置 depends_on 顺序） ====
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== ZK 就绪检查 ====
ZK_READY=0
for _ in $(seq 1 40); do
  if (echo ruok > /dev/tcp/127.0.0.1/${ZK_PORT}) 2>/dev/null; then ZK_READY=1; break; fi
  sleep 3
done
if [ "${ZK_READY}" != "1" ]; then
  echo "ZooKeeper 节点启动超时，最近日志:"
  docker logs "${ZK_CONTAINER}" --tail 40 2>&1 || true
  exit 1
fi
echo "ZooKeeper 节点已就绪（myid=${SELF_ID}）"

# ==== Kafka 就绪检查 ====
READY=0
for _ in $(seq 1 60); do
  if docker exec "${CONTAINER}" /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092 &>/dev/null; then READY=1; break; fi
  sleep 5
done
if [ "${READY}" != "1" ]; then
  echo "Kafka 节点启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 40 2>&1 || true
  exit 1
fi

# ==== 结果输出 ====
echo "Kafka（ZooKeeper 模式）节点已就绪"
echo "Kafka:     ${SELF_IP}:${PORT}（broker.id=${SELF_ID}，数据目录: ${HOME_DIR}/data）"
echo "ZooKeeper: ${SELF_IP}:${ZK_PORT}（myid=${SELF_ID}，ensemble: ${ZK_CONNECT}）"
echo "Bootstrap servers: ${NODE_IPS}" | sed "s/,/:${PORT},/g" | sed "s/$/:${PORT}/"
echo "compose: ${HOME_DIR}/compose.yml（改后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"

# ==== 可选 KafkaUI（仅首节点部署一次，连接整个集群） ====
if [ "${ENABLE_UI}" = "yes" ] && [ "${SELF_ID}" = "1" ]; then
  UI_CONTAINER="kafka-ui"
  UI_HOME="${HOME_DIR}-ui"
  mkdir -p "${UI_HOME}"

  if docker ps -a --format '{{.Names}}' | grep -qx "${UI_CONTAINER}"; then
    UI_PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${UI_CONTAINER}" 2>/dev/null || true)"
    if [ -z "${UI_PROJ}" ]; then
      echo "检测到旧版 kafka-ui 容器，迁移为 compose 管理"
      docker rm -f "${UI_CONTAINER}" &>/dev/null || true
    fi
  fi

  cat > "${UI_HOME}/compose.yml" <<UIEOF
name: kafka-ui
services:
  kafka-ui:
    image: ${UI_IMAGE}
    container_name: ${UI_CONTAINER}
    restart: always
    ports:
      - "${UI_PORT}:8080"
    environment:
      - KAFKA_CLUSTERS_0_NAME=kafka-cluster
      - KAFKA_CLUSTERS_0_BOOTSTRAPSERVERS=${BOOTSTRAP_LIST}
      - KAFKA_CLUSTERS_0_ZOOKEEPER=
      - DYNAMIC_CONFIG_ENABLED=true
    networks:
      - ${NET}

networks:
  ${NET}:
    external: true
UIEOF

  echo "拉取 KafkaUI 镜像 ${UI_IMAGE}"
  docker compose -f "${UI_HOME}/compose.yml" pull
  docker compose -f "${UI_HOME}/compose.yml" up -d

  UI_READY=0
  for _ in $(seq 1 30); do
    if docker exec "${UI_CONTAINER}" sh -c 'wget -qO- http://localhost:8080/actuator/health 2>/dev/null | grep -q UP' || \
       docker exec "${UI_CONTAINER}" sh -c 'curl -sf http://localhost:8080/actuator/health 2>/dev/null | grep -q UP'; then UI_READY=1; break; fi
    sleep 3
  done
  [ "${UI_READY}" = "1" ] || echo "警告: KafkaUI 未检测到健康状态，请检查日志（不影响 Kafka 本身）"
  echo "KafkaUI 已部署: http://${SELF_IP}:${UI_PORT}（连接集群: ${BOOTSTRAP_LIST}）"
  echo "KafkaUI compose: ${UI_HOME}/compose.yml"
fi