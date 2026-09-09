#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
HTTP_PORT="{{http_port}}"
TL_PORT="{{tcp_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
USERNAME="{{username}}"
PASSWORD="{{password}}"
CONTAINER=clickhouse

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/log"

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: clickhouse
services:
  clickhouse:
    image: {{image}}
    container_name: clickhouse
    restart: always
    ports:
      - "{{http_port}}:8123"
      - "{{tcp_port}}:9000"
    ulimits:
      nofile:
        soft: 262144
        hard: 262144
    environment:
      CLICKHOUSE_USER: "{{username}}"
      CLICKHOUSE_PASSWORD: "{{password}}"
      CLICKHOUSE_DB: "default"
    volumes:
      - {{home_dir}}/data:/var/lib/clickhouse
      - {{home_dir}}/log:/var/log/clickhouse-server
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
for _ in $(seq 1 30); do
  if docker exec "${CONTAINER}" clickhouse-client --query "SELECT 1" >/dev/null 2>&1; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "ClickHouse 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "ClickHouse 已就绪: http://{{__ip}}:${HTTP_PORT}（HTTP 端口 ${HTTP_PORT} / 原生端口 ${TL_PORT}，数据目录 ${HOME_DIR}/data）"
echo "连接示例: clickhouse-client -h {{__ip}} --port ${TL_PORT} -u ${USERNAME} --password '${PASSWORD}'"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"