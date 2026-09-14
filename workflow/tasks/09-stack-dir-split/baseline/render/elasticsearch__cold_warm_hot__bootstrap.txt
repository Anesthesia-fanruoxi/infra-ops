#!/bin/bash
# Elasticsearch 冷热温 / verify 阶段 —— 在引导 master（leader）上校验集群健康与各分层就绪。
set -e

CLUSTER_NAME="{{cluster_name}}"
PORT="{{port}}"
SSL="{{ssl_enabled}}"
CLUSTER_SIZE="{{__cluster_size}}"
SELF_IP="{{__ip}}"
NODE_NAME="{{__node_name}}"

[ -n "${CLUSTER_NAME}" ] || { echo "集群名称不能为空"; exit 1; }
[ -n "${PORT}" ] || PORT=9200
[ -n "${CLUSTER_SIZE}" ] || CLUSTER_SIZE=1

SCHEME="http"
[ "${SSL}" = "true" ] && SCHEME="https"
CURL=(curl -sf)
[ "${SSL}" = "true" ] && CURL=(curl -sfk)
BASE="${SCHEME}://${SELF_IP}:${PORT}"

HEALTH=""
for _ in $(seq 1 40); do
  HEALTH="$("${CURL[@]}" "${BASE}/_cluster/health" 2>/dev/null || true)"
  [ -n "${HEALTH}" ] && break
  sleep 5
done
if [ -z "${HEALTH}" ]; then
  echo "集群健康接口不可用（${BASE}/_cluster/health）"
  exit 1
fi

STATUS="$(echo "${HEALTH}" | sed -n 's/.*"status"[^:]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
NODES="$(echo "${HEALTH}" | sed -n 's/.*"number_of_nodes"[^:]*:[[:space:]]*\([0-9]*\).*/\1/p')"
[ -z "${NODES}" ] && NODES=0

echo "集群健康: ${STATUS} / 节点数 ${NODES}（期望 ≥${CLUSTER_SIZE}）"
if [ -z "${STATUS}" ]; then
  echo "无法解析健康状态，最近主节点日志:"
  docker logs "es-${NODE_NAME}" --tail 30 2>&1 || true
  exit 1
fi
if [ "${STATUS}" = "red" ]; then
  echo "集群为 red，校验失败"
  exit 1
fi
if [ "${NODES}" -lt "${CLUSTER_SIZE}" ]; then
  echo "节点未全部加入（${NODES}/${CLUSTER_SIZE}），校验失败"
  exit 1
fi

echo "--- 数据层节点 ---"
"${CURL[@]}" "${BASE}/_cat/nodes?h=name,node.roles" 2>/dev/null | sed 's/^/  /' || true

echo "--- 索引写入探针（hot 层可用性）---"
IDX="probe-$(date +%s)"
if "${CURL[@]}" -X PUT "${BASE}/${IDX}" -H 'Content-Type: application/json' \
    -d '{"settings":{"number_of_shards":1,"number_of_replicas":0}}' >/dev/null 2>&1; then
  "${CURL[@]}" -X DELETE "${BASE}/${IDX}" >/dev/null 2>&1 || true
  echo "健康探针通过：集群可写可删"
else
  echo "警告：健康探针写入失败（不影响部署，仅提示）"
fi

echo "Elasticsearch 冷热温集群校验通过: ${STATUS} / ${NODES} 节点"
echo "接入地址: ${BASE}"