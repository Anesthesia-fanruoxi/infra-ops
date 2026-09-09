#!/bin/bash
set -e

# Nacos 集群节点部署脚本（host 网络，指定首节点）：
#   - 首节点（is_bootstrap=true）额外部署 MySQL 容器并初始化 nacos 库（幂等），供全集群共享
#   - 每个节点以 MODE=cluster + NACOS_SERVERS（全节点 ip:port）组集群，统一 NACOS_AUTH_TOKEN
#   - host 网络，HTTP 8848 / gRPC 9848/9849 直通宿主机，客户端直接连任一节点 ip:8848
# 模板参数: {{image}} {{cluster_name}} {{port}} {{nacos_token}} {{db_username}} {{db_password}} {{home_dir}} {{mysql_image}}
# 内置变量: {{__ip}} {{__seq}} {{__role}} {{__is_bootstrap}} {{__node_ips}} {{__master_ip}}

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

# ==== 模板插值参数 ====
IMAGE="{{image}}"
CLUSTER_NAME="{{cluster_name}}"
PORT="{{port}}"
NACOS_TOKEN="{{nacos_token}}"
DB_USER="{{db_username}}"
DB_PASS="{{db_password}}"
DB_HOST="{{db_host}}"
HOME_DIR="{{home_dir}}"
MYSQL_IMAGE="{{mysql_image}}"
SELF_IP="{{__ip}}"
SELF_ID="{{__seq}}"
ROLE="{{__role}}"
IS_BOOTSTRAP="{{__is_bootstrap}}"
NODE_IPS="{{__node_ips}}"
MASTER_IP="{{__master_ip}}"
CONTAINER="nacos-${SELF_ID}"
MYSQL_CONTAINER="nacos-mysql"
GRPC_PORT=$((PORT + 1000))

[ -n "${IMAGE}" ] || IMAGE="nacos/nacos-server:v2.3.2"
[ -n "${CLUSTER_NAME}" ] || CLUSTER_NAME="nacos-cluster"
[ -n "${PORT}" ] || PORT=8848
[ -n "${NACOS_TOKEN}" ] || { echo "NACOS_AUTH_TOKEN 不能为空（≥32 字符，全节点必须一致）"; exit 1; }
[ -n "${DB_USER}" ] && [ -n "${DB_PASS}" ] || { echo "数据库用户名/密码不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${MYSQL_IMAGE}" ] || MYSQL_IMAGE="mysql:8.0"
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
[ -n "${GRPC_PORT}" ] || GRPC_PORT=9848

# 生成 NACOS_SERVERS：全节点 ip:port（空格分隔）
NACOS_SERVERS=""
IFS=',' read -ra IPS <<< "${NODE_IPS}"
for ip in "${IPS[@]}"; do
  ip_trim=$(echo "${ip}" | xargs)
  [ -n "${ip_trim}" ] || continue
  if [ -n "${NACOS_SERVERS}" ]; then NACOS_SERVERS="${NACOS_SERVERS} "; fi
  NACOS_SERVERS="${NACOS_SERVERS}${ip_trim}:${PORT}"
done
[ -n "${NACOS_SERVERS}" ] || { echo "生成 NACOS_SERVERS 失败"; exit 1; }

echo "=== Nacos 集群节点: ${SELF_IP} (master=${IS_BOOTSTRAP}) NACOS_SERVERS=${NACOS_SERVERS} ==="
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

# ================= 首节点：部署 MySQL + 初始化 nacos 库（仅在未指定外部 MySQL 时） =================
if [ "${IS_BOOTSTRAP}" = "true" ] && [ -z "${DB_HOST}" ]; then
  migrate_old "${MYSQL_CONTAINER}"
  cat > "${HOME_DIR}/mysql-compose.yml" <<YAMLEOF
name: nacos-mysql
services:
  mysql:
    image: ${MYSQL_IMAGE}
    container_name: ${MYSQL_CONTAINER}
    restart: always
    network_mode: host
    environment:
      - MYSQL_ROOT_PASSWORD=${DB_PASS}
      - MYSQL_DATABASE=mysql
    command:
      - --default-authentication-plugin=mysql_native_password
      - --character-set-server=utf8mb4
      - --collation-server=utf8mb4_unicode_ci
    volumes:
      - ${HOME_DIR}/mysql-data:/var/lib/mysql
YAMLEOF
  echo "拉取 MySQL 镜像 ${MYSQL_IMAGE}"
  docker compose -f "${HOME_DIR}/mysql-compose.yml" pull
  docker compose -f "${HOME_DIR}/mysql-compose.yml" up -d
  wait_tcp_host 127.0.0.1 3306 "MySQL" 45

  # 初始化 nacos 库（幂等：有表则跳过）
  TABLE_COUNT=$(docker exec "${MYSQL_CONTAINER}" mysql -uroot -p"${DB_PASS}" -D nacos -e "SHOW TABLES;" 2>/dev/null | wc -l || echo 0)
  if [ "${TABLE_COUNT}" -gt 5 ]; then
    echo "[MySQL] nacos 库已初始化（${TABLE_COUNT} 张表），跳过 SQL 导入"
  else
    echo "[MySQL] 初始化 nacos 数据库..."
    umask 077
    cat > /tmp/nacos_init_cluster.sql <<'NACOSQLEOF'
