#!/bin/bash
# RabbitMQ 缩容安全下线 —— 平台在某个存活节点上注入 remove_nodes（rabbit@IP 列表）后渲染执行。
#
# 为什么需要它：被移除节点若直接从集群「消失」，会以 down 节点长期留在集群视图（幽灵节点），
# 若它承载着 quorum 队列的副本，副本集合与真实选票不一致；后续缩容使队列低于多数派时不可用。
# 本脚本按官方《Removing a Node》流程逐个软下线：先远程 stop_app，再 forget_cluster_node
# （3.13.7 实测：forget 会连同 quorum 队列副本一并收缩，视图里不再残留该节点）。
#
# 实测语义与对策（rabbitmq 3.13.7）：
#   1) 目标仍在运行：直接 forget 报错 "must be stopped with 'rabbitmqctl -n <node> stop_app'"，
#      故先经 Erlang 分布远程 stop_app（同 cookie + host 网络）；stop_app 失败
#      （节点已宕机/不可达）不阻断，forget 对不可达节点同样成功（unresponsive 路径）；
#   2) 目标已不在视图：forget 报 {:not_a_cluster_node, ...}，脚本先查视图，已移除则跳过（幂等）；
#   3) 视图取自 cluster_status --formatter json，节点名按 JSON 字符串精确匹配
#      （避免 rabbit@10.0.3.1 误配 rabbit@10.0.3.12），停止的 disc 节点仍在 disk_nodes 中可查。
#
# 注意：forget 后该节点若再开机，会因 inconsistent_cluster 拒绝启动（须 reset 才能重新入群）；
# 平台流程随后即停容器并摘除主机，不涉及重启。
#
# 本脚本不包含停容器动作（由主流程在软下线成功后 docker compose down）。
set -e

NODES="{{remove_nodes}}"
CONTAINER=rabbitmq-node

[ -n "${NODES}" ] || { echo "未指定待下线节点"; exit 1; }

if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "未找到运行中的 ${CONTAINER} 容器，跳过软下线"
  exit 0
fi

ctl() { docker exec "${CONTAINER}" rabbitmqctl "$@"; }

# cluster_status 的 JSON 视图；节点名以带引号的精确串匹配
view() { ctl cluster_status --formatter json 2>/dev/null | tr -d '\r' || true; }
in_view() { printf '%s' "$1" | grep -Fq "\"$2\""; }

FORGOT=0
FAILED=0
SKIPPED=0
IFS=',' read -ra RAW <<< "${NODES}"
for t in "${RAW[@]}"; do
  t="$(printf '%s' "${t}" | xargs)"
  [ -n "${t}" ] || continue
  VIEW="$(view)"
  if [ -z "${VIEW}" ]; then
    echo "节点 ${t}: 无法读取集群状态（rabbitmqctl 无响应），跳过软下线"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi
  if ! in_view "${VIEW}" "${t}"; then
    echo "节点 ${t} 已不在集群视图，跳过"
    continue
  fi
  echo "停用目标节点应用：${t}（远程 stop_app）"
  if ctl -n "${t}" stop_app >/dev/null 2>&1; then
    echo "  已 stop_app"
  else
    echo "  警告：stop_app 失败（节点可能已不可达），继续执行 forget"
  fi
  if _OUT="$(ctl forget_cluster_node "${t}" 2>&1)"; then
    echo "  已忘记 ${t}"
  else
    echo "  forget 输出: ${_OUT}"
  fi
  VIEW="$(view)"
  if [ -z "${VIEW}" ] || in_view "${VIEW}" "${t}"; then
    echo "  失败：${t} 仍在集群视图中，中止缩容以保护 quorum"
    FAILED=$((FAILED + 1))
  else
    FORGOT=$((FORGOT + 1))
  fi
done

echo "软下线结果：已忘记 ${FORGOT} 个，失败 ${FAILED} 个，跳过 ${SKIPPED} 个"
if [ "${FAILED}" -gt 0 ]; then
  echo "存在未能忘记的节点：中止缩容（可修复后重试，脚本幂等）"
  exit 1
fi
echo "软下线完成，可安全停容器"
