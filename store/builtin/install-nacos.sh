#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
GRPC_PORT="{{grpc_port}}"
DB_USER="{{db_username}}"
DB_PASS="{{db_password}}"
IMAGE="{{image}}"
CONTAINER=nacos
NET=middleware_net

[ -n "${DB_USER}" ] && [ -n "${DB_PASS}" ] || { echo "数据库用户名/密码不能为空"; exit 1; }

# ==== 依赖检查：MySQL 容器（须先部署 MySQL 模块，同一主机） ====
if ! docker ps --format '{{.Names}}' | grep -qx "mysql"; then
  echo "未检测到运行中的 MySQL 容器，请先在同一主机执行「部署 MySQL」模板"; exit 1
fi

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 数据库初始化（已存在完整表结构则跳过） ====
TABLE_COUNT=$(docker exec mysql mysql -u"${DB_USER}" -p"${DB_PASS}" -D nacos -e "SHOW TABLES;" 2>/dev/null | wc -l || echo 0)
if [ "${TABLE_COUNT}" -gt 5 ]; then
  echo "nacos 数据库已初始化（${TABLE_COUNT} 张表），跳过 SQL 导入"
else
  echo "初始化 nacos 数据库..."
  cat > /tmp/nacos_init.sql <<'NACOSQLEOF'
@@NACOS_SQL@@
NACOSQLEOF
  sed -i "s/NACOS_USER_PLACEHOLDER/${DB_USER}/g; s/NACOS_PASSWORD_PLACEHOLDER/${DB_PASS}/g" /tmp/nacos_init.sql
  if grep -q "PLACEHOLDER" /tmp/nacos_init.sql; then
    echo "SQL 占位符替换失败"; exit 1
  fi
  # 建库/建用户需要 root 权限（nacos 用户仅有 nacos 库权限）
  MYSQL_ROOT="$(docker exec mysql printenv MYSQL_ROOT_PASSWORD 2>/dev/null || true)"
  if [ -z "${MYSQL_ROOT}" ]; then
    echo "无法从 mysql 容器读取 MYSQL_ROOT_PASSWORD 环境变量，无法自动初始化数据库"
    echo "SQL 文件已保留: /tmp/nacos_init.sql，可手动执行:"
    echo "  docker exec -i mysql mysql -uroot -p<root密码> < /tmp/nacos_init.sql"
    exit 1
  fi
  if docker exec mysql mysql -uroot -p"${MYSQL_ROOT}" < /tmp/nacos_init.sql; then
    rm -f /tmp/nacos_init.sql
  else
    echo "SQL 导入失败，文件已保留: /tmp/nacos_init.sql"
    exit 1
  fi
  NEW_COUNT=$(docker exec mysql mysql -u"${DB_USER}" -p"${DB_PASS}" -D nacos -e "SHOW TABLES;" 2>/dev/null | wc -l)
  echo "nacos 数据库初始化完成（${NEW_COUNT} 张表）"
fi

# ==== 幂等部署 ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${PORT}:8848" \
  -p "${GRPC_PORT}:9848" \
  -e MODE=standalone \
  -e SPRING_DATASOURCE_PLATFORM=mysql \
  -e MYSQL_SERVICE_HOST=mysql \
  -e MYSQL_SERVICE_PORT=3306 \
  -e MYSQL_SERVICE_USER="${DB_USER}" \
  -e MYSQL_SERVICE_PASSWORD="${DB_PASS}" \
  -e MYSQL_SERVICE_DB_NAME=nacos \
  "${IMAGE}"

# ==== 等待控制台就绪 ====
READY=0
for _ in $(seq 1 45); do
  if docker exec "${CONTAINER}" curl -sf -o /dev/null "http://localhost:8848/nacos/"; then READY=1; break; fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "Nacos 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "Nacos 已就绪: http://{{__ip}}:${PORT}/nacos/（默认账号 nacos/nacos，gRPC 端口 ${GRPC_PORT}）"
