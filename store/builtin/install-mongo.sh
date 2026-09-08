#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"

if [ -d "/data/middleware/mongodb" ] && [ ! -d "${HOME_DIR}" ]; then
  echo "警告: 检测到旧版数据目录 /data/middleware/mongodb，如需保留历史数据请先迁移到 ${HOME_DIR}，或将服务主目录填为旧路径"
fi
MONGO_USER="{{admin_username}}"
MONGO_PASS="{{admin_password}}"
IMAGE="{{image}}"
CONTAINER=mongodb

[ -n "${MONGO_PASS}" ] || { echo "管理员密码不能为空"; exit 1; }

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 配置文件（已有则备份） ====
mkdir -p "${HOME_DIR}/data"
if [ -f "${HOME_DIR}/mongodb.conf" ]; then
  cp "${HOME_DIR}/mongodb.conf" "${HOME_DIR}/mongodb.conf.bak.$(date +%s)"
  echo "已备份原有 mongodb.conf"
fi
cat > "${HOME_DIR}/mongodb.conf" <<'MONGOCONF'
storage:
  dbPath: /data/db
  directoryPerDB: true
systemLog:
  destination: file
  path: /var/log/mongodb/mongodb.log
  logAppend: true
net:
  bindIp: 0.0.0.0
  port: 27017
MONGOCONF

# ==== 敏感凭据落盘（env_file 不做插值，密码含 $ 等特殊字符也安全） ====
umask 077
printf 'MONGO_INITDB_ROOT_USERNAME=%s\nMONGO_INITDB_ROOT_PASSWORD=%s\n' "${MONGO_USER}" "${MONGO_PASS}" > "${HOME_DIR}/secrets.env"
umask 022

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
name: mongodb
services:
  mongodb:
    image: {{image}}
    container_name: mongodb
    restart: always
    ports:
      - "{{port}}:27017"
    env_file:
      - secrets.env
    volumes:
      - {{home_dir}}/data:/data/db
      - {{home_dir}}/mongodb.conf:/etc/mongodb.conf:ro
      - /etc/localtime:/etc/localtime:ro
    command: ["mongod", "-f", "/etc/mongodb.conf"]
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
for _ in $(seq 1 30); do
  if docker exec "${CONTAINER}" mongosh --quiet --eval "db.adminCommand('ping')" &>/dev/null; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "MongoDB 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

# ==== 管理员用户（首次初始化由镜像自动创建；已存在则跳过） ====
EXIST=$(docker exec "${CONTAINER}" mongosh --quiet --eval 'db.getSiblingDB("admin").getUser("'"${MONGO_USER}"'")' 2>/dev/null | grep user || true)
if [ -n "${EXIST}" ]; then
  echo "管理员用户 ${MONGO_USER} 已存在，跳过创建"
else
  cat > /tmp/init-mongo-admin.js <<EOF
use admin;
db.createUser({
  user: "${MONGO_USER}",
  pwd: "${MONGO_PASS}",
  roles: [{ role: "userAdminAnyDatabase", db: "admin" }, "readWriteAnyDatabase"]
});
EOF
  docker cp /tmp/init-mongo-admin.js "${CONTAINER}:/tmp/init-mongo-admin.js"
  if docker exec "${CONTAINER}" mongosh admin --quiet --file /tmp/init-mongo-admin.js &>/dev/null; then
    echo "管理员用户 ${MONGO_USER} 创建成功"
  else
    echo "管理员用户创建失败，请手动检查"
  fi
  rm -f /tmp/init-mongo-admin.js
fi

echo "MongoDB 已就绪: {{__ip}}:${PORT}（数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
