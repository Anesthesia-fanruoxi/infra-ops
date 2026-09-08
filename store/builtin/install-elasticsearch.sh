#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"

if [ -d "/data/middleware/elasticsearch" ] && [ ! -d "${HOME_DIR}" ]; then
  echo "警告: 检测到旧版数据目录 /data/middleware/elasticsearch，如需保留历史数据请先迁移到 ${HOME_DIR}，或将服务主目录填为旧路径"
fi
ES_PASS="{{elastic_password}}"
JAVA_OPTS="{{java_opts}}"
IMAGE="{{image}}"
CONTAINER=elasticsearch

[ -n "${ES_PASS}" ] || { echo "elastic 密码不能为空"; exit 1; }
[ -n "${JAVA_OPTS}" ] || JAVA_OPTS="-Xms512m -Xmx512m"

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 数据目录（ES 容器内 uid=1000） ====
mkdir -p "${HOME_DIR}/data"
chown -R 1000:0 "${HOME_DIR}/data"
chmod -R 755 "${HOME_DIR}/data"
rm -f "${HOME_DIR}/data/node.lock"

# ==== 敏感凭据落盘（env_file 不做插值，密码含 $ 等特殊字符也安全） ====
umask 077
printf 'ELASTIC_PASSWORD=%s\n' "${ES_PASS}" > "${HOME_DIR}/secrets.env"
umask 022

# ==== 迁移旧版 docker run 容器（数据目录保留；elastic 密码仅首次初始化数据时生效） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: elasticsearch
services:
  elasticsearch:
    image: {{image}}
    container_name: elasticsearch
    restart: always
    ports:
      - "{{port}}:9200"
    env_file:
      - secrets.env
    environment:
      - discovery.type=single-node
      - xpack.security.enabled=true
      - xpack.security.http.ssl.enabled=false
      - ES_JAVA_OPTS={{java_opts}}
    volumes:
      - {{home_dir}}/data:/usr/share/elasticsearch/data
    ulimits:
      memlock: -1
    healthcheck:
      test: ["CMD-SHELL", "curl -sf -u elastic:$${ELASTIC_PASSWORD} http://localhost:9200/_cluster/health || exit 1"]
      interval: 10s
      timeout: 10s
      retries: 30
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

# ==== 等待健康 ====
READY=0
for _ in $(seq 1 60); do
  STATUS=$(docker inspect -f '{{.State.Health.Status}}' "${CONTAINER}" 2>/dev/null || echo "unknown")
  if [ "${STATUS}" = "healthy" ]; then READY=1; break; fi
  if [ "${STATUS}" = "none" ] && docker exec "${CONTAINER}" curl -sf -u "elastic:${ES_PASS}" http://localhost:9200 &>/dev/null; then
    READY=1; break
  fi
  sleep 3
done
if [ "${READY}" != "1" ]; then
  echo "Elasticsearch 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Elasticsearch 已就绪: http://{{__ip}}:${PORT}（elastic / 数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
echo "提示: 数据目录非空时，本次环境变量中的密码不会改变已有密码"
