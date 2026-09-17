#!/bin/bash
# Redis Cluster 缩容安全下线 —— 平台在某个存活成员上注入 remove_nodes 后渲染执行。
#
# 为什么需要它：集群缩容若直接停容器，被移除节点持有的哈希槽仍指向一个已消失的节点，
# cluster_state 可能变成 fail，必须人工 `redis-cli --cluster del-node` 收尾。本脚本按
# 「迁槽 → 孤儿从重挂 → 从集群移除」三步把节点干净摘除，之后主流程才停该主机容器。
#
# 幂等：节点已不在集群 / 已无槽 / 未知条目的情况都只告警不失败，重复执行安全。
# 本脚本不包含停容器动作（由主流程在缩容成功后 docker compose down）。
set -e

PASS="{{password}}"
PORT="{{port}}"
REMOVE_NODES="{{remove_nodes}}"
CONTAINER=redis-cluster

[ -n "${PASS}" ] || { echo "访问密码不能为空"; exit 1; }
[ -n "${REMOVE_NODES}" ] || { echo "未指定待下线节点"; exit 1; }
[ -n "${PORT}" ] || PORT=6379

if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "未找到运行中的 ${CONTAINER} 容器，跳过软下线"
  exit 0
fi

cli() {
  docker exec "${CONTAINER}" redis-cli -a "${PASS}" --no-auth-warning -p "${PORT}" "$@"
}

NODES="$(cli cluster nodes 2>/dev/null | tr -d '\r' || true)"
[ -n "${NODES}" ] || { echo "无法读取集群节点表，跳过软下线"; exit 0; }

STATE="$(cli cluster info 2>/dev/null | tr -d '\r' || true)"
if ! printf '%s\n' "${STATE}" | grep -q 'cluster_state:ok'; then
  echo "集群当前不是 ok 状态，拒绝缩容（请先修复集群）："
  printf '%s\n' "${STATE}"
  exit 1
fi

# ---- 节点表解析助手（ip:port 去掉 @bus 端口；槽段从第 9 列起累加） ----
node_id_of() {
  printf '%s\n' "${NODES}" | awk -v t="$1" '{a=$2; sub(/@.*/,"",a); if (a==t) {print $1; exit}}'
}
addr_of() {
  printf '%s\n' "${NODES}" | awk -v id="$1" '$1==id {a=$2; sub(/@.*/,"",a); print a; exit}'
}
master_of() {
  printf '%s\n' "${NODES}" | awk -v id="$1" '$1==id {print $4; exit}'
}
slot_count_of() {
  printf '%s\n' "${NODES}" | awk -v id="$1" '$1==id {
    n=0
    for (i=9;i<=NF;i++) {
      if ($i ~ /^[0-9]+-[0-9]+$/) { split($i,a,"-"); n+=a[2]-a[1]+1 }
      else if ($i ~ /^[0-9]+$/) { n+=1 }
    }
    print n+0; exit
  }'
}
# 选一个存活主节点作为收槽与执行摘除的锚点（排除 fail 与待移除节点）
pick_target() {
  printf '%s\n' "${NODES}" | awk -v rem=",${REMOVE_NODES}," '
    $3 ~ /master/ && $3 !~ /fail/ {
      a=$2; sub(/@.*/,"",a);
      if (index(rem, "," a ",") > 0) next;
      print a; exit
    }'
}

TARGET_ADDR="$(pick_target || true)"
if [ -z "${TARGET_ADDR}" ]; then
  echo "没有可用的存活主节点（待移除节点可能覆盖了全部主节点），无法安全迁槽，中止缩容"
  exit 1
fi
TARGET_ID="$(node_id_of "${TARGET_ADDR}")"
[ -n "${TARGET_ID}" ] || { echo "无法定位主节点 ${TARGET_ADDR} 的节点 ID"; exit 1; }
echo "存活主节点（锚点）: ${TARGET_ADDR} → ${TARGET_ID}"

# ---- 解析待移除节点 ----
WANT=()
IFS=',' read -ra RAW <<< "${REMOVE_NODES}"
for t in "${RAW[@]}"; do
  t="$(printf '%s' "${t}" | xargs)"
  [ -n "${t}" ] && WANT+=("${t}")
