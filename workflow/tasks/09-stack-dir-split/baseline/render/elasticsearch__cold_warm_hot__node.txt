#!/bin/bash
# Elasticsearch 冷热温模式 —— RUN 分发器（一机多容器）
# 角色矩阵的每个勾选格 = 本机一个独立 ES 容器：ES_NODES 列出本机全部角色，
# 端口按角色固定偏移（master 9200 / 协调 9210 / hot 9220 / warm 9230 / cold 9240，transport +100），
# 每容器独立子目录 ${HOME_DIR}/<角色> 与容器名 es-${NODE_NAME}-<角色>。
# 每个容器的 JVM 堆由「规格档位 × 角色」决定（后端按 sizing.go 预设注入 __es_heap_<角色>），
# 不做主机级的堆统配：一机多容器时统一按整机内存取值会叠出超配。
# 由 {__run} 决定执行哪个阶段分支；masters/coords/data 阶段按角色认领本机容器。
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

CLUSTER_NAME="{{cluster_name}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
NODE_NAME="{{__node_name}}"
SELF_IP="{{__ip}}"
ES_NODES="{{__es_nodes}}"
ES_BOOTSTRAP="{{__es_bootstrap}}"
ES_BOOTSTRAP_NAME="{{__es_bootstrap_name}}"
SEED_HOSTS="{{__seed_hosts}}"
RUN="{{__run}}"
# 规格档位与逐角色堆内存：由后端按 sizing.go 的三档内部预设注入，
# 用户不再填写 -Xms/-Xmx/GC 参数（一机多容器必须由平台显式定堆，见 sizing.go 顶部说明）。
SIZING="{{__es_sizing}}"
SIZING_LABEL="{{__es_sizing_label}}"
HEAP_MASTER="{{__es_heap_master}}"
HEAP_COORDINATOR="{{__es_heap_coordinator}}"
HEAP_DATA_HOT="{{__es_heap_data_hot}}"
HEAP_DATA_WARM="{{__es_heap_data_warm}}"
HEAP_DATA_COLD="{{__es_heap_data_cold}}"

[ -n "${CLUSTER_NAME}" ] || { echo "集群名称不能为空"; exit 1; }
[ -n "${NODE_NAME}" ] || { echo "节点名称不能为空"; exit 1; }
[ -n "${SEED_HOSTS}" ] || { echo "种子地址不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="elasticsearch:9.5.3"
[ -n "${SIZING}" ] || SIZING=standard
[ -n "${SIZING_LABEL}" ] || SIZING_LABEL="标准使用"

# 逐角色堆内存：注入缺失时回落 ES 自带的 1g 默认值（而不是猜一个大值把宿主拖进 swap）。
heap_of() {
  case "$1" in
    master)      echo "${HEAP_MASTER:-1g}";;
    coordinator) echo "${HEAP_COORDINATOR:-1g}";;
    data_hot)    echo "${HEAP_DATA_HOT:-1g}";;
    data_warm)   echo "${HEAP_DATA_WARM:-1g}";;
    data_cold)   echo "${HEAP_DATA_COLD:-1g}";;
    *)           echo 1g;;
  esac
}

# 堆内存字符串转 MB（6g -> 6144），供宿主容量校验用。
heap_mb() {
  local v="$1" n
  n="${v%[gG]}"; n="${n%[mM]}"
  case "${v}" in
    *[gG]) echo $(( n * 1024 ));;
    *)     echo "${n}";;
  esac
}

