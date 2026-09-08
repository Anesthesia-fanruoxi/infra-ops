#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"

if [ -d "/data/middleware/mysql" ] && [ ! -d "${HOME_DIR}" ]; then
  echo "警告: 检测到旧版数据目录 /data/middleware/mysql，如需保留历史数据请先迁移到 ${HOME_DIR}，或将服务主目录填为旧路径"
fi
ROOT_PASS="{{root_password}}"
IMAGE="{{image}}"
CONTAINER=mysql

[ -n "${ROOT_PASS}" ] || { echo "root 密码不能为空"; exit 1; }

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

# ==== 配置文件（已有则备份） ====
mkdir -p "${HOME_DIR}/data"
if [ -f "${HOME_DIR}/my.cnf" ]; then
  cp "${HOME_DIR}/my.cnf" "${HOME_DIR}/my.cnf.bak.$(date +%s)"
  echo "已备份原有 my.cnf"
fi
# __DEPLOY_CONF__ my_cnf
cat > "${HOME_DIR}/my.cnf" <<'MYCNF'
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
# __DEPLOY_CONF_END__ my_cnf

# ==== 敏感凭据落盘（env_file 不做插值，密码含 $ 等特殊字符也安全） ====
umask 077
printf 'MYSQL_ROOT_PASSWORD=%s\n' "${ROOT_PASS}" > "${HOME_DIR}/secrets.env"
umask 022

# ==== 迁移旧版 docker run 容器（数据目录保留） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留；root 密码仅首次初始化数据时生效）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: mysql
services:
  mysql:
    image: {{image}}
    container_name: mysql
    restart: always
    ports:
      - "{{port}}:3306"
    env_file:
      - secrets.env
    volumes:
      - {{home_dir}}/data:/var/lib/mysql
      - {{home_dir}}/my.cnf:/etc/mysql/my.cnf:ro
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

echo "MySQL 已就绪: {{__ip}}:${PORT}（root / 数据目录 ${HOME_DIR}/data）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
if [ -n "$(ls -A "${HOME_DIR}/data" 2>/dev/null)" ]; then
  echo "提示: 数据目录非空，本次环境变量中的密码不会改变已有密码（密码仅首次初始化数据时生效）"
fi
