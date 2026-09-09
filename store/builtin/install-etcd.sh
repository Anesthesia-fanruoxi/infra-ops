#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
CLIENT_PORT="{{client_port}}"
PEER_PORT="{{peer_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
CONTAINER=etcd

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data"

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: etcd
services:
  etcd:
    image: {{image}}
    container_name: etcd
    restart: always
    ports:
      - "{{client_port}}:2379"
      - "{{peer_port}}:2380"
    command:
      - etcd
      - --name=etcd0
      - --advertise-client-urls=http://{{__ip}}:{{client_port}}
      - --listen-client-urls=http://0.0.0.0:2379
      - --listen-peer-urls=http://0.0.0.0:2380
      - --initial-advertise-peer-urls=http://etcd:2380
      - --initial-cluster=etcd0=http://etcd:2380
      - --initial-cluster-state=new
      - --data-dir=/etcd-data
      - --log-level=info
    volumes:
      - {{home_dir}}/data:/etcd-data
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
  if docker exec "${CONTAINER}" etcdctl endpoint health --cluster 2>/dev/null | grep -q "is healthy"; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "etcd 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "etcd 已就绪: http://{{__ip}}:${CLIENT_PORT}（数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"