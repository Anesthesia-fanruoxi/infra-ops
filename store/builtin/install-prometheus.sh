#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
RETENTION_DAYS="{{retention_days}}"
REMOTE_WRITE_URL="{{remote_write_url}}"
IMAGE="{{image}}"
SVC=prometheus

RETENTION_DAYS=${RETENTION_DAYS:-30}
# 内部固定 9090 端口，外部端口映射由 PORT 控制

# ==== 共享网络（供其他中间件容器按名字互访） ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/conf/scrape.d" "${HOME_DIR}/conf/targets"

# ==== 生成 compose 文件（后续改参数直接编辑此文件后重新 up 即可） ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
services:
  prometheus:
    image: {{image}}
    container_name: prometheus
    restart: always
    ports:
      - "{{port}}:9090"
    volumes:
      - {{home_dir}}/conf:/etc/prometheus
      - {{home_dir}}/data:/prometheus
    command:
      - --config.file=/etc/prometheus/prometheus.yml
      - --storage.tsdb.path=/prometheus
      - --storage.tsdb.retention.time={{retention_days}}d
      - --web.enable-lifecycle
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:9090/-/healthy"]
      interval: 15s
      timeout: 5s
      retries: 5
    networks:
      - middleware_net

networks:
  middleware_net:
    external: true
YAMLEOF

# ==== 生成主配置（每次运行重新生成；自定义抓取任务请放 scrape.d/ 与 targets/，不会被覆盖） ====
cat > "${HOME_DIR}/conf/prometheus.yml" <<'YAMLEOF'
global:
  scrape_interval: 15s
  evaluation_interval: 15s
__REMOTE_WRITE__
scrape_config_files:
  - /etc/prometheus/scrape.d/*.yml

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ["localhost:9090"]
YAMLEOF

# 可选：remote_write 推送到 VictoriaMetrics（变量留空则删除占位行）
if [ -n "${REMOTE_WRITE_URL}" ]; then
  sed -i "s|__REMOTE_WRITE__|remote_write:\\n  - url: ${REMOTE_WRITE_URL}|" "${HOME_DIR}/conf/prometheus.yml"
else
  sed -i '/__REMOTE_WRITE__/d' "${HOME_DIR}/conf/prometheus.yml"
fi

# ==== 采集任务定义（file_sd：targets 文件改动 10s 内自动生效，无需重启） ====
cat > "${HOME_DIR}/conf/scrape.d/node.yml" <<'YAMLEOF'
- job_name: node-exporter
  file_sd_configs:
    - files:
        - /etc/prometheus/targets/node.yml
      refresh_interval: 10s
YAMLEOF

# 默认为空目标列表（仅首次生成，后续不会被覆盖；自行编写的采集器按示例格式加入即可）
if [ ! -f "${HOME_DIR}/conf/targets/node.yml" ]; then
  cat > "${HOME_DIR}/conf/targets/node.yml" <<YAMLEOF
# 自定义采集器示例（暴露 /metrics 的任意 exporter，改完 10s 内自动生效）：
# - targets:
#     - 10.0.0.5:9100
#   labels:
#     instance: 10.0.0.5
[]
YAMLEOF
fi

# ==== 拉取并启动（compose 幂等：配置未变不动，变了自动 recreate） ====
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
  echo "Prometheus 健康检查失败，最近日志:"
  docker compose -f "${HOME_DIR}/compose.yml" logs --tail 30 2>&1 || true
  exit 1
fi

echo "Prometheus 已就绪: http://{{__ip}}:${PORT}"
echo "  Targets 页面  : http://{{__ip}}:${PORT}/targets（查看各采集目标状态）"
echo "  数据目录      : ${HOME_DIR}/data（保留 ${RETENTION_DAYS} 天）"
echo "  主配置        : ${HOME_DIR}/conf/prometheus.yml（每次运行会重新生成，勿手工改动）"
echo "  采集目标      : ${HOME_DIR}/conf/targets/node.yml（自定义采集器按文件内示例加入，10s 自动生效，无需重启）"
echo "  自定义任务    : 在 ${HOME_DIR}/conf/scrape.d/ 下新增 *.yml 即可（每项为一个抓取任务）"
if [ -n "${REMOTE_WRITE_URL}" ]; then
  echo "  remote_write  : ${REMOTE_WRITE_URL}"
fi
echo "  常用命令: cd ${HOME_DIR} && docker compose logs -f | docker compose restart | docker compose down"