@@NACOS_SQL@@
NACOSQLEOF
    umask 022
    sed -i "s/NACOS_USER_PLACEHOLDER/${DB_USER}/g; s/NACOS_PASSWORD_PLACEHOLDER/${DB_PASS}/g" /tmp/nacos_init_cluster.sql
    if grep -q "PLACEHOLDER" /tmp/nacos_init_cluster.sql; then
      echo "SQL 占位符替换失败"; exit 1
    fi
    if docker exec -i "${MYSQL_CONTAINER}" mysql -uroot -p"${DB_PASS}" < /tmp/nacos_init_cluster.sql; then
      rm -f /tmp/nacos_init_cluster.sql
    else
      echo "SQL 导入失败，文件已保留: /tmp/nacos_init_cluster.sql"; exit 1
    fi
    echo "[MySQL] nacos 数据库初始化完成"
  fi
fi

# ================= 外部 MySQL 连通性校验（仅指定了外部数据库时，所有节点执行） =================
if [ -n "${DB_HOST}" ]; then
  echo "校验外部 MySQL ${MYSQL_HOST}:${DB_PORT} 连通性及 nacos 库..."
  DB_OK=0
  for _ in $(seq 1 10); do
    if (echo > "/dev/tcp/${MYSQL_HOST}/${DB_PORT}") >/dev/null 2>&1; then DB_OK=1; break; fi
    sleep 3
  done
  if [ "${DB_OK}" != "1" ]; then
    echo "无法连接外部 MySQL ${MYSQL_HOST}:${DB_PORT}，请确认地址、网络与防火墙"; exit 1
  fi
  # 校验账号可访问 nacos 库（root 无法用 nacos 用户校验时忽略）
  if docker ps -a --format '{{.Names}}' | grep -qx "${MYSQL_CONTAINER}"; then
    docker exec "${MYSQL_CONTAINER}" mysql -uroot -p"${DB_PASS}" -D nacos -e "SELECT 1;" &>/dev/null \
      && echo "[MySQL] nacos 库可访问" || echo "[MySQL] 警告：未能以 ${DB_USER} 访问 nacos 库，确认外部库已初始化（含配置与认证建表）"
  fi
fi

# ================= 部署 Nacos 节点 =================
migrate_old "${CONTAINER}"
cat > "${HOME_DIR}/compose.yml" <<YAMLEOF
name: nacos-${CLUSTER_NAME}
services:
  nacos:
    image: ${IMAGE}
    container_name: ${CONTAINER}
    restart: always
    network_mode: host
    environment:
      - MODE=cluster
      - PREFER_HOST_MODE=ip
      - NACOS_SERVERS=${NACOS_SERVERS}
      - NACOS_AUTH_ENABLE=true
      - NACOS_AUTH_TOKEN=${NACOS_TOKEN}
      - NACOS_AUTH_IDENTITY_KEY=serverIdentity
      - NACOS_AUTH_IDENTITY_VALUE=security
      - SPRING_DATASOURCE_PLATFORM=mysql
      - MYSQL_SERVICE_HOST=${MYSQL_HOST}
      - MYSQL_SERVICE_PORT=${DB_PORT}
      - MYSQL_SERVICE_USER=${DB_USER}
      - MYSQL_SERVICE_PASSWORD=${DB_PASS}
      - MYSQL_SERVICE_DB_NAME=nacos
      - JVM_XMS=512m
      - JVM_XMX=512m
      - JVM_XMN=256m
    volumes:
      - ${HOME_DIR}/data:/home/nacos/data
  mysql_probe:
    image: ${IMAGE}
    container_name: ${CONTAINER}-probe
    restart: "no"
    network_mode: host
    entrypoint: [""]
    command: ["sh", "-c", "exit 0"]
YAMLEOF
# 移除探测服务（仅占位，防 YAML 结构校验问题）
sed -i '/^  mysql_probe:/,/^  #/d' "${HOME_DIR}/compose.yml"

echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ================= 就绪检查（控制台 + 集群成员） =================
READY=0
for _ in $(seq 1 60); do
  if docker logs "${CONTAINER}" 2>&1 | grep -q "Nacos started successfully" || \
     curl -sf -o /dev/null "http://127.0.0.1:${PORT}/nacos/"; then READY=1; break; fi
  sleep 3
done
[ "${READY}" = "1" ] || { echo "Nacos 启动超时，最近日志:"; docker logs "${CONTAINER}" --tail 40 2>&1 || true; exit 1; }

echo "=== Nacos 节点就绪: ${SELF_IP}:${PORT}/nacos/ ==="
echo "控制台: http://${SELF_IP}:${PORT}/nacos/（默认账号 nacos/nacos）"
echo "gRPC 端口: ${GRPC_PORT}/$((${GRPC_PORT}+1))（host 直通）"
echo "NACOS_SERVERS: ${NACOS_SERVERS}"
if [ -n "${DB_HOST}" ]; then
  echo "数据库: 外部 ${MYSQL_HOST}:${DB_PORT}/nacos（用户 ${DB_USER}）"
else
  echo "数据库: ${MASTER_IP}:3306/nacos（root/${DB_PASS}，自动部署）"
fi
echo "compose: ${HOME_DIR}/compose.yml（改后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"