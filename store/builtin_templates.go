// 内置部署模板预置数据：首次迁移时写入，is_builtin=1 不可删除。
package store

import "database/sql"

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
		name:        "安装 Docker",
		description: "通过官方脚本安装 Docker 并设置开机自启，已安装则跳过。",
		variables:   `[]`,
		script: `#!/bin/bash
set -e
if command -v docker &>/dev/null; then
  echo "Docker 已安装: $(docker --version)"
  exit 0
fi
curl -fsSL https://get.docker.com | bash
systemctl enable --now docker
echo "安装完成: $(docker --version)"`,
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
func seedBuiltinTemplates(db *sql.DB) error {
	for _, t := range builtinTemplates {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deploy_templates WHERE name=?`, t.name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
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
