#!/bin/bash
set -e

# PowerJob 集群节点部署脚本（host 网络，指定首节点）：
#   - 首节点（is_bootstrap=true）额外部署 MySQL 容器并初始化 powerjob 库（幂等），供全集群共享
#   - 每个节点部署一个 powerjob-server，经 JDBC 连接首节点 MySQL（对等集群，worker 连任一节点即可）
#   - host 网络，控制台 7700 / akka 10086 等端口直通宿主机
#   - **全部服务收敛进单一 compose 文件 ${HOME_DIR}/compose.yml**：引擎探活只 glob
#     compose.yml / docker-compose.yml / */compose.yml 三个约定名，停服/清理也只 down
#     ${HOME_DIR}/compose.yml。曾写 compose.yml.${SELF_ID}（server）+ mysql-compose.yml（MySQL），
#     二者都不在约定名内 ⇒ 探活恒假红、缩容/卸载不停容器。故 MySQL 改为同文件内的条件服务。
# 模板参数: {{image}} {{db_username}} {{db_password}} {{home_dir}} {{mysql_image}} {{akka_port}} {{server_port}}
# 内置变量: {{__ip}} {{__seq}} {{__role}} {{__is_bootstrap}} {{__node_ips}} {{__master_ip}}

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

# ==== 模板插值参数 ====
IMAGE="{{image}}"
DB_USER="{{db_username}}"
DB_PASS="{{db_password}}"
DB_HOST="{{db_host}}"
HOME_DIR="{{home_dir}}"
MYSQL_IMAGE="{{mysql_image}}"
AKKA_PORT="{{akka_port}}"
SERVER_PORT="{{server_port}}"
SELF_IP="{{__ip}}"
SELF_ID="{{__seq}}"
ROLE="{{__role}}"
IS_BOOTSTRAP="{{__is_bootstrap}}"
NODE_IPS="{{__node_ips}}"
MASTER_IP="{{__master_ip}}"
CONTAINER="powerjob-server-${SELF_ID}"
MYSQL_CONTAINER="powerjob-mysql"
COMPOSE="${HOME_DIR}/compose.yml"
PROJECT="powerjob-node-${SELF_ID}"

[ -n "${IMAGE}" ] || IMAGE="powerjob/powerjob-server:latest"
[ -n "${DB_USER}" ] && [ -n "${DB_PASS}" ] || { echo "数据库用户名/密码不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${MYSQL_IMAGE}" ] || MYSQL_IMAGE="mysql:8.0"
[ -n "${AKKA_PORT}" ] || AKKA_PORT=10086
[ -n "${SERVER_PORT}" ] || SERVER_PORT=7700
[ -n "${NODE_IPS}" ] || NODE_IPS="${SELF_IP}"
[ -n "${MASTER_IP}" ] || MASTER_IP="${SELF_IP}"
# 数据库地址推导：db_host 留空 -> 首节点自动部署 MySQL；否则解析外部 MySQL host[:port]
DB_PORT=3306
if [ -n "${DB_HOST}" ]; then
  if [[ "${DB_HOST}" == *":"* ]]; then
    MYSQL_HOST="${DB_HOST%%:*}"
    DB_PORT="${DB_HOST##*:}"
    [ -n "${MYSQL_HOST}" ] && [ -n "${DB_PORT}" ] || { echo "外部 MySQL 地址不合法: ${DB_HOST}"; exit 1; }
  else
    MYSQL_HOST="${DB_HOST}"
  fi
  echo "使用外部 MySQL: ${MYSQL_HOST}:${DB_PORT}"
else
  MYSQL_HOST="${MASTER_IP}"
  echo "未指定外部 MySQL，首节点将自动部署（连接地址 ${MYSQL_HOST}:${DB_PORT}）"
fi
# 首节点：is_bootstrap 或 role=master 或 seq==1
if [ "${IS_BOOTSTRAP}" != "true" ] && [ "${ROLE}" = "master" ]; then IS_BOOTSTRAP="true"; fi
if [ "${IS_BOOTSTRAP}" != "true" ] && [ "${SELF_ID}" = "1" ]; then IS_BOOTSTRAP="true"; fi
# 是否在本机拉起 MySQL：首节点且未指定外部库
LOCAL_DB="false"
if [ "${IS_BOOTSTRAP}" = "true" ] && [ -z "${DB_HOST}" ]; then LOCAL_DB="true"; fi

