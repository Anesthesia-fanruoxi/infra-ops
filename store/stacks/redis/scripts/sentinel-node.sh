#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

PORT="{{port}}"
SENTINEL_PORT="{{sentinel_port}}"
HOME_DIR="{{home_dir}}"
REDIS_PASS="{{password}}"
IMAGE="{{image}}"
ROLE="{{__role}}"
MASTER_IP="{{__master_ip}}"
MASTER_PORT="{{__master_port}}"
QUORUM="{{__quorum}}"
SELF_IP="{{__ip}}"
REDIS_CTR=redis-sentinel-data
SENTINEL_CTR=redis-sentinel

[ -n "${REDIS_PASS}" ] || { echo "访问密码不能为空"; exit 1; }
[ -n "${PORT}" ] || { echo "端口不能为空"; exit 1; }
[ -n "${SENTINEL_PORT}" ] || { echo "哨兵端口不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${MASTER_IP}" ] && [ -n "${MASTER_PORT}" ] || { echo "缺少主节点地址"; exit 1; }
[ -n "${QUORUM}" ] || { echo "缺少哨兵法定人数"; exit 1; }
if [ "${PORT}" = "${SENTINEL_PORT}" ]; then
  echo "Redis 端口与哨兵端口不能相同"
  exit 1
fi

sysctl -w vm.overcommit_memory=1 >/dev/null 2>&1 || true

mkdir -p "${HOME_DIR}/data"
if [ -f "${HOME_DIR}/redis.conf" ]; then
  cp "${HOME_DIR}/redis.conf" "${HOME_DIR}/redis.conf.bak.$(date +%s)"
  echo "已备份原有 redis.conf"
fi
if [ -f "${HOME_DIR}/sentinel.conf" ]; then
  cp "${HOME_DIR}/sentinel.conf" "${HOME_DIR}/sentinel.conf.bak.$(date +%s)"
  echo "已备份原有 sentinel.conf"
fi

umask 077
if [ "${ROLE}" = "replica" ]; then
  cat > "${HOME_DIR}/redis.conf" <<'CONFEOF'
@@REDIS_SENTINEL_REPLICA_CONF@@
CONFEOF
else
  cat > "${HOME_DIR}/redis.conf" <<'CONFEOF'
@@REDIS_SENTINEL_MASTER_CONF@@
CONFEOF
fi
cat > "${HOME_DIR}/sentinel.conf" <<'CONFEOF'
@@REDIS_SENTINEL_CONF@@
CONFEOF
umask 022
# 官方 redis 镜像以 uid 999 运行；root:600 会导致 can't open config file: Permission denied
# sentinel 会回写 sentinel.conf，同样需要该用户可写
chown 999:999 "${HOME_DIR}/redis.conf" "${HOME_DIR}/sentinel.conf" 2>/dev/null || true
chmod 644 "${HOME_DIR}/redis.conf" "${HOME_DIR}/sentinel.conf"
chown -R 999:999 "${HOME_DIR}/data" 2>/dev/null || true

for CTR in "${REDIS_CTR}" "${SENTINEL_CTR}"; do
  if docker ps -a --format '{{.Names}}' | grep -qx "${CTR}"; then
    PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CTR}" 2>/dev/null || true)"
    if [ -z "${PROJ}" ]; then
      echo "检测到旧容器 ${CTR}，迁移为 compose 管理"
      docker rm -f "${CTR}" &>/dev/null || true
    fi
  fi
done

cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: redis-sentinel
services:
  redis:
    image: {{image}}
    container_name: redis-sentinel-data
    restart: always
    network_mode: host
    volumes:
      - {{home_dir}}/data:/data
      - {{home_dir}}/redis.conf:/usr/local/etc/redis/redis.conf:ro
    command: ["redis-server", "/usr/local/etc/redis/redis.conf"]
    ulimits:
      nofile:
        soft: 65535
        hard: 65535
  sentinel:
    image: {{image}}
    container_name: redis-sentinel
    restart: always
    network_mode: host
    volumes:
      - {{home_dir}}/sentinel.conf:/usr/local/etc/redis/sentinel.conf
    command: ["redis-sentinel", "/usr/local/etc/redis/sentinel.conf"]
    depends_on:
      - redis
YAMLEOF

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

READY=0
for _ in $(seq 1 20); do
  if docker exec "${REDIS_CTR}" redis-cli -a "${REDIS_PASS}" --no-auth-warning -p "${PORT}" ping 2>/dev/null | grep -q PONG; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Redis 启动超时，最近日志:"
  docker logs "${REDIS_CTR}" --tail 30 2>&1 || true
  exit 1
fi

READY=0
for _ in $(seq 1 20); do
  if docker exec "${SENTINEL_CTR}" redis-cli -a "${REDIS_PASS}" --no-auth-warning -p "${SENTINEL_PORT}" ping 2>/dev/null | grep -q PONG; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Sentinel 启动超时，最近日志:"
  docker logs "${SENTINEL_CTR}" --tail 30 2>&1 || true
  exit 1
fi

if [ "${ROLE}" = "master" ]; then
  echo "Redis 主节点已就绪: ${SELF_IP}:${PORT}（哨兵 ${SELF_IP}:${SENTINEL_PORT}，quorum ${QUORUM}）"
else
  echo "Redis 从节点已就绪: ${SELF_IP}:${PORT} → replicaof ${MASTER_IP}:${MASTER_PORT}（哨兵 ${SELF_IP}:${SENTINEL_PORT}）"
fi
echo "compose 文件: ${HOME_DIR}/compose.yml"
