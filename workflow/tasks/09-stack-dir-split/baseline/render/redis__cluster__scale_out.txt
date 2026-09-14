#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

PASS="{{password}}"
PORT="{{port}}"
EXISTING="{{__master_ip}}:{{__master_port}}"
NEWS="{{__new_nodes}}"
CONTAINER=redis-cluster

[ -n "${PASS}" ] || { echo "访问密码不能为空"; exit 1; }
[ -n "${EXISTING}" ] || { echo "现有集群节点为空"; exit 1; }
[ -n "${NEWS}" ] || { echo "待加入节点列表为空"; exit 1; }
[ -n "${PORT}" ] || PORT=6379

cli() {
  docker exec "${CONTAINER}" redis-cli -a "${PASS}" --no-auth-warning -p "${PORT}" "$@"
}

if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "未找到运行中的 ${CONTAINER} 容器（请在已有集群节点上执行加节点）"
  exit 1
fi

STATE="$(cli cluster info 2>/dev/null | tr -d '\r' || true)"
if ! echo "${STATE}" | grep -q 'cluster_state:ok'; then
  echo "现有集群未处于 ok 状态，拒绝加节点"
  echo "${STATE}"
  exit 1
fi

IFS=',' read -ra ARR <<< "${NEWS}"
for n in "${ARR[@]}"; do
  n="$(echo "${n}" | xargs)"
  [ -n "${n}" ] || continue
  echo "CLUSTER ADDSLOT: add-node ${n} -> ${EXISTING}"
  cli --cluster add-node "${n}" "${EXISTING}" --cluster-yes
done

echo "加节点完成"
cli cluster info || true
cli cluster nodes || true
