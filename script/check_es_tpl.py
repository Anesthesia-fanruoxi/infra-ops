# 校验 ES 套件：冷热温（cwh）一机多容器各阶段渲染 + 集群旧模式回归
# （SSL 已随 2026-09 重设计整体移除；角色矩阵每格 = 一个独立容器，端口按角色固定偏移）
# 2026-09-15：JVM 堆改为「规格档位」内部预设（sizing.go）——本脚本不再喂 heap_xms/heap_xmx/jvm_opts，
#             改为喂 __es_heap_<角色> / __es_heap，并断言自由文本 JVM 参数不会回来。
# 模拟 LoadPhase 变量注入与 __ 运行期变量，解析 compose heredoc，断言无残留占位符。
import re
import sys
from pathlib import Path

ROOT = Path(r"D:\project\go\infra-ops\store\stacks\elasticsearch")

# 标准使用档的注入值，与 sizing.go esSizingProfiles[standard] 对应
BASE = {
    "cluster_name": "es-cluster", "image": "elasticsearch:9.5.3",
    "port": "9200", "transport_port": "9300", "home_dir": "/data/elasticsearch",
    "sizing": "standard",
    # 运行期注入（port/transport_port 仅供集群模式；冷热温端口按角色偏移；堆按档位×角色）
    "__ip": "10.0.0.1", "__node_name": "n1",
    "__seed_hosts": '"10.0.0.1:9300"', "__cluster_size": "8",
    "__es_sizing": "standard", "__es_sizing_label": "标准使用",
    # master/协调合并为一行（取原纯协调值），两角色堆一致
    "__es_heap_master": "8g", "__es_heap_coordinator": "8g",
    "__es_heap_data_hot": "16g", "__es_heap_data_warm": "8g", "__es_heap_data_cold": "4g",
    "__es_heap": "16g",
}

# 角色固定偏移端口在下方断言区逐一校验（与 vars.go cwhRolePort / 脚本 http_port 同表）

# 阶段 -> (本机角色清单 __es_nodes, __es_bootstrap)
RUN_ROLES = {
    "reset":   ("master,coordinator", "false"),
    "masters": ("master,coordinator", "true"),
    "coords":  ("master,coordinator", "false"),
    "data":    ("data_hot,data_warm,data_cold", "false"),
}


def render(text, params):
    for k, v in params.items():
        text = text.replace("{{" + k + "}}", v)
    return text


def check_placeholders(text, where):
    # 排除运行期 __ 变量与 docker compose 的 Go 模板（{{.Names}} / {{index ...}}）
    left = re.findall(r"\{\{(?!__|\.|index\s)[^{}]+\}\}", text)
    assert not left, f"{where} 残留占位符: {left}"


ok = True
cwh = (ROOT / "scripts" / "cold-warm-hot" / "node.sh").read_text(encoding="utf-8")
cluster = (ROOT / "scripts" / "node.sh").read_text(encoding="utf-8")

# 0) 自由文本 JVM 参数不得回归：堆只允许来自「规格档位」注入
#    （ES_JAVA_OPTS 是 compose 环境变量名，必须保留；故用前导边界排除 ES_ 前缀）
JVM_FREE_PAT = re.compile(
    r"\{\{(?:heap_xms|heap_xmx|jvm_opts|java_opts)\}\}"
    r"|(?<![A-Za-z0-9_])(?:heap_xms|heap_xmx|jvm_opts|java_opts|HEAP_XMS|HEAP_XMX|JVM_OPTS|JAVA_OPTS)"
)
for name, body in (("cold-warm-hot/node.sh", cwh), ("node.sh", cluster)):
    bad = sorted(set(m.group(0) for m in JVM_FREE_PAT.finditer(body)))
    assert not bad, f"{name} 出现自由文本 JVM 变量: {bad}"
assert "- ES_JAVA_OPTS=" in cwh and "- ES_JAVA_OPTS=" in cluster, "compose 的 ES_JAVA_OPTS 注入点应保留"
print("  [OK] 两个脚本均无自由文本 JVM 变量（堆只来自规格档位注入）")

# 1) 冷热温一机多容器：各阶段变量渲染干净 + 端口偏移/角色认领的结构断言
for run, (nodes, is_boot) in RUN_ROLES.items():
    p = dict(BASE, __es_nodes=nodes, __es_bootstrap=is_boot,
             __es_bootstrap_name="n1-master", __run=run)
    check_placeholders(render(cwh, p), f"cwh run={run}")
print("  [OK] 冷热温各阶段渲染无残留占位符")

# 1.1) 档位注入到位：逐角色堆被脚本消费，且 compose 用的是该角色的堆变量
r = render(cwh, dict(BASE, __es_nodes="master,coordinator", __run="masters"))
for role_heap in ('HEAP_DATA_HOT="16g"', 'HEAP_DATA_WARM="8g"', 'HEAP_DATA_COLD="4g"',
                  'HEAP_MASTER="8g"', 'HEAP_COORDINATOR="8g"'):
    assert role_heap in r, f"档位注入未到位: {role_heap}"
