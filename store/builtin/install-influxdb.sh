#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
ADMIN_USER="{{admin_username}}"
ADMIN_PASS="{{admin_password}}"
ORG="{{org}}"
BUCKET="{{bucket}}"
ADMIN_TOKEN="{{admin_token}}"
IMAGE="{{image}}"
CONTAINER=influxdb

[ -n "${ADMIN_USER}" ] || { echo "管理员用户名不能为空"; exit 1; }
[ -n "${ADMIN_PASS}" ] || { echo "管理员密码不能为空"; exit 1; }
[ -n "${ORG}" ] || { echo "组织名不能为空"; exit 1; }
[ -n "${BUCKET}" ] || { echo "初始数据桶不能为空"; exit 1; }

# Token 留空自动生成（64 位 hex）
if [ -z "${ADMIN_TOKEN}" ]; then
  ADMIN_TOKEN="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  echo "已自动生成管理员 Token: ${ADMIN_TOKEN}"
fi

# YAML 双引号标量转义：反斜杠与双引号（密码/Token 可能含特殊字符）
PASS_YAML="$(printf '%s' "${ADMIN_PASS}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
TOKEN_YAML="$(printf '%s' "${ADMIN_TOKEN}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
USER_YAML="$(printf '%s' "${ADMIN_USER}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
ORG_YAML="$(printf '%s' "${ORG}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
BUCKET_YAML="$(printf '%s' "${BUCKET}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 数据目录 ====
mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/config"

# ==== 迁移旧版 docker run 容器（数据目录保留） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件 ====
# INIT_* 环境变量仅在数据目录为空时执行一次初始化（已有数据自动忽略，重建容器安全）
cat > "${HOME_DIR}/compose.yml" <<YAMLEOF
name: influxdb
services:
  influxdb:
    image: {{image}}
    container_name: influxdb
    restart: always
    ports:
      - "{{port}}:8086"
    volumes:
      - {{home_dir}}/data:/var/lib/influxdb2
      - {{home_dir}}/config:/etc/influxdb2
    environment:
      DOCKER_INFLUXDB_INIT_MODE: setup
      DOCKER_INFLUXDB_INIT_USERNAME: "${USER_YAML}"
      DOCKER_INFLUXDB_INIT_PASSWORD: "${PASS_YAML}"
      DOCKER_INFLUXDB_INIT_ORG: "${ORG_YAML}"
      DOCKER_INFLUXDB_INIT_BUCKET: "${BUCKET_YAML}"
      DOCKER_INFLUXDB_INIT_ADMIN_TOKEN: "${TOKEN_YAML}"
    healthcheck:
      test: ["CMD", "influx", "ping"]
      interval: 15s
      timeout: 5s
      retries: 5
    networks:
      - middleware_net

networks:
  middleware_net:
    external: true
YAMLEOF

# ==== 拉取并启动（compose 幂等：配置未变不动，变了自动 recreate） ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 等待就绪 ====
READY=0
for _ in $(seq 1 20); do
  if docker exec "${CONTAINER}" influx ping >/dev/null 2>&1; then
    READY=1; break
  fi
  sleep 3
done
if [ "${READY}" != "1" ]; then
  echo "InfluxDB 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "InfluxDB 已就绪: http://{{__ip}}:${PORT}（UI 与 API 同端口）"
echo "  组织: ${ORG}  初始桶: ${BUCKET}"
echo "  管理员: ${ADMIN_USER}"
echo "  管理员 Token: ${ADMIN_TOKEN}（调用 API 时使用）"
echo "数据目录: ${HOME_DIR}/data（首次初始化后重建容器不会重复 setup）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
