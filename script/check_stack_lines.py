#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""任务 09（套件目录化拆分）规模与结构检查。

三种模式：
  python script/check_stack_lines.py             # 报告 + 与基线对比（不得劣化）
  python script/check_stack_lines.py --record    # 记录当前指标为基线
  python script/check_stack_lines.py --targets   # 叠加终态硬目标（P7 用）

指标口径：
  lines          单文件行数（api/stack、store 顶层、store/stacks/**、前端 stacks 相关）
  branches       套件分支数：api/stack 与 store 下 `X.Key == "…"` 形式的字符串判断
  offenders      超过 500 行的文件（规约：尽量 ≤500）

终态硬目标（--targets，均来自 docs/套件目录化拆分设计.md §10）：
  store/builtin_stacks.go ≤ 120 行
  template/static/pages/stacks.js ≤ 120 行
  api/stack 套件分支数 == 0（含 store 顶层）
  style.css 中无任何套件专属选择器（.stack-* / stacks 侧 .es-*，见 §10.4）
  全部受检文件 ≤ 500 行

关于 style.css 的行数口径（P7 修订）：
  原设 1400 行上限已撤销，改为**语义检查**（`style_css_stack_selectors`）。原因是
  boundary.md 只允许从 style.css 迁出 `.stack-*` 与 stacks 侧 `.es-*` 套件专属规则，
  而这类规则全部迁出后实测约 400 行，style.css 残量由其它页面的样式决定，
  在「不越界」的前提下无法继续下降；继续压行数必须拆分全站样式表，属范围外扩张。
  故 P7 起以「无套件专属选择器」为硬目标，行数由基线对比（不得劣化）兜底。

退出码：0 通过；1 不通过。
"""

import argparse
import json
import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BASELINE = ROOT / "workflow" / "tasks" / "09-stack-dir-split" / "baseline" / "metrics.json"

MAX_LINES = 500
TARGET_LINES = {
    "store/builtin_stacks.go": 120,
    "template/static/pages/stacks.js": 120,
}
# 500 行规约的豁免项：不属于本次拆分的可拆对象，各自单列上限（None = 不设上限）
EXEMPT_MAX_LINES = {
    "store/migrations.go": None,           # schema 迁移，按版本追加，不参与套件拆分
    "template/static/style.css": None,     # 全站样式表：只迁出套件专属规则，行数由语义检查+基线兜底
}

# style.css 语义检查：不得残留套件专属选择器（设计文档 §10.4）
#   通用骨架 .stk-* 的旧名 .stack-* / .sv-* / .inst-* / .plan-legend*
#   以及 stacks 侧冷热温角色区的 .es-*（ES 控制台的 .es-* 保留，故按精确名单排除）
STYLE_CSS = "template/static/style.css"
STYLE_STACK_RE = re.compile(
    r"\.stack-(?!form-)|\.sv-|\.inst-|\.plan-legend|@keyframes\s+stack"
    r"|\.es-role-(?:plan|counts|rows|row|host|hostname|hostip|checks|chip|io-warn|empty|warnings|warn)\b"
    r"|\.es-tier-chip\b|\.es-(?:master|coord|hot|warm|cold)\b"
)

# 套件分支判定：以 Key/StackKey 与字符串字面量比较（设计文档 §7 硬指标）
# 只统计生产代码（_test.go 里的断言与测试自身分发不算分支）
BRANCH_RE = re.compile(r'(?:\bKey|stack_key|StackKey)\s*==\s*"')
BRANCH_GLOBS = [
    "api/stack/**/*.go",
    "store/*.go",
    "store/stacks/**/*.go",
]


def scan_files():
    """受检文件：后端套件相关 + 前端套件相关（相对仓库根，POSIX 风格）。"""
    pats = [
        "api/stack/*.go",
        "store/*.go",
        "store/stacks/**/*.go",
        "template/static/pages/stacks.js",
        "template/static/pages/stacks-forms.js",
        "template/static/stacks/**/*.js",
        "template/static/stacks/**/*.css",
        "template/static/style.css",
        "template/index.html",
    ]
    out = []
    for pat in pats:
        for p in ROOT.glob(pat):
            if p.is_file():
                rel = p.relative_to(ROOT).as_posix()
                if rel not in out:
                    out.append(rel)
    return sorted(out)


def count_lines(path: Path) -> int:
    with path.open("r", encoding="utf-8", errors="replace") as f:
        return sum(1 for _ in f)


