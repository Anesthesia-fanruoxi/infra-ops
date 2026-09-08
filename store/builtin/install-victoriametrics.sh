#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"

if [ -d "/data/middleware/victoriametrics" ] && [ ! -d "${HOME_DIR}" ]; then
  echo "警告: 检测到旧版数据目录 /data/middleware/victoriametrics，如需保留历史数据请先迁移到 ${HOME_DIR}，或将服务主目录填为旧路径"
fi
RETENTION="{{retention}}"
IMAGE="{{image}}"
SVC=victoriametrics

RETENTION=${RETENTION:-3}

# ==== 共享网络（供其他中间件容器按名字互访） ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data"

# ==== 生成 compose 文件（后续改参数直接编辑此文件后重新 up 即可） ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
services:
  victoriametrics:
    image: {{image}}
    container_name: victoriametrics
    restart: always
    ports:
      - "{{port}}:8428"
    volumes:
      - {{home_dir}}/data:/storage
    command:
      - -storageDataPath=/storage
      - -retentionPeriod={{retention}}
      - -httpListenAddr=:8428
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8428/health"]
      interval: 15s
      timeout: 5s
      retries: 5
    networks:
      - middleware_net

networks:
  middleware_net:
    external: true
YAMLEOF

# ==== 拉取并启动（compose 幂等：配置未变不重建，变了自动 recreate） ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 健康检查 ====
READY=0
for _ in $(seq 1 15); do
  if [ "$(docker inspect --format='{{.State.Health.Status}}' "${SVC}" 2>/dev/null)" = "healthy" ]; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "VictoriaMetrics 健康检查失败，最近日志:"
  docker compose -f "${HOME_DIR}/compose.yml" logs --tail 30 2>&1 || true
  exit 1
fi

echo "VictoriaMetrics 已就绪: http://{{__ip}}:${PORT}"
echo "  vmui 查询界面 : http://{{__ip}}:${PORT}/vmui"
echo "  数据目录      : ${HOME_DIR}/data（保留 ${RETENTION} 个月）"
echo "  compose 文件  : ${HOME_DIR}/compose.yml（改参数后 cd ${HOME_DIR} && docker compose up -d 生效）"
echo "  Prometheus remote_write 地址: http://{{__ip}}:${PORT}/api/v1/write"
echo "  常用命令: cd ${HOME_DIR} && docker compose logs -f | docker compose restart | docker compose down"
