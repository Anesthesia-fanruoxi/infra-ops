#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""B9 临时扫描：套件蓝图变量 / 阶段声明 / 引擎侧待迁移引用点。"""
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def walk(rel, exts=(".go",)):
    base = os.path.join(ROOT, rel)
    for root, dirs, files in os.walk(base):
        dirs[:] = [d for d in dirs if d not in ("scripts", "configs")]
        for f in sorted(files):
            if f.endswith(exts):
                yield os.path.join(root, f).replace("\\", "/")


print("=== 蓝图变量 components / replicas ===")
pat = re.compile(r'Name:\s*"(components|replicas)"')
for p in walk("store/stacks"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if pat.search(l):
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== 声明 PhaseScaleOut / PhaseScaleIn 的套件 ===")
for p in walk("store/stacks"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if "PhaseScaleOut" in l or "PhaseScaleIn" in l:
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== Category 取值 ===")
for p in walk("store/stacks"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if "Category:" in l:
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== api/stack 内 randomKafkaClusterID / randomErlangCookie 引用 ===")
for p in walk("api/stack"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if "randomKafkaClusterID" in l or "randomErlangCookie" in l:
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== api/stack 内 parseReplicas 引用 ===")
for p in walk("api/stack"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if "parseReplicas" in l:
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== api/stack 内 validateStackTopology / masterMustBeSelected 引用 ===")
for p in walk("api/stack"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if "validateStackTopology" in l or "masterMustBeSelected" in l:
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== api/stack 内 stackVarsJSON / validateCWHRoles / validateCWHRemoveMasters 引用 ===")
for p in walk("api/stack"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if "stackVarsJSON" in l or "validateCWHRoles" in l or "validateCWHRemoveMasters" in l:
            print("  %s:%d %s" % (p, i, l.strip()))

print("=== api/stack 内 planForStackOp / validateBigdata* / mergeBigdataMasterExtra 引用 ===")
for p in walk("api/stack"):
    for i, l in enumerate(open(p, encoding="utf-8", errors="replace"), 1):
        if any(k in l for k in ("planForStackOp", "validateBigdata", "mergeBigdataMasterExtra",
                                "drainCWHRemoved", "stackCWHExtra", "stackClusterExtraMerged",
                                "bigdataMasterExtra", "loadBigdataRolePlan", "planGenericRoles",
                                "planBigdataRoles")):
            print("  %s:%d %s" % (p, i, l.strip()))
