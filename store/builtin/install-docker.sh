#!/bin/bash
set -e

if command -v docker &>/dev/null; then
  echo "Docker 已安装: $(docker --version)"
  exit 0
fi

# 仅支持 RHEL 系发行版
if [ ! -f /etc/os-release ]; then
  echo "无法识别系统（缺少 /etc/os-release）"; exit 1
fi
. /etc/os-release
case "${ID}" in
  rocky|centos|almalinux|rhel) ;;
  *) echo "不支持的系统: ${ID}"; exit 1 ;;
esac
MAJOR="${VERSION_ID%%.*}"

# 强制写入阿里云 docker-ce 源（覆盖旧文件，避免残留 download.docker.com 源导致元数据拉取失败）
cat > /etc/yum.repos.d/docker-ce.repo <<REPOEOF
[docker-ce-stable]
name=Docker CE Stable - \$basearch
baseurl=https://mirrors.aliyun.com/docker-ce/linux/centos/${MAJOR}/\$basearch/stable
enabled=1
gpgcheck=1
gpgkey=https://mirrors.aliyun.com/docker-ce/linux/centos/gpg
REPOEOF

# 安装仓库中的最新版本
FULL_VER=$(dnf -q --showduplicates list available docker-ce | awk '/^docker-ce\./{print $2}' | sort -V | tail -1)
if [ -z "${FULL_VER}" ]; then
  echo "未找到可用的 docker-ce 版本"; exit 1
fi
echo "将安装 docker-ce ${FULL_VER}"

# docker-ce 与 cli 锁定同版本；两者 epoch 不同，去掉 epoch 按版本匹配
VER="${FULL_VER#*:}"
if ! dnf install -y "docker-ce-${VER}" "docker-ce-cli-${VER}" containerd.io docker-buildx-plugin docker-compose-plugin; then
  echo "常规安装失败，使用 --nogpgcheck 重试核心包"
  dnf install -y --nogpgcheck "docker-ce-${VER}" "docker-ce-cli-${VER}" containerd.io
fi

# daemon.json：镜像加速 / 日志轮转 / 数据目录 / 网桥配置
mkdir -p /etc/docker /data/docker
cat > /etc/docker/daemon.json <<'EOF'
{
  "registry-mirrors": [
    "https://docker.m.daocloud.io",
    "https://dockerhub.icu",
    "https://h9vtw6kz.mirror.aliyuncs.com"
  ],
  "exec-opts": ["native.cgroupdriver=systemd"],
  "log-driver": "json-file",
  "log-opts": {"max-size": "100m", "max-file": "3"},
  "storage-driver": "overlay2",
  "data-root": "/data/docker",
  "bip": "10.200.0.1/24",
  "ipv6": false,
  "live-restore": true,
  "default-ulimits": {"nofile": {"Name": "nofile", "Hard": 65535, "Soft": 65535}}
}
EOF

systemctl daemon-reload
systemctl enable --now docker
systemctl is-active --quiet docker || { echo "docker 服务未正常启动"; exit 1; }
echo "安装完成: $(docker --version)"
docker info 2>/dev/null | grep -E "Server Version|Docker Root Dir" || true