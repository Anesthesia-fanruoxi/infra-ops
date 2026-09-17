#!/bin/bash
set -e

command -v docker &>/dev/null || { echo "未检测到 Docker，请先执行「安装 Docker」模板"; exit 1; }
docker info &>/dev/null || { echo "docker 服务未运行"; exit 1; }
docker compose version &>/dev/null || { echo "未检测到 docker compose 插件，请先执行「安装 Docker」模板（含 compose-plugin）"; exit 1; }

# ==== 参数 ====
PORT="{{port}}"
HOME_DIR="{{home_dir}}"
USERS_RAW="{{users}}"
IMAGE="{{image}}"
CONTAINER=sftp

case "${PORT}" in ''|*[!0-9]*) echo "端口必须是数字: ${PORT}"; exit 1;; esac
[ -n "${USERS_RAW}" ] || { echo "用户清单不能为空，格式：用户名:密码:uid（如 alice:Pass1234:1001），多个用英文逗号分隔"; exit 1; }

# ==== 生成 users.conf（atmoz/sftp 格式：用户:密码:uid:gid:目录） ====
# 目录给 upload：chroot 要求家目录归 root，用户实际可写的是家目录下的 upload 子目录，
# 容器启动时会自动 mkdir 并按 uid:gid 授权。
mkdir -p "${HOME_DIR}/home"
umask 077
USERS_TMP="${HOME_DIR}/users.conf.new"
: > "${USERS_TMP}"
SEEN_NAME=" "
SEEN_UID=" "
for ENTRY in $(printf '%s' "${USERS_RAW}" | tr -d '\r' | sed 's/，/,/g' | tr ',' '\n'); do
  [ -n "${ENTRY}" ] || continue
  if ! printf '%s' "${ENTRY}" | grep -q '^[^:]*:[^:]*:[^:]*$'; then
    echo "用户条目格式错误（应为 用户名:密码:uid，密码不能含冒号/空格/逗号）: ${ENTRY}"; exit 1
  fi
  NAME="$(printf '%s' "${ENTRY}" | cut -d: -f1)"
  PASS="$(printf '%s' "${ENTRY}" | cut -d: -f2)"
  UIDV="$(printf '%s' "${ENTRY}" | cut -d: -f3)"
  case "${NAME}" in [a-z_][a-zA-Z0-9_-]*) ;; *) echo "用户名非法（小写字母开头，仅字母/数字/_/-）: ${NAME}"; exit 1;; esac
  case "${PASS}" in ''|*[[:space:]]*) echo "密码不能为空或含空格: ${NAME}"; exit 1;; esac
  case "${UIDV}" in ''|*[!0-9]*) echo "uid 必须是纯数字: ${NAME}=${UIDV}"; exit 1;; esac
  [ "${UIDV}" -ge 1000 ] || { echo "uid 必须 >=1000（避免与系统用户冲突）: ${NAME}=${UIDV}"; exit 1; }
  case "${SEEN_NAME}" in *" ${NAME} "*) echo "用户名重复: ${NAME}"; exit 1;; esac
  SEEN_NAME="${SEEN_NAME}${NAME} "
  case "${SEEN_UID}" in *" ${UIDV} "*) echo "uid 重复: ${UIDV}"; exit 1;; esac
  SEEN_UID="${SEEN_UID}${UIDV} "
  printf '%s:%s:%s:%s:upload\n' "${NAME}" "${PASS}" "${UIDV}" "${UIDV}" >> "${USERS_TMP}"
done
[ -s "${USERS_TMP}" ] || { echo "用户清单解析结果为空"; exit 1; }

# ==== 变更检测：users.conf 有变化才重启容器，未变则不动 ====
NEED_RESTART=0
if [ -f "${HOME_DIR}/users.conf" ]; then
  if ! cmp -s "${USERS_TMP}" "${HOME_DIR}/users.conf"; then
    NEED_RESTART=1
    mv -f "${USERS_TMP}" "${HOME_DIR}/users.conf"
    echo "用户清单有变更，稍后重启容器使其生效"
  else
    rm -f "${USERS_TMP}"
    echo "用户清单未变化"
  fi
else
  NEED_RESTART=1
  mv -f "${USERS_TMP}" "${HOME_DIR}/users.conf"
fi
umask 022

# ==== 迁移旧版 docker run 容器（数据目录保留） ====
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  PROJ="$(docker inspect --format='{{index .Config.Labels "com.docker.compose.project"}}' "${CONTAINER}" 2>/dev/null || true)"
  if [ -z "${PROJ}" ]; then
    echo "检测到旧版 docker run 容器，迁移为 compose 管理（数据保留）"
    docker rm -f "${CONTAINER}" &>/dev/null || true
  fi
fi

# ==== 生成 compose 文件 ====
cat > "${HOME_DIR}/compose.yml" <<'YAMLEOF'
name: sftp
services:
  sftp:
    image: {{image}}
    container_name: sftp
    restart: always
    ports:
      - "{{port}}:22"
    volumes:
      - {{home_dir}}/users.conf:/etc/sftp/users.conf:ro
      - {{home_dir}}/home:/home
YAMLEOF

# ==== 拉取并启动（compose 幂等：配置未变不动，变了自动 recreate） ====
echo "拉取镜像 ${IMAGE}"
docker compose -f "${HOME_DIR}/compose.yml" pull
docker compose -f "${HOME_DIR}/compose.yml" up -d
if [ "${NEED_RESTART}" = "1" ]; then
  docker restart "${CONTAINER}" >/dev/null
fi

# ==== 等待就绪（端口连通即视为 sshd 起来） ====
READY=0
for _ in $(seq 1 15); do
  if (exec 3<>"/dev/tcp/127.0.0.1/${PORT}") 2>/dev/null; then READY=1; break; fi
  sleep 2
done
if [ "${READY}" != "1" ]; then
  echo "SFTP 启动超时（端口 ${PORT} 未就绪），最近日志:"
  docker logs "${CONTAINER}" --tail 30 2>&1 || true
  exit 1
fi

FIRST_USER="$(head -1 "${HOME_DIR}/users.conf" | cut -d: -f1)"
echo "SFTP 已就绪: {{__ip}}:${PORT}（用户数据 ${HOME_DIR}/home/<用户名>/upload，每个用户 chroot 只能看见自己的目录）"
echo "登录示例: sftp -P ${PORT} ${FIRST_USER}@{{__ip}}（WinSCP/其他客户端请选 SFTP 协议并指定端口 ${PORT}）"
echo "加用户: 编辑 ${HOME_DIR}/users.conf 追加一行「用户名:密码:uid:uid:upload」（uid 顺延，如 1003）后执行 docker restart ${CONTAINER}"
echo "改密/删用户: 重跑本模板（以模板变量为准覆盖 users.conf 并自动重启）；users.conf 为明文密码，权限 600 仅 root 可读"
echo "compose 文件: ${HOME_DIR}/compose.yml（数据目录 ${HOME_DIR}/home 已持久化，重建容器不丢数据）"
