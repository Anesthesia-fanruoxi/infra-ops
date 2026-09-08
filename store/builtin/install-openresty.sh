#!/bin/bash
set -e

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

INSTALLED=0
if command -v openresty &>/dev/null; then
  echo "OpenResty 已安装: $(openresty -v 2>&1)"
  INSTALLED=1
fi

if [ "${INSTALLED}" != "1" ]; then
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
fi

systemctl enable --now openresty
systemctl is-active --quiet openresty || { echo "openresty 服务未正常启动"; exit 1; }

# ==== 生成生产级 nginx.conf ====
NGX_CONF=/usr/local/openresty/nginx/conf/nginx.conf
NGX_BIN="$(command -v openresty)"

# 工作用户（openresty 官方 RPM 默认 nginx，缺失则补建）
NGX_USER="nginx"
if ! id "${NGX_USER}" &>/dev/null; then useradd -r -s /sbin/nologin "${NGX_USER}"; fi

# config 引用的目录：证书 / 站点 / 缓存 / 日志，创建并设定属主
mkdir -p /etc/nginx/cert /etc/nginx/conf.d \
  /var/cache/openresty/proxy_temp /var/cache/openresty/proxy_cache /var/log/openresty
chown -R "${NGX_USER}:${NGX_USER}" /etc/nginx/cert /var/cache/openresty /var/log/openresty

# 备份现有主配置后写入生产级配置（nginx.conf 内容由平台经资源占位符注入，注释中勿写占位符字面量，否则会被误替换）
[ -f "${NGX_CONF}" ] && cp "${NGX_CONF}" "${NGX_CONF}.bak.$(date +%s)"
# __DEPLOY_CONF__ nginx_conf
cat > "${NGX_CONF}" <<'NGINXEOF'
@@NGINX_CONF@@
NGINXEOF
# __DEPLOY_CONF_END__ nginx_conf

# ==== 生成默认站点（conf.d），安装后即可通过 http://<ip> 访问 ====
DEFAULT_CONF=/etc/nginx/conf.d/default.conf
cat > "${DEFAULT_CONF}" <<'DEFAULTCONF'
# 平台生成的默认站点：安装后即可通过 http://<ip> 访问；需自定义站点时请替换本文件
server {
    listen 80 default_server;
    server_name _;
    access_log off;

    location = / {
        default_type text/html; charset utf-8;
        return 200 '<!doctype html><html><head><meta charset="utf-8"><title>Ready</title></head><body style="font-family:sans-serif;text-align:center;margin-top:12vh"><h1 style="color:#2f5a6b">OpenResty 已就绪</h1><p>默认站点由 infra-ops 安装模板生成</p><p style="color:#999">如需自定义站点，替换 conf.d 下的 default.conf 并 reload 即可</p></body></html>';
    }

    location / {
        default_type text/plain; charset utf-8;
        return 200 'OpenResty is running.\n';
    }
}
DEFAULTCONF
echo "默认站点已生成: ${DEFAULT_CONF}"

# 语法校验通过才重载；失败保留本次写入并报错，便于人工排查
"${NGX_BIN}" -t
systemctl reload openresty || "${NGX_BIN}" -s reload
echo "OpenResty 配置已生效: $(openresty -v 2>&1)"
echo "生产级 nginx.conf 已写入 ${NGX_CONF}"

# ==== 防火墙放行 Web 端口（否则外部访问会超时 ERR_CONNECTION_TIMED_OUT） ====
if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld 2>/dev/null; then
  for P in 80 443; do firewall-cmd --permanent --add-port=${P}/tcp >/dev/null 2>&1 || true; done
  firewall-cmd --reload >/dev/null 2>&1 || true
  echo "firewalld 已放行 80/443 端口"
elif command -v iptables >/dev/null 2>&1; then
  iptables -C INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null || true
  iptables -C INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || true
  echo "iptables 已放行 80/443 端口"
fi

# 自检：80 端口是否真正监听，输出便于回溯
if command -v ss >/dev/null 2>&1; then
  if ss -tln 2>/dev/null | grep -q ':80 '; then
    echo "自检通过：80 端口监听中，可通过 http://<主机IP>/ 访问"
  else
    echo "警告：80 端口未监听，请检查 nginx 配置与错误日志"
  fi
fi