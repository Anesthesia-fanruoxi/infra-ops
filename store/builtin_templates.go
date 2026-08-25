// 内置部署模板预置数据：首次迁移时写入，is_builtin=1 不可删除。
package store

import (
	"database/sql"
	"strings"
)

type builtinTemplate struct {
	name        string
	description string
	script      string
	variables   string // JSON: [{name,label,default,required}]
}

var builtinTemplates = []builtinTemplate{
	{
		name:        "新增 Linux 用户",
		description: "创建带家目录和 bash 的普通用户，已存在则跳过。",
		variables:   `[{"name":"username","label":"用户名","default":"","required":true}]`,
		script: `#!/bin/bash
set -e
if id "{{username}}" &>/dev/null; then
  echo "用户 {{username}} 已存在，跳过"
  exit 0
fi
useradd -m -s /bin/bash "{{username}}"
echo "用户 {{username}} 创建成功"
id "{{username}}"`,
	},
	{
		name:        "批量修改主机名",
		description: "按「前缀+序号」批量命名：如前缀 node-、起始编号 1，选中 10 台则依次命名为 node-1 ~ node-10。序号按任务内主机清单顺序分配（页面上可先按主机名或 IP 排序再全选）。同时持久化 hostname、更新 /etc/hosts，并自动同步平台台账中的主机名。",
		variables:   `[{"name":"prefix","label":"主机名前缀（含连接符）","default":"node-","required":true},{"name":"start_num","label":"起始编号","default":"1","required":true}]`,
		script: `#!/bin/bash
set -e
PREFIX="{{prefix}}"
START_NUM="{{start_num}}"
SEQ={{__seq}}
case "${START_NUM}" in ''|*[!0-9]*) echo "起始编号必须是数字: ${START_NUM}"; exit 1 ;; esac
if [ -z "${PREFIX}" ]; then echo "主机名前缀不能为空"; exit 1; fi
NEW_NAME="$(printf '%s%d' "${PREFIX}" $((SEQ + START_NUM - 1)))"
if ! echo "${NEW_NAME}" | grep -qE '^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$'; then
  echo "非法主机名: ${NEW_NAME}（仅允许字母、数字、- 和 .）"; exit 1
fi
OLD=$(hostname)
hostnamectl set-hostname "${NEW_NAME}"
# 更新 /etc/hosts 中 127.0.1.1 的旧主机名映射（如存在）
if grep -qE '^127\.0\.1\.1[[:space:]]' /etc/hosts 2>/dev/null; then
  sed -i "s/^\\(127\\.0\\.1\\.1[[:space:]]\\+\\).*/\\1${NEW_NAME}/" /etc/hosts
fi
echo "本机序号=${SEQ}，系统主机名已由 ${OLD} 修改为: ${NEW_NAME}"
# 自报新名字给平台，自动同步台账
echo "infra-ops:set-name=${NEW_NAME}"`,
	},
	{
		name:        "安装 Docker",
		description: "适配 Rocky/CentOS 9/10：阿里云源安装最新版 Docker CE（含 buildx/compose 插件），写入 daemon.json（镜像加速/日志轮转/数据目录）。已安装则跳过。",
		variables:   `[]`,
		script: `#!/bin/bash
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
docker info 2>/dev/null | grep -E "Server Version|Docker Root Dir" || true`,
	},
	{
		name:        "安装 OpenResty",
		description: "OpenResty 官方源安装最新版（Rocky/EL 8/9；Rocky 10 自动回退 el9 源）。已安装则跳过。",
		variables:   `[]`,
		script: `#!/bin/bash
set -e

if command -v openresty &>/dev/null; then
  echo "OpenResty 已安装: $(openresty -v 2>&1)"
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

if [ "${MAJOR}" -ge 10 ]; then
  # EL10：官方暂无 rocky/10 目录，直接写死 rocky/9 源
  cat > /etc/yum.repos.d/openresty.repo <<'REPOEOF'
[openresty]
name=Official OpenResty Open Source Repository for Rocky Linux
baseurl=https://openresty.org/package/rocky/9/$basearch
gpgcheck=1
repo_gpgcheck=0
gpgkey=https://openresty.org/package/pubkey2.gpg
enabled=1
enabled_metadata=1
REPOEOF
else
  # EL9+ 用 openresty2.repo，EL8 用 openresty.repo；dnf5 新语法失败回退旧语法
  if [ "${MAJOR}" -le 8 ]; then
    REPO_URL="https://openresty.org/package/rocky/openresty.repo"
  else
    REPO_URL="https://openresty.org/package/rocky/openresty2.repo"
  fi
  if ! dnf repolist --enabled 2>/dev/null | grep -q '^openresty'; then
    dnf config-manager addrepo --from-repofile="${REPO_URL}" 2>/dev/null \
      || dnf config-manager --add-repo "${REPO_URL}"
  fi
fi

# 安装仓库中的最新版本
FULL_VER=$(dnf -q --showduplicates list available openresty | awk '/^openresty\./{print $2}' | sort -V | tail -1)
if [ -z "${FULL_VER}" ]; then
  echo "未找到可用的 openresty 版本"; exit 1
fi
echo "将安装 openresty ${FULL_VER}"

dnf install -y "openresty-${FULL_VER}"

systemctl enable --now openresty
systemctl is-active --quiet openresty || { echo "openresty 服务未正常启动"; exit 1; }
echo "安装完成: $(openresty -v 2>&1)"`,
	},
	{
		name:        "时区与 NTP 同步",
		description: "设置系统时区并安装 chrony 完成时间同步。",
		variables:   `[{"name":"timezone","label":"时区","default":"Asia/Shanghai","required":true}]`,
		script: `#!/bin/bash
set -e
timedatectl set-timezone "{{timezone}}"
if command -v apt-get &>/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null 2>&1 || true
  apt-get install -y chrony >/dev/null 2>&1 || true
elif command -v yum &>/dev/null; then
  yum install -y chrony >/dev/null 2>&1 || true
fi
systemctl enable --now chronyd 2>/dev/null || systemctl enable --now chrony 2>/dev/null || true
chronyc makestep 2>/dev/null || true
echo "时区: $(timedatectl show -p Timezone --value)"
echo "时间: $(date '+%F %T')"`,
	},
	{
		name:        "内核网络参数调优",
		description: "开启 BBR 拥塞控制并优化常见内核网络参数。",
		variables:   `[]`,
		script: `#!/bin/bash
set -e
cat > /etc/sysctl.d/99-tuning.conf <<EOF
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.tcp_max_syn_backlog=8192
net.core.somaxconn=8192
net.ipv4.tcp_fastopen=3
vm.swappiness=10
EOF
sysctl --system >/dev/null
echo "BBR 状态: $(sysctl -n net.ipv4.tcp_congestion_control)"`,
	},
	{
		name:        "磁盘空间清理",
		description: "清理包管理缓存、旧日志与临时文件，输出清理前后磁盘使用对比。",
		variables:   `[]`,
		script: `#!/bin/bash
echo "=== 清理前 ==="
df -h / | tail -1
if command -v apt-get &>/dev/null; then
  apt-get clean
  apt-get autoremove -y >/dev/null 2>&1 || true
fi
if command -v yum &>/dev/null; then
  yum clean all >/dev/null 2>&1 || true
fi
journalctl --vacuum-size=100M >/dev/null 2>&1 || true
find /var/log -name "*.gz" -mtime +7 -delete 2>/dev/null || true
echo "=== 清理后 ==="
df -h / | tail -1`,
	},
	{
		name:        "修改 SSH 端口",
		description: "修改 sshd 监听端口并重启服务。执行前请确认防火墙已放行新端口！",
		variables:   `[{"name":"ssh_port","label":"新端口","default":"","required":true}]`,
		script: `#!/bin/bash
set -e
cp /etc/ssh/sshd_config /etc/ssh/sshd_config.bak.$(date +%s)
sed -i "s/^#\?Port .*/Port {{ssh_port}}/" /etc/ssh/sshd_config
if ! grep -q "^Port {{ssh_port}}" /etc/ssh/sshd_config; then
  echo "Port {{ssh_port}}" >> /etc/ssh/sshd_config
fi
sshd -t
systemctl restart sshd 2>/dev/null || systemctl restart ssh
echo "SSH 端口已改为 {{ssh_port}}"
echo "警告：请先用新端口验证可连通，再关闭当前会话！"` ,
	},
}

