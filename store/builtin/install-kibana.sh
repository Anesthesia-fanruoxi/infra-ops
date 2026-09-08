#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"

if [ -d "/data/middleware/kibana" ] && [ ! -d "${HOME_DIR}" ]; then
  echo "警告: 检测到旧版数据目录 /data/middleware/kibana，如需保留历史数据请先迁移到 ${HOME_DIR}，或将服务主目录填为旧路径"
fi
ES_PASS="{{elastic_password}}"
KIBANA_PASS="{{kibana_password}}"
IMAGE="{{image}}"
CONTAINER=kibana

[ -n "${ES_PASS}" ] && [ -n "${KIBANA_PASS}" ] || { echo "elastic 密码与 kibana_system 密码均不能为空"; exit 1; }

# ==== 依赖检查：Elasticsearch 容器（须先部署 ES 模块，同一主机） ====
if ! docker ps --format '{{.Names}}' | grep -qx "elasticsearch"; then
  echo "未检测到运行中的 Elasticsearch 容器，请先在同一主机执行「部署 Elasticsearch」模板"; exit 1
fi

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 等待 ES 就绪 ====
for _ in $(seq 1 40); do
  if docker exec elasticsearch curl -sf -u "elastic:${ES_PASS}" http://localhost:9200 &>/dev/null; then break; fi
  sleep 3
done
if ! docker exec elasticsearch curl -sf -u "elastic:${ES_PASS}" http://localhost:9200 &>/dev/null; then
  echo "Elasticsearch 未就绪或 elastic 密码不正确"; exit 1
fi

# ==== 设置 kibana_system 用户密码（幂等覆盖） ====
RESP=$(docker exec elasticsearch curl -s -X POST \
  -u "elastic:${ES_PASS}" \
  -H "Content-Type: application/json" \
  http://localhost:9200/_security/user/kibana_system/_password \
  -d "{\"password\":\"${KIBANA_PASS}\"}" 2>&1)
if echo "${RESP}" | grep -q '"acknowledged":true'; then
  echo "kibana_system 用户密码设置成功"
elif docker exec elasticsearch curl -sf -u "kibana_system:${KIBANA_PASS}" http://localhost:9200 &>/dev/null; then
  echo "kibana_system 用户密码已正确，跳过"
else
  echo "kibana_system 密码设置失败，响应: ${RESP}"
  exit 1
fi

# ==== 数据目录 ====
mkdir -p "${HOME_DIR}/data"
chown -R 1000:0 "${HOME_DIR}/data"
chmod -R 755 "${HOME_DIR}/data"

# ==== 敏感凭据落盘（env_file 不做插值，密码含 $ 等特殊字符也安全） ====
umask 077
printf 'ELASTICSEARCH_PASSWORD=%s\n' "${KIBANA_PASS}" > "${HOME_DIR}/secrets.env"
umask 022

# ==== 迁移旧版 docker run 容器 ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: kibana
services:
  kibana:
    image: {{image}}
    container_name: kibana
    restart: always
    ports:
      - "{{port}}:5601"
    env_file:
      - secrets.env
    environment:
      - SERVER_NAME=kibana
      - ELASTICSEARCH_HOSTS=http://elasticsearch:9200
      - ELASTICSEARCH_USERNAME=kibana_system
    volumes:
      - {{home_dir}}/data:/usr/share/kibana/data
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
for _ in $(seq 1 60); do
  if docker exec "${CONTAINER}" curl -sf -o /dev/null "http://localhost:5601/api/status"; then READY=1; break; fi
  sleep 3
done
if [ "${READY}" != "1" ]; then
  echo "Kibana 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Kibana 已就绪: http://{{__ip}}:${PORT}（登录账号 elastic，密码与 ES 一致）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
