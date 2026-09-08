#!/bin/bash
set -e

# 前置依赖：Docker（请先通过「安装 Docker」模板完成）
if ! command -v docker &>/dev/null; then
  echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1
fi
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
REG_USER="{{username}}"
REG_PASS="{{password}}"
CONTAINER=registry
IMAGE=registry:2

case "${PORT}" in ''|*[!0-9]*) echo "监听端口必须是数字: ${PORT}"; exit 1 ;; esac
if [ -n "${REG_USER}" ] && [ -z "${REG_PASS}" ]; then
  echo "已填写认证用户名，但认证密码为空"; exit 1
fi

# ==== 可选：htpasswd 基础认证 ====
if [ -n "${REG_USER}" ]; then
  echo "启用 htpasswd 基础认证，用户: ${REG_USER}"
  mkdir -p "${HOME_DIR}/auth"
  # registry 镜像不含 htpasswd，借用 httpd:2.4-alpine 生成 bcrypt（-B）
  docker run --rm --entrypoint htpasswd httpd:2.4-alpine -Bbn "${REG_USER}" "${REG_PASS}" \
    > "${HOME_DIR}/auth/htpasswd"
  chmod 600 "${HOME_DIR}/auth/htpasswd"
fi

# ==== 数据目录（bind mount，重建容器数据不丢） ====
mkdir -p "${HOME_DIR}/data"

# ==== 迁移旧版 docker run 容器（数据目录保留） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件（认证段按需注入） ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: registry
services:
  registry:
    image: registry:2
    container_name: registry
    restart: always
    ports:
      - "{{port}}:5000"
    volumes:
      - {{home_dir}}/data:/var/lib/registry
__AUTH_VOLUMES__
__AUTH_ENV__
YAMLEOF
if [ -n "${REG_USER}" ]; then
  AUTH_VOL="      - ${HOME_DIR}/auth/htpasswd:/auth/htpasswd:ro"
  AUTH_ENV="    environment:\\n      - REGISTRY_AUTH=htpasswd\\n      - REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm\\n      - REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd"
  sed -i "s|__AUTH_VOLUMES__|${AUTH_VOL}|; s|__AUTH_ENV__|${AUTH_ENV}|" "${HOME_DIR}/compose.yml"
else
  sed -i '/__AUTH_VOLUMES__/d; /__AUTH_ENV__/d' "${HOME_DIR}/compose.yml"
fi

# ==== 拉取并启动（compose 幂等：配置未变不动，变了自动 recreate） ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 健康检查 /v2/ 探活 ====
if command -v curl &>/dev/null; then
  if [ -n "${REG_USER}" ]; then
    CURL=(curl -sf -u "${REG_USER}:${REG_PASS}")
  else
    CURL=(curl -sf)
  fi
  HEALTH_OK=0
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if "${CURL[@]}" -o /dev/null "http://127.0.0.1:${PORT}/v2/"; then HEALTH_OK=1; break; fi
    sleep 2
  done
  if [ "${HEALTH_OK}" != "1" ]; then
    echo "registry 健康检查失败，最近日志:"
    docker logs --tail 30 "${CONTAINER}" 2>&1 || true
    exit 1
  fi
else
  echo "缺少 curl，跳过健康检查"
fi

# ==== 结果汇总 ====
REG_ADDR="{{__ip}}:${PORT}"
echo "私有镜像仓库已就绪: ${REG_ADDR}"
if [ -n "${REG_USER}" ]; then
  echo "登录: docker login ${REG_ADDR}  （用户: ${REG_USER}）"
fi
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
echo "推送示例:"
echo "  docker tag myapp:v1 ${REG_ADDR}/myapp:v1"
echo "  docker push ${REG_ADDR}/myapp:v1"
echo "注意: 当前为 HTTP 明文仓库，客户端需在 /etc/docker/daemon.json 的"
echo "  insecure-registries 中加入 \"${REG_ADDR}\" 后 systemctl restart docker"
echo "空间回收: docker exec ${CONTAINER} bin/registry garbage-collect /etc/docker/registry/config.yml"
