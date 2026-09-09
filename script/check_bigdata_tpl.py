# 校验 bigdata 组合套件：模拟 LoadPhase 资源注入 + 变量渲染，解析所有 heredoc 产物
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

import yaml

ROOT = Path(r"D:\project\go\infra-ops\store\stacks\bigdata")

PARAMS = {
    "__ip": "10.0.0.1", "__master_ip": "10.0.0.1", "__role": "master", "__nodes": "10.0.0.1",
    "__node_ips": "10.0.0.1,10.0.0.2,10.0.0.3", "__seq": "1",
    "components": "hdfs,spark,flink,hive", "image": "apache/hadoop:3.3.6",
    "nn_rpc_port": "9000", "replication": "2", "image_spark": "apache/spark:3.5.1",
    "master_port": "7077", "webui_port": "8080", "worker_cores": "4", "worker_mem": "8g",
    "image_flink": "flink:1.19.1", "jm_rpc_port": "6123", "tm_slots": "4",
    "image_hive": "apache/hive:4.0.0", "home_dir": "/data/bigdata",
    "image_zookeeper": "zookeeper:3.9", "nm_mem": "8192", "nm_vcores": "4",
    "image_hbase": "apache/hbase:2.5.10",
    "image_trino": "trinodb/trino:435", "trino_http_port": "8080", "trino_mem": "4G",
}

ALL_COMPS = ["hdfs", "zookeeper", "yarn", "spark", "flink", "hive", "hbase", "trino"]


def render(text, params):
    for k, v in params.items():
        text = text.replace("{{" + k + "}}", v)
    return text


def heredocs(script):
    # 带引号（不插值）与不带引号（bash 插值）heredoc 都解析
    out = [(m.group(1), m.group(2)) for m in re.finditer(r"<<'([A-Z]+)'\n(.*?)\n\1\n", script, re.S)]
    out += [(m.group(1), m.group(2)) for m in re.finditer(r"<<([A-Z]+)\n(.*?)\n\1\n", script, re.S)]
    return out


ok = True

hive_site = (ROOT / "configs" / "hive" / "hive-site.xml").read_text(encoding="utf-8")
core_site = (ROOT / "configs" / "hdfs" / "core-site.xml").read_text(encoding="utf-8")
hdfs_site = (ROOT / "configs" / "hdfs" / "hdfs-site.xml").read_text(encoding="utf-8")
yarn_site = (ROOT / "configs" / "yarn" / "yarn-site.xml").read_text(encoding="utf-8")
hbase_site = (ROOT / "configs" / "hbase" / "hbase-site.xml").read_text(encoding="utf-8")
node = (ROOT / "scripts" / "node.sh").read_text(encoding="utf-8")
node = (node.replace("@@CORE_SITE@@", core_site).replace("@@HDFS_SITE@@", hdfs_site)
        .replace("@@HIVE_SITE@@", hive_site).replace("@@YARN_SITE@@", yarn_site)
        .replace("@@HBASE_SITE@@", hbase_site))

# 1) 组件组合 × master/worker：全 heredoc 必须可解析
COMBO = (
    "hdfs",
    "hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino",   # 全家桶
    "hdfs,zookeeper,hbase",                               # HBase 依赖链
    "hdfs,hive,trino",                                    # Trino 依赖链
)
for comps in COMBO:
    for role in ("master", "worker"):
        p = dict(PARAMS, components=comps, __role=role)
        rendered = render(node, p)
        leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", rendered)
        if leftover:
            print(f"  [FAIL] components={comps} role={role} 残留变量: {leftover}")
            ok = False
            continue
        for tag, body in heredocs(rendered):
            if tag in ("XMLEOF",):
                ET.fromstring(body)
                continue
            if tag in ("PROPSEOF", "JVMEOF", "CFGEOF", "CATEOF"):
                continue  # properties/jvm/config 文本产物
            y = yaml.safe_load(body)
            if "services" not in (y or {}):
                print(f"  [FAIL] components={comps} role={role} heredoc {tag} 缺 services")
                ok = False
        print(f"  [OK] components={comps} role={role}: heredoc 全部可解析")

# 2) 无 hdfs 组合必须被脚本拒绝
p = dict(PARAMS, components="spark,hive")
r = render(node, p)
assert "HDFS 为必选组件" in r, "缺少 hdfs 必选守卫"
print("  [OK] 无 hdfs 组合被脚本拒绝")

# 3) bootstrap.sh
boot = (ROOT / "scripts" / "bootstrap.sh").read_text(encoding="utf-8")
for comps in ("hdfs", "hdfs,hive,trino", "hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino"):
    r = render(boot, dict(PARAMS, components=comps))
    leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", r)
    assert not leftover, f"bootstrap 残留变量: {leftover}"
    print(f"  [OK] bootstrap components={comps}")

# 4) XML 产物
for name, raw in (("core-site", core_site), ("hdfs-site", hdfs_site),
                  ("hive-site", hive_site), ("yarn-site", yarn_site), ("hbase-site", hbase_site)):
    root = ET.fromstring(render(raw, PARAMS))
    n = len(root.findall("property"))
    print(f"  [OK] {name}.xml: {n} 项")

# 5) 全组件脚本守卫：node.sh 必须覆盖全部组件分支
for c in ALL_COMPS:
    assert f"has {c};" in node, f"node.sh 缺少组件分支: {c}"
print(f"  [OK] node.sh 覆盖全部 {len(ALL_COMPS)} 个组件分支")

print("ALL BIGDATA RENDER CHECKS PASSED" if ok else "CHECKS FAILED")
sys.exit(0 if ok else 1)
