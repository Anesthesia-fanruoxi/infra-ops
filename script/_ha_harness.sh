#!/usr/bin/env bash
# 一次性 harness：验证 wait_zk 的探测集修复（run 76 死锁回归）。
# 场景复刻 run 76：ensemble 5 节点，leader 是第 5 台，客户端连接串 ZK_IPS 只有前 3 台。
# 断言：新 wait_zk（探 ZK_ALL_IPS 全集）通过；旧逻辑（探 ZK_IPS）失败。
set -u

# shellcheck disable=SC1091
source "D:/project/go/infra-ops/store/stacks/bigdata/scripts/ha.sh"   # 仅声明不执行（设计保证）

seq() { echo 1; }    # 压缩重试：60 轮 → 1 轮
sleep() { :; }

# zk_state 网络实现与 ha.sh 逐字一致，仅把 ip 映射到本地假端点端口
zk_state() {
  local ip="$1" port
  case "${ip}" in
    10.77.0.1) port=12181 ;; 10.77.0.2) port=12182 ;; 10.77.0.3) port=12183 ;;
    10.77.0.4) port=12184 ;; 10.77.0.5) port=12185 ;; *) return 0 ;;
  esac
  local r=""
  r="$( { exec 3<>"/dev/tcp/127.0.0.1/${port}" && printf 'mntr' >&3 && timeout 3 cat <&3; } 2>/dev/null )" || true
  printf '%s\n' "${r}" | sed -n 's/^zk_server_state[[:space:]]*//p' | head -n 1
}

ZK_IPS="10.77.0.1,10.77.0.2,10.77.0.3"
ZK_ALL_IPS="10.77.0.1,10.77.0.2,10.77.0.3,10.77.0.4,10.77.0.5"

if wait_zk; then
  echo "HARNESS: new-wait_zk PASS（5 节点全集探测，leader 在连接串之外仍可见）"
else
  echo "HARNESS: new-wait_zk FAIL"
  exit 1
fi

old_wait_zk() { # 修复前实现：探测集 = ZK_IPS（客户端连接串）
  local _z st N TOTAL NEED L
  local -a Z
  IFS=',' read -ra Z <<< "${ZK_IPS}"
  TOTAL=${#Z[@]}; NEED=$(( TOTAL / 2 + 1 ))
  for _ in $(seq 1 60); do
    N=0; L=0
    for _z in "${Z[@]}"; do
      st="$(zk_state "${_z}")"
      case "${st}" in
        leader)   N=$((N+1)); L=1 ;;
        follower) N=$((N+1)) ;;
      esac
    done
    if [ "${N}" -ge "${NEED}" ] && [ "${L}" = "1" ]; then return 0; fi
    sleep 3
  done
  return 1
}
if old_wait_zk 2>/dev/null; then
  echo "HARNESS: old-wait_zk 意外通过 —— 反向验证失败"
  exit 1
else
  echo "HARNESS: old-wait_zk FAIL（复现 run 76 死锁，符合预期）"
fi
echo "HARNESS-OK"
