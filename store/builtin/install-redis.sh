#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
DATA_DIR="{{data_dir}}"
REDIS_PASS="{{password}}"
IMAGE="{{image}}"
CONTAINER=redis
NET=middleware_net

[ -n "${REDIS_PASS}" ] || { echo "访问密码不能为空"; exit 1; }

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${DATA_DIR}/data"

# ==== 幂等部署（数据目录保留） ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${PORT}:6379" \
  -v "${DATA_DIR}/data:/data" \
  "${IMAGE}" \
  redis-server --requirepass "${REDIS_PASS}" --appendonly yes

# ==== 健康检查 ====
READY=0
for _ in $(seq 1 15); do
  if docker exec "${CONTAINER}" redis-cli -a "${REDIS_PASS}" --no-auth-warning ping 2>/dev/null | grep -q PONG; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Redis 健康检查失败，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Redis 已就绪: {{__ip}}:${PORT}（数据目录 ${DATA_DIR}/data，已开启 AOF 持久化）"
