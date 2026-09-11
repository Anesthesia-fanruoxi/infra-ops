# 校验 ES 套件：冷热温（cwh）各阶段 × SSL 开关渲染 + 集群旧模式回归
# 模拟 LoadPhase 变量注入与 __ 运行期变量，解析 compose heredoc，断言无残留占位符。
import re
import sys
from pathlib import Path

import yaml

ROOT = Path(r"D:\project\go\infra-ops\store\stacks\elasticsearch")

BASE = {
    "cluster_name": "es-cluster", "image": "elasticsearch:9.5.3",
    "port": "9200", "transport_port": "9300", "home_dir": "/data/elasticsearch",
    "ssl_enabled": "false", "coordinator_count": "2",
    "heap_xms": "1g", "heap_xmx": "1g", "jvm_opts": "-XX:+UseG1GC",
    # 运行期注入
    "__ip": "10.0.0.1", "__node_name": "n1",
    "__seed_hosts": '"10.0.0.1:9300","10.0.0.2:9300"', "__cluster_size": "8",
}

# 阶段 -> (host roles csv, __es_roles yml 值, is_master, is_bootstrap)
RUN_ROLES = {
    "reset": ("coordinator", "", "false", "false"),
    "masters": ("master", '"master"', "true", "true"),
    "coords": ("coordinator", "", "false", "false"),
    "data": ("data_hot,data_warm,data_cold", '"data_hot","data_warm","data_cold"', "false", "false"),
}


def render(text, params):
    for k, v in params.items():
        text = text.replace("{{" + k + "}}", v)
    return text


def heredocs(script):
    out = [(m.group(1), m.group(2)) for m in re.finditer(r"<<'([A-Z]+)'\n(.*?)\n\1\n", script, re.S)]
    out += [(m.group(1), m.group(2)) for m in re.finditer(r"<<([A-Z]+)\n(.*?)\n\1\n", script, re.S)]
    return out


def check_placeholders(text, where):
    # 排除运行期 __ 变量与 docker compose 的 Go 模板（{{.Names}} / {{index ...}}）
    left = re.findall(r"\{\{(?!__|\.|index\s)[^{}]+\}\}", text)
    assert not left, f"{where} 残留占位符: {left}"


def bash_resolve(body, cert=None):
    # 模拟 bash 在非引号 heredoc 中对 ${var} 的运行时求值：本脚本场景下其值为空或指定的证书挂载行。
    if cert is not None:
        body = body.replace("${certline}", cert)
    return re.sub(r"^\$\{[a-zA-Z_]+\}\s*$", "", body, flags=re.M)


ok = True
cwh = (ROOT / "scripts" / "cold-warm-hot" / "node.sh").read_text(encoding="utf-8")

# 1) 冷热温：每阶段 × 对应角色渲染，compose heredoc 必须可解析、无残留占位符
for run, (roles, es_roles, is_master, is_boot) in RUN_ROLES.items():
    p = dict(BASE, roles=roles, __es_roles=es_roles, __es_is_master=is_master,
             __es_bootstrap=is_boot, __es_bootstrap_name="n1", __run=run)
    rendered = render(cwh, p)
    check_placeholders(rendered, f"cwh run={run}")
    body_tags = [t for t, _ in heredocs(rendered)]
    assert "YAMLEOF" in body_tags, f"cwh run={run} 缺 YAMLEOF compose heredoc"
    for tag, body in heredocs(rendered):
        if tag == "YAMLEOF":
            y = yaml.safe_load(bash_resolve(body))
            assert "services" in (y or {}), f"cwh run={run} YAMLEOF 缺 services"
    print(f"  [OK] 冷热温 run={run} roles={roles}: compose heredoc 解析通过")

# 2) SSL 开关：ssl_enabled=true 时 yml 含 PEM 证书配置，compose 挂载 certs 目录
for ssl in ("true", "false"):
    p = dict(BASE, ssl_enabled=ssl, roles="master", __es_roles='"master"',
             __es_is_master="true", __es_bootstrap="true", __es_bootstrap_name="n1", __run="masters")
    r = render(cwh, p)
    if ssl == "true":
        assert "xpack.security.http.ssl.enabled: true" in r, "SSL 形态缺 http.ssl.enabled"
        assert "certs" in r and ":ro" in r, "SSL 形态 compose 缺 certs 挂载"
        assert not re.search(r"\.p12", r), "SSL 形态不应再引用 p12"
        assert "xpack.security.enabled: false" in r, "SSL 形态应显式 security=false（避免隐性认证）"
    else:
        assert "xpack.security.enabled: false" in r, "明文形态应显式 security=false"
    print(f"  [OK] ssl_enabled={ssl}: yml/compose 渲染符合预期")

# 3) 冷热温 bootstrap.sh（verify）渲染：SSL 开/关 curl scheme 正确
boot_script = (ROOT / "scripts" / "cold-warm-hot" / "bootstrap.sh").read_text(encoding="utf-8")
for ssl in ("true", "false"):
    r = render(boot_script, dict(BASE, ssl_enabled=ssl))
    check_placeholders(r, "bootstrap ssl=" + ssl)
    expect = "https" if ssl == "true" else "http"
    assert f'SCHEME="{expect}"' in r, f"bootstrap ssl={ssl} 期望 scheme={expect}"
    print(f"  [OK] bootstrap ssl={ssl}: scheme={expect}")

# 4) 冷热温 scale_in.sh 渲染：注入下线节点集合
scale_script = (ROOT / "scripts" / "cold-warm-hot" / "scale_in.sh").read_text(encoding="utf-8")
r = render(scale_script, dict(BASE, self_ip="10.0.0.1", remove_nodes="n3,n5", remove_masters="n5"))
check_placeholders(r, "scale_in")
assert 'REMOVE_NODES="n3,n5"' in r and 'REMOVE_MASTERS="n5"' in r
print("  [OK] scale_in.sh: 下线节点集渲染正确")

# 5) 集群旧模式：仅镜像升至 9.5.3，其余零行为回归（渲染通过）
cluster = (ROOT / "scripts" / "node.sh").read_text(encoding="utf-8")
cr = render(cluster, dict(BASE, __run="node", java_opts="-Xms1g -Xmx1g", __is_bootstrap="skipped"))
check_placeholders(cr, "cluster mode")
assert re.search(r"IMAGE=[\"']?elasticsearch:9\.5\.3", cr), "集群模式默认镜像应为 9.5.3"
print("  [OK] 集群旧模式渲染通过（默认镜像 9.5.3）")

print("\nES 三场景渲染校验： 全部通过" if ok else "\n存在失败")
sys.exit(0 if ok else 1)