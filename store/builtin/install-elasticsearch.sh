#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
DATA_DIR="{{data_dir}}"
ES_PASS="{{elastic_password}}"
JAVA_OPTS="{{java_opts}}"
IMAGE="{{image}}"
CONTAINER=elasticsearch
NET=middleware_net

[ -n "${ES_PASS}" ] || { echo "elastic 密码不能为空"; exit 1; }
[ -n "${JAVA_OPTS}" ] || JAVA_OPTS="-Xms512m -Xmx512m"

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 数据目录（ES 容器内 uid=1000） ====
mkdir -p "${DATA_DIR}/data"
chown -R 1000:0 "${DATA_DIR}/data"
chmod -R 755 "${DATA_DIR}/data"
rm -f "${DATA_DIR}/data/node.lock"

# ==== 幂等部署（数据目录保留；elastic 密码仅首次初始化数据时生效） ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${PORT}:9200" \
  -e discovery.type=single-node \
  -e ELASTIC_PASSWORD="${ES_PASS}" \
  -e xpack.security.enabled=true \
  -e xpack.security.http.ssl.enabled=false \
  -e ES_JAVA_OPTS="${JAVA_OPTS}" \
  -v "${DATA_DIR}/data:/usr/share/elasticsearch/data" \
  --ulimit memlock=-1:-1 \
  --health-cmd "curl -sf -u elastic:${ES_PASS} http://localhost:9200/_cluster/health || exit 1" \
  --health-interval 10s \
  --health-timeout 10s \
  --health-retries 30 \
  "${IMAGE}"

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

echo "Elasticsearch 已就绪: http://{{__ip}}:${PORT}（elastic / 数据目录 ${DATA_DIR}/data）"
echo "提示: 数据目录非空时，本次环境变量中的密码不会改变已有密码"
