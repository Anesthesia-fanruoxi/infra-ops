#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
CONTAINER=consul

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data"

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: consul
services:
  consul:
    image: {{image}}
    container_name: consul
    restart: always
    ports:
      - "{{port}}:8500"
    command:
      - "agent"
      - "-server"
      - "-bootstrap-expect=1"
      - "-ui"
      - "-client=0.0.0.0"
      - "-bind=0.0.0.0"
      - "-data-dir=/consul/data"
      - "-node=consul-{{__ip}}"
    volumes:
      - {{home_dir}}/data:/consul/data
    networks:
      - middleware_net

networks:
  middleware_net:
    external: true
YAMLEOF

# ==== 拉取并启动 ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 等待就绪 ====
READY=0
for _ in $(seq 1 15); do
  if docker exec "${CONTAINER}" consul members 2>/dev/null | grep -q alive; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Consul 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Consul 已就绪: http://{{__ip}}:${PORT}/ui（数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"