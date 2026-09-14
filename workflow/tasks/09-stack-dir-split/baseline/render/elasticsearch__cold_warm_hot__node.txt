#!/bin/bash
# Elasticsearch 冷热温模式 —— RUN 分发器（主脑流水线多阶段）
# 由 {__run} 决定执行哪个 run_<key> 分支；角色由平台注入的 __es_* 变量派生。
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

CLUSTER_NAME="{{cluster_name}}"
PORT="{{port}}"
TRANSPORT_PORT="{{transport_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
SSL="{{ssl_enabled}}"
HEAP_XMS="{{heap_xms}}"
HEAP_XMX="{{heap_xmx}}"
JVM_OPTS="{{jvm_opts}}"
NODE_NAME="{{__node_name}}"
SELF_IP="{{__ip}}"
ES_ROLES="{{__es_roles}}"
ES_IS_MASTER="{{__es_is_master}}"
ES_BOOTSTRAP="{{__es_bootstrap}}"
ES_BOOTSTRAP_NAME="{{__es_bootstrap_name}}"
SEED_HOSTS="{{__seed_hosts}}"
RUN="{{__run}}"

[ -n "${CLUSTER_NAME}" ] || { echo "集群名称不能为空"; exit 1; }
[ -n "${NODE_NAME}" ] || { echo "节点名称不能为空"; exit 1; }
[ -n "${PORT}" ] || PORT=9200
[ -n "${TRANSPORT_PORT}" ] || TRANSPORT_PORT=9300
[ -n "${SEED_HOSTS}" ] || { echo "种子地址不能为空"; exit 1; }
[ -n "${HEAP_XMS}" ] || HEAP_XMS=1g
[ -n "${HEAP_XMX}" ] || HEAP_XMX=1g
[ -n "${JVM_OPTS}" ] || JVM_OPTS="-XX:+UseG1GC"
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="elasticsearch:9.5.3"

CONTAINER="es-${NODE_NAME}"
ROLES_CSV="{{roles}}"

roles_has() { # roles_has <needle>  <ROLES_CSV>
  local needle="$1"; shift
  case ",${ROLES_CSV:-}," in *",${needle},"*) return 0;; *) return 1;; esac
}
is_master() { roles_has master; }
is_coord() { [ -z "${ES_ROLES}" ] && ! is_master; }
is_data()   { [ -n "${ES_ROLES}" ] && ! is_master; }

node_roles_yml() {
  if is_master; then echo 'node.roles: [ "master" ]'
  elif [ -z "${ES_ROLES}" ]; then echo 'node.roles: [ ]'
  else
    # 把 "data_hot","data_warm" 拼接为数组行
    echo "node.roles: [ ${ES_ROLES} ]"
  fi
}

init_masters_yml() {
  if [ "${ES_BOOTSTRAP}" = "true" ] && [ -n "${ES_BOOTSTRAP_NAME}" ]; then
    echo "cluster.initial_master_nodes: [ \"${ES_BOOTSTRAP_NAME}\" ]"
  fi
}

ssl_yml() {
  if [ "${SSL}" = "true" ]; then
    # 仅做 HTTP 层 TLS 加密传输（自签证书，PEM 免密码），不启用安全认证，
    # 使探活/校验/缩容下线等脚本可用 curl -k 免认证访问；客户端可下载 ca.crt 建立信任。
    # 注意：显式保持 xpack.security.enabled=false，避免 http.ssl 隐性带上认证导致接口 401。
    echo "xpack.security.enabled: false"
    echo "xpack.security.http.ssl.enabled: true"
    echo "xpack.security.http.ssl.certificate: certs/http.crt"
    echo "xpack.security.http.ssl.key: certs/http.key"
  else
    echo "xpack.security.enabled: false"
    echo "xpack.security.enrollment.enabled: false"
  fi
}

ensure_certs() { # 生成 CA 与节点自签证书（HTTP SSL），证书落盘 home_dir/certs，不入库
  local certdir="${HOME_DIR}/certs"
  mkdir -p "${certdir}"
  if [ ! -f "${certdir}/ca.crt" ]; then
    openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=${CLUSTER_NAME}-ca" \
      -keyout "${certdir}/ca.key" -out "${certdir}/ca.crt" >/dev/null 2>&1
  fi
  if [ ! -f "${certdir}/http.key" ]; then
    openssl req -newkey rsa:2048 -nodes -subj "/CN=${NODE_NAME}" \
      -keyout "${certdir}/http.key" -out "${certdir}/http.csr" >/dev/null 2>&1
    openssl x509 -req -in "${certdir}/http.csr" -CA "${certdir}/ca.crt" -CAkey "${certdir}/ca.key" \
      -CAcreateserial -days 3650 -out "${certdir}/http.crt" >/dev/null 2>&1
  fi
  # ES 要求私钥文件非 world-readable，统一收归 es 用户（1000）读取权限
  chown -R 1000:0 "${certdir}" 2>/dev/null || true
  chmod 644 "${certdir}/http.crt" "${certdir}/ca.crt" 2>/dev/null || true
  chmod 600 "${certdir}/http.key" "${certdir}/ca.key" 2>/dev/null || true
}

