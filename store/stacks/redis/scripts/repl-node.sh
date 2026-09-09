#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

PORT="{{port}}"
HOME_DIR="{{home_dir}}"
REDIS_PASS="{{password}}"
IMAGE="{{image}}"
ROLE="{{__role}}"
MASTER_IP="{{__master_ip}}"
MASTER_PORT="{{__master_port}}"
SELF_IP="{{__ip}}"
CONTAINER=redis-repl

[ -n "${REDIS_PASS}" ] || { echo "访问密码不能为空"; exit 1; }
[ -n "${PORT}" ] || { echo "端口不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }

mkdir -p "${HOME_DIR}/data"
if [ -f "${HOME_DIR}/redis.conf" ]; then
  cp "${HOME_DIR}/redis.conf" "${HOME_DIR}/redis.conf.bak.$(date +%s)"
  echo "已备份原有 redis.conf"
fi

umask 077
if [ "${ROLE}" = "replica" ]; then
  [ -n "${MASTER_IP}" ] && [ -n "${MASTER_PORT}" ] || { echo "从节点缺少主节点地址"; exit 1; }
  cat > "${HOME_DIR}/redis.conf" <<'CONFEOF'
@@REDIS_REPLICA_CONF@@
CONFEOF
else
  cat > "${HOME_DIR}/redis.conf" <<'CONFEOF'
@@REDIS_MASTER_CONF@@
CONFEOF
fi
umask 022
# 官方 redis 镜像以 uid 999 运行；root:600 会导致 can't open config file: Permission denied
chown 999:999 "${HOME_DIR}/redis.conf" 2>/dev/null || true
chmod 644 "${HOME_DIR}/redis.conf"
chown -R 999:999 "${HOME_DIR}/data" 2>/dev/null || true

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧容器，迁移为 compose 管理"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: redis-repl
services:
  redis:
    image: {{image}}
    container_name: redis-repl
    restart: always
    ports:
      - "{{port}}:6379"
    volumes:
      - {{home_dir}}/data:/data
      - {{home_dir}}/redis.conf:/usr/local/etc/redis/redis.conf:ro
    command: ["redis-server", "/usr/local/etc/redis/redis.conf"]
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a \"$$(sed -n 's/^requirepass[[:space:]]//p' /usr/local/etc/redis/redis.conf)\" ping | grep -q PONG"]
      interval: 15s
      timeout: 5s
      retries: 5
YAMLEOF

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

READY=0
for _ in $(seq 1 20); do
  if docker exec "${CONTAINER}" redis-cli -a "${REDIS_PASS}" --no-auth-warning ping 2>/dev/null | grep -q PONG; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Redis 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

if [ "${ROLE}" = "master" ]; then
  echo "Redis 主节点已就绪: ${SELF_IP}:${PORT}（数据目录 ${HOME_DIR}/data）"
else
  echo "Redis 从节点已就绪: ${SELF_IP}:${PORT} → replicaof ${MASTER_IP}:${MASTER_PORT}"
fi
echo "compose 文件: ${HOME_DIR}/compose.yml"
