#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""任务 09 F1–F7 前端目录化：从 pages/stacks.js 抽取模板与分析成员清单（只读分析）。

用法：
  python script/_extract_stack_frontend.py members   # 打印 computed/methods 成员与行数
  python script/_extract_stack_frontend.py tpl       # 校验模板字符串可无损拆分
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "template" / "static" / "pages" / "stacks.js"

MEMBER_RE = re.compile(r"^    (?:async\s+)?([A-Za-z_$][\w$]*)\s*[:(]")


def read_src():
    return SRC.read_text(encoding="utf-8").split("\n")


def block_bounds(lines, opener_prefix):
    """返回 (open_idx, close_idx) —— close_idx 为块内最后一个成员行的下标（按花括号深度定位）。"""
    for i, ln in enumerate(lines):
        if ln.strip() == opener_prefix:
            start = i + 1
            depth = 1
            for j in range(start, len(lines)):
                depth += lines[j].count("{") - lines[j].count("}")
                if depth == 0:
                    return start, j
            raise SystemExit("找不到块结束: %s" % opener_prefix)
    raise SystemExit("找不到块开始: %s" % opener_prefix)


def split_members(lines, start, end):
    """按 4 空格缩进的成员起始行切分 [start, end)。

    每个成员**尾部**紧邻的注释/空行归属于**下一个**成员（注释是下一个成员的文档），
    保证跨文件搬移后注释不跟错人。
    """
    starts = []
    for i in range(start, end):
        m = MEMBER_RE.match(lines[i])
        if m:
            starts.append((i, m.group(1)))
    chunks = []
    for k, (i, name) in enumerate(starts):
        j = starts[k + 1][0] if k + 1 < len(starts) else end
        chunks.append([name, lines[i:j]])
    out = []
    carry = []
    for name, body in chunks:
        body = carry + body
        carry = []
        while body:
            last = body[-1].strip()
            if last == "" or last.startswith("//"):
                carry.insert(0, body.pop())
            else:
                break
        out.append((name, body))
    if carry:
        out[-1] = (out[-1][0], out[-1][1] + carry)
    return out


def template_bounds(lines):
    """template: ` ... `, 的行区间（含首尾行）。"""
    for i, ln in enumerate(lines):
        if ln.rstrip() == "  template: `":
            for j in range(i + 1, len(lines)):
                if lines[j] == "  `,":
                    return i, j
            raise SystemExit("模板未闭合")
    raise SystemExit("找不到 template 起始")


def cmd_members():
    lines = read_src()
    for prefix in ("computed: {", "methods: {"):
        start, end = block_bounds(lines, prefix)
        print("==", prefix, "lines", start + 1, "-", end)
        total = 0
        for name, body in split_members(lines, start, end):
            print("  %-28s %4d" % (name, len(body)))
            total += len(body)
        print("   小计 %d 行" % total)


def cmd_tpl():
    lines = read_src()
    i, j = template_bounds(lines)
    # 模板正文：首行 `  template: ` + 反引号 → 去掉前缀取反引号后内容
    segs = []
    segs.append(lines[i][len("  template: "):])  # "`"
    segs.extend(lines[i + 1:j])
    segs.append("  `")
    joined = "\n".join(segs)
    print("template 起始行", i + 1, "结束行", j + 1, "行数", j - i)
    print("前 3 行:", repr(joined[:60]))
    print("末 3 行:", repr(joined[-60:]))


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else "members"
    {"members": cmd_members, "tpl": cmd_tpl}[cmd]()
