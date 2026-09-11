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
    "ha": "false",
    "nn_rpc_port": "9000", "replication": "2", "image_spark": "apache/spark:3.5.1",
    "master_port": "7077", "webui_port": "8080", "worker_cores": "4", "worker_mem": "8g",
    "jn_rpc_port": "8485", "jn_http_port": "8484",
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
mapred_site = (ROOT / "configs" / "hdfs" / "mapred-site.xml").read_text(encoding="utf-8")
hbase_site = (ROOT / "configs" / "hbase" / "hbase-site.xml").read_text(encoding="utf-8")
node = (ROOT / "scripts" / "node.sh").read_text(encoding="utf-8")
node = (node.replace("@@CORE_SITE@@", core_site).replace("@@HDFS_SITE@@", hdfs_site)
        .replace("@@HIVE_SITE@@", hive_site).replace("@@YARN_SITE@@", yarn_site)
        .replace("@@MAPRED_SITE@@", mapred_site)
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

# 1a) ZooKeeper ZOO_SERVERS 必须带 ;2181（官方镜像 clientPort），否则 quorum 有、2181 永不监听
_zk_render = render(node, dict(PARAMS, components="hdfs,zookeeper", __role="worker"))
if "2888:3888;2181" not in _zk_render:
    print("  [FAIL] ZOO_SERVERS 缺少 ;2181 clientPort 后缀")
    ok = False
elif "ZOOKEEPER_CLIENT_PORT" in _zk_render:
    print("  [FAIL] 仍使用无效变量 ZOOKEEPER_CLIENT_PORT（应为 ZOO_SERVERS 内 ;2181）")
    ok = False
else:
    print("  [OK] ZooKeeper ZOO_SERVERS 含 ;2181 clientPort")

# 1b) 角色规划分离：主节点(10.0.0.1)本机是 master 角色，但 NN/RM/Trino 规划在别的节点 →
#     本机应落 DataNode/NM/Worker 分支，heredoc 仍全部可解析
for comps in ("hdfs", "hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino"):
    p = dict(PARAMS, components=comps, __role="master",
             __master_hdfs="10.0.0.2", __master_yarn="10.0.0.3", __master_trino="10.0.0.2",
             masters='{"hdfs":"10.0.0.2","yarn":"10.0.0.3","trino":"10.0.0.2"}')
    rendered = render(node, p)
    leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", rendered)
    assert not leftover, f"角色分离场景残留变量: {leftover}"
    for tag, body in heredocs(rendered):
        if tag == "XMLEOF":
            ET.fromstring(body)
        elif tag == "YAMLEOF":
            assert "services" in (yaml.safe_load(body) or {})
    assert "仅部署在 10.0.0.1" not in rendered or "10.0.0.1" in rendered
    print(f"  [OK] 角色分离 components={comps}: master 角色主机上的分支正常")

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

# 5) 全组件脚本守卫：node.sh 的 RUN 分发器必须覆盖全部组件分支（hdfs 拆为 boot/dn 两阶段）
for c in ALL_COMPS:
    assert f"has {c} && deploy_" in node, f"node.sh 缺少组件分支: {c}"
assert "has hdfs && deploy_hdfs_boot" in node and "has hdfs && deploy_hdfs_dn" in node, "HDFS 应拆为 boot/dn 两阶段"
print(f"  [OK] node.sh 覆盖全部 {len(ALL_COMPS)} 个组件分支（HDFS 拆为 boot/dn）")

# ================= §B8: HA 渲染校验 =================
ha_script = (ROOT / "scripts" / "ha.sh").read_text(encoding="utf-8")

def _xapi(n, v):
    return f"  <property>\n    <name>{n}</name>\n    <value>{v}</value>\n  </property>\n"

def blk_check(rendered):
    # 提取 core-site heredoc 中的 fs.defaultFS 值
    m = re.search(r"<name>fs.defaultFS</name>\s*<value>([^<]+)</value>", rendered)
    return m.group(1) if m else ""

def ha_xml_blocks(comps):
    """镜像 api/bigdataHAXMLBlocks 的 HA 条件块（供渲染校验；值仅需合法 XML）。"""
    zkips = "10.0.0.1,10.0.0.2,10.0.0.3"
    ns = "ns1"; JNS = "10.0.0.1:9000;10.0.0.2:9000;10.0.0.3:9000"
    core = (_xapi("ha.zookeeper.quorum", zkips) +
            _xapi("dfs.nameservices", ns) +
            _xapi("dfs.ha.namenodes." + ns, "nn1,nn2") +
            _xapi("dfs.client.failover.proxy.provider." + ns,
                  "org.apache.hadoop.hdfs.server.namenode.ha.ConfiguredFailoverProxyProvider"))
    hdfs = (_xapi("dfs.namenode.shared.edits.dir", "qjournal://" + JNS + "/" + ns) +
            _xapi("dfs.journalnode.edits.dir", "/hadoop/dfs/journal") +
            _xapi("dfs.namenode.rpc-address." + ns + ".nn1", "10.0.0.1:9000") +
            _xapi("dfs.namenode.rpc-address." + ns + ".nn2", "10.0.0.2:9000") +
            _xapi("dfs.namenode.http-address." + ns + ".nn1", "10.0.0.1:9870") +
            _xapi("dfs.ha.automatic-failover.enabled", "true"))
    yarn = (_xapi("yarn.resourcemanager.ha.enabled", "true") +
            _xapi("yarn.resourcemanager.hostname.rm1", "10.0.0.1") +
            _xapi("yarn.resourcemanager.hostname.rm2", "10.0.0.2") +
            _xapi("yarn.resourcemanager.zk-address", zkips) +
            _xapi("yarn.resourcemanager.recovery.enabled", "true"))
    jdo = (_xapi("javax.jdo.option.ConnectionURL",
                 "jdbc:mysql://10.0.0.1:3306/metastore?useSSL=false") +
           _xapi("javax.jdo.option.ConnectionUserName", "root"))
    zkprop = _xapi("hive.server2.support.dynamic.service.discovery", "true")
    ms = {k: "" for k in
          ("__ha_core_props", "__ha_hdfs_props", "__ha_yarn_props",
           "__ha_hive_jdo", "__ha_hive_ms", "__ha_hive_zk")}
    ha_on = lambda c: c in comps
    if ha_on("yarn"):
        ms["__ha_yarn_props"] = yarn
    if ha_on("hive"):
        ms["__ha_hive_ms"] = "thrift://10.0.0.1:9083,thrift://10.0.0.2:9083"
        ms["__ha_hive_jdo"] = jdo
        ms["__ha_hive_zk"] = zkprop
    return dict(ms, __ha_core_props=core, __ha_hdfs_props=hdfs)

# HA 主参数集：self=10.0.0.1(nn1/master)；secondary=.2；worker=.3
HAP = dict(PARAMS, ha="true", hdfs_nameservice="ns1", hive_db_image="mysql:8.4",
           hive_db_password="HiveDb@123",
           __role="master", __ip="10.0.0.1", __node_ips="10.0.0.1,10.0.0.2,10.0.0.3", __seq="1",
           __master_hdfs="10.0.0.1", __master_yarn="10.0.0.1", __master_spark="10.0.0.1",
           __master_flink="10.0.0.1", __master_hive="10.0.0.1", __master_hbase="10.0.0.1",
           __master_trino="10.0.0.1",
           __zk_ips="10.0.0.1,10.0.0.2,10.0.0.3", __hdfs_entry="hdfs://ns1",
           __nn1_ip="10.0.0.1", __nn2_ip="10.0.0.2", __jn_ips="10.0.0.1,10.0.0.2,10.0.0.3",
           __rm1_ip="10.0.0.1", __rm2_ip="10.0.0.2",
           __spark_m2_ip="10.0.0.2", __flink_jm2_ip="10.0.0.2", __hmaster2_ip="10.0.0.2",
           __ms_ips="10.0.0.1,10.0.0.2", __hs2_ips="10.0.0.1,10.0.0.2",
           __hive_db_ip="10.0.0.1", __hive_ms_uris="thrift://10.0.0.1:9083,thrift://10.0.0.2:9083")

def ha_node(comps, role_ip, role):
    """渲染注入 ha.sh + HA 配置块的 node.sh。"""
    blk = ha_xml_blocks(comps)
    p = dict(HAP, components=comps, __ip=role_ip, __role=role)
    if role_ip == "10.0.0.2":
        p["__role"] = "worker"
    p.update(blk)
    nd = node.replace("@@HA_SH@@", ha_script)
    # ha.sh 的 compose heredoc 里有占位整行的 shell 变量（如 ${NM_VOL_}），
    # 它不是模板变量、只在运行时展开，这里按空行处理以便 YAML 解析。
    nd = re.sub(r"^\$\{[A-Za-z_][A-Za-z0-9_]*\}$", "", nd, flags=re.M)
    return render(nd, p), p

# HA 场景 1：全家桶，本机 nn1(master)：core-site fs.defaultFS=nameservice、yarn/hive 双实例块注入
for comps in ("hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino",
              "hdfs,zookeeper,yarn,hive,metastore_db"):
    rendered, _p = ha_node(comps, "10.0.0.1", "master")
    leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", rendered)
    assert not leftover, f"HA master 残留变量: {leftover}"
    # 引擎注入的所有 __ 占位符在本脚本场景赋值，不应残留
    assert "deploy_hdfs_ha" in rendered, "ha.sh 未注入"
    assert blk_check(rendered) == "hdfs://ns1", f"core-site 未走 nameservice: {blk_check(rendered)!r}"
    for tag, body in heredocs(rendered):
        if tag in ("XMLEOF", "HDFS_SITE", "HIVE_SITE", "YARN_SITE", "HBASE_SITE"):
            ET.fromstring(body)
        elif tag in ("HAEOF", "DBEOF", "YAMLEOF"):
            # HAEOF 可能是 Hive compose 的拼接片段（不含顶层 services），仅需为合法 YAML dict
            y = yaml.safe_load(body)
            assert y and isinstance(y, dict), f"HA heredoc {tag} 非法 YAML"
    if "yarn" in comps:
        assert "yarn.resourcemanager.ha.enabled" in rendered
        assert "deploy_yarn_ha" in rendered
    if "hive" in comps:
        assert "deploy_hive_ha" in rendered
        assert "thrift://10.0.0.1:9083,thrift://10.0.0.2:9083" in rendered
    print(f"  [OK] HA master components={comps}: heredoc 全解析 + HA 配置注入")

# HA 场景 2：secondary 主机(10.0.0.2)：HDFS nn2、YARN rm2、Spark m2、Flink jm2、Hive ms2+hs2
for comps in ("hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino",):
    rendered, _p = ha_node(comps, "10.0.0.2", "worker")
    leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", rendered)
    assert not leftover, f"HA secondary 残留变量: {leftover}"
    assert "bootstrapStandby" in rendered, "nn2 应走 bootstrapStandby"
    assert "resourcemanager2" in rendered, "Host2 应部署 RM2(standby)"
    assert "spark-master2" in rendered
    assert "flink-jobmanager2" in rendered
    assert "hive-metastore2" in rendered
    for tag, body in heredocs(rendered):
        if tag in ("XMLEOF", "HDFS_SITE", "HIVE_SITE", "YARN_SITE", "HBASE_SITE"):
            ET.fromstring(body)
        elif tag in ("HAEOF", "DBEOF", "YAMLEOF"):
            y = yaml.safe_load(body)
            assert y and isinstance(y, dict), f"HA secondary heredoc {tag} 非法 YAML"
    print(f"  [OK] HA secondary(10.0.0.2): nn2/rm2/m2/jm2/hs2 全部走 standby 分支")

# HA 场景 3：worker(10.0.0.3)：纯 DataNode/NM/Worker/TM/RegionServer，Hive 非承载主机
rendered, _p = ha_node("hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino", "10.0.0.3", "worker")
leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", rendered)
assert not leftover, f"HA worker 残留变量: {leftover}"
assert "hadoop-datanode" in rendered or "datanode" in rendered
assert "hive-metastore2" not in rendered or "CONTAINER_ARR" in rendered
for tag, body in heredocs(rendered):
    if tag in ("XMLEOF", "HDFS_SITE", "HIVE_SITE", "YARN_SITE", "HBASE_SITE"):
        ET.fromstring(body)
    elif tag in ("HAEOF", "DBEOF", "YAMLEOF"):
        y = yaml.safe_load(body)
        assert y and isinstance(y, dict), f"HA worker heredoc {tag} 非法 YAML"
print("  [OK] HA worker(10.0.0.3): 纯工作节点分支")

# HA 场景 4：bootstrap.sh 带 HA 参数（含套件全开/仅 HDFS）
for comps in ("hdfs", "hdfs,zookeeper,yarn,spark,flink,hive,hbase,trino"):
    p = dict(HAP, components=comps)
    rb = render(boot, p)
    leftover = re.findall(r"\{\{(?!__)[a-zA-Z_]+\}\}", rb)
    assert not leftover, f"HA bootstrap 残留变量: {leftover}"
    assert 'HA="true"' in rb, "bootstrap 未注入 HA 开关"
    print(f"  [OK] HA bootstrap components={comps}")

print("  [OK] HA 渲染校验完成（HA 全开/单组件/角色分离三场景）")

print("ALL BIGDATA RENDER CHECKS PASSED" if ok else "CHECKS FAILED")
sys.exit(0 if ok else 1)
