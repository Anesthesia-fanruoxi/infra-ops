#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

CLUSTER_NAME="{{cluster_name}}"
PORT="{{port}}"
TRANSPORT_PORT="{{transport_port}}"
JAVA_OPTS="{{java_opts}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
IS_BOOTSTRAP="{{__is_bootstrap}}"
NODE_NAME="{{__node_name}}"
SELF_IP="{{__ip}}"
CONTAINER="es-${NODE_NAME}"

[ -n "${CLUSTER_NAME}" ] || { echo "集群名称不能为空"; exit 1; }
[ -n "${NODE_NAME}" ] || { echo "节点名称不能为空"; exit 1; }
[ -n "${PORT}" ] || PORT=9200
[ -n "${TRANSPORT_PORT}" ] || TRANSPORT_PORT=9300
[ -n "${JAVA_OPTS}" ] || JAVA_OPTS="-Xms1g -Xmx1g"
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="elasticsearch:8.17.0"

mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/logs"
chown -R 1000:0 "${HOME_DIR}/data" "${HOME_DIR}/logs" 2>/dev/null || chmod -R 777 "${HOME_DIR}/data" "${HOME_DIR}/logs"

umask 077
if [ "${IS_BOOTSTRAP}" = "true" ]; then
  cat > "${HOME_DIR}/elasticsearch.yml" <<'EOF'
@@ES_MASTER_YML@@
EOF
else
  cat > "${HOME_DIR}/elasticsearch.yml" <<'EOF'
@@ES_MEMBER_YML@@
EOF
fi
umask 022
# elasticsearch 镜像以 uid 1000 运行；root:600 的 yml 会导致无法读取配置
chown 1000:0 "${HOME_DIR}/elasticsearch.yml" 2>/dev/null || true
chmod 644 "${HOME_DIR}/elasticsearch.yml"

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: es-cluster
services:
  elasticsearch:
    image: {{image}}
    container_name: es-{{__node_name}}
    restart: always
    network_mode: host
    environment:
      - ES_JAVA_OPTS={{java_opts}}
      - bootstrap.memory_lock=true
    ulimits:
      memlock:
        soft: -1
        hard: -1
    volumes:
      - {{home_dir}}/data:/usr/share/elasticsearch/data
      - {{home_dir}}/logs:/usr/share/elasticsearch/logs
      - {{home_dir}}/elasticsearch.yml:/usr/share/elasticsearch/config/elasticsearch.yml:ro
YAMLEOF

sysctl -w vm.max_map_count=262144 >/dev/null 2>&1 || true

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

READY=0
for _ in $(seq 1 80); do
  if curl -sf "http://127.0.0.1:${PORT}/" &>/dev/null; then READY=1; break; fi
  sleep 5
done
if [ "${READY}" != "1" ]; then
  echo "Elasticsearch 节点启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 40 2>&1 || true
  exit 1
fi

echo "Elasticsearch 节点已就绪: ${NODE_NAME} / 集群 ${CLUSTER_NAME}"
echo "HTTP: http://${SELF_IP}:${PORT}（Transport ${SELF_IP}:${TRANSPORT_PORT}，host 网络）"
if [ "${IS_BOOTSTRAP}" = "true" ]; then
  echo "本机为引导节点；集群成形后建议删除 elasticsearch.yml 中的 cluster.initial_master_nodes"
fi
echo "compose: ${HOME_DIR}/compose.yml"
