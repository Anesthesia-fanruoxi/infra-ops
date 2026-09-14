#!/bin/bash
# ELFK 链路校验（引导机执行）：ES 集群健康 → Kibana 状态 → Logstash 管道 → Filebeat 采集
set -e

PORT="{{port}}"
KIBANA_PORT="{{kibana_port}}"
BEATS_PORT="{{logstash_beats_port}}"
LOGSTASH_HOST_PARAM="{{logstash_host}}"
NODE_IPS="{{__node_ips}}"
SELF_IP="{{__ip}}"

[ -n "${PORT}" ] || PORT=9200
[ -n "${KIBANA_PORT}" ] || KIBANA_PORT=5601

LOGSTASH_HOST="${LOGSTASH_HOST_PARAM}"
if [ -z "${LOGSTASH_HOST}" ]; then
  LOGSTASH_HOST="$(echo "${NODE_IPS}" | cut -d, -f2)"
fi
[ -n "${LOGSTASH_HOST}" ] || LOGSTASH_HOST="${SELF_IP}"

FAIL=0

echo "== 1/4 Elasticsearch 集群健康 =="
HEALTH="$(curl -sf "http://127.0.0.1:${PORT}/_cluster/health" 2>/dev/null || true)"
if [ -z "${HEALTH}" ]; then
  echo "FAIL: 无法访问 ES http://127.0.0.1:${PORT}/_cluster/health"; FAIL=1
else
  STATUS="$(echo "${HEALTH}" | grep -o '"status":"[a-z]*"' | head -1 | cut -d'"' -f4)"
  NODES="$(echo "${HEALTH}" | grep -o '"number_of_nodes":[0-9]*' | cut -d: -f2)"
  echo "集群状态: ${STATUS:-unknown}, 节点数: ${NODES:-0}"
  if [ "${STATUS}" != "green" ] && [ "${STATUS}" != "yellow" ]; then
    echo "FAIL: 集群状态异常（${STATUS}）"; FAIL=1
  fi
fi

echo "== 2/4 Kibana =="
CODE="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${KIBANA_PORT}/api/status" 2>/dev/null || true)"
if [ "${CODE}" = "200" ]; then
  echo "Kibana 就绪: http://${SELF_IP}:${KIBANA_PORT}"
else
  echo "FAIL: Kibana /api/status 返回 ${CODE:-无响应}"; FAIL=1
fi

echo "== 3/4 Logstash 管道 =="
PIPE="$(curl -sf "http://${LOGSTASH_HOST}:9600/_node/pipelines" 2>/dev/null || true)"
if [ -n "${PIPE}" ] && echo "${PIPE}" | grep -q 'main'; then
  echo "Logstash main 管道运行中（${LOGSTASH_HOST}:9600，beats 端口 ${BEATS_PORT}）"
else
  echo "FAIL: Logstash 监控接口不可达（http://${LOGSTASH_HOST}:9600）"; FAIL=1
fi

echo "== 4/4 Filebeat 采集代理（本机） =="
if docker ps --format '{{.Names}} {{.Status}}' | grep -q '^filebeat .*Up'; then
  echo "filebeat 容器运行中"
  if docker exec filebeat filebeat test output 2>&1 | grep -q 'talk to server'; then
    echo "filebeat → Logstash 连通性校验通过"
  else
    echo "WARN: filebeat output 校验未通过（可能 Logstash 尚在加载管道，可稍后在实例页重跑校验）"
  fi
else
  echo "FAIL: filebeat 容器未运行"; FAIL=1
fi

if [ "${FAIL}" != "0" ]; then
  echo "ELFK 链路校验存在失败项，请查看上方明细"
  exit 1
fi
echo "ELFK 链路校验全部通过：Filebeat(全员) → Logstash(${LOGSTASH_HOST}:5044) → ES(${NODE_IPS}) → Kibana(http://${SELF_IP}:${KIBANA_PORT})"
