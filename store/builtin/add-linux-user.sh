#!/bin/bash
set -e
if id "{{username}}" &>/dev/null; then
  echo "用户 {{username}} 已存在，跳过"
  exit 0
fi
useradd -m -s /bin/bash "{{username}}"
echo "用户 {{username}} 创建成功"
id "{{username}}"