#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
DATA_DIR="{{data_dir}}"
ES_PASS="{{elastic_password}}"
KIBANA_PASS="{{kibana_password}}"
IMAGE="{{image}}"
CONTAINER=kibana
NET=middleware_net

[ -n "${ES_PASS}" ] && [ -n "${KIBANA_PASS}" ] || { echo "elastic 密码与 kibana_system 密码均不能为空"; exit 1; }

# ==== 依赖检查：Elasticsearch 容器（须先部署 ES 模块，同一主机） ====
if ! docker ps --format '{{.Names}}' | grep -qx "elasticsearch"; then
  echo "未检测到运行中的 Elasticsearch 容器，请先在同一主机执行「部署 Elasticsearch」模板"; exit 1
fi

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 等待 ES 就绪 ====
for _ in $(seq 1 40); do
  if docker exec elasticsearch curl -sf -u "elastic:${ES_PASS}" http://localhost:9200 &>/dev/null; then break; fi
  sleep 3
done
if ! docker exec elasticsearch curl -sf -u "elastic:${ES_PASS}" http://localhost:9200 &>/dev/null; then
  echo "Elasticsearch 未就绪或 elastic 密码不正确"; exit 1
fi

# ==== 设置 kibana_system 用户密码（幂等覆盖） ====
RESP=$(docker exec elasticsearch curl -s -X POST \
  -u "elastic:${ES_PASS}" \
  -H "Content-Type: application/json" \
  http://localhost:9200/_security/user/kibana_system/_password \
  -d "{\"password\":\"${KIBANA_PASS}\"}" 2>&1)
if echo "${RESP}" | grep -q '"acknowledged":true'; then
  echo "kibana_system 用户密码设置成功"
elif docker exec elasticsearch curl -sf -u "kibana_system:${KIBANA_PASS}" http://localhost:9200 &>/dev/null; then
  echo "kibana_system 用户密码已正确，跳过"
else
  echo "kibana_system 密码设置失败，响应: ${RESP}"
  exit 1
fi

# ==== 数据目录 ====
mkdir -p "${DATA_DIR}/data"
chown -R 1000:0 "${DATA_DIR}/data"
chmod -R 755 "${DATA_DIR}/data"

# ==== 幂等部署 ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${PORT}:5601" \
  -e SERVER_NAME=kibana \
  -e ELASTICSEARCH_HOSTS=http://elasticsearch:9200 \
  -e ELASTICSEARCH_USERNAME=kibana_system \
  -e ELASTICSEARCH_PASSWORD="${KIBANA_PASS}" \
  -v "${DATA_DIR}/data:/usr/share/kibana/data" \
  "${IMAGE}"

# ==== 等待就绪 ====
READY=0
for _ in $(seq 1 60); do
  if docker exec "${CONTAINER}" curl -sf -o /dev/null "http://localhost:5601/api/status"; then READY=1; break; fi
  sleep 3
done
if [ "${READY}" != "1" ]; then
  echo "Kibana 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Kibana 已就绪: http://{{__ip}}:${PORT}（登录账号 elastic，密码与 ES 一致）"
