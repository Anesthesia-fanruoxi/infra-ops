#!/bin/bash
set -e

# 生产级内核/网络参数调优：写入 /etc/sysctl.d/99-tuning.conf（重启仍生效），逐条容错应用。
cat > /etc/sysctl.d/99-tuning.conf <<'EOF'
# ==== 拥塞控制：BBR（高带宽、低延迟，需内核 >= 4.9） ====
# 排队策略 fq：与 BBR 搭配，平滑突发流量、降低排队延迟与丢包
net.core.default_qdisc=fq
# 启用 BBR 拥塞控制：相比 CUBIC，高带宽下更能榨干链路且延迟更低
net.ipv4.tcp_congestion_control=bbr
# ==== TCP 连接生命周期 ====
# tcp_syncookies：开启 SYN Cookie，抵御 SYN Flood 打满半连接池
net.ipv4.tcp_syncookies=1
# tcp_tw_reuse：允许安全复用 TIME_WAIT 端口，提升短连接并发吞吐
net.ipv4.tcp_tw_reuse=1
# tcp_fin_timeout：FIN 二段等待秒数，缩短连接释放、减少孤儿套接字占用
net.ipv4.tcp_fin_timeout=15
# tcp_max_tw_buckets：TIME_WAIT 总量上限，超过由内核回收，防止句柄堆积
net.ipv4.tcp_max_tw_buckets=180000
# tcp_max_syn_backlog：SYN 半连接队列长度，抵御突发握手风暴
net.ipv4.tcp_max_syn_backlog=8192
# tcp_keepalive_*：闲置 600s 后开始探活、间隔 30s、3 次失败判定断开，配合 LB/NAT 清理死链
net.ipv4.tcp_keepalive_time=600
net.ipv4.tcp_keepalive_intvl=30
net.ipv4.tcp_keepalive_probes=3
# ==== 连接队列与内核收发缓冲 ====
# somaxconn：accept 完整连接队列上限；后端服务(如 nginx listen)的 backlog 建议同步调大
net.core.somaxconn=8192
# netdev_max_backlog：网卡收包队列长度，一次性突发下抵御软中断积压丢包
net.core.netdev_max_backlog=262144
# rmem/wmem_max + tcp_rmem/tcp_wmem：放大收发缓冲，提升高延迟/大包链路吞吐
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.tcp_rmem=4096 87380 16777216
net.ipv4.tcp_wmem=4096 65536 16777216
# ip_local_port_range：扩大本地出连可用端口范围，避免高并发出连时端口耗尽
net.ipv4.ip_local_port_range=1024 65535
# ==== TCP 特性 ====
# tcp_sack：选择性确认，高丢包网络下收敛重传、提升效率
net.ipv4.tcp_sack=1
# tcp_window_scaling：TCP 窗口扩展，跨高带宽-延迟链路（长肥管道）显著提速
net.ipv4.tcp_window_scaling=1
# tcp_fastopen：服务端+客户端均启用 TFO，可省一次握手 RTT
net.ipv4.tcp_fastopen=3
# ==== 文件句柄 / inotify / 内存 ====
# fs.file-max：系统最大文件句柄数，高并发连接或大量打开文件时需放大
fs.file-max=2097152
# inotify_*：文件监听上限，node 运行、日志收集等大量 watcher 时不报 ENOSPC
fs.inotify.max_user_watches=524288
fs.inotify.max_user_instances=8192
# vm.swappiness：倾向使用内存缓存而非 swap，减少交换抖动对延迟的影响
vm.swappiness=10
EOF

# 逐条应用：当前内核不存在的键仅跳过（|| true），保证单键异常不中断整个脚本
while IFS= read -r line; do
  case "$line" in
    ''|\#*) continue ;;
  esac
  sysctl -w "${line}" >/dev/null 2>&1 || true
done < /etc/sysctl.d/99-tuning.conf

COUNT=$(grep -vcE '^[[:space:]]*(#|$)' /etc/sysctl.d/99-tuning.conf)
echo "BBR 状态: $(sysctl -n net.ipv4.tcp_congestion_control)"
echo "已应用 ${COUNT} 项内核参数（写入 /etc/sysctl.d/99-tuning.conf，重启后仍生效）"