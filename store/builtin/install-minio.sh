#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
API_PORT="{{api_port}}"
CONSOLE_PORT="{{console_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
ROOT_USER="{{root_user}}"
ROOT_PASSWORD="{{root_password}}"
CONTAINER=minio

[ -n "${ROOT_USER}" ] || { echo "访问账号不能为空"; exit 1; }
[ -n "${ROOT_PASSWORD}" ] || { echo "访问密码不能为空（不少于 8 位）"; exit 1; }

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data"

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: minio
services:
  minio:
    image: {{image}}
    container_name: minio
    restart: always
    ports:
      - "{{api_port}}:9000"
      - "{{console_port}}:9001"
    environment:
      MINIO_ROOT_USER: "{{root_user}}"
      MINIO_ROOT_PASSWORD: "{{root_password}}"
    command: ["server", "/data", "--console-address", ":9001"]
    volumes:
      - {{home_dir}}/data:/data
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
  if docker exec "${CONTAINER}" curl -sf http://localhost:9000/minio/health/live >/dev/null 2>&1; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "MinIO 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "MinIO 已就绪: 控制台 http://{{__ip}}:${CONSOLE_PORT}，S3 端点 http://{{__ip}}:${API_PORT}（数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"