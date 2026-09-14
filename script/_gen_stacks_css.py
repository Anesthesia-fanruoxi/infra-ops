#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""任务 09 F1–F7：把 template/static/style.css 中的**套件专属规则**
迁到 template/static/stacks/common/common.css 与各套件 style.css，并前缀化命名空间。

归属规则（前缀路由，与设计文档 §5.4 约定一致）：
  通用骨架 .stk-*        ← 原 .stack-*（除下述专属）、.sv-*、.inst-*、.plan-legend*、@keyframes stack*
  bigdata .stk-bd-*      ← 原 .stack-comp-* / .stack-ha-* / .stack-role-plan|grid|item|label
  redis   .stk-redis-*   ← 原 .stack-replicas
  elasticsearch .stk-es-* ← 原 ES 冷热温角色区 .es-role-plan|counts|rows|row|host|hostname|hostip|
                            checks|chip|io-warn|empty|warnings|warn / .es-tier-chip /
                            .es-master|coord|hot|warm|cold
  其余规则（含 ES 控制台的 .es-*，如 .es-role-list）**原样保留**在 style.css。

硬约束：迁出块 + 保留块按原顺序拼接必须与原文件**逐字节一致**（不丢行、不改序）。
用法：python script/_gen_stacks_css.py [--check]
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FE = ROOT / "template" / "static"
CSS = FE / "style.css"
OUT = FE / "stacks"

# 源文件快照：本脚本会瘦身 style.css，为保证可重复执行（幂等），原始样式单独留档。
SNAPSHOT = ROOT / "script" / "_style_src_snapshot.css"


def load_src(path, snapshot):
    txt = path.read_text(encoding="utf-8")
    if txt.count("\n") > 2000:          # 原始样式表：刷新快照并返回
        snapshot.write_text(txt, encoding="utf-8", newline="")
        return txt
    if snapshot.exists():               # 已瘦身：回退快照
        snap = snapshot.read_text(encoding="utf-8")
        if snap.count("\n") > 2000:
            return snap
    raise SystemExit("style.css 已瘦身且无可用快照：%s" % path)

# ---------------------------------------------------------------- 归属判定
AVATARS = ["redis", "kafka", "es", "rabbitmq", "rocketmq", "nacos", "powerjob", "bigdata"]

COMMON_TOKENS = [
    r"\.stack-(?!form-)",
    r"\.sv-",
    r"\.inst-",
    r"\.plan-legend",
    r"@keyframes\s+stack",
]
ES_TOKENS = [
    r"\.es-role-(?:plan|counts|rows|row|host|hostname|hostip|checks|chip|io-warn|empty|warnings|warn)\b",
    r"\.es-tier-chip\b",
    r"\.es-(?:master|coord|hot|warm|cold)\b",
]
BD_TOKENS = [r"\.stack-comp-", r"\.stack-ha-", r"\.stack-role-(?:plan|grid|item|label)"]
REDIS_TOKENS = [r"\.stack-replicas\b"]
AVATAR_RE = re.compile(r"\.sv-(?:%s)\b" % "|".join(AVATARS))

_COMMON_RE = re.compile("|".join(COMMON_TOKENS + ES_TOKENS))
_BD_RE = re.compile("|".join(BD_TOKENS))
_REDIS_RE = re.compile("|".join(REDIS_TOKENS))
_ES_RE = re.compile("|".join(ES_TOKENS))
_SV_RE = re.compile(r"\.sv-")

# 与 _gen_stacks_fe.py 完全一致的样式前缀化映射
_TOKENS = (["stack-(?!form-)"] + ["sv-(?:%s)" % "|".join(AVATARS)] +
           ["sv-", "inst-", "plan-legend", "es-role-", "es-tier-",
            "es-master", "es-coord", "es-hot", "es-warm", "es-cold"])
RENAME_RE = re.compile(r"\b(" + "|".join(_TOKENS) + r")")


def rename(text, suite=None):
    if suite == "bigdata":
        text = text.replace("stack-comp-", "stk-bd-comp-")
        text = text.replace("stack-ha-", "stk-bd-ha-")
        for n in ("stack-role-plan", "stack-role-grid", "stack-role-item", "stack-role-label"):
            text = text.replace(n, n.replace("stack-role-", "stk-bd-role-"))
    if suite == "redis":
        text = text.replace("stack-replicas", "stk-redis-replicas")

    def _sub(m):
        tok = m.group(1)
        if tok.startswith("stack-"):
            return "stk-" + tok[6:]
        if tok.startswith("sv-"):
            for a in AVATARS:
                if tok == "sv-" + a:
                    return "stk-av-" + a
            return "stk-vf-" + tok[3:]
        if tok.startswith("inst-"):
            return "stk-inst-" + tok[5:]
        if tok.startswith("plan-legend"):
            return "stk-legend" + tok[len("plan-legend"):]
        if tok.startswith("es-"):
            return "stk-" + tok
        raise SystemExit("未预期前缀: " + tok)

    return RENAME_RE.sub(_sub, text)


def rename_atrule(text, suite=None):
    """@keyframes stackXxx / animation: stackXxx 一并前缀化，避免遗留指向旧名。"""
    text = re.sub(r"\bstack([A-Z]\w*)", lambda m: "stk" + m.group(1), text)
    return text


# ---------------------------------------------------------------- 块解析
def parse_blocks(lines):
    """返回 [(trivia_start, end, selector)]；块含其前导注释/空行。"""
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


