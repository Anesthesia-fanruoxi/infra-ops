#!/bin/bash
# ELFK 日志套件 —— RUN 分发器（主脑流水线多阶段，非主从型组件编排）
# 由 {{__run}} 决定执行哪个 run_<key> 分支：
#   reset    清理旧环境与残留容器（仅整机部署执行，扩容/加装跳过）
#   es       Elasticsearch cluster 拓扑（1 引导 master + N data，复用 cluster 模式机制）
#   kibana   Kibana（仅引导机执行，其余机器直接跳过）
#   logstash Logstash（仅落点机执行，其余机器直接跳过）
#   filebeat Filebeat 采集代理（全员）
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板"; exit 1; }

CLUSTER_NAME="{{cluster_name}}"
PORT="{{port}}"
TRANSPORT_PORT="{{transport_port}}"
ES_HEAP="{{es_heap}}"
HOME_DIR="{{home_dir}}"
IMAGE_ES="{{image_es}}"
IMAGE_KIBANA="{{image_kibana}}"
IMAGE_LOGSTASH="{{image_logstash}}"
IMAGE_FILEBEAT="{{image_filebeat}}"
KIBANA_PORT="{{kibana_port}}"
BEATS_PORT="{{logstash_beats_port}}"
LOGSTASH_HOST_PARAM="{{logstash_host}}"
NODE_NAME="{{__node_name}}"
SELF_IP="{{__ip}}"
IS_BOOTSTRAP="{{__is_bootstrap}}"
NODE_IPS="{{__node_ips}}"
RUN="{{__run}}"

[ -n "${CLUSTER_NAME}" ] || { echo "集群名称不能为空"; exit 1; }
[ -n "${NODE_NAME}" ] || { echo "节点名称不能为空"; exit 1; }
[ -n "${HOME_DIR}" ] || { echo "服务主目录不能为空"; exit 1; }
[ -n "${PORT}" ] || PORT=9200
[ -n "${TRANSPORT_PORT}" ] || TRANSPORT_PORT=9300
[ -n "${ES_HEAP}" ] || ES_HEAP="-Xms1g -Xmx1g"
[ -n "${IMAGE_ES}" ] || IMAGE_ES="elasticsearch:9.5.3"
[ -n "${IMAGE_KIBANA}" ] || IMAGE_KIBANA="kibana:9.5.3"
[ -n "${IMAGE_LOGSTASH}" ] || IMAGE_LOGSTASH="logstash:9.5.3"
[ -n "${IMAGE_FILEBEAT}" ] || IMAGE_FILEBEAT="filebeat:9.5.3"
[ -n "${KIBANA_PORT}" ] || KIBANA_PORT=5601
[ -n "${BEATS_PORT}" ] || BEATS_PORT=5044

# Logstash 落点解析：显式 logstash_host 优先，否则取成员列表第 2 台（与角色规划一致）
resolve_logstash_host() {
  if [ -n "${LOGSTASH_HOST_PARAM}" ]; then
    echo "${LOGSTASH_HOST_PARAM}"
    return
  fi
  echo "${NODE_IPS}" | cut -d, -f2
}
LOGSTASH_HOST="$(resolve_logstash_host)"
[ -n "${LOGSTASH_HOST}" ] || LOGSTASH_HOST="${SELF_IP}"

is_on_host() { [ "${SELF_IP}" = "$1" ]; }

run_reset() {
  echo "清理 ELFK 旧环境（保留数据目录内容不动）"
  for c in kibana logstash filebeat es-${NODE_NAME}; do
    docker rm -f "${c}" &>/dev/null || true
  done
  echo "reset 完成"
}

