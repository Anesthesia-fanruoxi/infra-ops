#!/bin/bash
set -e

# K8s 节点统一前置初始化：供后续用 sealos 等工具部署 Kubernetes/Cilium 前，把节点预置到可用的系统状态。
# 每个动作由独立开关变量控制（yes=执行，no=跳过），默认全开；已满足的命令自动幂等。
# 变量: {{swap_off}} {{selinux_off}} {{firewall_off}} {{net_forward}} {{ebpf_tune}} {{time_sync}} {{install_containerd}}

# ==== 仅支持 RHEL 系发行版 ====
if [ ! -f /etc/os-release ]; then
  echo "无法识别系统（缺少 /etc/os-release）"; exit 1
fi
. /etc/os-release
case "${ID}" in
  rocky|centos|almalinux|rhel) ;;
  *) echo "不支持的系统: ${ID}"; exit 1 ;;
esac

SWAP_OFF="{{swap_off}}"
SELINUX_OFF="{{selinux_off}}"
FIREWALL_OFF="{{firewall_off}}"
NET_FORWARD="{{net_forward}}"
EBPF_TUNE="{{ebpf_tune}}"
TIME_SYNC="{{time_sync}}"
INSTALL_CONTAINERD="{{install_containerd}}"

[ -n "${SWAP_OFF}" ] || SWAP_OFF=yes
[ -n "${SELINUX_OFF}" ] || SELINUX_OFF=yes
[ -n "${FIREWALL_OFF}" ] || FIREWALL_OFF=yes
[ -n "${NET_FORWARD}" ] || NET_FORWARD=yes
[ -n "${EBPF_TUNE}" ] || EBPF_TUNE=yes
[ -n "${TIME_SYNC}" ] || TIME_SYNC=yes
[ -n "${INSTALL_CONTAINERD}" ] || INSTALL_CONTAINERD=yes

# ==== 1. 关闭 swap（kubelet 硬性要求） ====
if [ "${SWAP_OFF}" = "yes" ]; then
  echo "--- 关闭 swap ---"
  swapoff -a 2>/dev/null || true
  # 注释 /etc/fstab 中 swap 条目，保证重启后不恢复（用 \s+ 兼容空格分隔）
  if [ -f /etc/fstab ]; then
    sed -i 's|^\([^#].*\sswap\s.*\)$|#\1|' /etc/fstab
  fi
  if command -v swapoff >/dev/null 2>&1; then
    echo "swap 当前状态: $(free -h | awk '/Swap/{print "已用 "$3" / 总量 "$2}')"
  fi
  echo "swap 已关闭，并已注释 /etc/fstab 中的 swap 条目"
fi

# ==== 2. 关闭 SELinux ====
if [ "${SELINUX_OFF}" = "yes" ]; then
  echo "--- 关闭 SELinux ---"
  if [ -f /etc/selinux/config ]; then
    sed -i 's/^SELINUX=enforcing/SELINUX=disabled/; s/^SELINUX=permissive/SELINUX=disabled/' /etc/selinux/config
  fi
  if command -v setenforce >/dev/null 2>&1; then
    setenforce 0 2>/dev/null || true
  fi
  echo "SELinux 已切换为 disabled（立即失效，重启永久生效）"
fi

# ==== 3. 关闭 firewalld ====
if [ "${FIREWALL_OFF}" = "yes" ]; then
  echo "--- 关闭 firewalld ---"
  if systemctl list-unit-files 2>/dev/null | grep -q '^firewalld.service'; then
    systemctl disable --now firewalld 2>/dev/null || true
    echo "firewalld 已 disable 并停止；建议关闭后统一由云平台/安全组管理出站入站规则"
  else
    echo "未检测到 firewalld，跳过"
  fi
fi

# ==== 4. 内核模块加载 + 网络转发参数 ====
if [ "${NET_FORWARD}" = "yes" ]; then
  echo "--- 加载内核模块与网络转发参数 ---"
  # 写入 /etc/modules-load.d 保证开机加载
  MOD_FILE=/etc/modules-load.d/k8s-init.conf
  touch "${MOD_FILE}"
  for m in overlay br_netfilter; do
    if ! grep -qx "${m}" "${MOD_FILE}"; then echo "${m}" >> "${MOD_FILE}"; fi
    modprobe "${m}" 2>/dev/null || true
  done

  # K8s 必需的网络桥接转发参数（写入 sysctl.d 持久化）
  SYSCTL_FILE=/etc/sysctl.d/99-k8s-init.conf
  cat > "${SYSCTL_FILE}" <<'EOF'
net.bridge.bridge-nf-call-iptables=1
net.bridge.bridge-nf-call-ip6tables=1
net.ipv4.ip_forward=1
net.ipv6.conf.all.forwarding=1
EOF
  sysctl --system >/dev/null 2>&1 || sysctl -p "${SYSCTL_FILE}" >/dev/null 2>&1 || true
  echo "已加载模块 overlay/br_netfilter，已启用 ip_forward 与 bridge-nf-call"