write_yml() {
  local certline=""
  [ "${SSL}" = "true" ] && certline="  - {{home_dir}}/certs:/usr/share/elasticsearch/config/certs:ro"
  cat > "${HOME_DIR}/elasticsearch.yml" <<ESYML
cluster.name: ${CLUSTER_NAME}
node.name: ${NODE_NAME}
$(node_roles_yml)
path.data: /usr/share/elasticsearch/data
path.logs: /usr/share/elasticsearch/logs
network.host: 0.0.0.0
http.port: ${PORT}
transport.port: ${TRANSPORT_PORT}
transport.host: 0.0.0.0
network.publish_host: ${SELF_IP}
discovery.seed_hosts: [ ${SEED_HOSTS} ]
$(init_masters_yml)
$(ssl_yml)
ESYML
  chown 1000:0 "${HOME_DIR}/elasticsearch.yml" 2>/dev/null || true
  chmod 644 "${HOME_DIR}/elasticsearch.yml"
  rm -f "${HOME_DIR}/compose.ymlx" 2>/dev/null || true
  cat > "${HOME_DIR}/compose.yml" <<YAMLEOF
name: es-${CLUSTER_NAME}
services:
  elasticsearch:
    image: ${IMAGE}
    container_name: ${CONTAINER}
    restart: always
    network_mode: host
    environment:
      - ES_JAVA_OPTS=-Xms${HEAP_XMS} -Xmx${HEAP_XMX} ${JVM_OPTS}
      - bootstrap.memory_lock=true
    ulimits:
      memlock:
        soft: -1
        hard: -1
    volumes:
      - ${HOME_DIR}/data:/usr/share/elasticsearch/data
      - ${HOME_DIR}/logs:/usr/share/elasticsearch/logs
      - ${HOME_DIR}/elasticsearch.yml:/usr/share/elasticsearch/config/elasticsearch.yml:ro
${certline}
YAMLEOF
  [ "${SSL}" = "true" ] && ensure_certs
}

compose_down() {
  docker compose -f "${HOME_DIR}/compose.yml" down --remove-orphans 2>/dev/null || true
}

start_and_wait() {
  echo "拉取镜像 ${IMAGE}"
  docker compose -f "${HOME_DIR}/compose.yml" pull
  docker compose -f "${HOME_DIR}/compose.yml" up -d
  local scheme="http"
  [ "${SSL}" = "true" ] && scheme="https"
  local READY=0
  for _ in $(seq 1 80); do
    curl -sfk "https://127.0.0.1:${PORT}/" >/dev/null 2>&1 && { READY=1; break; }
    [ "${SSL}" = "true" ] || curl -sf "http://127.0.0.1:${PORT}/" >/dev/null 2>&1 && { READY=1; break; }
    sleep 5
  done
  if [ "${READY}" != "1" ]; then
    echo "Elasticsearch 节点启动超时，最近日志:"
    docker logs "${CONTAINER}" --tail 40 2>&1 || true
    exit 1
  fi
}

run_reset() {
  compose_down
  rm -rf "${HOME_DIR}/data" "${HOME_DIR}/logs" 2>/dev/null || true
  rm -f "${HOME_DIR}/elasticsearch.yml" "${HOME_DIR}/compose.yml"
  mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/logs"
  echo "已清理旧环境与残留容器(${NODE_NAME})"
}

run_masters() {
  is_master || { echo "本机非 master 候选，跳过"; return 0; }
  write_yml
  start_and_wait
  echo "master 候选已就绪: ${NODE_NAME}"
}

run_coords() {
  is_coord || { echo "本机非纯协调节点，跳过"; return 0; }
  write_yml
  start_and_wait
  echo "纯协调节点已就绪: ${NODE_NAME}（node.roles: []）"
}

run_data() {
  is_data || { echo "本机非数据节点，跳过"; return 0; }
  write_yml
  start_and_wait
  echo "数据节点已就绪: ${NODE_NAME}（${ES_ROLES}）"
}

mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/logs"
chown -R 1000:0 "${HOME_DIR}/data" "${HOME_DIR}/logs" 2>/dev/null || chmod -R 777 "${HOME_DIR}/data" "${HOME_DIR}/logs"
sysctl -w vm.max_map_count=262144 >/dev/null 2>&1 || echo "提示: 建议开启 vm.max_map_count=262144"

case "${RUN}" in
  reset) run_reset ;;
  masters) run_masters ;;
  coords) run_coords ;;
  data) run_data ;;
  *) echo "未知阶段 ${RUN}"; exit 1 ;;
esac

echo "节点 ${NODE_NAME} 阶段 ${RUN} 完成"
echo "资源: ${HOME_DIR}/compose.yml"