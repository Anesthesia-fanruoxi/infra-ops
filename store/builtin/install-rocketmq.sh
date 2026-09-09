#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
NS_PORT="{{ns_port}}"
BROKER_PORT="{{broker_port}}"
VIP_PORT="{{vip_port}}"
HOME_DIR="{{home_dir}}"
IMAGE="{{image}}"
CONTAINER_NS=rocketmq-namesrv
CONTAINER_B=rocketmq-broker

# ==== 共享网络 ====
NET=middleware_net
docker network inspect "${NET}" &>/dev/null || docker network create "${NET}"

[ -n "${NS_PORT}" ] && [ -n "${BROKER_PORT}" ] || true
mkdir -p "${HOME_DIR}/conf" "${HOME_DIR}/store" "${HOME_DIR}/logs/namesrv" "${HOME_DIR}/logs/broker"

# ==== broker.conf（brokerIP1 必须是集群外部可达的主机 IP） ====
cat > "${HOME_DIR}/conf/broker.conf" <<CONFEOF
brokerClusterName=RocketMQ-Cluster
brokerName=broker-a
brokerId=0
brokerIP1={{__ip}}
namesrvAddr=rocketmq-namesrv:9876
listenPort=10911
autoCreateTopicEnable=true
autoCreateSubscriptionGroup=true
deleteWhen=04
fileReservedTime=48
flushDiskType=ASYNC_FLUSH
CONFEOF

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: rocketmq
services:
  namesrv:
    image: {{image}}
    container_name: rocketmq-namesrv
    restart: always
    ports:
      - "{{ns_port}}:9876"
    environment:
      JAVA_OPT_EXT: "-server -Xms512m -Xmx512m -Xmn256m"
    command: ["sh", "mqnamesrv"]
    volumes:
      - {{home_dir}}/logs/namesrv:/home/rocketmq/logs
    networks:
      - middleware_net

  broker:
    image: {{image}}
    container_name: rocketmq-broker
    restart: always
    depends_on:
      - namesrv
    ports:
      - "{{broker_port}}:10911"
      - "{{vip_port}}:10909"
    environment:
      JAVA_OPT_EXT: "-server -Xms1g -Xmx1g -Xmn512m"
    command: ["sh", "mqbroker", "-n", "rocketmq-namesrv:9876", "-c", "/home/rocketmq/broker.conf"]
    volumes:
      - {{home_dir}}/conf/broker.conf:/home/rocketmq/broker.conf:ro
      - {{home_dir}}/store:/home/rocketmq/store
      - {{home_dir}}/logs/broker:/home/rocketmq/logs
    networks:
      - middleware_net

networks:
  middleware_net:
    external: true
YAMLEOF

# ==== 拉取并启动 ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d

# ==== 等待就绪（broker 日志出现 boot success） ====
READY=0
for _ in $(seq 1 45); do
  if [ -n "$(docker logs "${CONTAINER_B}" --tail 200 2>&1 | grep -o 'boot success' | head -1)" ] \
     && [ -n "$(docker exec "${CONTAINER_B}" sh -c 'echo "" > /dev/tcp/127.0.0.1/10911 2>/dev/null' && echo ok 2>/dev/null)" ]; then
    READY=1; break
  fi
  sleep 2
done
# /dev/tcp 在部分 shell 不可用，退化为仅靠日志
if [ "${READY}" != "1" ] && [ -n "$(docker logs "${CONTAINER_B}" --tail 300 2>&1 | grep -o 'boot success' | head -1)" ]; then
  READY=1
fi
if [ "${READY}" != "1" ]; then
  echo "RocketMQ 启动超时，最近日志:"
  docker logs "${CONTAINER_B}" --tail 40 2>&1 || true
  exit 1
fi

echo "RocketMQ 已就绪: namesrv {{__ip}}:${NS_PORT}，broker {{__ip}}:${BROKER_PORT}（数据目录 ${HOME_DIR}/store）"
echo "broker.conf: ${HOME_DIR}/conf/broker.conf（brokerIP1 已指向主机 IP，改后需重启容器）"
echo "compose 文件: ${HOME_DIR}/compose.yml（改参数后 docker compose -f ${HOME_DIR}/compose.yml up -d 生效）"