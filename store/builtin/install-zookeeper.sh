#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
CLIENT_PORT="{{client_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
CONTAINER=zookeeper

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/datalog"

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: zookeeper
services:
  zookeeper:
    image: {{image}}
    container_name: zookeeper
    restart: always
    ports:
      - "{{client_port}}:2181"
    environment:
      ZOO_MY_ID: "1"
      ZOO_SERVERS: "server.1=0.0.0.0:2888:3888;2181"
    volumes:
      - {{home_dir}}/data:/data
      - {{home_dir}}/datalog:/datalog
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

# ==== 等待就绪（通过四字命令 ruok/mntr 探测） ====
READY=0
for _ in $(seq 1 15); do
  if [ "$(docker exec "${CONTAINER}" sh -c "echo mntr | nc 127.0.0.1 2181" 2>/dev/null | grep -c '^zk_server_state=leader\|^zk_server_state=follower')" = "1" ]; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Zookeeper 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Zookeeper 已就绪: {{__ip}}:${CLIENT_PORT}（数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"