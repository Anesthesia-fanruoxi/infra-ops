#!/bin/bash
# Elasticsearch 冷热温 / 缩容安全下线 —— 在存活 leader 上先排空分片、再让被缩节点安全退场，
# 集群回 green 后由主流程对该主机 docker compose down（本脚本不含停容器动作）。
# 由平台在 scale_in 时于某个存活 master/协作节点注入 {__remove_nodes}/{__remove_masters} 后渲染执行。
set -e

CLUSTER_NAME="{{cluster_name}}"
PORT="{{port}}"
SSL="{{ssl_enabled}}"
SELF_IP="{{self_ip}}"
REMOVE_NODES="{{remove_nodes}}"
REMOVE_MASTERS="{{remove_masters}}"

[ -n "${REMOVE_NODES}" ] || { echo "未指定待下线节点"; exit 1; }
[ -n "${PORT}" ] || PORT=9200
[ -n "${SELF_IP}" ] || { echo "缺少本机 IP，无法执行下线"; exit 1; }

SCHEME="http"
[ "${SSL}" = "true" ] && SCHEME="https"
CURL=(curl -sf)
[ "${SSL}" = "true" ] && CURL=(curl -sfk)
BASE="${SCHEME}://${SELF_IP}:${PORT}"
NODES="$(echo "${REMOVE_NODES}" | tr ',' ' ')"

echo "目标集群: ${BASE}  待下线节点: ${NODES}"

# 1) 排空：把待下线节点的分片迁走（官方 exclude 推荐路径）
for NODE in ${NODES}; do
  echo "排空节点 ${NODE} 分片 ..."
  "${CURL[@]}" -X PUT "${BASE}/_cluster/settings" \
    -H 'Content-Type: application/json' \
    -d "{\"persistent\":{\"cluster.routing.allocation.exclude._name\":\"${NODE}\"}}" >/dev/null 2>&1 \
    || echo "  警告：设置 exclude 失败（${NODE}），继续"
done

# 2) 待下线 master 候选：更新投票配置，避免移除时选举仲裁失效
if [ -n "${REMOVE_MASTERS}" ]; then
  for M in $(echo "${REMOVE_MASTERS}" | tr ',' ' '); do
    echo "为 master 候选 ${M} 追加投票排除 ..."
    "${CURL[@]}" -X POST "${BASE}/_cluster/voting_config_exclusions?node_names=${M}&timeout=30s" >/dev/null 2>&1 \
      || echo "  警告：voting exclusion（${M}）失败，继续"
  done
fi

# 3) 等待集群排空完成（不再有分片重分配，状态回 green）
for _ in $(seq 1 60); do
  HEALTH="$("${CURL[@]}" "${BASE}/_cluster/health?wait_for_status=green&wait_for_no_relocating_shards=true&timeout=5s" 2>/dev/null || true)"
  ST="$(echo "${HEALTH}" | sed -n 's/.*"status"[^:]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  [ "${ST}" = "green" ] && { echo "集群已回 green，分片迁移完成"; exit 0; }
  sleep 10
done
echo "等待超时：集群未能回到 green，请人工检查分片迁移状态"
exit 1