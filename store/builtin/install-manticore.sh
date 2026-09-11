#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
SQL_PORT="{{sql_port}}"
HTTP_PORT="{{http_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
CONTAINER=manticore

[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 配置文件（容器内端口固定 9306/9308，宿主端口经映射自定义） ====
mkdir -p "${HOME_DIR}/data"
if [ -f "${HOME_DIR}/manticore.conf" ]; then
  cp "${HOME_DIR}/manticore.conf" "${HOME_DIR}/manticore.conf.bak.$(date +%s)"
  echo "已备份原有 manticore.conf"
fi
cat > "${HOME_DIR}/manticore.conf" <<'CONF'
searchd {
  listen = 9306:mysql
  listen = 9308:http
  pid_file = /var/run/manticore/searchd.pid
  data_dir = /var/lib/manticore
}
CONF

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
name: manticore
services:
  manticore:
    image: {{image}}
    container_name: manticore
    restart: always
    ulimits:
      nproc: 65535
      nofile:
        soft: 65535
        hard: 65535
    ports:
      - "{{sql_port}}:9306"
      - "{{http_port}}:9308"
    volumes:
      - {{home_dir}}/data:/var/lib/manticore
      - {{home_dir}}/manticore.conf:/etc/manticoresearch/manticore.conf
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

# ==== 等待就绪（searchd 起来后 9306/9308 均 TCP 可连） ====
READY=0
for _ in $(seq 1 20); do
  if docker exec "${CONTAINER}" bash -c 'exec 3<>/dev/tcp/127.0.0.1/9308' 2>/dev/null && \
     docker exec "${CONTAINER}" bash -c 'exec 3<>/dev/tcp/127.0.0.1/9306' 2>/dev/null; then
    READY=1; break
  fi
  sleep 3
done
if [ "${READY}" != "1" ]; then
  echo "Manticore 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Manticore 已就绪: {{__ip}}"
echo "  MySQL 协议: {{__ip}}:${SQL_PORT}（mysql -h {{__ip}} -P ${SQL_PORT} 直连）"
echo "  HTTP API:  http://{{__ip}}:${HTTP_PORT}/sql?query=SHOW%20TABLES"
echo "数据目录: ${HOME_DIR}/data"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
