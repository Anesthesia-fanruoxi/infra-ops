#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""B9 临时扫描 2：脚本里引用的注入变量（{{__xxx}}），用于判定阶段注入的影响面。"""
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

RE_VAR = re.compile(r"\{\{(__[A-Za-z0-9_]+)\}\}")

targets = [
    "store/stacks/elasticsearch/scripts/cold-warm-hot/bootstrap.sh",
    "store/stacks/elasticsearch/scripts/cold-warm-hot/node.sh",
    "store/stacks/elasticsearch/scripts/cold-warm-hot/scale_in.sh",
    "store/stacks/bigdata/scripts/bootstrap.sh",
    "store/stacks/redis/scripts/cluster-bootstrap.sh",
    "store/stacks/redis/scripts/cluster-add.sh",
    "store/stacks/elfk/scripts/verify.sh",
]
for rel in targets:
    p = os.path.join(ROOT, rel)
    if not os.path.exists(p):
        print("缺失 %s" % rel)
        continue
    text = open(p, encoding="utf-8", errors="replace").read()
    names = sorted(set(RE_VAR.findall(text)))
    print("%s -> %s" % (rel, ", ".join(names) if names else "(无非空注入变量)"))