# PowerJob 首个 server 首次启动会自动初始化表结构（JPA ddl + 内置权限），无需手工 SQL
JDBC_URL="jdbc:mysql://${MYSQL_HOST}:${DB_PORT}/powerjob?useUnicode=true&characterEncoding=UTF-8&serverTimezone=Asia/Shanghai&lower_case_table_names=1"

echo "=== PowerJob 节点: ${SELF_IP} (bootstrap=${IS_BOOTSTRAP}) 数据库=${MYSQL_HOST}:${DB_PORT} ==="
mkdir -p "${HOME_DIR}"

migrate_old() {
  local C="$1"
  if docker ps -a --format '{{.Names}}' | grep -qx "${C}"; then
    local PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${C}" 2>/dev/null || true)"
    if [ -z "${PROJ}" ]; then
      echo "检测到旧容器（非 compose 管理）: ${C}，移除后重建"
      docker rm -f "${C}" &>/dev/null || true
    fi
  fi
}
# 容器已被别的 compose 项目接管时移除，避免 container_name 冲突（旧版 MySQL 单独一个项目）
adopt_own_project() {
  local C="$1"
  docker ps -a --format '{{.Names}}' | grep -qx "${C}" || return 0
  local PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${C}" 2>/dev/null || true)"
  if [ -n "${PROJ}" ] && [ "${PROJ}" != "${PROJECT}" ]; then
    echo "容器 ${C} 属于旧 compose 项目 ${PROJ}，移除后并入 ${PROJECT}"
    docker rm -f "${C}" &>/dev/null || true
  fi
}
wait_tcp_host() { # wait_tcp_host <ip> <port> <名称> <次数>
  local IP="$1" PORT="$2" NAME="$3" TIMES="$4" READY=0
  for _ in $(seq 1 "${TIMES}"); do
    (echo > "/dev/tcp/${IP}/${PORT}") >/dev/null 2>&1 && { READY=1; break; }
    sleep 3
  done
  if [ "${READY}" != "1" ]; then
    echo "${NAME} 端口 ${IP}:${PORT} 未就绪，最近日志:"
    docker logs "${MYSQL_CONTAINER}" --tail 30 2>&1 || true
    exit 1
  fi
}

# ==== 迁移旧版 compose 文件（compose.yml.<seq> / mysql-compose.yml -> compose.yml） ====
for legacy in "${HOME_DIR}"/compose.yml.* "${HOME_DIR}/mysql-compose.yml"; do
  [ -f "${legacy}" ] || continue
  echo "检测到旧版 compose: ${legacy}，停止后并入 ${COMPOSE}"
  docker compose -f "${legacy}" down --remove-orphans &>/dev/null || true
  rm -f "${legacy}"
done
migrate_old "${CONTAINER}"
migrate_old "${MYSQL_CONTAINER}"
adopt_own_project "${CONTAINER}"
adopt_own_project "${MYSQL_CONTAINER}"

# ================= 生成单一 compose 文件（MySQL 仅首节点且未指定外部库时，作为条件服务） =================
{
  echo "name: ${PROJECT}"
  echo "services:"
  if [ "${LOCAL_DB}" = "true" ]; then
    cat <<YAMLEOF
  mysql:
    image: ${MYSQL_IMAGE}
    container_name: ${MYSQL_CONTAINER}
    restart: always
    network_mode: host
    environment:
      - MYSQL_ROOT_PASSWORD=${DB_PASS}
      - MYSQL_DATABASE=powerjob
      - TZ=Asia/Shanghai
    command:
      - --default-authentication-plugin=mysql_native_password
      - --character-set-server=utf8mb4
      - --collation-server=utf8mb4_unicode_ci
      - --lower_case_table_names=1
    volumes:
      - ${HOME_DIR}/mysql-data:/var/lib/mysql
YAMLEOF
  fi
  cat <<YAMLEOF
  server:
    image: ${IMAGE}
    container_name: ${CONTAINER}
    restart: always
    network_mode: host
    environment:
      - JVMOPTIONS=-Xmx512m
      - PARAMS=--oms.mongodb.enable=false --spring.datasource.core.jdbc-url=${JDBC_URL} --spring.datasource.core.username=${DB_USER} --spring.datasource.core.password=${DB_PASS} --oms.local.remote-endpoint=${SELF_IP}:${AKKA_PORT}
    volumes:
      - ${HOME_DIR}/server-${SELF_ID}:/root/powerjob/server/
YAMLEOF
} > "${COMPOSE}"

