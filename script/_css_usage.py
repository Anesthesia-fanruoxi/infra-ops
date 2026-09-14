#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""按「类名实际使用方」判定 style.css 中哪些规则属于套件专属（分析用）。

做法：
  1. 收集前端各文件的类名 token（class="..." 、:class="..." 里的字符串、以及 JS 中的 'xxx' 类名常量）；
  2. stacks 侧集合 = pages/stacks.js + pages/stacks-forms.js + static/stacks/**；
     其他侧集合 = pages/*.js（除 stacks*）+ app.js + vendor 无关；
  3. style.css 的规则块：选择器里的 class token 若「只在 stacks 侧出现」则**全部**安全迁出；
     若出现「其他侧也用到」的 class，则该规则留在 style.css（避免误伤）。
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FE = ROOT / "template" / "static"
CSS = FE / "style.css"

CLASS_RE = re.compile(r"[.#]([A-Za-z_][\w-]*)")
TOKEN_RE = re.compile(r"\b[A-Za-z_][\w-]*\b")


def classes_in_text(text):
    """收集文本里的类名：class="a b"、'a b' 形式的选择器片段、以及 kebab 词。"""
    out = set()
    # class="..." / :class="..."
    for m in re.finditer(r'class\s*=\s*"([^"]*)"', text):
        for t in m.group(1).split():
            out.add(t.strip("{}'\"`"))
    for m in re.finditer(r"class\s*=\s*'([^']*)'", text):
        for t in m.group(1).split():
            out.add(t.strip("{}'\"`"))
    # 字符串字面量中的 kebab 类名（如 roleTextClass 返回 'rt-primary'、
    # registry 等处以 'stk-xxx' 拼接）
    for m in re.finditer(r"['\"`]([a-zA-Z][\w-]*[a-zA-Z0-9])['\"`]", text):
        s = m.group(1)
        if "-" in s:
            out.add(s)
    return out


def scan(paths):
    out = set()
    for p in paths:
        try:
            out |= classes_in_text(p.read_text(encoding="utf-8", errors="replace"))
        except Exception as e:
            print("skip", p, e, file=sys.stderr)
    return out


def collect_stacks_files():
    files = [FE / "pages" / "stacks.js", FE / "pages" / "stacks-forms.js"]
    files += sorted((FE / "stacks").rglob("*.js"))
    return [p for p in files if p.exists()]


def collect_other_files():
    files = [FE / "app.js"]
    for p in sorted((FE / "pages").glob("*.js")):
        if p.name.startswith("stacks"):
            continue
        files.append(p)
    return files


def parse_blocks(lines):
    blocks = []
    i, n = 0, len(lines)
    trivia = 0
    while i < n:
        s = lines[i].strip()
        if s == "" or (s.startswith("/*") and s.endswith("*/")):
            i += 1
            continue
        if s.startswith("/*"):
            while i < n and "*/" not in lines[i]:
                i += 1
            i += 1
            continue
        sel_start = i
        while i < n and "{" not in lines[i]:
            i += 1
        if i >= n:
            break
        sel = "\n".join(lines[sel_start:i + 1])
        depth, j = 0, i
        while j < n:
            depth += lines[j].count("{") - lines[j].count("}")
            j += 1
            if depth <= 0:
                break
        blocks.append((trivia, j, sel))
        i = j
        trivia = i
    return blocks


def main():
    stacks_cls = scan(collect_stacks_files())
    other_cls = scan(collect_other_files())
    only_stacks = {c for c in stacks_cls if c not in other_cls}

    lines = CSS.read_text(encoding="utf-8").split("\n")
    blocks = parse_blocks(lines)

    move = []
    keep = []
    for ts, e, sel in blocks:
        cls = set(CLASS_RE.findall(sel))
        vendor = {c for c in cls if c.startswith("el-") or c.startswith("is-") or c == "dot"
                  or c == "cell" or c == "active" or c == "title" or c == "mono"}
        real = cls - vendor
        if not real:
            keep.append((ts, e, sel))
            continue
        if real <= only_stacks:
            move.append((ts, e, sel))
        else:
            keep.append((ts, e, sel))

    moved_lines = sum(e - ts for ts, e, _ in move)
    kept_lines = sum(e - ts for ts, e, _ in keep)
    trivia_lines = len(lines) - sum(e - ts for ts, e, _ in blocks)
    print("总行 %d；块 %d" % (len(lines), len(blocks)))
    print("可迁出块 %d（含前导絮 %d 行）" % (len(move), moved_lines))
    print("保留块 %d（%d 行）" % (len(keep), kept_lines))
    print("前导絮行 %d" % trivia_lines)
    print("迁出后 style.css 预计 ≈ %d 行" % (kept_lines + trivia_lines - (len(move) and 0)))

    # 列出被判定「因他人也使用而保留」的 stacks 风格选择器，人工复核
    print("\n---- 含 stacks 风格 token 但被保留的块（需人工复核） ----")
    token = re.compile(r"\.(?:stack-(?!form-)|sv-|inst-|plan-legend|es-role-|es-tier-)")
    n = 0
    for ts, e, sel in keep:
        if token.search(sel):
            n += 1
            print("L%-5d %s" % (ts + 1, " ".join(sel.split())[:130]))
    print("共 %d 块" % n)

    # 迁出块的归属建议
    bd = re.compile(r"\.(?:stack-comp-|stack-ha-|stack-role-(?:plan|grid|item|label))")
    redis = re.compile(r"\.stack-replicas\b")
    es = re.compile(r"\.(?:es-role-(?:plan|counts|rows|row|host|hostname|hostip|checks|chip|io-warn|empty|warnings|warn)|es-tier-chip|es-master\b|es-coord\b|es-hot\b|es-warm\b|es-cold\b)")
    stats = {"common": 0, "bigdata": 0, "redis": 0, "elasticsearch": 0}
    for ts, e, sel in move:
        if bd.search(sel):
            stats["bigdata"] += e - ts
        elif redis.search(sel):
            stats["redis"] += e - ts
        elif es.search(sel):
            stats["elasticsearch"] += e - ts
        else:
            stats["common"] += e - ts
    print("\n归属行数:", stats)
    return 0


if __name__ == "__main__":
    sys.exit(main())
