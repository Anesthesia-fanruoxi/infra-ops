#!/bin/bash
set -e

# RocketMQ 集群一步成型节点部署脚本：
#   - 首节点（is_bootstrap=true，即指定 Master）部署 NameServer + Broker Master（brokerId=0）
#   - 其余节点自动作为同一 brokerName 组的 Broker Slave（brokerId=__seq）加入，
#     并自动以「首节点 IP:ns_port」作为 namesrvAddr（无需手工分两步部署）
#   - 全部容器使用 host 网络：brokerIP1 取本机 ip，确保跨主机客户端/主备可寻址
#   - 监督校验：轮询 namesrv/broker 端口就绪，并经 mqadmin clusterList 验证本 broker 已注册到 namesrv
# 模板参数: {{image}} {{cluster_name}} {{broker_name}} {{ns_port}} {{broker_port}} {{home_dir}}
# 内置变量: {{__ip}} {{__seq}} {{__role}} {{__master_ip}} {{__is_bootstrap}} {{__broker_id}}

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

# ==== 模板插值参数 ====
IMAGE="{{image}}"
CLUSTER_NAME="{{cluster_name}}"
BROKER_NAME="{{broker_name}}"
NS_PORT="{{ns_port}}"
BROKER_PORT="{{broker_port}}"
HOME_DIR="{{home_dir}}"
SELF_IP="{{__ip}}"
SELF_ID="{{__seq}}"
ROLE="{{__role}}"
MASTER_IP="{{__master_ip}}"
IS_BOOTSTRAP="{{__is_bootstrap}}"
BROKER_ID="{{__broker_id}}"

[ -n "${IMAGE}" ] || IMAGE="apache/rocketmq:4.9.7"
[ -n "${CLUSTER_NAME}" ] || { echo "集群名不能为空"; exit 1; }
[ -n "${BROKER_NAME}" ] || { echo "broker 组名不能为空"; exit 1; }
[ -n "${NS_PORT}" ] || NS_PORT=9876
[ -n "${BROKER_PORT}" ] || BROKER_PORT=10911
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${MASTER_IP}" ] || MASTER_IP="${SELF_IP}"
[ -n "${BROKER_ID}" ] || BROKER_ID="${SELF_ID}"
# 首节点（is_bootstrap=true）的 brokerId 恒为 0
if [ "${IS_BOOTSTRAP}" = "true" ]; then BROKER_ID="0"; fi

echo "=== RocketMQ 集群一节点: 集群=${CLUSTER_NAME}/组=${BROKER_NAME} 本机=${SELF_IP} master=${IS_BOOTSTRAP} brokerId=${BROKER_ID} ==="
mkdir -p "${HOME_DIR}/conf" "${HOME_DIR}/store/namesrv" "${HOME_DIR}/logs/namesrv" "${HOME_DIR}/logs/broker"
# apache/rocketmq 镜像以 uid 3000 运行
chown -R 3000:3000 "${HOME_DIR}/store" "${HOME_DIR}/logs" "${HOME_DIR}/conf" 2>/dev/null || true

# ==== 迁移旧版 docker run 容器 ====
migrate_old() {
  local C="$1"
  if docker ps -a --format '{{.Names}}' | grep -qx "${C}"; then
    local PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${C}" 2>/dev/null || true)"
    if [ -z "${PROJ}" ]; then
      echo "检测到旧容器（非 compose 管理）: ${C}，移除后重建"
      docker rm -f "${C}" &>/dev/null || true
    fi
  fi
}
wait_tcp() { # wait_tcp <ip> <port> <容器> <名称> <次数>
  local IP="$1" PORT="$2" C="$3" NAME="$4" TIMES="$5" READY=0
  for _ in $(seq 1 "${TIMES}"); do
    if docker exec "${C}" bash -c "</dev/tcp/${IP}/${PORT}" 2>/dev/null; then READY=1; break; fi
    sleep 2
  done
  if [ "${READY}" != "1" ]; then
    echo "${NAME} 端口 ${IP}:${PORT} 未就绪，最近日志:"
    docker logs "${C}" --tail 40 2>&1 || true
    exit 1
  fi
}

# ================= NameServer（仅首节点） =================
if [ "${IS_BOOTSTRAP}" = "true" ]; then
  NS_CONTAINER="rmq-namesrv-${CLUSTER_NAME}"
  migrate_old "${NS_CONTAINER}"
  cat > "${HOME_DIR}/namesrv-compose.yml" <<YAMLEOF
