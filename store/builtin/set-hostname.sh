#!/bin/bash
set -e
NEW_NAME="{{new_name}}"
if [ -z "${NEW_NAME}" ]; then echo "新主机名不能为空"; exit 1; fi
if ! echo "${NEW_NAME}" | grep -qE '^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$'; then
  echo "非法主机名: ${NEW_NAME}（仅允许字母、数字、- 和 .）"; exit 1
fi
OLD=$(hostname)
hostnamectl set-hostname "${NEW_NAME}"
# 更新 /etc/hosts 中 127.0.1.1 的旧主机名映射（如存在）
if grep -qE '^127\.0\.1\.1[[:space:]]' /etc/hosts 2>/dev/null; then
  sed -i "s/^\(127\.0\.1\.1[[:space:]]\+\).*/\1${NEW_NAME}/" /etc/hosts
fi
echo "系统主机名已由 ${OLD} 修改为: ${NEW_NAME}"
# 自报新名字给平台，自动同步台账
echo "infra-ops:set-name=${NEW_NAME}"