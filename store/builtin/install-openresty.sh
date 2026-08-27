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

# 备份现有主配置后写入生产级配置（@@NGINX_CONF@@ 由平台注入 nginx.conf 内容）
[ -f "${NGX_CONF}" ] && cp "${NGX_CONF}" "${NGX_CONF}.bak.$(date +%s)"
cat > "${NGX_CONF}" <<'NGINXEOF'
@@NGINX_CONF@@
NGINXEOF

# 语法校验通过才重载；失败保留本次写入并报错，便于人工排查
"${NGX_BIN}" -t
systemctl reload openresty || "${NGX_BIN}" -s reload
echo "OpenResty 配置已生效: $(openresty -v 2>&1)"
echo "生产级 nginx.conf 已写入 ${NGX_CONF}"