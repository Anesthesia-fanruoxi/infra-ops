#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
DATA_DIR="{{data_dir}}"
ROOT_PASS="{{root_password}}"
IMAGE="{{image}}"
CONTAINER=mysql
NET=middleware_net

[ -n "${ROOT_PASS}" ] || { echo "root 密码不能为空"; exit 1; }

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 配置文件（已有则备份） ====
mkdir -p "${DATA_DIR}/data"
if [ -f "${DATA_DIR}/my.cnf" ]; then
  cp "${DATA_DIR}/my.cnf" "${DATA_DIR}/my.cnf.bak.$(date +%s)"
  echo "已备份原有 my.cnf"
fi
cat > "${DATA_DIR}/my.cnf" <<'MYCNF'
[client]
default-character-set=utf8mb4
[mysql]
default-character-set=utf8mb4
[mysqld]
default_authentication_plugin=mysql_native_password
default-time_zone='+8:00'
skip-name-resolve
server-id=1
sql_mode=STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION
pid-file=/var/run/mysqld/mysqld.pid
socket=/var/run/mysqld/mysqld.sock
datadir=/var/lib/mysql
secure-file-priv=NULL
max_connections=10000
innodb_buffer_pool_size=2G
binlog_cache_size=1M
binlog_stmt_cache_size=1M
lower_case_table_names=1
skip-log-bin
MYCNF

# ==== 幂等部署（数据目录保留；注意 root 密码仅在首次初始化数据时生效） ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${PORT}:3306" \
  -e MYSQL_ROOT_PASSWORD="${ROOT_PASS}" \
  -v "${DATA_DIR}/data:/var/lib/mysql" \
  -v "${DATA_DIR}/my.cnf:/etc/mysql/my.cnf:ro" \
  "${IMAGE}"

# ==== 等待就绪 ====
READY=0
for _ in $(seq 1 60); do
  if docker exec "${CONTAINER}" mysqladmin ping -h localhost -uroot -p"${ROOT_PASS}" &>/dev/null; then
    READY=1; break
  fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "MySQL 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "MySQL 已就绪: {{__ip}}:${PORT}（root / 数据目录 ${DATA_DIR}/data）"
if [ -n "$(ls -A "${DATA_DIR}/data" 2>/dev/null)" ]; then
  echo "提示: 数据目录非空，本次环境变量中的密码不会改变已有密码（密码仅首次初始化数据时生效）"
fi