# es 分支：与 elasticsearch cluster 模式一致（引导/成员配置由平台素材注入）
run_es() {
  mkdir -p "${HOME_DIR}/data" "${HOME_DIR}/logs"
  chown -R 1000:0 "${HOME_DIR}/data" "${HOME_DIR}/logs" 2>/dev/null || chmod -R 777 "${HOME_DIR}/data" "${HOME_DIR}/logs"

  umask 077
  if [ "${IS_BOOTSTRAP}" = "true" ]; then
    cat > "${HOME_DIR}/elasticsearch.yml" <<'EOF'
@@ES_MASTER_YML@@
EOF
  else
    cat > "${HOME_DIR}/elasticsearch.yml" <<'EOF'
@@ES_MEMBER_YML@@
EOF
  fi
  umask 022
  chown 1000:0 "${HOME_DIR}/elasticsearch.yml" 2>/dev/null || true
  chmod 644 "${HOME_DIR}/elasticsearch.yml"

  if docker ps -a --format '{{.Names}}' | grep -qx "es-${NODE_NAME}"; then
    PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "es-${NODE_NAME}" 2>/dev/null || true)"
    if [ -z "${PROJ}" ]; then
      echo "检测到旧容器，迁移为 compose 管理（数据保留）"
      docker rm -f "es-${NODE_NAME}" &>/dev/null || true
    fi
  fi

  cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: elfk-es
services:
  elasticsearch:
    image: {{image_es}}
    container_name: es-{{__node_name}}
    restart: always
    network_mode: host
    environment:
      - ES_JAVA_OPTS={{es_heap}}
      - bootstrap.memory_lock=true
    ulimits:
      memlock:
        soft: -1
        hard: -1
    volumes:
      - {{home_dir}}/data:/usr/share/elasticsearch/data
      - {{home_dir}}/logs:/usr/share/elasticsearch/logs
      - {{home_dir}}/elasticsearch.yml:/usr/share/elasticsearch/config/elasticsearch.yml:ro
YAMLEOF

  sysctl -w vm.max_map_count=262144 >/dev/null 2>&1 || true

  echo "拉取镜像 ${IMAGE_ES}"
  docker compose -f "${HOME_DIR}/compose.yml" pull
  docker compose -f "${HOME_DIR}/compose.yml" up -d

  READY=0
  for _ in $(seq 1 80); do
    if curl -sf "http://127.0.0.1:${PORT}/" &>/dev/null; then READY=1; break; fi
    sleep 5
  done
  if [ "${READY}" != "1" ]; then
    echo "Elasticsearch 节点启动超时，最近日志:"
    docker logs "es-${NODE_NAME}" --tail 40 2>&1 || true
    exit 1
  fi
  echo "Elasticsearch 节点已就绪: ${NODE_NAME} / 集群 ${CLUSTER_NAME}（http://${SELF_IP}:${PORT}）"
}

run_kibana() {
  if [ "${IS_BOOTSTRAP}" != "true" ]; then
    echo "Kibana 固定部署在引导机，本机（${SELF_IP}）跳过"
    return 0
  fi
  local dir="${HOME_DIR%/*}/kibana"
  mkdir -p "${dir}"
  cat > "${dir}/kibana.yml" <<'YMLEOF'
server.name: kibana
server.host: "0.0.0.0"
elasticsearch.hosts: ["http://127.0.0.1:{{port}}"]
elasticsearch.requestTimeout: 60000
xpack.security.enabled: false
YMLEOF
  cat > "${dir}/compose.yml" <<'YAMLEOF'
name: elfk-kibana
services:
  kibana:
    image: {{image_kibana}}
    container_name: kibana
    restart: always
    network_mode: host
    environment:
      - ELASTICSEARCH_HOSTS=http://127.0.0.1:{{port}}
    volumes:
      - {{home_dir}}/../kibana/kibana.yml:/usr/share/kibana/config/kibana.yml:ro
YAMLEOF
  echo "拉取镜像 ${IMAGE_KIBANA}"
  docker compose -f "${dir}/compose.yml" pull
  docker compose -f "${dir}/compose.yml" up -d

  READY=0
  for _ in $(seq 1 60); do
    code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${KIBANA_PORT}/api/status" 2>/dev/null || true)"
    if [ "${code}" = "200" ]; then READY=1; break; fi
    sleep 5
  done
  if [ "${READY}" != "1" ]; then
    echo "Kibana 启动超时，最近日志:"
    docker logs kibana --tail 40 2>&1 || true
    exit 1
  fi
  echo "Kibana 已就绪: http://${SELF_IP}:${KIBANA_PORT}"
}

