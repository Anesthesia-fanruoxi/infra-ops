#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"

if [ -d "/data/middleware/redis" ] && [ ! -d "${HOME_DIR}" ]; then
  echo "警告: 检测到旧版数据目录 /data/middleware/redis，如需保留历史数据请先迁移到 ${HOME_DIR}，或将服务主目录填为旧路径"
fi
REDIS_PASS="{{password}}"
IMAGE="{{image}}"
CONTAINER=redis

[ -n "${REDIS_PASS}" ] || { echo "访问密码不能为空"; exit 1; }

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 配置文件（密码写入配置文件而非 compose，避免特殊字符破坏 YAML/插值） ====
mkdir -p "${HOME_DIR}/data"
if [ -f "${HOME_DIR}/redis.conf" ]; then
  cp "${HOME_DIR}/redis.conf" "${HOME_DIR}/redis.conf.bak.$(date +%s)"
  echo "已备份原有 redis.conf"
fi
umask 077
printf 'requirepass %s\nappendonly yes\ndir /data\n' "${REDIS_PASS}" > "${HOME_DIR}/redis.conf"
umask 022
# 官方 redis 镜像以 uid 999 运行；root:600 会导致 can't open config file: Permission denied
chown 999:999 "${HOME_DIR}/redis.conf" 2>/dev/null || true
chmod 644 "${HOME_DIR}/redis.conf"
chown -R 999:999 "${HOME_DIR}/data" 2>/dev/null || true

# ==== 迁移旧版 docker run 容器（数据目录保留） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: redis
services:
  redis:
    image: {{image}}
    container_name: redis
    restart: always
    ports:
      - "{{port}}:6379"
    volumes:
      - {{home_dir}}/data:/data
      - {{home_dir}}/redis.conf:/usr/local/etc/redis/redis.conf:ro
    command: ["redis-server", "/usr/local/etc/redis/redis.conf"]
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a \"$$(cat /usr/local/etc/redis/redis.conf | sed -n s/^requirepass[[:space:]]//p)\" ping | grep -q PONG"]
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
for _ in $(seq 1 15); do
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

echo "Redis 已就绪: {{__ip}}:${PORT}（数据目录 ${HOME_DIR}/data，已开启 AOF 持久化）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