fi

# ==== 5. eBPF（Cilium）内核调优 ====
if [ "${EBPF_TUNE}" = "yes" ]; then
  echo "--- eBPF（Cilium）内核调优 ---"
  # Cilium 使用 eBPF 数据面：关闭 rp_filter（严格模式会造成 veth 报文回落）、加大连接跟踪与文件句柄上限
  EBPF_SYSCTL=/etc/sysctl.d/98-cilium-ebpf.conf
  cat > "${EBPF_SYSCTL}" <<'EOF'
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
net.core.bpf_jit_enable=1
net.ipv4.tcp_rmem=4096 87380 33554432
net.ipv4.tcp_wmem=4096 16384 33554432
fs.file-max=2097152
fs.inotify.max_user_instances=8192
fs.inotify.max_user_watches=524288
EOF
  sysctl --system >/dev/null 2>&1 || sysctl -p "${EBPF_SYSCTL}" >/dev/null 2>&1 || true

  # 挂载 BPF 文件系统（Cilium bpffs 依赖 /sys/fs/bpf）
  TARGET=/sys/fs/bpf
  if ! mountpoint -q "${TARGET}" 2>/dev/null; then
    mount -t bpf bpf "${TARGET}" 2>/dev/null || true
  fi
  if ! grep -qF "${TARGET}" /etc/fstab 2>/dev/null; then printf 'bpf %s bpf noauto,nodev 0 0\n' "${TARGET}" >> /etc/fstab; fi

  # 提示：确认内核支持 eBPF
  KERN="$(uname -r)"
  echo "当前内核: ${KERN}"
  if ls /boot/config-${KERN} >/dev/null 2>&1 && grep -qE '^CONFIG_BPF=(y|m)' "/boot/config-${KERN}"; then
    echo "内核已启用 BPF 支持（CONFIG_BPF），可配合 Cilium eBPF 数据面"
  else
    echo "未在 /boot/config-${KERN} 中确认 CONFIG_BPF；若内核版本偏低，Cilium 会回退特性，建议使用 lts/最新稳定的内核对齐 eBPF"
  fi
  echo "eBPF 调优参数已写入 ${EBPF_SYSCTL}，bpffs 已准备"
fi

# ==== 6. 时间同步 ====
if [ "${TIME_SYNC}" = "yes" ]; then
  echo "--- 时间同步 ---"
  if command -v chronyd >/dev/null 2>&1 || rpm -q chrony >/dev/null 2>&1; then
    systemctl enable --now chronyd 2>/dev/null || systemctl restart chronyd 2>/dev/null || true
    echo "chronyd 已启用"
  else
    if ! dnf install -y chrony >/dev/null 2>&1; then
      echo "警告: chrony 安装失败，请手动配置时间同步（sealos 部署前会做时间预检）"
    else
      systemctl enable --now chronyd 2>/dev/null || true
      echo "chrony 已安装并启用"
    fi
  fi
fi

# ==== 7. 预装 containerd（sealos 默认运行时） ====
if [ "${INSTALL_CONTAINERD}" = "yes" ]; then
  echo "--- 预装 containerd ---"
  if command -v containerd >/dev/null 2>&1; then
    echo "containerd 已存在: $(containerd --version 2>/dev/null | head -1 || true)"
  else
    # 复用阿里云 docker-ce 源安装 containerd.io（与「安装 Docker」同源，避免重复配置仓库时冲突）
    if [ ! -f /etc/yum.repos.d/docker-ce.repo ]; then
      cat > /etc/yum.repos.d/docker-ce.repo <<REPOEOF
[docker-ce-stable]
name=Docker CE Stable - \$basearch
baseurl=https://mirrors.aliyun.com/docker-ce/linux/centos/${VERSION_ID%%.*}/\$basearch/stable
enabled=1
gpgcheck=1
gpgkey=https://mirrors.aliyun.com/docker-ce/linux/centos/gpg
REPOEOF
    fi
    if ! dnf install -y containerd.io >/dev/null 2>&1; then
      echo "containerd 安装失败，请检查网络/仓库后重试"; exit 1
    fi
  fi

  # 生成默认配置并切换到 systemd cgroup 驱动（kubelet 依赖，否则节点无法就绪）
  mkdir -p /etc/containerd
  containerd config default > /etc/containerd/config.toml 2>/dev/null || true
  if [ -f /etc/containerd/config.toml ]; then
    sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
  fi
  systemctl enable --now containerd 2>/dev/null || true
  systemctl is-active --quiet containerd || { echo "containerd 服务未正常运行"; exit 1; }
  echo "containerd 已启用（systemd cgroup 驱动）"
fi

echo ""
echo "K8s 节点初始化完成。接下来可用 sealos 等工具部署 Kubernetes 集群。"