def collect():
    lines = {}
    for rel in scan_files():
        lines[rel] = count_lines(ROOT / rel)

    branch_files = []
    total_branches = 0
    details = []
    for pat in BRANCH_GLOBS:
        for p in ROOT.glob(pat):
            if not p.is_file() or p.name.endswith("_test.go"):
                continue
            rel = p.relative_to(ROOT).as_posix()
            n = 0
            with p.open("r", encoding="utf-8", errors="replace") as f:
                for i, line in enumerate(f, 1):
                    hits = len(BRANCH_RE.findall(line))
                    if hits:
                        n += hits
                        details.append({"file": rel, "line": i, "text": line.strip()[:160]})
            if n:
                branch_files.append(rel)
                total_branches += n

    return {
        "lines": lines,
        "branch_total": total_branches,
        "branch_files": sorted(branch_files),
        "branch_details": details,
    }


def load_baseline():
    if not BASELINE.exists():
        return None
    with BASELINE.open("r", encoding="utf-8") as f:
        return json.load(f)


def compare(cur, base, problems):
    base_lines = base.get("lines", {})
    for rel, n in sorted(cur["lines"].items()):
        if rel in base_lines and n > base_lines[rel]:
            problems.append("行数劣化: %s %d -> %d（基线 %d）" % (rel, base_lines[rel], n, base_lines[rel]))
    bt = base.get("branch_total", 0)
    if cur["branch_total"] > bt:
        problems.append("套件分支数劣化: %d -> %d（基线 %d）" % (bt, cur["branch_total"], bt))


def style_css_stack_selectors():
    """返回 style.css 中仍残留的套件专属选择器 [(行号, 文本)]（跳过注释块）。"""
    p = ROOT / STYLE_CSS
    if not p.exists():
        return [("", "缺少 %s" % STYLE_CSS)]
    out = []
    in_comment = False
    with p.open("r", encoding="utf-8", errors="replace") as f:
        for i, line in enumerate(f, 1):
            s = line.strip()
            if in_comment:
                if "*/" in s:
                    in_comment = False
                continue
            if s.startswith("/*"):
                if "*/" not in s:
                    in_comment = True
                continue
            # 去掉行内注释后再判定，避免注释里提到旧类名造成误报
            code = line.split("/*")[0]
            if STYLE_STACK_RE.search(code):
                out.append((i, line.strip()[:160]))
    return out