name: rmq-${CLUSTER_NAME}-namesrv
services:
  namesrv:
    image: ${IMAGE}
    container_name: ${NS_CONTAINER}
    restart: always
    network_mode: host
    environment:
      - JAVA_OPT_EXT=-server -Xms512m -Xmx512m -Xmn256m
    volumes:
      - ${HOME_DIR}/logs/namesrv:/home/rocketmq/logs
      - ${HOME_DIR}/store/namesrv:/home/rocketmq/store
    command: ["sh", "mqnamesrv"]
YAMLEOF
  echo "拉取镜像 ${IMAGE}"
  docker compose -f "${HOME_DIR}/namesrv-compose.yml" pull
  docker compose -f "${HOME_DIR}/namesrv-compose.yml" up -d
  wait_tcp 127.0.0.1 "${NS_PORT}" "${NS_CONTAINER}" "NameServer" 40
  echo "[NameServer] 已就绪: ${SELF_IP}:${NS_PORT}"
fi

# ================= Broker（所有节点：首节点 master / 其余 slave） =================
BROKER_CONTAINER="rmq-broker-${BROKER_NAME}-${BROKER_ID}"
migrate_old "${BROKER_CONTAINER}"

umask 077
if [ "${IS_BOOTSTRAP}" = "true" ]; then
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

# 自动把素材中的 {{namesrv_addr}} 替换为「首节点 IP:NS_PORT」
sed -i "s|{{namesrv_addr}}|${MASTER_IP}:${NS_PORT}|g" "${HOME_DIR}/conf/broker.conf"

cat > "${HOME_DIR}/broker-compose.yml" <<YAMLEOF
name: rmq-${CLUSTER_NAME}-broker
services:
  broker:
    image: ${IMAGE}
    container_name: ${BROKER_CONTAINER}
    restart: always
    network_mode: host
    environment:
      - JAVA_OPT_EXT=-server -Xms1g -Xmx1g -Xmn512m
    volumes:
      - ${HOME_DIR}/store:/home/rocketmq/store
      - ${HOME_DIR}/logs/broker:/home/rocketmq/logs
      - ${HOME_DIR}/conf/broker.conf:/home/rocketmq/conf/broker.conf:ro
    command: ["sh", "mqbroker", "-c", "/home/rocketmq/conf/broker.conf"]
YAMLEOF

# 首节点若与 namesrv 同机，broker 的 namesrvAddr 指向本机（与上面 sed 目标一致）
if [ "${IS_BOOTSTRAP}" = "true" ]; then
  sed -i "s|{{namesrv_addr}}|${MASTER_IP}:${NS_PORT}|g" "${HOME_DIR}/conf/broker.conf"
fi

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/broker-compose.yml" pull
docker compose -f "${HOME_DIR}/broker-compose.yml" up -d
wait_tcp 127.0.0.1 "${BROKER_PORT}" "${BROKER_CONTAINER}" "Broker" 50

# ================= 监督校验：broker 是否成功注册到 namesrv =================
NAMESRV_ENDPOINT="${MASTER_IP}:${NS_PORT}"
echo "[BROKER] 经 mqadmin clusterList 校验注册状态（namesrv=${NAMESRV_ENDPOINT}）..."
REG_OK=0
for _ in $(seq 1 20); do
  LIST=$(docker exec "${BROKER_CONTAINER}" sh /home/rocketmq/bin/mqadmin clusterList -n "${NAMESRV_ENDPOINT}" 2>/dev/null || true)
  if echo "${LIST}" | grep -q "${SELF_IP}:${BROKER_PORT}"; then REG_OK=1; break; fi
  sleep 3
done
[ "${REG_OK}" = "1" ] && echo "[BROKER] 已在 clusterList 中查到本 broker：${SELF_IP}:${BROKER_PORT}"
[ "${REG_OK}" = "1" ] || echo "警告: 未在 clusterList 中查到本 broker，请确认 namesrv 可达（${NAMESRV_ENDPOINT}）与 broker.conf 的 namesrvAddr"

echo "=== RocketMQ 节点部署完成：${SELF_IP} ==="
if [ "${IS_BOOTSTRAP}" = "true" ]; then
  echo "[NameServer] ${SELF_IP}:${NS_PORT}"
  echo "[Broker-Master] ${SELF_IP}:${BROKER_PORT}（brokerId=0，namesrvAddr=${NAMESRV_ENDPOINT}）"
else
  echo "[Broker-Slave] ${SELF_IP}:${BROKER_PORT}（brokerId=${BROKER_ID}，组=${BROKER_NAME}，namesrvAddr=${NAMESRV_ENDPOINT}）"
fi
echo "compose: ${HOME_DIR}/namesrv-compose.yml / ${HOME_DIR}/broker-compose.yml"