echo "拉取镜像 ${IMAGE}"
docker compose -f "${COMPOSE}" pull

# ================= 首节点：启动 MySQL + 初始化 powerjob 库（仅在未指定外部 MySQL 时） =================
if [ "${LOCAL_DB}" = "true" ]; then
  docker compose -f "${COMPOSE}" up -d mysql
  wait_tcp_host 127.0.0.1 3306 "MySQL" 45

  # 授权业务库用户（幂等）
  docker exec "${MYSQL_CONTAINER}" mysql -uroot -p"${DB_PASS}" -e \
    "CREATE DATABASE IF NOT EXISTS \`powerjob\` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
     CREATE USER IF NOT EXISTS '${DB_USER}'@'%' IDENTIFIED BY '${DB_PASS}';
     ALTER USER '${DB_USER}'@'%' IDENTIFIED BY '${DB_PASS}';
     GRANT ALL PRIVILEGES ON \`powerjob\`.* TO '${DB_USER}'@'%';
     FLUSH PRIVILEGES;" 2>/dev/null || {
    echo "[MySQL] 数据库/用户已存在则忽略，继续"
  }
  echo "[MySQL] powerjob 数据库就绪"
fi

# ================= 外部 MySQL 连通性校验（仅指定了外部数据库时，所有节点执行） =================
if [ -n "${DB_HOST}" ]; then
  echo "校验外部 MySQL ${MYSQL_HOST}:${DB_PORT} 连通性..."
  DB_OK=0
  for _ in $(seq 1 10); do
    if (echo > "/dev/tcp/${MYSQL_HOST}/${DB_PORT}") >/dev/null 2>&1; then DB_OK=1; break; fi
    sleep 3
  done
  if [ "${DB_OK}" != "1" ]; then
    echo "无法连接外部 MySQL ${MYSQL_HOST}:${DB_PORT}，请确认地址、网络与防火墙"; exit 1
  fi
  echo "[MySQL] 外部数据库可连接（已授权 ${DB_USER} 拥有 powerjob 数据库权限并已建表）"
fi

# ================= 部署 PowerJob server 节点 =================
docker compose -f "${COMPOSE}" up -d server

# ================= 就绪检查（控制台端口） =================
READY=0
for _ in $(seq 1 90); do
  if (echo > "/dev/tcp/${SELF_IP}/${SERVER_PORT}") >/dev/null 2>&1 || \
     docker logs "${CONTAINER}" 2>&1 | grep -qiE "Started.*Application|Tomcat started|successfully"; then READY=1; break; fi
  sleep 3
done
[ "${READY}" = "1" ] || { echo "PowerJob 启动超时，最近日志:"; docker logs "${CONTAINER}" --tail 50 2>&1 || true; exit 1; }
# 稍等 HTTP 完全就绪，供控制台访问
sleep 5

echo "=== PowerJob 节点就绪: ${SELF_IP}:${SERVER_PORT} ==="
echo "控制台: http://${SELF_IP}:${SERVER_PORT}/（默认账号 powerjob/powerjob123456）"
echo "Akka 端口: ${AKKA_PORT}（host 直通，供 worker 连接）"
if [ -n "${DB_HOST}" ]; then
  echo "数据库: 外部 ${MYSQL_HOST}:${DB_PORT}/powerjob（用户 ${DB_USER}）"
else
  echo "数据库: ${MASTER_IP}:3306/powerjob（root/${DB_PASS}，自动部署）"
fi
echo "compose: ${COMPOSE}（改后 docker compose -f ${COMPOSE} up -d 生效）"
