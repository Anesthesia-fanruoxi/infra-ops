#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

AMQP_PORT="{{amqp_port}}"
MGMT_PORT="{{mgmt_port}}"
COOKIE="{{erlang_cookie}}"
ADMIN_USER="{{admin_user}}"
ADMIN_PASS="{{admin_pass}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
IS_BOOTSTRAP="{{__is_bootstrap}}"
JOIN_HOST="{{__join_host}}"
SELF_IP="{{__ip}}"
CONTAINER=rabbitmq-node

[ -n "${COOKIE}" ] || { echo "erlang_cookie 不能为空，所有节点必须一致"; exit 1; }
[ -n "${ADMIN_PASS}" ] || { echo "管理密码不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="rabbitmq:3.13-management"
[ -n "${AMQP_PORT}" ] || AMQP_PORT=5672
[ -n "${MGMT_PORT}" ] || MGMT_PORT=15672
[ -n "${ADMIN_USER}" ] || ADMIN_USER="admin"

mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/log"
# rabbitmq 镜像以 uid 999 运行
chown -R 999:999 "${HOME_DIR}/data" "${HOME_DIR}/log" 2>/dev/null || true

umask 077
cat > "${HOME_DIR}/rabbitmq.conf" <<'CONFEOF'
@@RABBITMQ_CONF@@
CONFEOF
umask 022
chown 999:999 "${HOME_DIR}/rabbitmq.conf" 2>/dev/null || true
chmod 644 "${HOME_DIR}/rabbitmq.conf"

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧容器，迁移为 compose 管理"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: rabbitmq-cluster
services:
  rabbitmq:
    image: {{image}}
    container_name: rabbitmq-node
    restart: always
    network_mode: host
    hostname: {{__ip}}
    environment:
      - RABBITMQ_NODENAME=rabbit@{{__ip}}
      - RABBITMQ_USE_LONGNAME=true
      - RABBITMQ_ERLANG_COOKIE={{erlang_cookie}}
      - RABBITMQ_DEFAULT_USER={{admin_user}}
      - RABBITMQ_DEFAULT_PASS={{admin_pass}}
    volumes:
      - {{home_dir}}/data:/var/lib/rabbitmq
      - {{home_dir}}/log:/var/log/rabbitmq
      - {{home_dir}}/rabbitmq.conf:/etc/rabbitmq/rabbitmq.conf:ro
YAMLEOF

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

READY=0
for _ in $(seq 1 60); do
  if curl -sf -u "${ADMIN_USER}:${ADMIN_PASS}" "http://127.0.0.1:${MGMT_PORT}/api/overview" &>/dev/null; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "RabbitMQ 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 40 2>&1 || true
  exit 1
fi

docker exec "${CONTAINER}" rabbitmq-plugins enable rabbitmq_management >/dev/null 2>&1 || true

if [ "${IS_BOOTSTRAP}" != "true" ]; then
  [ -n "${JOIN_HOST}" ] || { echo "加入节点缺少主节点地址"; exit 1; }
  echo "正在加入集群: ${JOIN_HOST}"
  docker exec "${CONTAINER}" rabbitmqctl stop_app
  if docker exec "${CONTAINER}" rabbitmqctl join_cluster "${JOIN_HOST}"; then
    docker exec "${CONTAINER}" rabbitmqctl start_app
    echo "集群 join 完成"
  else
    echo "join 失败，已尝试恢复独立节点：请核对 erlang_cookie 与主节点 rabbit@IP"
    docker exec "${CONTAINER}" rabbitmqctl start_app || true
    exit 1
  fi
fi

echo "RabbitMQ 节点已就绪（host 网络）"
echo "AMQP:   ${SELF_IP}:${AMQP_PORT}"
echo "管理台: http://${SELF_IP}:${MGMT_PORT}（${ADMIN_USER}）"
echo "节点名: rabbit@${SELF_IP}"
echo "compose: ${HOME_DIR}/compose.yml"
