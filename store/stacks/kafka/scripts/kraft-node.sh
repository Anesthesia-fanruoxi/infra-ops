#!/bin/bash
set -e

# Kafka 3.x KRaft 集群节点部署脚本：broker+controller 混部，免 ZooKeeper
# 模板参数:  {{image}} {{mem}} {{port}} {{controller_port}} {{cluster_id}} {{home_dir}}
# 内置变量:  {{__ip}} {{__seq}} {{__node_ips}} {{__nodes}} {{__cluster_size}}
# 行为:     controller.quorum.voters 由 __node_ips 按序号自动生成（1@ip1:port,2@ip2:port,...）；
#           node.id = __seq；advertised.listeners 指向本机宿主机 IP，跨主机/客户端均经宿主机端口互通；
#           首次启动前本地 format 存储（--ignore-formatted 可重入），内部 topic 副本数自动取 min(3,N)。

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

# ==== 模板插值参数 ====
IMAGE="{{image}}"
MEM="{{mem}}"
PORT="{{port}}"
CONTROLLER_PORT="{{controller_port}}"
CLUSTER_ID="{{cluster_id}}"
HOME_DIR="{{home_dir}}"
ENABLE_UI="{{enable_ui}}"
UI_IMAGE="{{ui_image}}"
UI_PORT="{{ui_port}}"
SELF_IP="{{__ip}}"
SELF_ID="{{__seq}}"
NODE_IPS="{{__node_ips}}"
VOTER_IPS="{{__voter_ips}}"
PROCESS_ROLES="{{__process_roles}}"
CLUSTER_SIZE="{{__cluster_size}}"
CONTAINER="kafka-${SELF_ID}"
NET="middleware_net"

[ -n "${IMAGE}" ] || IMAGE="apache/kafka:3.7.0"
[ -n "${MEM}" ] || MEM="1g"
[ -n "${PORT}" ] || PORT=9092
[ -n "${CONTROLLER_PORT}" ] || CONTROLLER_PORT=9093
[ -n "${CLUSTER_ID}" ] || { echo "集群 ID 不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${NODE_IPS}" ] || { echo "成员 IP 列表为空"; exit 1; }
[ -n "${VOTER_IPS}" ] || VOTER_IPS="${NODE_IPS}"
[ -n "${PROCESS_ROLES}" ] || PROCESS_ROLES="broker,controller"
[ -n "${CLUSTER_SIZE}" ] || CLUSTER_SIZE=1

# ==== 内部 topic 副本数：min(3, N) / min(2, N) ====
if [ "${CLUSTER_SIZE}" -ge 3 ]; then RF=3; else RF=${CLUSTER_SIZE}; fi
if [ "${CLUSTER_SIZE}" -ge 2 ]; then MIN_ISR=2; else MIN_ISR=1; fi

# ==== controller.quorum.voters：按仲裁成员 IP 顺序生成 1@ip:port,... ====
VOTERS=""
i=1
IFS=',' read -ra IPS <<< "${VOTER_IPS}"
for ip in "${IPS[@]}"; do
  ip_trim=$(echo "${ip}" | xargs)
  [ -n "${ip_trim}" ] || continue
  if [ -n "${VOTERS}" ]; then VOTERS="${VOTERS},"; fi
  VOTERS="${VOTERS}${i}@${ip_trim}:${CONTROLLER_PORT}"
  i=$((i+1))
done
[ -n "${VOTERS}" ] || { echo "生成 controller.quorum.voters 失败"; exit 1; }

# ==== KafkaUI bootstrap 列表：所有节点 ip:PORT（首节点可选部署） ====
BOOTSTRAP_LIST=""
if [ "${ENABLE_UI}" = "yes" ]; then
  BOOTSTRAP_LIST="$(echo "${NODE_IPS}" | sed "s/,/:${PORT},/g")"
  BOOTSTRAP_LIST="${BOOTSTRAP_LIST}:${PORT}"
fi

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 目录与权限（apache/kafka 镜像内 uid=1000） ====
mkdir -p "${HOME_DIR}/data"
chown -R 1000:0 "${HOME_DIR}/data" 2>/dev/null || chmod -R 777 "${HOME_DIR}/data"

# ==== 生成 server.properties ====
umask 077
cat > "${HOME_DIR}/server.properties" <<EOF
process.roles=${PROCESS_ROLES}
node.id=${SELF_ID}
controller.quorum.voters=${VOTERS}
listeners=PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093
advertised.listeners=PLAINTEXT://${SELF_IP}:${PORT}
inter.broker.listener.name=PLAINTEXT
controller.listener.names=CONTROLLER
listener.security.protocol.map=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT
log.dirs=/var/lib/kafka/data
num.partitions=3
default.replication.factor=${RF}
offsets.topic.replication.factor=${RF}
transaction.state.log.replication.factor=${RF}
transaction.state.log.min.isr=${MIN_ISR}
group.initial.rebalance.delay.ms=0
auto.create.topics.enable=true
EOF
umask 022
# apache/kafka 镜像以 uid 1000 运行；root:600 会导致无法读取 server.properties
chown 1000:0 "${HOME_DIR}/server.properties" 2>/dev/null || true
chmod 644 "${HOME_DIR}/server.properties"

# ==== 迁移旧版 docker run 容器（数据保留） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose.yml ====
cat > "${HOME_DIR}/compose.yml" <<YAMLEOF
name: kafka-kraft
services:
  kafka:
    image: ${IMAGE}
    container_name: ${CONTAINER}
    restart: always
    command: ["/opt/kafka/bin/kafka-server-start.sh", "/etc/kafka/server.properties"]
    ports:
      - "${PORT}:9092"
      - "${CONTROLLER_PORT}:9093"
    environment:
      - KAFKA_HEAP_OPTS=-Xms${MEM} -Xmx${MEM}
    volumes:
      - ${HOME_DIR}/server.properties:/etc/kafka/server.properties:ro
      - ${HOME_DIR}/data:/var/lib/kafka/data
    networks:
      - ${NET}

networks:
  ${NET}:
    external: true
YAMLEOF

# ==== 拉取镜像 + 本地 format 存储（幂等） ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
echo "格式化 Kafka 存储（cluster_id=${CLUSTER_ID}）"
docker run --rm \
  -v "${HOME_DIR}/server.properties:/etc/kafka/server.properties:ro" \
  -v "${HOME_DIR}/data:/var/lib/kafka/data" \
  "${IMAGE}" \
  /opt/kafka/bin/kafka-storage.sh format -t "${CLUSTER_ID}" -c /etc/kafka/server.properties --ignore-formatted

# ==== 启动 ====
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 就绪检查（broker API 可达即视为就绪） ====
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
echo "Kafka KRaft 节点已就绪"
echo "节点: node.id=${SELF_ID} / 数据目录: ${HOME_DIR}/data"
echo "Bootstrap servers: ${NODE_IPS}" | sed "s/,/:${PORT},/g" | sed "s/$/:${PORT}/"
echo "本机接入点: ${SELF_IP}:${PORT}（Controller: ${SELF_IP}:${CONTROLLER_PORT}）"
echo "compose: ${HOME_DIR}/compose.yml（改后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"

# ==== 可选 KafkaUI（仅首节点部署一次，连接整个集群） ====
if [ "${ENABLE_UI}" = "yes" ] && [ "${SELF_ID}" = "1" ]; then
  UI_CONTAINER="kafka-ui"
  UI_HOME="${HOME_DIR}-ui"
  mkdir -p "${UI_HOME}"

  # 迁移旧版 docker run 容器
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