def apply_targets(cur, problems):
    for rel, limit in TARGET_LINES.items():
        n = cur["lines"].get(rel)
        if n is None:
            problems.append("终态目标缺少文件: %s（应存在）" % rel)
        elif n > limit:
            problems.append("终态目标未达标: %s %d 行 > %d 行" % (rel, n, limit))
    if cur["branch_total"] > 0:
        problems.append("终态目标未达标: 套件分支数 %d > 0" % cur["branch_total"])
    # style.css 语义检查：套件专属选择器必须清空（§10.4）
    leftover = style_css_stack_selectors()
    if leftover:
        problems.append("终态目标未达标: %s 仍残留套件专属选择器 %d 处" % (STYLE_CSS, len(leftover)))
        for ln, txt in leftover[:8]:
            problems.append("    L%s %s" % (ln, txt))
    for rel, n in sorted(cur["lines"].items()):
        if rel in EXEMPT_MAX_LINES:
            limit = EXEMPT_MAX_LINES[rel]
            if limit is not None and n > limit:
                problems.append("超豁免上限: %s (%d > %d)" % (rel, n, limit))
            continue
        if n > MAX_LINES:
            problems.append("超 500 行: %s (%d)" % (rel, n))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--record", action="store_true", help="记录当前指标为基线")
    ap.add_argument("--targets", action="store_true", help="叠加终态硬目标（P7）")
    args = ap.parse_args()

    cur = collect()

    if args.record:
        BASELINE.parent.mkdir(parents=True, exist_ok=True)
        payload = dict(cur)
        payload["_note"] = ("任务 09 基线（P0 首录见 metrics.p0.json；B10 删壳前见 metrics.p7-preb10.json；"
                            "P7+B10 删壳终态见 metrics.p7-b10.json）。本份在上一份（部署期缺陷修复）之上叠加"
                            "「部署资产」功能（套件离线物料上传/代下 + SFTP 分发）："
                            "api/stack/stack_engine.go 216→220（provisionAssets 调用点）、"
                            "store/migrations.go 733→752（V28 stack_assets 表）、"
                            "template/static/stacks/bigdata/roles.js 184→263（第二步资产检查条）、"
                            "template/static/stacks/bigdata/style.css 98→124（.stk-bd-asset* 样式）、"
                            "template/static/stacks/common/wizard.js 306→309（assetCheck 响应式字段）。"
                            "其后叠加「Hive HA 元数据库密码前端必填提示」："
                            "template/static/stacks/bigdata/index.js 163→168（visibleSharedVars 对 hive_db_password "
                            "在前端补 required，仅提示星号 + 新建时提交前拦截，后端 Required 仍为 false、不加校验；"
                            "同时吸收前次删除「预览角色计划」的 template/static/stacks/bigdata/roles.js 263→187）。"
                            "本轮叠加「探活弹框：组件页签修复 + Web 端点外链」："
                            "template/static/stacks/common/verify-dialog.js 140→143（6 个无参挂点从 methods 挪到 computed，"
                            "修模板裸引用取到 bound function 的缺陷）、"
                            "template/static/stacks/common/tpl-verify.js 188→194（两组端点芯片各加一个「打开」外链）、"
                            "template/static/stacks/common/labels.js 73→75（isWebUrl 助手）；"
                            "verify.css 用扩展既有选择器的方式实现未增行数。"
                            "本轮叠加「接入地址：最外框 + 每屏约 4 台主机的横向滚动卡片」（每行 = 服务名称/协议/访问地址/复制，"
                            "服务名底色按端点角色推断主·备·平级）："
                            "template/static/stacks/common/tpl-verify.js 194→202（两处接入地址改为 .stk-vf-ep-box 外框 + 新行结构 + 端口数计数）、"
                            "template/static/stacks/common/verify.css 157→179（.stk-vf-ep-box 外框、卡片等分 4 列且 overflow-x 横向滚动、"
                            ".stk-vf-ep-svc 三态底色、细滚动条）、"
                            "template/static/stacks/common/labels.js 75→85（epRole/epRoleClass/epRoleText）。"
                            "新增文件不参与劣化比对：api/stack/stack_asset.go（资产管理 API）、"
                            "api/stack/stack_asset_push.go（SFTP 分发钩子）、store/stacks/bigdata/assets.go（资产声明）、"
                            "store/repo/asset.go、model/asset.go。"
                            "本轮叠加「探活页签剔除无归属组件的空壳页签」（bigdata 的 metastore_db 由 HA+Hive 自动追加，"
                            "属运行期组件：容器 hive-metastore-db 的检查项归属 hive、endpoint 也无 case，页签恒空）："
                            "template/static/stacks/common/verify-dialog.js 143→162（新增模块级 probeAttributed 过滤助手、"
                            "verifyComponents 计算属性接过滤、watch 把被过滤掉的活动页签回落 all；"
                            "过滤在通用层实现，套件分支仍为 0；仅当结果带组件归属信息时生效，"
                            "整体探活失败时退回声明清单）。拆分期不得劣化，--targets 校验终态语义）")
        with BASELINE.open("w", encoding="utf-8", newline="\n") as f:
            json.dump(payload, f, ensure_ascii=False, indent=2, sort_keys=True)
            f.write("\n")
        print("已记录基线: %s" % BASELINE.relative_to(ROOT).as_posix())
        print("  受检文件 %d 个，合计 %d 行；套件分支 %d 处（%d 个文件）"
              % (len(cur["lines"]), sum(cur["lines"].values()), cur["branch_total"], len(cur["branch_files"])))
        return 0

    problems = []
    base = load_baseline()
    if base is None:
        problems.append("缺少基线文件 %s（先执行 --record）" % BASELINE.relative_to(ROOT).as_posix())
    else:
        compare(cur, base, problems)
    if args.targets:
        apply_targets(cur, problems)

    print("== 受检文件 %d 个，合计 %d 行 ==" % (len(cur["lines"]), sum(cur["lines"].values())))
    for rel, n in sorted(cur["lines"].items(), key=lambda kv: -kv[1])[:12]:
        print("  %6d  %s" % (n, rel))
    if len(cur["lines"]) > 12:
        print("  ...（其余 %d 个文件均较短）" % (len(cur["lines"]) - 12))

    print("== 套件分支 %d 处，分布 %d 个文件 ==" % (cur["branch_total"], len(cur["branch_files"])))
    for rel in cur["branch_files"]:
        n = sum(1 for d in cur["branch_details"] if d["file"] == rel)
        print("  %4d  %s" % (n, rel))

    if args.targets:
        leftover = style_css_stack_selectors()
        print("== %s 套件专属选择器 %d 处 ==" % (STYLE_CSS, len(leftover)))
        for ln, txt in leftover[:8]:
            print("  L%s %s" % (ln, txt))

    if problems:
        print("\n不通过：")
        for p in problems:
            print("  - %s" % p)
        return 1
    print("\n通过：规模与结构指标均未劣化%s。" % ("，且终态目标达标" if args.targets else ""))
    return 0


if __name__ == "__main__":
    sys.exit(main())