// seedBuiltinTemplates 预置内置模板（幂等，按 name 去重）。
// 内置模板随版本演进：脚本有变化时原地刷新（内置模板 UI 只读，不存在覆盖用户修改的问题）；
// 已不在当前内置列表中的历史内置模板会被清理。
func seedBuiltinTemplates(db *sql.DB) error {
	// 清理历史版本遗留的内置模板
	names := make([]string, len(builtinTemplates))
	args := make([]interface{}, len(builtinTemplates))
	for i, t := range builtinTemplates {
		names[i] = t.name
		args[i] = t.name
	}
	if _, err := db.Exec(
		`DELETE FROM deploy_templates WHERE is_builtin=1 AND name NOT IN (`+strings.Repeat("?,", len(args)-1)+"?)", args...,
	); err != nil {
		return err
	}

	for _, t := range builtinTemplates {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deploy_templates WHERE name=?`, t.name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			var curScript, curVars string
			if err := db.QueryRow(`SELECT script, variables FROM deploy_templates WHERE name=? AND is_builtin=1`, t.name).Scan(&curScript, &curVars); err == nil && (curScript != t.script || curVars != t.variables) {
				if _, err := db.Exec(
					`UPDATE deploy_templates SET description=?, script=?, variables=?, updated_at=datetime('now','localtime') WHERE name=? AND is_builtin=1`,
					t.description, t.script, t.variables, t.name,
				); err != nil {
					return err
				}
			}
			continue
		}
		_, err := db.Exec(
			`INSERT INTO deploy_templates(name, description, script, variables, is_builtin) VALUES(?,?,?,?,1)`,
			t.name, t.description, t.script, t.variables,
		)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	return nil
}
