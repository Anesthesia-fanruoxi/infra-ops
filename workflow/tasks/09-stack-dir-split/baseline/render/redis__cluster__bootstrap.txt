#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

PASS="{{password}}"
PORT="{{port}}"
REPLICAS="{{replicas}}"
NODES="{{__nodes}}"
CONTAINER=redis-cluster

[ -n "${PASS}" ] || { echo "访问密码不能为空"; exit 1; }
[ -n "${NODES}" ] || { echo "节点列表为空"; exit 1; }
[ -n "${REPLICAS}" ] || REPLICAS=0

cli() {
  docker exec "${CONTAINER}" redis-cli -a "${PASS}" --no-auth-warning -p "${PORT}" "$@"
}

if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "未找到运行中的 ${CONTAINER} 容器"; exit 1
fi

STATE="$(cli cluster info 2>/dev/null | tr -d '\r' || true)"
if echo "${STATE}" | grep -q 'cluster_state:ok'; then
  echo "集群已处于 ok 状态，跳过 CLUSTER CREATE"
  cli cluster nodes || true
  exit 0
fi

NODE_ARGS=$(echo "${NODES}" | tr ',' ' ')
echo "初始化集群: ${NODE_ARGS}  replicas=${REPLICAS}"
# shellcheck disable=SC2086
cli --cluster create ${NODE_ARGS} --cluster-replicas "${REPLICAS}" --cluster-yes

echo "集群初始化完成"
cli cluster info || true
cli cluster nodes || true
