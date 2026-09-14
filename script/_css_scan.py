#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""style.css 规则块解析与套件归属统计（分析用，不写文件）。

块 = 前导絮（注释/空行）+ 选择器行 + 到匹配 '}' 的规则体。
递归进入 @media / @supports。
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CSS = ROOT / "template" / "static" / "style.css"

STACK_TOKENS = [
    r"\.stack-(?!form-)",          # 通用 .stack-*（排除 .stack-form-* 组件名）
    r"\.sv-",                      # 套件卡片头像 / 探活字段
    r"\.inst-",                    # 实例卡片
    r"\.plan-legend",              # 角色计划图例
    r"\.es-role-", r"\.es-tier-",  # ES 冷热温角色
    r"\.es-master\b", r"\.es-coord\b", r"\.es-hot\b", r"\.es-warm\b", r"\.es-cold\b",
]
STACK_RE = re.compile("|".join(STACK_TOKENS))

BD_TOKENS = [r"\.stack-comp-", r"\.stack-ha-", r"\.stack-role-(?:plan|grid|item|label)"]
BD_RE = re.compile("|".join(BD_TOKENS))
REDIS_TOKENS = [r"\.stack-replicas\b"]
REDIS_RE = re.compile("|".join(REDIS_TOKENS))
ES_TOKENS = [r"\.es-role-", r"\.es-tier-", r"\.es-master\b", r"\.es-coord\b",
             r"\.es-hot\b", r"\.es-warm\b", r"\.es-cold\b"]
ES_RE = re.compile("|".join(ES_TOKENS))


def parse_blocks(lines):
    """返回 [(start, end_exclusive, selector_text, body_text, is_atrule)]（0 基，[start,end)）。"""
    blocks = []
    i = 0
    n = len(lines)
    trivia_start = 0
    while i < n:
        line = lines[i]
        s = line.strip()
        if s == "" or (s.startswith("/*") and s.endswith("*/")):
            i += 1
            continue
        if s.startswith("/*"):  # 多行注释
            while i < n and "*/" not in lines[i]:
                i += 1
            i += 1
            continue
        # 选择器起点
        sel_start = i
        while i < n and "{" not in lines[i]:
            i += 1
        if i >= n:
            break
        sel = "\n".join(lines[sel_start:i + 1])
        depth = 0
        j = i
        while j < n:
            depth += lines[j].count("{") - lines[j].count("}")
            j += 1
            if depth <= 0:
                break
        body = "\n".join(lines[i:j])
        blocks.append((trivia_start, sel_start, j, sel, body))
        i = j
        trivia_start = i
    return blocks


def classify(sel):
    if not STACK_RE.search(sel):
        return None
    if BD_RE.search(sel):
        return "bigdata"
    if REDIS_RE.search(sel):
        return "redis"
    if ES_RE.search(sel):
        return "elasticsearch"
    return "common"


def main():
    lines = CSS.read_text(encoding="utf-8").split("\n")
    blocks = parse_blocks(lines)
    counts = {"common": 0, "bigdata": 0, "redis": 0, "elasticsearch": 0}
    keep_lines = 0
    for ts, ss, e, sel, body in blocks:
        kind = classify(sel)
        span = e - ts
        if kind:
            counts[kind] += e - ss
        else:
            keep_lines += e - ss
    print("style.css 总行 %d，规则块 %d" % (len(lines), len(blocks)))
    print("套件规则行数:", counts)
    moved = sum(counts.values())
    print("迁出行数(不含前导絮) = %d" % moved)
    print("保留行数(不含前导絮) = %d" % keep_lines)
    print("预计 style.css 剩余 = %d" % (keep_lines + (len(lines) - sum(b[2] - b[0] for b in blocks))))
    print("---- 归类明细（每块一行） ----")
    for ts, ss, e, sel, body in blocks:
        kind = classify(sel)
        if kind:
            one = " ".join(sel.split())
            print("%-14s L%-5d %s" % (kind, ss + 1, one[:110]))


if __name__ == "__main__":
    sys.exit(main())
