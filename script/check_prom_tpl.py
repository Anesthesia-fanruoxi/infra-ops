import re, yaml, io, sys

SH = r"D:\project\go\infra-ops\store\builtin\install-prometheus.sh"
src = open(SH, encoding="utf-8").read()

# 按出现顺序提取所有 heredoc 内容（YAMLEOF 定界）
blocks = re.findall(r"<<'?YAMLEOF'?\n(.*?)\nYAMLEOF", src, re.S)
assert len(blocks) == 4, f"expect 4 heredocs, got {len(blocks)}"
compose, prom, scrape_d, targets = blocks

# 模拟 Go 侧变量替换 + 主机侧 {{__ip}} 替换
subs = {
    "{{image}}": "prom/prometheus:v2.55.1",
    "{{port}}": "9090",
    "{{home_dir}}": "/data/prometheus",
    "{{retention_days}}": "30",
    "{{__ip}}": "10.10.0.252",
}
def render(t):
    for k, v in subs.items():
        t = t.replace(k, v)
    return t

cases = {
    "compose.yml": render(compose),
    "prometheus.yml(remote_write=空)": render(prom).replace("__REMOTE_WRITE__\n", ""),
    "scrape.d/node.yml": render(scrape_d),
    "targets/node.yml": render(targets),
}

# remote_write 注入变体：模拟 sed "s|__REMOTE_WRITE__|remote_write:\n  - url: X|"
injected = render(prom).replace("__REMOTE_WRITE__", "remote_write:\n  - url: http://10.10.0.252:8428/api/v1/write")
cases["prometheus.yml(remote_write=VM)"] = injected

ok = True
for name, content in cases.items():
    try:
        data = yaml.safe_load(content)
        print(f"[PASS] {name}")
        if name.startswith("prometheus.yml(remote_write=VM)"):
            assert data.get("remote_write") == [{"url": "http://10.10.0.252:8428/api/v1/write"}], "remote_write 内容不符"
            print("       remote_write 校验通过:", data["remote_write"])
        if name == "targets/node.yml":
            assert data == [] or data is None, f"targets 应为空列表，实际: {data!r}"
    except Exception as e:
        ok = False
        print(f"[FAIL] {name}: {e}")
        print(content)

sys.exit(0 if ok else 1)
