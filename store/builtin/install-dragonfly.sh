#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
PASS="{{password}}"
CONTAINER=dragonfly

[ -n "${PASS}" ] || { echo "访问密码不能为空"; exit 1; }

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data"

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: dragonfly
services:
  dragonfly:
    image: {{image}}
    container_name: dragonfly
    restart: always
    ports:
      - "{{port}}:6379"
    command:
      - dragonfly
      - --requirepass={{password}}
      - --dir=/data
      - --bind=0.0.0.0
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

# ==== 等待就绪（RESP Ping 探测） ====
READY=0
for _ in $(seq 1 15); do
  if docker exec "${CONTAINER}" /bin/sh -c 'echo -ne "PING\r\n" | nc -w 2 127.0.0.1 6379' 2>/dev/null | grep -q "PONG"; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "DragonflyDB 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "DragonflyDB 已就绪: {{__ip}}:${PORT}（数据目录 ${HOME_DIR}/data，Redis 协议兼容）"
echo "连接示例: redis-cli -h {{__ip}} -p ${PORT} -a '${PASS}' ping"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"