run_logstash() {
  if ! is_on_host "${LOGSTASH_HOST}"; then
    echo "Logstash 固定部署在 ${LOGSTASH_HOST}，本机（${SELF_IP}）跳过"
    return 0
  fi
  local dir="${HOME_DIR%/*}/logstash"
  mkdir -p "${dir}/pipeline"
  # ES 输出地址：全部成员（任一可用即可写入）
  local es_list
  es_list="$(echo "${NODE_IPS}" | awk -v p="${PORT}" '{n=split($0,a,","); s=""; for(i=1;i<=n;i++){s=s (i>1?", ":"") "\"http://" a[i] ":" p "\""} print s}')"
  cat > "${dir}/pipeline/pipeline.conf" <<PIPEEOF
input {
  beats {
    port => ${BEATS_PORT}
  }
}
filter {
}
output {
  elasticsearch {
    hosts => [${es_list}]
    index => "logs-%{[@metadata][beat]}-%{+YYYY.MM.dd}"
  }
}
PIPEEOF
  cat > "${dir}/logstash.yml" <<'LSEOF'
http.host: "0.0.0.0"
xpack.monitoring.enabled: false
LSEOF
  cat > "${dir}/compose.yml" <<'YAMLEOF'
name: elfk-logstash
services:
  logstash:
    image: {{image_logstash}}
    container_name: logstash
    restart: always
    network_mode: host
    environment:
      - LS_JAVA_OPTS=-Xms512m -Xmx512m
    volumes:
      - {{home_dir}}/../logstash/pipeline:/usr/share/logstash/pipeline:ro
      - {{home_dir}}/../logstash/logstash.yml:/usr/share/logstash/config/logstash.yml:ro
YAMLEOF
  echo "拉取镜像 ${IMAGE_LOGSTASH}"
  docker compose -f "${dir}/compose.yml" pull
  docker compose -f "${dir}/compose.yml" up -d

  READY=0
  for _ in $(seq 1 60); do
    if curl -sf "http://127.0.0.1:9600/_node/pipelines" &>/dev/null; then READY=1; break; fi
    sleep 5
  done
  if [ "${READY}" != "1" ]; then
    echo "Logstash 启动超时，最近日志:"
    docker logs logstash --tail 40 2>&1 || true
    exit 1
  fi
  echo "Logstash 已就绪（beats ${SELF_IP}:${BEATS_PORT} → ES，监控端口 9600）"
}

run_filebeat() {
  local dir="${HOME_DIR%/*}/filebeat"
  mkdir -p "${dir}"
  cat > "${dir}/filebeat.yml" <<'FBEOF'
filebeat.inputs:
  - type: filestream
    id: docker-containers
    paths:
      - /var/lib/docker/containers/*/*.log
    parsers:
      - container: ~
    processors:
      - add_host_metadata: ~

output.logstash:
  hosts: ["${LOGSTASH_HOST}:{{logstash_beats_port}}"]

logging.level: info
FBEOF
  cat > "${dir}/compose.yml" <<YAMLEOF
name: elfk-filebeat
services:
  filebeat:
    image: {{image_filebeat}}
    container_name: filebeat
    restart: always
    user: root
    environment:
      - LOGSTASH_HOST=${LOGSTASH_HOST}
    volumes:
      - {{home_dir}}/../filebeat/filebeat.yml:/usr/share/filebeat/filebeat.yml:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /var/lib/docker/containers:/var/lib/docker/containers:ro
YAMLEOF
  echo "拉取镜像 ${IMAGE_FILEBEAT}（输出 Logstash ${LOGSTASH_HOST}:${BEATS_PORT}）"
  docker compose -f "${dir}/compose.yml" pull
  docker compose -f "${dir}/compose.yml" up -d

  # 校验配置与输出连通性（test output 走真实连 Logstash）
  sleep 3
  if docker exec filebeat filebeat test config 2>&1; then
    echo "filebeat 配置校验通过"
  else
    echo "filebeat 配置校验失败，最近日志:"
    docker logs filebeat --tail 30 2>&1 || true
    exit 1
  fi
  echo "Filebeat 已启动（本机 ${SELF_IP}）"
}

case "${RUN}" in
  reset)    run_reset ;;
  es)       run_es ;;
  kibana)   run_kibana ;;
  logstash) run_logstash ;;
  filebeat) run_filebeat ;;
  *) echo "未知流水线阶段: ${RUN}"; exit 1 ;;
esac
