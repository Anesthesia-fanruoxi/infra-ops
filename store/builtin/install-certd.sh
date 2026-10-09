#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HTTPS_PORT="{{https_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
RESET_ADMIN="{{reset_admin_passwd}}"
CONTAINER=certd

[ -n "${PORT}" ] || PORT=7001
[ -n "${HTTPS_PORT}" ] || HTTPS_PORT=7002
[ -n "${HOME_DIR}" ] || { echo "数据目录不能为空"; exit 1; }
[ -n "${IMAGE}" ] || IMAGE="registry.cn-shenzhen.aliyuncs.com/handsfree/certd:latest"
# yes/no -> true/false（certd_system_resetAdminPasswd 只认 true/false）
[ "${RESET_ADMIN}" = "yes" ] && RESET_ADMIN="true" || RESET_ADMIN="false"

mkdir -p "${HOME_DIR}"

# ==== 生成 compose 文件（数据目录与证书库都在 ${HOME_DIR}，请定期备份该目录） ====
cat > "${HOME_DIR}/compose.yml" <<YAMLEOF
name: certd
services:
  certd:
    image: ${IMAGE}
    container_name: ${CONTAINER}
    restart: unless-stopped
    ports:
      - "${PORT}:7001"
      - "${HTTPS_PORT}:7002"
    volumes:
      - ${HOME_DIR}:/app/data
    labels:
      com.centurylinklabs.watchtower.enable: "true"
    environment:
      - TZ=Asia/Shanghai
      - certd_system_resetAdminPasswd=${RESET_ADMIN}
      - certd_koa_hostname=0.0.0.0
YAMLEOF

# ==== 拉取并启动 ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 等待就绪 ====
READY=0
for _ in $(seq 1 45); do
  if (echo > "/dev/tcp/127.0.0.1/${PORT}") >/dev/null 2>&1; then READY=1; break; fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "certd 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

echo "certd 已就绪: http://{{__ip}}:${PORT}（HTTPS 端口 ${HTTPS_PORT}）"
echo "数据目录: ${HOME_DIR}（数据库与证书都在这里，请定期备份以保障容灾）"
if [ "${RESET_ADMIN}" = "true" ]; then
  echo "注意: 本次以 resetAdminPasswd=true 启动，管理员密码已重置为 123456，登录后请及时改密并把该参数改回 no 重跑本模板。"
fi
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"
