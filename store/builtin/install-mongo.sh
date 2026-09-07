#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
DATA_DIR="{{data_dir}}"
MONGO_USER="{{admin_username}}"
MONGO_PASS="{{admin_password}}"
IMAGE="{{image}}"
CONTAINER=mongodb
NET=middleware_net

[ -n "${MONGO_PASS}" ] || { echo "管理员密码不能为空"; exit 1; }

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 配置文件（已有则备份） ====
mkdir -p "${DATA_DIR}/db"
if [ -f "${DATA_DIR}/mongodb.conf" ]; then
  cp "${DATA_DIR}/mongodb.conf" "${DATA_DIR}/mongodb.conf.bak.$(date +%s)"
  echo "已备份原有 mongodb.conf"
fi
cat > "${DATA_DIR}/mongodb.conf" <<'MONGOCONF'
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

# ==== 幂等部署（数据目录保留） ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${PORT}:27017" \
  -e MONGO_INITDB_ROOT_USERNAME="${MONGO_USER}" \
  -e MONGO_INITDB_ROOT_PASSWORD="${MONGO_PASS}" \
  -v "${DATA_DIR}/db:/data/db" \
  -v "${DATA_DIR}/mongodb.conf:/etc/mongodb.conf:ro" \
  -v /etc/localtime:/etc/localtime:ro \
  "${IMAGE}" \
  mongod -f /etc/mongodb.conf

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

echo "MongoDB 已就绪: {{__ip}}:${PORT}（数据目录 ${DATA_DIR}/db）"