def route(sel):
    """返回目标文件键：common / verify / bigdata / redis / elasticsearch；None = 留在 style.css。"""
    if not _COMMON_RE.search(sel):
        return None
    if _BD_RE.search(sel):
        return "bigdata"
    if _REDIS_RE.search(sel):
        return "redis"
    if _ES_RE.search(sel):
        return "elasticsearch"
    # 套件头像色块（.stack-inst-avatar.sv-redis 等）属实例卡片，归通用骨架
    if AVATAR_RE.search(sel):
        return "common"
    # 其余 .sv-* 为探活对话框骨架，单独一份（common.css 控制在 500 行内）
    if _SV_RE.search(sel):
        return "verify"
    return "common"


HEADERS = {
    "common": (
        "/* 套件部署页 · 通用骨架样式（设计文档 §4.2 common/common.css / §5.4 命名空间）。\n"
        "   命名空间：通用 .stk-*（向导 / 主机表 / 角色计划 / 实例卡片）。\n"
        "   迁自 template/static/style.css 的 .stack-* / .inst-* / .plan-legend* / 套件头像色块规则，\n"
        "   仅做前缀化（.stack- -> .stk-、.inst- -> .stk-inst-、.plan-legend -> .stk-legend、\n"
        "   .sv-<套件> -> .stk-av-<套件>），未改动任何声明值与选择器结构；\n"
        "   探活对话框骨架在 common/verify.css，套件专属差异样式在各自 static/stacks/<key>/style.css。 */"
    ),
    "verify": (
        "/* 套件部署页 · 探活对话框骨架样式（common/verify.css）。\n"
        "   迁自 style.css 的 .sv-* 规则（.sv- -> .stk-vf-），与 common/verify-dialog.js 配套；\n"
        "   数据由各套件 hooks 提供，本文件不含任何套件专属选择器。 */"
    ),
    "bigdata": (
        "/* 大数据底座套件样式（设计文档 §4.2 / §5.4：前缀 .stk-bd-*）。\n"
        "   迁自 style.css 的 .stack-comp-* / .stack-ha-* / .stack-role-plan|grid|item|label 规则。 */"
    ),
    "redis": (
        "/* Redis 套件样式（设计文档 §4.2 / §5.4：前缀 .stk-redis-*）。\n"
        "   迁自 style.css 的 .stack-replicas 规则。 */"
    ),
    "elasticsearch": (
        "/* Elasticsearch 套件样式（设计文档 §4.2 / §5.4：前缀 .stk-es-*）。\n"
        "   迁自 style.css 中 stacks 侧冷热温角色区 .es-role-*/.es-tier-chip/.es-master|coord|hot|warm|cold；\n"
        "   注意：ES **控制台**（pages/es_*.js）使用的 .es-* 样式**不在**本文件，仍保留在 style.css。 */"
    ),
}

# 键 → 输出相对路径
OUT_PATHS = {
    "common": "common/common.css",
    "verify": "common/verify.css",
    "bigdata": "bigdata/style.css",
    "redis": "redis/style.css",
    "elasticsearch": "elasticsearch/style.css",
}


def main():
    check_only = "--check" in sys.argv
    raw = load_src(CSS, SNAPSHOT)
    lines = raw.split("\n")
    blocks = parse_blocks(lines)

    buckets = {"common": [], "verify": [], "bigdata": [], "redis": [], "elasticsearch": []}
    kept = []
    for ts, e, sel in blocks:
        kind = route(sel)
        chunk = "\n".join(lines[ts:e])
        if kind is None:
            kept.append(chunk)
        else:
            buckets[kind].append(rename(rename_atrule(chunk, kind), kind))

    # 字节守恒：按原顺序把「迁移块原样 + 保留块原样」拼回，必须与原文件逐字节一致
    ordered = sorted([(ts, e) for ts, e, sel in blocks])
    body_lines = []
    for ts, e in ordered:
        body_lines.extend(lines[ts:e])
    rebuilt_full = "\n".join(lines[:blocks[0][0]] + body_lines + lines[blocks[-1][1]:])
    if rebuilt_full != raw:
        raise SystemExit("字节守恒校验失败：重建结果与原文件不一致（块解析有遗漏）")

    moved = sum(len(buckets[k]) for k in buckets)
    kept_lines = []
    for ts, e, sel in blocks:
        if route(sel) is None:
            kept_lines.extend(lines[ts:e])
    kept_txt = "\n".join(lines[:blocks[0][0]] + kept_lines + lines[blocks[-1][1]:])
    print("原 style.css %d 行；迁出块 %d 个 → common %d / verify %d / bigdata %d / redis %d / elasticsearch %d"
          % (len(lines), moved, len(buckets["common"]), len(buckets["verify"]),
             len(buckets["bigdata"]), len(buckets["redis"]), len(buckets["elasticsearch"])))
    print("迁出后 style.css = %d 行" % (kept_txt.count("\n") + 1))
    if check_only:
        return 0

    for key, rel in OUT_PATHS.items():
        if not buckets[key]:
            continue
        body = "\n\n".join(buckets[key])
        p = OUT / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(HEADERS[key] + "\n\n" + body + "\n", encoding="utf-8", newline="\n")
        n = (HEADERS[key] + "\n\n" + body + "\n").count("\n")
        print("  写出 %s (%d 行)%s" % (p.relative_to(ROOT).as_posix(), n,
                                     "  <<< 超 500 行" if n > 500 else ""))
    CSS.write_text(kept_txt, encoding="utf-8", newline="\n")
    print("  写出 template/static/style.css (%d 行)" % (kept_txt.count("\n") + 1))
    return 0


if __name__ == "__main__":
    sys.exit(main())