done

REM_IDS=()
REM_ADDRS=()
for t in "${WANT[@]}"; do
  id="$(node_id_of "${t}")"
  if [ -z "${id}" ]; then
    echo "节点 ${t} 已不在集群中，跳过"
    continue
  fi
  REM_IDS+=("${id}")
  REM_ADDRS+=("${t}")
  echo "待下线节点 ${t} → ${id}"
done
if [ "${#REM_IDS[@]}" -eq 0 ]; then
  echo "待下线节点均已不在集群中，无需软下线"
  exit 0
fi

is_removing() {
  for x in "${REM_IDS[@]}"; do
    if [ "${x}" = "$1" ]; then return 0; fi
  done
  return 1
}

# ---- 1) 待移除主节点的从节点重挂到锚点，避免其被摘除后成为孤儿 ----
idx=0
for id in "${REM_IDS[@]}"; do
  mid="$(master_of "${id}")"
  idx=$((idx + 1))
  if [ -z "${mid}" ] || [ "${mid}" = "-" ]; then
    continue
  fi
  if is_removing "${mid}"; then
    addr="${REM_ADDRS[$((idx - 1))]}"
    host="${addr%%:*}"
    rport="${addr##*:}"
    echo "从节点 ${addr} 的主 ${mid} 也将下线，重挂到 ${TARGET_ID}"
    if docker exec "${CONTAINER}" redis-cli -a "${PASS}" --no-auth-warning -h "${host}" -p "${rport}" \
        CLUSTER REPLICATE "${TARGET_ID}" >/dev/null 2>&1; then
      echo "  已重挂 ${addr}"
    else
      echo "  警告：重挂失败，${addr} 将变为孤儿（不影响集群可用性，可稍后人工 replicaof）"
    fi
  fi
done

# ---- 2) 迁槽：待移除主节点持有的哈希槽全部交给锚点 ----
for id in "${REM_IDS[@]}"; do
  n="$(slot_count_of "${id}")"
  if [ -z "${n}" ]; then n=0; fi
  if [ "${n}" -gt 0 ]; then
    echo "迁移 ${id} 的 ${n} 个哈希槽 → ${TARGET_ID}"
    if ! cli --cluster reshard "${TARGET_ADDR}" --cluster-from "${id}" --cluster-to "${TARGET_ID}" \
        --cluster-slots "${n}" --cluster-yes; then
      echo "槽迁移失败，中止缩容以保护数据（集群未做任何摘除动作）"
      exit 1
    fi
  else
    echo "节点 ${id} 未持有哈希槽，无需迁移"
  fi
done

# ---- 3) 从集群摘除：先摘从节点，再摘主节点 ----
for id in "${REM_IDS[@]}"; do
  mid="$(master_of "${id}")"
  if [ -n "${mid}" ] && [ "${mid}" != "-" ]; then
    echo "从集群摘除从节点 ${id}"
    cli --cluster del-node "${TARGET_ADDR}" "${id}" || echo "  警告：del-node ${id} 失败，可稍后人工重试"
  fi
done
for id in "${REM_IDS[@]}"; do
  mid="$(master_of "${id}")"
  if [ -z "${mid}" ] || [ "${mid}" = "-" ]; then
    echo "从集群摘除主节点 ${id}"
    cli --cluster del-node "${TARGET_ADDR}" "${id}" || echo "  警告：del-node ${id} 失败，可稍后人工重试"
  fi
done

# ---- 4) 收尾校验：集群仍需处于 ok 才允许停容器 ----
STATE="$(cli cluster info 2>/dev/null | tr -d '\r' || true)"
echo "缩容后集群状态："
printf '%s\n' "${STATE}"
cli cluster nodes 2>/dev/null || true
if printf '%s\n' "${STATE}" | grep -q 'cluster_state:ok'; then
  echo "软下线完成，集群回 ok，可安全停容器"
  exit 0
fi
echo "集群状态不是 ok，请人工检查后再停容器（已摘除的节点不会再参与集群）"
exit 1
