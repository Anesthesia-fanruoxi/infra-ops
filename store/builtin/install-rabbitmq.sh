#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }

# ==== 参数 ====
AMQP_PORT="{{amqp_port}}"
MGMT_PORT="{{mgmt_port}}"
MQ_USER="{{username}}"
MQ_PASS="{{password}}"
IMAGE="{{image}}"
INSTALL_PLUGIN="{{delayed_plugin}}"
CONTAINER=rabbitmq
NET=middleware_net
PLUGIN_VER=3.12.0
PLUGIN_NAME="rabbitmq_delayed_message_exchange-${PLUGIN_VER}.ez"

[ -n "${MQ_PASS}" ] || { echo "管理员密码不能为空"; exit 1; }
case "${INSTALL_PLUGIN}" in yes|no) ;; *) echo "delayed_plugin 仅支持 yes/no: ${INSTALL_PLUGIN}"; exit 1 ;; esac

# ==== 共享网络 ====
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

PLUGIN_ARGS=()
if [ "${INSTALL_PLUGIN}" = "yes" ]; then
  WORK_DIR="/data/middleware/rabbitmq/plugins"
  mkdir -p "${WORK_DIR}"
  PLUGIN_FILE="${WORK_DIR}/${PLUGIN_NAME}"
  if [ -s "${PLUGIN_FILE}" ]; then
    echo "延迟队列插件已存在: ${PLUGIN_FILE}"
  else
    # 从 GitHub 官方 release 下载，失败轮换加速地址（共 6 次）
    SRC_URL="https://github.com/rabbitmq/rabbitmq-delayed-message-exchange/releases/download/v${PLUGIN_VER}/${PLUGIN_NAME}"
    PROXIES=("" "https://ghfast.top" "https://gh-proxy.com" "https://gh.ddlc.top")
    OK=0
    for i in 1 2 3 4 5 6; do
      P="${PROXIES[$(( (i-1) % 4 ))]}"
      URL="${P}${SRC_URL}"
      echo "下载插件 (尝试 ${i}/6): ${URL}"
      if curl -sfL --connect-timeout 10 --max-time 300 "${URL}" -o "${PLUGIN_FILE}" && [ "$(stat -c%s "${PLUGIN_FILE}" 2>/dev/null || echo 0)" -gt 10000 ]; then
        OK=1; break
      fi
      rm -f "${PLUGIN_FILE}"
      sleep 2
    done
    [ "${OK}" = "1" ] || { echo "插件下载失败，可手动放置到 ${PLUGIN_FILE} 后重跑"; exit 1; }
  fi
  echo "插件文件: ${PLUGIN_FILE} ($(( $(stat -c%s "${PLUGIN_FILE}") / 1024 )) KB)"
  PLUGIN_ARGS=(-v "${PLUGIN_FILE}:/opt/rabbitmq/plugins/${PLUGIN_NAME}")
fi

# ==== 幂等部署 ====
docker rm -f "${CONTAINER}" &>/dev/null || true
echo "拉取镜像 ${IMAGE}"
docker pull "${IMAGE}"

docker run -d --name "${CONTAINER}" --restart=always \
  --network "${NET}" \
  -p "${AMQP_PORT}:5672" \
  -p "${MGMT_PORT}:15672" \
  -e RABBITMQ_DEFAULT_USER="${MQ_USER}" \
  -e RABBITMQ_DEFAULT_PASS="${MQ_PASS}" \
  ${PLUGIN_ARGS[@]+"${PLUGIN_ARGS[@]}"} \
  "${IMAGE}"

# ==== 等待就绪 ====
READY=0
for _ in $(seq 1 45); do
  if docker exec "${CONTAINER}" rabbitmqctl status &>/dev/null; then READY=1; break; fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "RabbitMQ 启动超时，最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

# ==== 启用延迟队列插件 ====
if [ "${INSTALL_PLUGIN}" = "yes" ]; then
  if docker exec "${CONTAINER}" rabbitmq-plugins list 2>/dev/null | grep -q "rabbitmq_delayed_message_exchange.*E\*"; then
    echo "延迟队列插件已启用，跳过"
  else
    if docker exec "${CONTAINER}" rabbitmq-plugins enable rabbitmq_delayed_message_exchange; then
      echo "延迟队列插件启用成功（交换机类型 x-delayed-message，发送时设置 x-delay 头部，单位毫秒）"
    else
      echo "插件启用失败，可手动执行: docker exec ${CONTAINER} rabbitmq-plugins enable rabbitmq_delayed_message_exchange"
      exit 1
    fi
  fi
fi

echo "RabbitMQ 已就绪: AMQP {{__ip}}:${AMQP_PORT}，管理界面 http://{{__ip}}:${MGMT_PORT}（${MQ_USER}）"
