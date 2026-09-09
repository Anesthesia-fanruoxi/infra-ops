#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

NS_NAME="{{__node_name}}"
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
SELF_IP="{{__ip}}"
CONTAINER="rmq-namesrv-${NS_NAME}"
NET="middleware_net"

[ -n "${NS_NAME}" ] || { echo "namesrv 实例名不能为空"; exit 1; }
[ -n "${PORT}" ] || PORT=9876
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="apache/rocketmq:4.9.7"

docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"
mkdir -p "${HOME_DIR}/logs" "${HOME_DIR}/store"
chown -R 3000:3000 "${HOME_DIR}/logs" "${HOME_DIR}/store" 2>/dev/null || true

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧容器，迁移为 compose 管理"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: rmq-namesrv
services:
  namesrv:
    image: {{image}}
    container_name: rmq-namesrv-{{__node_name}}
    restart: always
    ports:
      - "{{port}}:9876"
    environment:
      - JAVA_OPT_EXT=-server -Xms256m -Xmx256m -Xmn256m
    volumes:
      - {{home_dir}}/logs:/home/rocketmq/logs
      - {{home_dir}}/store:/home/rocketmq/store
    command: ["sh", "mqnamesrv"]
    networks:
      - middleware_net

networks:
  middleware_net:
    external: true
YAMLEOF

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

READY=0
for _ in $(seq 1 40); do
  if docker exec "${CONTAINER}" bash -c "</dev/tcp/127.0.0.1/9876" 2>/dev/null; then READY=1; break; fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "NameServer 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "RocketMQ NameServer 已就绪: ${SELF_IP}:${PORT}"
echo "多个 NameServer 时，Broker 的 namesrvAddr 填 ip1:${PORT},ip2:${PORT}"
echo "compose: ${HOME_DIR}/compose.yml"