for role in ("master", "coordinator", "data_hot", "data_warm", "data_cold"):
    assert re.search(r"\b" + role + r"\)\s*echo \"\$\{HEAP_" + role.upper() + r":-1g\}\"", r), \
        f"heap_of 缺角色分支: {role}"
assert 'HEAP="$(heap_of "${ROLE}")"' in r, "write_yml 应按角色取堆"
assert "- ES_JAVA_OPTS=-Xms${HEAP} -Xmx${HEAP} -XX:+UseG1GC" in r, "compose 的 ES_JAVA_OPTS 应由该角色堆拼装"
assert "check_host_capacity" in r and "MemTotal" in r, "应做「本机容器堆合计 vs 宿主内存」软校验"
print("  [OK] 规格档位逐角色堆注入、脚本取用与宿主容量软校验齐备")

for off in ("9200", "9210", "9220", "9230", "9240"):
    assert re.search(r"echo " + off + ";;", cwh), f"http_port 缺偏移 {off}"
for off in ("9300", "9310", "9320", "9330", "9340"):
    assert re.search(r"echo " + off + ";;", cwh), f"transport_port 缺偏移 {off}"
assert 'ES_NODES="{{__es_nodes}}"' in cwh, "node.sh 应消费 __es_nodes 本机容器清单"
assert re.search(r"masters\)\s*\[\s*\"\$1\" = \"master\"\s*\]", cwh), "masters 阶段应只认领 master"
assert re.search(r"coords\)\s*\[\s*\"\$1\" = \"coordinator\"\s*\]", cwh), "coords 阶段应只认领 coordinator"
assert re.search(r"data\)\s*case \"\$1\" in data_hot\|data_warm\|data_cold", cwh), "data 阶段应认领数据三角色"
assert "ALL_ROLES=" in cwh, "reset 应遍历全角色清单清理"
print("  [OK] 端口偏移表与角色认领分支完整")

# 2) 冷热温 bootstrap.sh（verify）：入口端口按本机角色探测（协调优先），固定 http 接入
boot_script = (ROOT / "scripts" / "cold-warm-hot" / "bootstrap.sh").read_text(encoding="utf-8")
r = render(boot_script, dict(BASE, __es_nodes="coordinator,master"))
check_placeholders(r, "bootstrap")
assert 'BASE="http://${SELF_IP}:$(entry_port)"' in r, "bootstrap 应通过 entry_port 推导入口"
assert re.search(r"coordinator\)\s*echo 9210; return;;", r), "entry_port 应优先协调 9210"
assert re.search(r"master\)\s*echo 9200; return;;", r), "entry_port 协调缺省时回落 master 9200"
assert r.index("9210") < r.index("9200"), "entry_port 协调入口应排在 master 之前"
print("  [OK] bootstrap: entry_port 推导入口（协调 9210 优先），固定 http scheme")

# 3) 冷热温 scale_in.sh 渲染：注入容器粒度下线集合；不得依赖 __es_*（渲染链路不含）
scale_script = (ROOT / "scripts" / "cold-warm-hot" / "scale_in.sh").read_text(encoding="utf-8")
r = render(scale_script, dict(BASE, self_ip="10.0.0.1",
                              remove_nodes="n1-master,n3-data_hot", remove_masters="n1-master"))
check_placeholders(r, "scale_in")
assert "{{__es_nodes}}" not in r, "scale_in 渲染链路不含 __es_*，脚本不得依赖"
assert 'REMOVE_NODES="n1-master,n3-data_hot"' in r and 'REMOVE_MASTERS="n1-master"' in r
assert re.search(r"for P in 9210 9200", r), "scale_in entry_port 应按端口探测"
print("  [OK] scale_in.sh: 容器粒度下线集渲染正确，入口端口探测")

# 4) 集群旧模式：仅镜像升至 9.5.3，堆由档位注入（数据节点档）
cr = render(cluster, dict(BASE, __run="node", __is_bootstrap="skipped"))
check_placeholders(cr, "cluster mode")
assert re.search(r"IMAGE=[\"']?elasticsearch:9\.5\.3", cr), "集群模式默认镜像应为 9.5.3"
assert "- ES_JAVA_OPTS=-Xms16g -Xmx16g -XX:+UseG1GC" in cr, "集群模式 ES_JAVA_OPTS 应取注入的数据节点堆"
assert 'HEAP="{{__es_heap}}"' in cluster, "集群模式应消费 __es_heap 注入"
assert "规格档位" in cr, "集群模式应回显档位（便于日志定位）"
print("  [OK] 集群旧模式渲染通过（镜像 9.5.3，堆按档位注入）")

print("\nES 三场景渲染校验： 全部通过" if ok else "\n存在失败")
sys.exit(0 if ok else 1)