# 一机多容器最容易踩的坑：本机多个容器的堆加起来超过整机内存。
# ES 官方要求堆 ≤ 宿主内存 50%（其余留给 Lucene 的文件系统缓存），这里按软约束提示，不阻断部署。
check_host_capacity() {
  [ -n "${ES_NODES}" ] || return 0
  local total=0 role mem_mb
  for role in ${ES_NODES//,/ }; do
    [ -n "${role}" ] || continue
    total=$(( total + $(heap_mb "$(heap_of "${role}")") ))
  done
  mem_mb="$(awk '/^MemTotal:/{print int($2/1024)}' /proc/meminfo 2>/dev/null || true)"
  [ -n "${mem_mb}" ] && [ "${mem_mb}" -gt 0 ] 2>/dev/null || return 0
  echo "规格档位 ${SIZING_LABEL}(${SIZING})：本机容器堆合计 $(( total / 1024 ))G / 宿主内存 $(( mem_mb / 1024 ))G"
  if [ "${total}" -gt $(( mem_mb / 2 )) ]; then
    echo "警告: 本机容器堆合计 $(( total / 1024 ))G 已超过宿主内存的 50%——建议把角色拆到独立主机，或改用更小的规格档位"
  fi
}

# 角色固定偏移端口（与后端 vars.go cwhRolePort/cwhRoleTransport 同表，改动需两处同步）
http_port() {
  case "$1" in
    master) echo 9200;; coordinator) echo 9210;; data_hot) echo 9220;; data_warm) echo 9230;; data_cold) echo 9240;;
  esac
}
transport_port() {
  case "$1" in
    master) echo 9300;; coordinator) echo 9310;; data_hot) echo 9320;; data_warm) echo 9330;; data_cold) echo 9340;;
  esac
}

# 本机角色认领：阶段 masters 认领 master，coords 认领 coordinator，data 认领数据三角色
stage_role() {
  case "${RUN}" in
    masters) [ "$1" = "master" ];;
    coords)  [ "$1" = "coordinator" ];;
    data)    case "$1" in data_hot|data_warm|data_cold) return 0;; *) return 1;; esac;;
    *)       return 1;;
  esac
}

# 仅本机的初始引导 master 容器写入 cluster.initial_master_nodes
init_masters_yml() {
  if [ "$1" = "master" ] && [ "${ES_BOOTSTRAP}" = "true" ] && [ -n "${ES_BOOTSTRAP_NAME}" ]; then
    echo "cluster.initial_master_nodes: [ \"${ES_BOOTSTRAP_NAME}\" ]"
  fi
}

# 写单容器配置（yml 内联；node.roles 按角色固定：master / 纯协调空数组 / 数据单角色）
write_yml() { # write_yml <role> <dir>
  local ROLE="$1" DIR="$2" HP TP ROLES_YML HEAP
  HP="$(http_port "${ROLE}")"
  TP="$(transport_port "${ROLE}")"
  HEAP="$(heap_of "${ROLE}")"
  case "${ROLE}" in
    master)     ROLES_YML='node.roles: [ "master" ]';;
    coordinator) ROLES_YML='node.roles: [ ]';;
    *)          ROLES_YML="node.roles: [ \"${ROLE}\" ]";;
  esac
  mkdir -p "${DIR}/data" "${DIR}/logs"
  cat > "${DIR}/elasticsearch.yml" <<ESYML
cluster.name: ${CLUSTER_NAME}
node.name: ${NODE_NAME}-${ROLE}
${ROLES_YML}
path.data: /usr/share/elasticsearch/data
path.logs: /usr/share/elasticsearch/logs
network.host: 0.0.0.0
http.port: ${HP}
transport.port: ${TP}
transport.host: 0.0.0.0
network.publish_host: ${SELF_IP}
discovery.seed_hosts: [ ${SEED_HOSTS} ]
$(init_masters_yml "${ROLE}")
xpack.security.enabled: false
xpack.security.enrollment.enabled: false
ESYML
  chown 1000:0 "${DIR}/elasticsearch.yml" 2>/dev/null || true
  chmod 644 "${DIR}/elasticsearch.yml"
  cat > "${DIR}/compose.yml" <<YAMLEOF
