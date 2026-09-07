#!/bin/bash
set -e

# ==== 参数 ====
JDK_VERSION="{{version}}"
INSTALL_DIR="{{install_dir}}"
JDK_PATH="${INSTALL_DIR}/jdk-${JDK_VERSION}"

# 幂等：同版本已装则跳过
if [ -x "${JDK_PATH}/bin/java" ]; then
  echo "OpenJDK ${JDK_VERSION} 已安装: ${JDK_PATH}，跳过"
  exit 0
fi

# ==== 架构检测 ====
ARCH=$(uname -m)
case "${ARCH}" in
  x86_64) JDK_ARCH="x64" ;;
  aarch64|arm64) JDK_ARCH="aarch64" ;;
  *) echo "不支持的架构: ${ARCH}（仅支持 x86_64/aarch64）"; exit 1 ;;
esac

# ==== 下载（华为云镜像源） ====
FILENAME="openjdk-${JDK_VERSION}_linux-${JDK_ARCH}_bin.tar.gz"
URL="https://repo.huaweicloud.com/openjdk/${JDK_VERSION}/${FILENAME}"
TMP_DIR=/tmp/jdk_install
mkdir -p "${TMP_DIR}"
cd "${TMP_DIR}"

echo "下载 OpenJDK ${JDK_VERSION} (${JDK_ARCH}): ${URL}"
rm -f "${FILENAME}"
if ! wget -q "${URL}" -O "${FILENAME}"; then
  echo "下载失败，请检查网络或版本号是否存在于华为云镜像源"; exit 1
fi

# ==== 解压安装 ====
rm -rf "${JDK_PATH}"
tar -xzf "${FILENAME}" -C "${INSTALL_DIR}"
if [ ! -x "${JDK_PATH}/bin/java" ]; then
  echo "解压失败或目录结构异常: ${JDK_PATH}"; exit 1
fi

# ==== 环境变量 ====
cat > /etc/profile.d/java.sh <<EOF
export JAVA_HOME=${JDK_PATH}
export PATH=\$JAVA_HOME/bin:\$PATH
EOF
chmod +x /etc/profile.d/java.sh
# 清理 /etc/profile 中旧版残留的 JAVA_HOME 配置
sed -i '/^export JAVA_HOME=/d;/^export PATH=\$JAVA_HOME/d;/^export CLASSPATH=\./d' /etc/profile 2>/dev/null || true

export JAVA_HOME="${JDK_PATH}"
export PATH="${JAVA_HOME}/bin:$PATH"

java -version
echo "OpenJDK ${JDK_VERSION} 安装完成: ${JDK_PATH}"
echo "环境变量文件: /etc/profile.d/java.sh（新会话自动生效；当前会话可 source /etc/profile.d/java.sh）"
rm -rf "${TMP_DIR}"
