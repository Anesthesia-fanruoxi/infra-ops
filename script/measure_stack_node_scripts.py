#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""measure_stack_node_scripts.py —— 套件 node.sh 的「离线执行」测量台。

背景：套件的 node.sh 有两类必须上真机才会暴露的坑（本轮 D1/D2 就是这么漏掉的）：
  1. 写出的 compose 文件名不在引擎约定名内（compose.yml / docker-compose.yml / */compose.yml）
     ⇒ 探活恒假红、缩容/卸载不停容器；
  2. 首节点条件服务（MySQL / NameServer）被拆进独立 compose 文件 ⇒ 同上。

做法：取「已注入 @@ASSET@@ 的渲染产物」（render 基线）补齐 {{模板变量}} 与内置变量，
      注入 stub 函数后真跑一遍 bash，再校验：
        - 脚本退出码 0；
        - 生成的 ${HOME_DIR}/compose.yml 能被 YAML 解析；
        - services / container_name 与预期一致（首节点条件服务是否按开关出现）；
        - 旧版 compose 文件已被 down 且删除（迁移块生效，避免 container_name 冲突）；
        - 无残留未替换的模板变量。

实现要点（Windows/MSYS 环境踩过的坑）：
  * stub 必须用 **BASH_ENV 注入 shell 函数**，不能靠 PATH 上的同名可执行文件：
    MSYS bash 会把 /usr/bin 排到继承来的 PATH **之前**，`sleep` 永远命中 /usr/bin/sleep
    （`docker` 因为 /usr/bin 下不存在才碰巧能被覆盖），导致脚本里的 `sleep 3`
    轮询循环真等几十秒 ⇒ 测量台假死。函数定义的优先级高于 PATH 查找，才拦得住。
  * 测试地址一律用 127.0.0.1：脚本就绪等待里既有 `docker exec ... /dev/tcp` 也有裸
    `/dev/tcp IP:PORT`，用不可达网段会让 connect 长时间阻塞。
  * 启动前强制校验 stub 已生效，避免误操作宿主机 Docker。