name: es-${NODE_NAME}-${ROLE}
services:
  elasticsearch:
    image: ${IMAGE}
    container_name: es-${NODE_NAME}-${ROLE}
    restart: always
    network_mode: host
    environment:
      - ES_JAVA_OPTS=-Xms${HEAP} -Xmx${HEAP} -XX:+UseG1GC
      - bootstrap.memory_lock=true
    ulimits:
      memlock:
        soft: -1
        hard: -1
    volumes:
      - ${DIR}/data:/usr/share/elasticsearch/data
      - ${DIR}/logs:/usr/share/elasticsearch/logs
      - ${DIR}/elasticsearch.yml:/usr/share/elasticsearch/config/elasticsearch.yml:ro
YAMLEOF
}

start_and_wait() { # start_and_wait <role> <dir>
  local ROLE="$1" DIR="$2" HP READY
  HP="$(http_port "${ROLE}")"
  echo "拉取镜像 ${IMAGE}（es-${NODE_NAME}-${ROLE}）"
  docker compose -f "${DIR}/compose.yml" pull
  docker compose -f "${DIR}/compose.yml" up -d
  READY=0
  for _ in $(seq 1 80); do
    curl -sf "http://127.0.0.1:${HP}/" >/dev/null 2>&1 && { READY=1; break; }
    sleep 5
  done
  if [ "${READY}" != "1" ]; then
    echo "Elasticsearch 容器启动超时: es-${NODE_NAME}-${ROLE}，最近日志:"
    docker logs "es-${NODE_NAME}-${ROLE}" --tail 40 2>&1 || true
    exit 1
  fi
}

# 已知角色全集（reset 清理巡检用；新增角色需同步 http_port/transport_port）
ALL_ROLES="master coordinator data_hot data_warm data_cold"

run_reset() {
  local ROLE DIR
  for ROLE in ${ALL_ROLES}; do
    DIR="${HOME_DIR}/${ROLE}"
    if [ -f "${DIR}/compose.yml" ]; then
      docker compose -f "${DIR}/compose.yml" down --remove-orphans 2>/dev/null || true
    fi
    rm -rf "${DIR}/data" "${DIR}/logs" 2>/dev/null || true
    rm -f "${DIR}/elasticsearch.yml" "${DIR}/compose.yml"
  done
  # 兼容旧版单容器目录：清理历史残留
  if [ -f "${HOME_DIR}/compose.yml" ]; then
    docker compose -f "${HOME_DIR}/compose.yml" down --remove-orphans 2>/dev/null || true
  fi
  rm -rf "${HOME_DIR}/data" "${HOME_DIR}/logs" "${HOME_DIR}/elasticsearch.yml" "${HOME_DIR}/compose.yml" 2>/dev/null || true
  echo "已清理旧环境与残留容器(${NODE_NAME})"
}

run_stage() { # run_stage <stage-label>
  [ -n "${ES_NODES}" ] || { echo "本机未配置角色，跳过"; return 0; }
  local ROLE DIR CLAIMED=0
  for ROLE in ${ES_NODES//,/ }; do
    [ -n "${ROLE}" ] || continue
    stage_role "${ROLE}" || continue
    DIR="${HOME_DIR}/${ROLE}"
    write_yml "${ROLE}" "${DIR}"
    start_and_wait "${ROLE}" "${DIR}"
    echo "容器已就绪: es-${NODE_NAME}-${ROLE}（堆 $(heap_of "${ROLE}")）"
    CLAIMED=1
  done
  [ "${CLAIMED}" = "1" ] || echo "本机无${1}容器，跳过"
}

mkdir -p "${HOME_DIR}"
sysctl -w vm.max_map_count=262144 >/dev/null 2>&1 || echo "提示: 建议开启 vm.max_map_count=262144"
# reset 阶段不拉起容器，无需做宿主容量提示
[ "${RUN}" = "reset" ] || check_host_capacity

case "${RUN}" in
  reset)   run_reset ;;
  masters) run_stage "master 候选" ;;
  coords)  run_stage "纯协调" ;;
  data)    run_stage "数据" ;;
  *) echo "未知阶段 ${RUN}"; exit 1 ;;
esac

echo "主机 ${NODE_NAME} 阶段 ${RUN} 完成"
