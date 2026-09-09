#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

CLUSTER_NAME="{{cluster_name}}"
BROKER_NAME="{{broker_name}}"
BROKER_ID="{{__broker_id}}"
BROKER_PORT="{{broker_port}}"
NAMESRV_ADDR="{{namesrv_addr}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
ROLE="{{__role}}"
SELF_IP="{{__ip}}"
CONTAINER="rmq-broker-${CLUSTER_NAME}-${BROKER_NAME}-${BROKER_ID}"

[ -n "${CLUSTER_NAME}" ] || { echo "集群名不能为空"; exit 1; }
[ -n "${BROKER_NAME}" ] || { echo "broker 组名不能为空"; exit 1; }
[ -n "${BROKER_ID}" ] || BROKER_ID=0
[ -n "${BROKER_PORT}" ] || BROKER_PORT=10911
[ -n "${NAMESRV_ADDR}" ] || { echo "namesrvAddr 不能为空（填全部 namesrv 的 ip:端口，逗号分隔）"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="apache/rocketmq:4.9.7"

mkdir -p "${HOME_DIR}/store" "${HOME_DIR}/logs" "${HOME_DIR}/conf"
chown -R 3000:3000 "${HOME_DIR}/store" "${HOME_DIR}/logs" "${HOME_DIR}/conf" 2>/dev/null || true

umask 077
if [ "${ROLE}" = "master" ] || [ "${BROKER_ID}" = "0" ]; then
  cat > "${HOME_DIR}/conf/broker.conf" <<'CONFEOF'
@@BROKER_MASTER_CONF@@
CONFEOF
else
  cat > "${HOME_DIR}/conf/broker.conf" <<'CONFEOF'
@@BROKER_SLAVE_CONF@@
CONFEOF
fi
umask 022
chown 3000:3000 "${HOME_DIR}/conf/broker.conf" 2>/dev/null || true
chmod 644 "${HOME_DIR}/conf/broker.conf"

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧容器，迁移为 compose 管理"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: rmq-broker
services:
  broker:
    image: {{image}}
    container_name: rmq-broker-{{cluster_name}}-{{broker_name}}-{{__broker_id}}
    restart: always
    network_mode: host
    environment:
      - JAVA_OPT_EXT=-server -Xms512m -Xmx512m -Xmn256m
    volumes:
      - {{home_dir}}/store:/home/rocketmq/store
      - {{home_dir}}/logs:/home/rocketmq/logs
      - {{home_dir}}/conf/broker.conf:/home/rocketmq/conf/broker.conf:ro
    command: ["sh", "mqbroker", "-c", "/home/rocketmq/conf/broker.conf"]
YAMLEOF

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

READY=0
for _ in $(seq 1 50); do
  if docker exec "${CONTAINER}" bash -c "</dev/tcp/127.0.0.1/${BROKER_PORT}" 2>/dev/null; then READY=1; break; fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Broker 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 40 2>&1 || true
  exit 1
fi

echo "RocketMQ Broker 已就绪 → 集群:${CLUSTER_NAME} / 组:${BROKER_NAME} / id:${BROKER_ID}"
echo "监听: ${SELF_IP}:${BROKER_PORT}（host 网络，brokerIP1=${SELF_IP}）"
echo "namesrvAddr: ${NAMESRV_ADDR}"
echo "compose: ${HOME_DIR}/compose.yml"