用法：python script/measure_stack_node_scripts.py [--suite rocketmq] [--keep]
"""
import argparse
import io
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import threading

try:
    import yaml
except ImportError:
    print("需要 pyyaml：pip install pyyaml")
    sys.exit(2)

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
RENDER_DIR = os.path.join(ROOT, "workflow", "tasks", "09-stack-dir-split", "baseline", "render")
BASH = r"C:\Program Files\Git\bin\bash.exe"
if not os.path.exists(BASH):
    BASH = "bash"

# 一份够用的模板变量取值（不含 namesrv_addr：那是 broker.conf 里由脚本自己 sed 的占位符）
BASE_VARS = {
    "image": "example.com/ns/img:1.0",
    "mysql_image": "mysql:8.0",
    "cluster_name": "demo",
    "broker_name": "demo-broker",
    "ns_port": "9876",
    "broker_port": "10911",
    "port": "8848",
    "nacos_token": "A" * 40,
    "db_username": "demo_user",
    "db_password": "demo_pass_123",
    "akka_port": "10086",
    "server_port": "7700",
    "amqp_port": "5672",
    "mgmt_port": "15672",
    "erlang_cookie": "COOKIECOOKIE",
    "admin_user": "admin",
    "admin_pass": "admin123",
}

SUITES = {
    "rocketmq": {
        "render": "rocketmq__cluster__node.txt",
        "legacy": ["namesrv-compose.yml", "broker-compose.yml"],
        "cases": [
            {
                "name": "首节点(is_bootstrap=true)",
                "vars": {"__ip": "127.0.0.1", "__seq": "1", "__role": "master", "__master_ip": "127.0.0.1",
                         "__is_bootstrap": "true", "__broker_id": "0"},
                "services": ["namesrv", "broker"],
                "containers": ["rmq-namesrv-demo", "rmq-broker-demo-broker-0"],
            },
            {
                "name": "成员节点(is_bootstrap=false)",
                "vars": {"__ip": "127.0.0.1", "__seq": "2", "__role": "worker", "__master_ip": "127.0.0.1",
                         "__is_bootstrap": "false", "__broker_id": "2"},
                "services": ["broker"],
                "containers": ["rmq-broker-demo-broker-2"],
            },
        ],
        "extra_checks": [("broker.conf namesrvAddr", "conf/broker.conf", "namesrvAddr=127.0.0.1:9876")],
    },
    "nacos": {
        "render": "nacos__cluster__node.txt",
        "legacy": ["mysql-compose.yml"],
        "cases": [
            {
                "name": "首节点+自建 MySQL(db_host 为空)",
                "vars": {"__ip": "127.0.0.1", "__seq": "1", "__role": "master", "__is_bootstrap": "true",
                         "__node_ips": "127.0.0.1,127.0.0.2,127.0.0.3", "__master_ip": "127.0.0.1", "db_host": ""},
                "services": ["mysql", "nacos"],
                "containers": ["nacos-mysql", "nacos-1"],
            },
            {
                "name": "成员节点(指定外部 MySQL)",
                "vars": {"__ip": "127.0.0.1", "__seq": "2", "__role": "worker", "__is_bootstrap": "false",
                         "__node_ips": "127.0.0.1,127.0.0.2,127.0.0.3", "__master_ip": "127.0.0.1",
                         "db_host": "127.0.0.1:3306"},
                "services": ["nacos"],
                "containers": ["nacos-2"],
            },
        ],
    },
    "powerjob": {
        "render": "powerjob__cluster__node.txt",
        "legacy": ["compose.yml.1", "mysql-compose.yml"],
        "cases": [
            {
                "name": "首节点+自建 MySQL(db_host 为空)",
                "vars": {"__ip": "127.0.0.1", "__seq": "1", "__role": "master", "__is_bootstrap": "true",
                         "__node_ips": "127.0.0.1,127.0.0.2", "__master_ip": "127.0.0.1", "db_host": ""},
                "services": ["mysql", "server"],
                "containers": ["powerjob-mysql", "powerjob-server-1"],
            },
            {
                "name": "成员节点(指定外部 MySQL)",
                "vars": {"__ip": "127.0.0.1", "__seq": "2", "__role": "worker", "__is_bootstrap": "false",
                         "__node_ips": "127.0.0.1,127.0.0.2", "__master_ip": "127.0.0.1", "db_host": "127.0.0.1:3306"},
                "services": ["server"],
                "containers": ["powerjob-server-2"],
            },
        ],
    },
}

# BASH_ENV 注入的 stub：函数优先级高于 PATH，才能真正拦下 sleep / docker。
STUB_SHIM = r"""# 由 script/measure_stack_node_scripts.py 注入：离线执行套件 node.sh 用
docker() {
  printf 'docker %s\n' "$*" >> "${FAKE_LOG}"
  case "$1" in
    exec) printf '%s\n' "${FAKE_EXEC_OUT}" ;;
    logs) printf 'Nacos started successfully\nStarted Application in 12.3 seconds\n' ;;
  esac
  return 0
}
sleep() { return 0; }
curl() { return 0; }
chown() { return 0; }
"""


def write_stub_shim(workdir):
    p = os.path.join(workdir, "stub_env.sh")
    io.open(p, "w", encoding="utf-8", newline="\n").write(STUB_SHIM)
    return p


def child_env(shim, bindir, log):
    env = dict(os.environ)
    env["BASH_ENV"] = shim.replace("\\", "/")
    env["PATH"] = bindir.replace("\\", "/") + os.pathsep + env.get("PATH", "")
    env["FAKE_LOG"] = log.replace("\\", "/")
    env["FAKE_EXEC_OUT"] = "127.0.0.1:10911"
    return env


def assert_stub_active(shim, bindir):
    """确认 stub 真生效，避免误操作宿主机 Docker。"""
    env = child_env(shim, bindir, os.path.join(bindir, "probe.log"))
    r = subprocess.run([BASH, "-c", "docker ps; type sleep"], env=env, capture_output=True, timeout=60)
    out = r.stdout.decode("utf-8", "replace")
    if "sleep is a function" not in out:
        print("拒绝执行：stub 未生效（sleep 未被函数拦截），实际输出 %r" % out[:200])
        return False
    if not os.path.exists(env["FAKE_LOG"]):
        print("拒绝执行：docker stub 未被调用")
        return False
    return True


def start_tcp_listener(port):
    """让 wait_tcp_host（裸 /dev/tcp 探测）能连上；失败返回 None。"""
    try:
        srv = socket.socket()
        srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        srv.bind(("127.0.0.1", port))
        srv.listen(16)
    except OSError:
        return None

    def loop():
        while True:
            try:
                c, _ = srv.accept()
                c.close()
            except OSError:
                return

    threading.Thread(target=loop, daemon=True).start()
    return srv


def substitute(text, vars_map):
    missing = []

    def rep(m):
        k = m.group(1)
        if k in vars_map:
            return vars_map[k]
        missing.append(k)
        return m.group(0)

    return re.sub(r"\{\{([A-Za-z0-9_]+)\}\}", rep, text), missing


def run_case(suite, case, workdir, bindir, shim):
    home = os.path.join(workdir, "home_" + suite + "_" + re.sub(r"\W+", "", case["name"]))
    os.makedirs(home, exist_ok=True)
    log = os.path.join(home, "docker.log")
    io.open(log, "w", encoding="utf-8").close()

    # 预置旧版 compose 文件，验证迁移块
    for legacy in SUITES[suite]["legacy"]:
        io.open(os.path.join(home, legacy), "w", encoding="utf-8").write("name: legacy\nservices: {}\n")

    raw = io.open(os.path.join(RENDER_DIR, SUITES[suite]["render"]), encoding="utf-8").read()
    if "@@" in raw:
        return ["渲染产物仍有 @@ASSET@@ 残留，先跑 SNAPSHOT_WRITE=1"]
    vars_map = dict(BASE_VARS)
    vars_map["home_dir"] = home.replace("\\", "/")
    vars_map.update(case["vars"])
    text, missing = substitute(raw, vars_map)
    # namesrv_addr 由脚本自身 sed，属预期残留
    unexpected = [m for m in missing if m != "namesrv_addr"]
    errs = []
    if unexpected:
        errs.append("未替换的模板变量: " + ",".join(sorted(set(unexpected))))

    sp = os.path.join(home, "run.sh")
    io.open(sp, "w", encoding="utf-8", newline="\n").write(text)

    env = child_env(shim, bindir, log)
    try:
        r = subprocess.run([BASH, sp], cwd=home, env=env, capture_output=True, timeout=90)
    except subprocess.TimeoutExpired as e:
        part = (e.stdout or b"").decode("utf-8", "replace")
        return errs + ["脚本执行超时（90s）；尾部输出:\n%s" % part[-1500:]]
    out = r.stdout.decode("utf-8", "replace")
    err = r.stderr.decode("utf-8", "replace")
    if r.returncode != 0:
        errs.append("脚本退出码 %d；尾部输出:\n%s" % (r.returncode, (out + err)[-1200:]))

    compose = os.path.join(home, "compose.yml")
    if not os.path.exists(compose):
        errs.append("未生成 compose.yml")
        return errs

    try:
        doc = yaml.safe_load(io.open(compose, encoding="utf-8").read())
    except Exception as e:
        errs.append("compose.yml YAML 解析失败: %s" % e)
        return errs

    svcs = doc.get("services") or {}
    got_svcs = sorted(svcs.keys())
    if got_svcs != sorted(case["services"]):
        errs.append("services 不符：期望 %s，实际 %s" % (sorted(case["services"]), got_svcs))
    got_ctrs = sorted(v.get("container_name", "") for v in svcs.values())
    if got_ctrs != sorted(case["containers"]):
        errs.append("container_name 不符：期望 %s，实际 %s" % (sorted(case["containers"]), got_ctrs))
    for name, v in svcs.items():
        if v.get("network_mode") != "host":
            errs.append("服务 %s 不是 host 网络" % name)

    # 旧版 compose 必须已被 down 且删除
    dlog = io.open(log, encoding="utf-8").read()
    for legacy in SUITES[suite]["legacy"]:
        p = os.path.join(home, legacy)
        if os.path.exists(p):
            errs.append("旧版 compose 未删除: %s" % legacy)
        if ("-f %s down" % p.replace("\\", "/")) not in dlog:
            errs.append("旧版 compose 未执行 down: %s" % legacy)

    # 额外内容断言（如 broker.conf 的 namesrvAddr）
    for label, rel, needle in SUITES[suite].get("extra_checks", []):
        fp = os.path.join(home, rel.replace("/", os.sep))
        if not os.path.exists(fp):
            errs.append("%s 缺失: %s" % (label, rel))
            continue
        body = io.open(fp, encoding="utf-8", errors="replace").read()
        if needle not in body:
            errs.append("%s 未命中 %r" % (label, needle))

    return errs


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--suite", default="", help="只跑指定套件")
    ap.add_argument("--keep", action="store_true", help="保留临时目录")
    args = ap.parse_args()

    workdir = tempfile.mkdtemp(prefix="measure_nodestack_")
    bindir = os.path.join(workdir, "bin")
    os.makedirs(bindir, exist_ok=True)
    shim = write_stub_shim(workdir)
    if not assert_stub_active(shim, bindir):
        shutil.rmtree(workdir, ignore_errors=True)
        return 2

    listeners = []
    if start_tcp_listener(3306) is None:
        print("警告：3306 已被占用（自建 MySQL 用例的端口等待可能失败）")
    else:
        listeners.append(True)  # 监听线程随进程退出，无需显式关闭

    total = bad = 0
    for suite, spec in SUITES.items():
        if args.suite and suite != args.suite:
            continue
        print("=" * 64)
        print("套件 %s（渲染产物 %s）" % (suite, spec["render"]), flush=True)
        for case in spec["cases"]:
            total += 1
            errs = run_case(suite, case, workdir, bindir, shim)
            if errs:
                bad += 1
                print("  ✗ %s" % case["name"])
                for e in errs:
                    print("      - %s" % e)
            else:
                print("  ✓ %s（services=%s）" % (case["name"], ",".join(case["services"])))

    if args.keep:
        print("\n临时目录保留: %s" % workdir)
    else:
        shutil.rmtree(workdir, ignore_errors=True)
    print("\n用例 %d 个，失败 %d 个" % (total, bad))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
