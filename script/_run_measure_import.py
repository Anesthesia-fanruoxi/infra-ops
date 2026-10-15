# -*- coding: utf-8 -*-
"""跑 script/measure_es_import.html 量台：无头 Edge + --dump-dom，抽出 #out 里的断言。

这个台子是 file:// 协议，headless 下 --dump-dom 拿到的是渲染后的 DOM，
断言结果写在 <pre id="out"> 里，按 id 抠出来即可 —— 别用 --screenshot 去「看图猜数字」，
截图只用来复核对齐/字重这类目视项。

用法：
  python script/_run_measure_import.py                # 4 场景 × 1440/800 两档视口，打印断言
  python script/_run_measure_import.py long result    # 只跑指定场景
  python script/_run_measure_import.py --shot         # 出 PNG 到 script/_shots/（--shot 与场景名可同用）

注意：每次跑都会新建 Edge 临时 profile，脚本结束自动删掉（297 个文件的目录，别往库里留）。
"""
import os
import re
import shutil
import subprocess
import sys

EDGE = r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
HERE = os.path.dirname(os.path.abspath(__file__))
PAGE = "file:///" + os.path.join(HERE, "measure_es_import.html").replace("\\", "/")
SHOTS = os.path.join(HERE, "_shots")

STAGES = ["preview", "long", "result", "archive"]
VIEWPORTS = [(1440, 900), (800, 900)]


def edge(profile, url, w, h, extra):
    cmd = [
        EDGE, "--headless=new", "--disable-gpu", "--no-first-run",
        "--user-data-dir=%s" % profile,
        "--window-size=%d,%d" % (w, h),
        "--virtual-time-budget=12000",
    ] + extra + [url]
    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180)
    return p.stdout.decode("utf-8", "replace"), p.returncode, p.stderr.decode("utf-8", "replace")


def dump(stage, w, h, profile):
    url = "%s?stage=%s" % (PAGE, stage)
    out, rc, err = edge(profile, url, w, h, ["--dump-dom"])
    m = re.search(r'<pre id="out">(.*?)</pre>', out, re.S)
    if not m:
        return "(no #out; dom %d bytes, rc=%d) %s" % (len(out), rc, err[:400])
    body = m.group(1)
    for a, b in (("&lt;", "<"), ("&gt;", ">"), ("&amp;", "&"), ("&quot;", '"')):
        body = body.replace(a, b)
    return body.strip()


def shot(stage, w, h, profile):
    os.makedirs(SHOTS, exist_ok=True)
    png = os.path.join(SHOTS, "imp-%s-%d.png" % (stage, w))
    # shot=true 让台子跳过写 #out（截图里不该出现那块深色断言面板）
    edge(profile, "%s?stage=%s&shot=true" % (PAGE, stage), w, h, ["--screenshot=%s" % png])
    return "%s  %s" % (png, os.path.getsize(png) if os.path.exists(png) else "MISSING")


def main():
    want_shot = "--shot" in sys.argv
    only = [a for a in sys.argv[1:] if not a.startswith("--")] or None
    profile = os.path.join(HERE, "__measure_profile")
    try:
        for w, h in VIEWPORTS:
            for st in STAGES:
                if only and st not in only:
                    continue
                print("=" * 78)
                print("### %s @ %dx%d" % (st, w, h))
                print(shot(st, w, h, profile) if want_shot else dump(st, w, h, profile))
            print()
    finally:
        shutil.rmtree(profile, ignore_errors=True)


if __name__ == "__main__":
    main()
