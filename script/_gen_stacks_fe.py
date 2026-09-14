#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""任务 09 F1–F7：把 pages/stacks.js + pages/stacks-forms.js 目录化拆分为
template/static/stacks/{registry.js,common/*,<套件>/*}，并把 pages/stacks.js 降为挂载壳。

原则「只搬不改」——代码逐字搬运，仅做三类必要改写：
  1. 骨架中套件专属的分支点改为按槽位分发的通用实现（hooks，骨架不认识任何套件）；
  2. 样式命名空间前缀化（.stack-* -> .stk-*；套件侧 .stack-comp-*/.es-* 等 -> .stk-bd-*/.stk-es-*/.stk-redis-*）；
  3. 模板中 8 处套件专属表达式改为通用分发名。

用法：python script/_gen_stacks_fe.py
"""
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FE = ROOT / "template" / "static"
SRC = FE / "pages" / "stacks.js"
FORMS_SRC = FE / "pages" / "stacks-forms.js"
OUT = FE / "stacks"

# 源文件快照：本脚本会把 pages/stacks.js 改写为挂载壳并删除 pages/stacks-forms.js，
# 为保证脚本可重复执行（幂等），两份原始源码单独留档；源文件已降级/删除时从此处读取。
SRC_SNAPSHOT = ROOT / "script" / "_stacks_src_snapshot.js"
FORMS_SNAPSHOT = ROOT / "script" / "_stacks_forms_src_snapshot.js"


def load_src(path, snapshot, min_lines, what):
    if path.exists():
        txt = path.read_text(encoding="utf-8")
        if txt.count("\n") >= min_lines:        # 原始源码：刷新快照并返回
            snapshot.write_text(txt, encoding="utf-8", newline="\n")
            return txt
    if snapshot.exists():                       # 已迁移/删除：回退快照
        snap = snapshot.read_text(encoding="utf-8")
        if snap.count("\n") >= min_lines:
            return snap
    raise SystemExit("找不到 %s 的原始源码，且快照不可用：%s" % (what, path))


SRC_TEXT = load_src(SRC, SRC_SNAPSHOT, 500, "pages/stacks.js")
SRC_LINES = SRC_TEXT.split("\n")
FORMS_LINES = load_src(FORMS_SRC, FORMS_SNAPSHOT, 500, "pages/stacks-forms.js").split("\n")


# ==========================================================================
# 工具
# ==========================================================================
def find_block(lines, opener, start=0):
    for i in range(start, len(lines)):
        if lines[i].rstrip() == opener:
            depth = 0
            for j in range(i, len(lines)):
                depth += lines[j].count("{") - lines[j].count("}")
                if depth == 0 and j > i:
                    return i, j, lines[i + 1:j]
            raise SystemExit("未闭合: %s" % opener)
    raise SystemExit("找不到: %s" % opener)


def split_members(lines, start, end):
    re_member = re.compile(r"^    (?:async\s+)?([A-Za-z_$][\w$]*)\s*[:(]")
    starts = []
    for i in range(start, end):
        m = re_member.match(lines[i])
        if m:
            starts.append((i, m.group(1)))
    chunks = []
    for k, (i, name) in enumerate(starts):
        j = starts[k + 1][0] if k + 1 < len(starts) else end
        chunks.append([name, lines[i:j]])
    out, carry = [], []
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


def members_dict(lines, opener):
    o, c, _ = find_block(lines, opener)
    return dict(split_members(lines, o + 1, c))


def slice_lines(a, b):
    """1 基闭区间切片（stacks-forms.js）。"""
    return FORMS_LINES[a - 1:b]


# ==========================================================================
# 样式命名空间前缀化（单遍替换，避免二次改写）
# ==========================================================================
AVATARS = ["redis", "kafka", "es", "rabbitmq", "rocketmq", "nacos", "powerjob", "bigdata"]
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


# ==========================================================================
# 1. 模板切片
# ==========================================================================
TPL_RANGES = [("hero", 7, 76), ("wizard", 77, 260), ("preflight", 261, 304),
              ("instance", 305, 370), ("verify", 371, 554), ("run", 555, 592)]

TPL_SUBS = [
    ('v-if="isEsCwh(it)"', 'v-if="instTierStyle(it)"'),
    ('<span v-for="t in instTierTags(it)" :key="t" class="es-tier-chip">{{t}}</span>',
     '<span v-for="t in instTierStyle(it).items" :key="t" :class="instTierStyle(it).cls">{{t}}</span>'),
    ('v-if="isBigdataVerify && verifyActiveTab !== \'all\'"',
     'v-if="verifyTabbed && verifyActiveTab !== \'all\'"'),
    ('v-if="isBigdataVerify"', 'v-if="verifyTabbed"'),
    ('label="容器实例"', ':label="verifyCompCountLabel"'),
    (':label="verifyTarget?.stack_key === \'redis\' ? \'复制\' : \'实例\'"', ':label="verifyCountLabel"'),
    ('v-if="esVerifyHostRole(hg)"', 'v-if="verifyHostRole(hg)"'),
    ('{{esVerifyHostRole(hg)}}', '{{verifyHostRole(hg)}}'),
]


def build_tpl_parts():
    parts = {name: "\n".join(SRC_LINES[a - 1:b]) for name, a, b in TPL_RANGES}
    parts["run"] += "\n</div>"
    expect = "\n" + "\n".join(SRC_LINES[6:592] + ["</div>"])
    for old, new in TPL_SUBS:
        expect = expect.replace(old, new)
    expect = rename(expect)
    for name in parts:
        for old, new in TPL_SUBS:
            parts[name] = parts[name].replace(old, new)
        parts[name] = rename(parts[name])
    joined = "\n" + "\n".join(parts[r[0]] for r in TPL_RANGES)
    if joined != expect:
        for i, (x, y) in enumerate(zip(joined, expect)):
            if x != y:
                raise SystemExit("模板重建不一致 @%d:\n  got %r\n  exp %r"
                                 % (i, joined[max(0, i - 80):i + 80], expect[max(0, i - 80):i + 80]))
        raise SystemExit("模板重建长度不一致 %d vs %d" % (len(joined), len(expect)))
    return parts


# ==========================================================================
# 2. computed / methods 分桶
# ==========================================================================
# 迁往套件目录（骨架改为通用分发），不再进骨架分桶
MOVED_TO_SUITE = {
    "esRoleLabel", "isEsCwh", "hostRolesTags", "instTierTags", "esMemberRoles",
    "caDownloadAvailable", "downloadCa", "esVerifyHostRole",
    "isBigdataVerify", "COMP_LABELS", "verifyComponents", "currentVerifyCompLabel",
    "verifyCompEndpoints", "verifyCompEndpointsByHost", "verifyCompHostRows",
    "instIsHa", "haDrawnRoles", "memTags",
}

BUCKETS = {
    "labels.js": {
        "methods": ["escHtml", "tipHtml", "opLabel", "modeLabel", "roleLabel", "phaseName",
                    "phaseText", "phaseTag", "hostStatusCls", "hostStatusText", "logTime",
                    "taskTagType", "taskStatusLabel", "formatTime",
                    "epProtocolLabel", "epProtocolClass", "copyText"],
    },
    "host-table.js": {
        "computed": ["filteredHosts", "hostVars", "visibleSharedVars", "selectedHosts",
                     "minHosts", "hostHint"],
        "methods": ["onRoleHostSelection", "syncRoleHostTableSelection", "toggleHost",
                    "toggleSelectAll", "toggleHostSortOrder", "sortHostList",
                    "hostVarPlaceholder", "hostParamValue", "onHostParam", "copyFirstToAll"],
    },
    "role-plan.js": {
        "computed": ["needMaster", "needOpPlan", "useRolePlan"],
        "methods": ["syncMasters", "isHaMode", "mastersForSubmit", "resolveMasterHostId",
                    "hostRolePlanSummary", "ensureDefaultRoles", "roleTextClass",
                    "refreshOpPlan", "replanInst", "hostRoleTag"],
    },
    "instance-card.js": {
        "methods": ["instCompTags", "instDisplayComps", "installedCompDefs", "removeOneComp",
                    "compsOf", "unusedCompsOf", "removableCompsOf", "busyInst", "instFailed",
                    "instReady", "instUninstalled", "canReinstall", "isPlatformStack",
                    "activeInstHosts", "isContainerStack", "runtimeLabel", "stackAvatar",
                    "compLabel", "instStatusType", "instStatusLabel", "applyInstanceContext",
                    "prepareInstance", "openWizard", "openScaleOut", "openScaleIn",
                    "openAddComp", "openRemoveComp", "openInstance", "loadInstPlan",
                    "renameInstance", "reinstallInstance", "uninstallInstance",
                    "purgeInstance", "deleteInstance"],
    },
    "verify-dialog.js": {
        "computed": ["verifyParams", "verifyPassword", "verifyEndpoints",
                     "verifyEndpointsByHost", "verifyHostRows"],
        "methods": ["openVerifyDialog", "runVerify"],
    },
    "run-log.js": {
        "computed": ["showBootstrapPill", "showPrereqPill"],
        "methods": ["onLogDrawerBeforeClose", "connectSetup", "closeSetup", "openDrawer",
                    "closeDrawer", "connectLog", "disconnectLog", "onLogScroll",
                    "scrollLogsBottom"],
    },
    "wizard.js": {
        "computed": ["selectedStack", "stackGroups", "selectedMode", "selfCtx", "formEntry",
                     "step1FormComp", "opFormComp", "step2FormComp", "step1Hints", "wizardTitle",
                     "wizardFirstStepTitle", "wizardStepIndex", "defaultClusterName",
                     "unusedWizardComps", "canStep2", "canStep3", "canRun", "wizardHint",
                     "runningCount", "logDrawerLocked", "preflightReady", "filteredLogs"],
        "methods": ["loadStacks", "loadHosts", "loadInstances", "resetWizard", "selectStack",
                    "formToggle", "onModeChange", "onBoolVar", "onHaToggle", "goStep",
                    "startPreflight", "confirmRun", "confirmScaleIn", "confirmRemoveComp",
                    "submitWizard", "afterStackOp"],
    },
}

FILE_HEADERS = {
    "labels.js": "通用文案与格式化：标签映射、日志时间、探活协议徽标、复制",
    "host-table.js": "主机表与逐主机参数：筛选 / 排序 / 勾选 / 参数编辑",
    "role-plan.js": "角色计划：主角色落点、预览、缩容保护与角色文案",
    "instance-card.js": "实例卡片与实例抽屉：状态、组件标签、生命周期操作",
    "verify-dialog.js": "探活对话框：端点聚合与节点结果表（套件差异经 hooks 注入）",
    "run-log.js": "运行日志抽屉与双 SSE（运行轨迹 + 日志流）",
    "wizard.js": "向导骨架：步骤流转、套件/模式选择、参数装配与提交",
}

# 被改写为「按槽位分发」的骨架成员
OVERRIDES = {
    "wizardHint": '''    wizardHint() {
      if (this.step === 2 && !this.canStep3) {
        // 套件专属拦截提示（冷热温缺 master / 大数据缺 NameNode）；未实现则给通用文案
        const hk = this.hook(this.selectedKey, 'step2BlockedHint')
        const msg = hk ? (hk.call(this, this) || '') : ''
        if (msg) return msg
        return '主机数量或角色规划不满足当前模式'
      }
      if (this.step === 3 && !this.canRun) {
        const miss = this.visibleSharedVars.find(v => v.required && !(this.sharedParams[v.name] || v.default || '').trim())
        return miss ? '请填写' + miss.label : '参数未填写完整'
      }
      return ''
    },'''.split("\n"),
    "verifyEndpoints": '''    verifyEndpoints() {
      const it = this.verifyTarget
      const extractIP = (url) => { const m = url && url.match(/(\\d+\\.\\d+\\.\\d+\\.\\d+)/); return m ? m[1] : url }
      const inferRole = (name) => { if (/主\\b|master/i.test(name)) return 'master'; if (/从\\b|replica|slave/i.test(name)) return 'replica'; return '' }
      if (!it) return (this.verifyResult?.endpoints || []).map(ep => ({ name: ep.name, component: ep.component, url: ep.url, role: ep.role || inferRole(ep.name), host_ip: extractIP(ep.url) }))
      if (this.verifyResult?.endpoints?.length) {
        return this.verifyResult.endpoints.map(ep => ({ name: ep.name, component: ep.component, url: ep.url, role: ep.role || inferRole(ep.name), host_ip: extractIP(ep.url) }))
      }
      const hosts = (it.hosts || []).filter(h => h.status !== 'removed')
      const p = this.verifyParams
      const port = p.port || '6379'
      const out = []
      hosts.forEach(h => {
        let hp = {}
        try { hp = JSON.parse(h.params_json || '{}') || {} } catch (e) { hp = {} }
        const pt = hp.port || port
        // 套件专属端点合成（redis 双协议 / bigdata 按组件拆分）；未实现则走通用 tcp
        const hk = this.hook(it.stack_key, 'endpointFor')
        const eps = hk ? hk.call(this, this, h, hp, p, pt) : null
        if (eps) { eps.forEach(e => out.push(e)); return }
        out.push({ name: (this.roleLabel(h.role) || '节点'), url: 'tcp://' + h.host_ip, role: h.role, host_ip: h.host_ip, host_name: h.host_name })
      })
      return out
    },'''.split("\n"),
    "hostRoleTag": '''    hostRoleTag(h) {
      const isMaster = h.id === this.masterHostId
      const t = this.formEntry?.roleTag?.(this, isMaster)
      if (t) return t
      const hk = this.hook(this.selectedKey, 'memberRoleTag')
      const v = hk ? (hk.call(this, this, isMaster) || '') : ''
      if (v) return v
      if (isMaster) return '主'
      return '工作节点'
    },'''.split("\n"),
}

# 骨架新增的通用分发点（各文件以第二个 mixin 追加）
ADDITIONS = {
    "wizard.js": '''    // —— 套件能力分发（骨架不认识任何套件）——
    drv(key) { return (window.StackDrivers && window.StackDrivers[key]) || null },
    hook(key, name) {
      const d = this.drv(key)
      const h = d && d.hooks && d.hooks[name]
      return typeof h === 'function' ? h : null
    },'''.split("\n"),
    "instance-card.js": '''    // —— 套件专属展示的分发点 ——
    instKey() { const it = this.instDetail || this.verifyTarget; return (it && it.stack_key) || '' },
    instIsHa(it) { const h = this.hook(it && it.stack_key, 'instIsHa'); return h ? !!h.call(this, this, it) : false },
    memTags(h) { const hk = this.hook(this.instKey(), 'memTags'); return hk ? hk.call(this, this, h) : [] },
    instTierStyle(it) { const h = this.hook(it && it.stack_key, 'instTierStyle'); return h ? h.call(this, this, it) : null },
    caDownloadAvailable() { const h = this.hook(this.instKey(), 'caDownloadAvailable'); return h ? !!h.call(this, this) : false },
    async downloadCa() { const h = this.hook(this.instKey(), 'downloadCa'); if (h) return h.call(this, this) },'''.split("\n"),
    "verify-dialog.js": '''    // —— 套件专属展示的分发点（探活）——
    verifyTabbed() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyTabbed'); return h ? !!h.call(this, this) : false },
    verifyComponents() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyComponents'); return h ? h.call(this, this) : [] },
    currentVerifyCompLabel() { const h = this.hook(this.verifyTarget?.stack_key, 'currentVerifyCompLabel'); return h ? h.call(this, this) : '' },
    verifyCompEndpoints() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompEndpoints'); return h ? h.call(this, this) : [] },
    verifyCompEndpointsByHost() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompEndpointsByHost'); return h ? h.call(this, this) : [] },
    verifyCompHostRows() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompHostRows'); return h ? h.call(this, this) : [] },
    verifyCountLabel() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCountLabel'); return (h && h.call(this, this)) || '实例' },
    verifyCompCountLabel() { const h = this.hook(this.verifyTarget?.stack_key, 'verifyCompCountLabel'); return (h && h.call(this, this)) || '实例' },
    verifyHostRole(hg) { const h = this.hook(this.verifyTarget?.stack_key, 'verifyHostRole'); return h ? (h.call(this, this, hg) || '') : '' },'''.split("\n"),
}


def ens_comma(body):
    """成员块末尾补逗号（源文件中最后一个成员没有尾逗号，搬移后必须补上）。"""
    body = list(body)
    tail = []
    while body and (body[-1].strip() == "" or body[-1].strip().startswith("//")):
        tail.insert(0, body.pop())
    if body and not body[-1].rstrip().endswith(","):
        body[-1] = body[-1] + ","
    return body + tail


def build_mixins():
    c = members_dict(SRC_LINES, "  computed: {")
    m = members_dict(SRC_LINES, "  methods: {")
    used = set()
    files = {}
    for fname, spec in BUCKETS.items():
        blocks = []
        for kind, names in spec.items():
            src = c if kind == "computed" else m
            body = []
            for n in names:
                if n not in src:
                    raise SystemExit("成员缺失: %s.%s" % (fname, n))
                used.add((kind, n))
                if n in OVERRIDES:
                    body.extend(ens_comma(OVERRIDES[n]))
                else:
                    body.extend(ens_comma(src[n]))
            blocks.append((kind, body))
        files[fname] = blocks
    missing = [(k, n) for k in ("computed", "methods")
               for n in (c if k == "computed" else m)
               if (k, n) not in used and n not in MOVED_TO_SUITE]
    if missing:
        raise SystemExit("有成员未分桶: %s" % missing)
    moved = [(k, n) for k in ("computed", "methods")
             for n in (c if k == "computed" else m)
             if (k, n) not in used and n in MOVED_TO_SUITE]
    print("  已分发到套件目录的成员 %d 个" % len(moved))
    return files


def emit_mixin(fname, blocks):
    out = ["// 套件部署页 · 通用骨架 · %s" % FILE_HEADERS[fname],
           "// 迁自 pages/stacks.js（逐字保留），套件专属分支改为按槽位分发。",
           ";(function () {",
           "  const P = (window.StacksParts = window.StacksParts || {})",
           "  // 源文件模块级助手（逗号列表 → 数组）：骨架统一提供在 StacksParts.splitList",
           "  const splitList = P.splitList",
           "  P.mixins.push({"]
    for kind, body in blocks:
        out.append("    %s: {" % kind)
        out.extend(rename("\n".join(body)).split("\n"))
        out.append("    },")
    out += ["  })"]
    if fname in ADDITIONS:
        out += ["", "  P.mixins.push({", "    methods: {"] + ADDITIONS[fname] + ["    }", "  })"]
    out += ["})()", ""]
    return "\n".join(out)


# ==========================================================================
# 3. 套件文件
# ==========================================================================
def forms_entry(key):
    for i, ln in enumerate(FORMS_LINES):
        if ln.rstrip() == "    %s: {" % key:
            depth = 0
            for j in range(i, len(FORMS_LINES)):
                depth += FORMS_LINES[j].count("{") - FORMS_LINES[j].count("}")
                if depth == 0 and j > i:
                    return FORMS_LINES[i + 1:j]
    return None


def emit_forms(key, suite=None):
    entry = forms_entry(key)
    if entry is None:
        return ["    forms: {},"]
    return ["    forms: {"] + rename("\n".join(entry), suite).split("\n") + ["    },"]


def emit_component(key, comp_name, global_name, lines, header=None):
    """套件独有组件：原样迁入 + 自注册（套件内 script 顺序因此不影响装配）。"""
    body = rename("\n".join(lines), key).split("\n")
    head = header or ("该套件独有的表单向导组件；app.js 按 window.%s 全局注册，名称保持不变。" % global_name)
    reg = [
        "",
        "// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配，",
        "// 也不再依赖 index.js 的加载时机。",
        ";(function () {",
        "  const P = (window.StacksParts = window.StacksParts || {})",
        "  P.register('%s', { components: { '%s': window.%s } })" % (key, comp_name, global_name),
        "})()",
        "",
    ]
    return ["// " + head] + body + reg


def write(rel, text):
    p = OUT / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text, encoding="utf-8", newline="\n")
    return p


PARTS_JS = """// 套件前端装配容器（设计文档 §4.2 common/）。
//
// 分工：每个 common/*.js 只把自己那部分 computed / methods push 进 window.StacksParts.mixins，
// 模板片段登记到 window.StacksParts.tpl；挂载壳 pages/stacks.js 负责拼装成 window.StacksPage。
//
// 套件差异一律经 window.StackDrivers 注入，骨架不认识任何套件：
//   hooks       骨架扩展点（按槽位名分发，未实现的套件走通用默认）
//   components  该套件独有的 Vue 组件（同时挂 window.StackFormXxx 供 app.js 全局注册）
//   hints       第 1 步底部提示文案
//   forms       表单向导插槽（原 window.StackForms[key]，语义不变）
;(function () {
  window.StacksParts = {
    mixins: [],
    tpl: {},
    tplOrder: ['hero', 'wizard', 'preflight', 'instance', 'verify', 'run'],

    // 逗号列表 → 数组（探活表格的省略号 + tooltip 展示用）
    splitList: (s) => String(s == null ? '' : s).split(/,\\s*/).map(x => x.trim()).filter(Boolean),

    // 套件注册：可多次调用（index.js 登记表单/组件，select.js / verify.js 等追加 hooks）
    register(key, driver) {
      driver = driver || {}
      const D = (window.StackDrivers = window.StackDrivers || {})
      const F = (window.StackForms = window.StackForms || {})
      const prev = D[key] || {}
      const forms = Object.assign({}, prev.forms || {}, driver.forms || {})
      D[key] = {
        key: key,
        forms: forms,
        hooks: Object.assign({}, prev.hooks || {}, driver.hooks || {}),
        components: Object.assign({}, prev.components || {}, driver.components || {}),
        hints: driver.hints !== undefined ? driver.hints : (prev.hints || forms.hints || [])
      }
      F[key] = forms
      return D[key]
    },

    // 汇总各套件的独有组件为页面局部组件表
    componentsOf() {
      const out = {}
      Object.keys(window.StackDrivers || {}).forEach(k => {
        const cs = (window.StackDrivers[k] || {}).components || {}
        Object.keys(cs).forEach(n => { if (cs[n]) out[n] = cs[n] })
      })
      return out
    },

    // 静态引入清单（设计文档 §5.4 方案 A；同时留给后续按需加载评估用）
    assetsOf() {
      return Object.keys(window.StackDrivers || {}).sort().map(k => ({
        key: k,
        script: '/static/stacks/' + k + '/index.js',
        style: '/static/stacks/' + k + '/style.css'
      }))
    },

    // 模板片段按渲染顺序拼装，与拆分前 pages/stacks.js 的单串逐字节一致
    template() {
      return '\\n' + this.tplOrder.map(k => this.tpl[k] || '').join('\\n')
    }
  }
})()
"""

REGISTRY_JS = """// 套件前端注册表（设计文档 §4.2 / §5.4）。
//
// window.StackDrivers 由各套件目录的 index.js 注册；本文件提供容器与启动冒烟断言。
// 兼容：window.StackForms 仍是「表单向导插槽」视图（骨架与 app.js 均按它取用），
// 由 StacksParts.register 同步写入，故 app.js 的全局组件注册无需改动。
;(function () {
  window.StackDrivers = window.StackDrivers || {}
  window.StackForms = window.StackForms || {}

  // 9 个内置套件必须全部注册：漏引某个 <script> 时立刻在控制台暴露，
  // 而不是等用户点开该套件向导才发现交互缺失。
  window.StacksRegistryReady = function () {
    const want = ['redis', 'bigdata', 'kafka', 'elasticsearch', 'rabbitmq',
                  'rocketmq', 'nacos', 'powerjob', 'elfk']
    const miss = want.filter(k => !window.StackDrivers[k])
    if (miss.length) console.error('[stacks] 套件前端未注册：' + miss.join('、'))
    return miss
  }
})()
"""

ASSETS_JS = """// 前端资源登记（设计文档 §4.2 assets.js）。
//
// 页面对套件资源一律**静态引入**（index.html 按套件分组），无运行时注入；
// 这里登记资源清单，供后续评估「按需加载」方案（§5.4 方案 B）时直接取用。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  window.StacksAssets = {
    common: [
      '/static/stacks/registry.js',
      '/static/stacks/common/parts.js',
      '/static/stacks/common/common.css'
    ],
    stacks() { return P.assetsOf() }
  }
})()
"""

SHELL_JS = """// 套件部署页 · 挂载壳（设计文档 §4.2 的终态形态：只保留挂载与路由，<= 120 行）。
//
// 页面本体 = 通用骨架（/static/stacks/common/ 的 mixins + 模板片段）
//          + 各套件差异（/static/stacks/<key>/，经 window.StackDrivers 的 hooks / components 注入）。
// 本文件只做装配与启动冒烟，不含任何套件分支。必须在全部 /static/stacks/** 之后加载。
;(function () {
  const P = window.StacksParts
  if (!P) {
    console.error('[stacks] 通用骨架未加载：请检查 /static/stacks/common/ 的 script 顺序')
    return
  }

  // 启动冒烟：9 个套件 key 必须齐全（缺少时控制台报错，便于发现漏引文件）
  if (window.StacksRegistryReady) window.StacksRegistryReady()

  window.StacksPage = {
    props: ['page', 'user', 'versionData'],
    components: P.componentsOf(),
    template: P.template(),
    mixins: P.mixins
  }
})()
"""

# --------------------------------------------------------------------------
# 套件 hooks（自 pages/stacks.js 对应分支迁入，逻辑逐字保留）
# --------------------------------------------------------------------------
ES_VERIFY = """// Elasticsearch 套件 · 探活与角色标签扩展点。
// 冷热温（cold_warm_hot）的分层角色是 ES 独有语义；其余展示一律交还骨架的通用主/从。
// 逻辑自 pages/stacks.js 的 ES 分支原样迁入。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})

  const ES_ROLE_LABEL = {
    master: 'master 候选', coordinator: '纯协调',
    data_hot: '数据-hot', data_warm: '数据-warm', data_cold: '数据-cold'
  }
  const esRoleLabel = (r) => ES_ROLE_LABEL[r] || r
  const isEsCwh = (it) => !!it && it.stack_key === 'elasticsearch' && it.mode === 'cold_warm_hot'
  const hostRolesTags = (h) => {
    let hp = {}
    try { hp = JSON.parse((h && h.params_json) || '{}') || {} } catch (e) { hp = {} }
    const roles = String(hp.roles || '').split(',').map(s => s.trim()).filter(Boolean)
    return roles.length ? roles.map(r => esRoleLabel(r)) : []
  }
  // 实例卡片/抽屉：冷热温实例展示分层分布（数据-hot/warm/cold 聚合；含 master/协调）
  const instTierTags = (it) => {
    if (!isEsCwh(it)) return []
    const seen = new Set()
    ;(it.hosts || []).forEach(h => hostRolesTags(h).forEach(t => seen.add(t)))
    const order = ['数据-hot', '数据-warm', '数据-cold', 'master 候选', '纯协调']
    return order.filter(t => seen.has(t))
  }

  P.register('elasticsearch', {
    hooks: {
      // 实例卡片标签：冷热温改用分层 chip（类名由套件给出，骨架不认识 ES）
      instTierStyle(it) {
        return isEsCwh(it) ? { cls: 'stk-es-tier-chip', items: instTierTags(it) } : null
      },
      // 抽屉成员标签：冷热温取角色分层
      memTags(h) {
        return isEsCwh(this.instDetail) ? hostRolesTags(h) : []
      },
      // 冷热温 + SSL 开启时可下载 CA 证书
      caDownloadAvailable() {
        const it = this.instDetail || this.verifyTarget
        if (!isEsCwh(it)) return false
        try { return JSON.parse(it.params_json || '{}').ssl_enabled === 'true' } catch (e) { return false }
      },
      async downloadCa() {
        const it = this.instDetail || this.verifyTarget
        if (!it) return
        try {
          const text = await window.api.get('/stacks/instances/' + it.id + '/ca', { responseType: 'text', timeout: 30000 })
          const str = String(text || '')
          if (!str.includes('BEGIN CERTIFICATE')) { ElMessage.error('未获取到 CA 证书'); return }
          const blob = new Blob([str], { type: 'application/x-pem-file' })
          const url = URL.createObjectURL(blob)
          const a = document.createElement('a'); a.href = url; a.download = (it.name || 'es') + '-ca.crt'; a.click()
          URL.revokeObjectURL(url)
          ElMessage.success('已下载 CA 证书')
        } catch (e) { ElMessage.error(e.message || '下载 CA 失败') }
      },
      // 探活接入地址主机头：冷热温按实际分层角色展示（其余套件交还骨架的主/从）
      verifyHostRole(hg) {
        if (!isEsCwh(this.verifyTarget)) return ''
        const parts = []
        const seen = new Set()
        ;(hg.endpoints || []).forEach(ep => {
          const r = (ep.role || '').trim()
          if (r && !seen.has(r)) { seen.add(r); parts.push(esRoleLabel(r)) }
        })
        return parts.join(' / ')
      }
    }
  })
})()
"""

ES_CLUSTER = """// Elasticsearch 套件 · 第 2 步拦截提示（冷热温：先有 master 候选，再有数据层）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', {
    hooks: {
      step2BlockedHint() {
        if (this.mode !== 'cold_warm_hot') return ''
        const hasMaster = this.selectedHosts.some(h => {
          const hp = this.hostParams[h.id] || {}
          return String(hp.roles || '').split(',').map(s => s.trim()).includes('master')
        })
        if (!hasMaster) return '请在上方主机行勾选至少 1 台「master 候选」（数量已满足，最少 ' + this.minHosts + ' 台）'
        return '请在上方主机行勾选数据层角色（hot/warm/cold 至少 1 台）'
      }
    }
  })
})()
"""

BD_VERIFY = """// 大数据底座套件 · 探活（多组件标签式）与实例角色展示扩展点。
// 逻辑自 pages/stacks.js 的 bigdata 分支原样迁入。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  const splitList = P.splitList

  const COMP_LABELS = { hdfs: 'HDFS', zookeeper: 'ZooKeeper', yarn: 'YARN', spark: 'Spark', flink: 'Flink', hive: 'Hive', hbase: 'HBase', trino: 'Trino' }
  const isBigdata = (ctx) => ctx.verifyTarget?.stack_key === 'bigdata'

  function verifyComponents(ctx) {
    if (!isBigdata(ctx)) return []
    const order = (ctx.verifyParams.components || '').split(',').map(s => s.trim()).filter(Boolean)
    const seen = new Set(), keys = []
    ;[...order, ...(ctx.verifyResult?.endpoints || []).map(e => e.component)]
      .forEach(k => { if (k && !seen.has(k)) { seen.add(k); keys.push(k) } })
    const L = COMP_LABELS
    return keys.map(k => ({ key: k, label: L[k] || k.toUpperCase() }))
  }
  function currentVerifyCompLabel(ctx) {
    const c = verifyComponents(ctx).find(c => c.key === ctx.verifyActiveTab)
    return c ? c.label : ''
  }
  function verifyCompEndpoints(ctx) {
    const comp = ctx.verifyActiveTab
    if (!comp || comp === 'all') return []
    return ctx.verifyEndpoints.filter(ep => ep.component === comp)
  }
  function verifyCompEndpointsByHost(ctx) {
    const eps = verifyCompEndpoints(ctx)
    const map = new Map()
    eps.forEach(ep => {
      const key = ep.host_ip || ep.url
      if (!map.has(key)) {
        map.set(key, { host_ip: key, host_name: ep.host_name || key, endpoints: [] })
      }
      map.get(key).endpoints.push(ep)
    })
    return Array.from(map.values())
  }
  function verifyCompHostRows(ctx) {
    const comp = ctx.verifyActiveTab
    if (!comp || comp === 'all') return []
    const fullMap = Object.fromEntries(ctx.verifyHostRows.map(r => [r.ip, r]))
    const out = []
    for (const h of (ctx.verifyResult?.hosts || [])) {
      const compChecks = (h.checks || []).filter(c => c.component === comp)
      if (!compChecks.length) continue
      const f = fullMap[h.host_ip] || {}
      const instances = compChecks.filter(c => c.ok).map(c => c.name.replace(/^容器\\s*/, ''))
      const instStr = instances.length ? instances.join(', ') : '无'
      const uptimeStr = f.uptime || '-'
      out.push({
        ip: h.host_ip, host_name: h.host_name,
        ok: compChecks.every(c => c.ok),
        role: f.role || '-',
        instances: instStr,
        instList: instances.length > 1 ? instances : [],
        version: f.version || '-',
        uptime: uptimeStr,
        uptimeList: uptimeStr !== '-' ? splitList(uptimeStr) : []
      })
    }
    return out
  }
  // 集群实例是否以 HA 模式创建
  function instIsHa(ctx, it) {
    if (!it || it.stack_key !== 'bigdata') return false
    try { return JSON.parse(it.params_json || '{}').ha === 'true' } catch (e) { return false }
  }
  // 实例抽屉：按主机展示 HA 全角色标签（镜像后端确定性分配，仅用于展示）
  function haDrawnRoles(ctx, ip) {
    const it = ctx.instDetail
    if (!it || it.stack_key !== 'bigdata' || !instIsHa(ctx, it)) return []
    let params, masters
    try { params = JSON.parse(it.params_json || '{}'); masters = JSON.parse(params.masters || '{}') || {} } catch (e) { return [] }
    const comps = (params.components || '').split(',').map(x => x.trim()).filter(Boolean)
    const hosts = (it.hosts || []).filter(x => x.status !== 'removed').slice().sort((a, b) => (a.seq || 0) - (b.seq || 0))
    const ips = hosts.map(h => h.host_ip)
    const primary = (hosts.find(h => h.role === 'master') || {}).host_ip || ips[0] || ''
    const primaryOf = comp => masters[comp] || primary
    const secondaryOf = (comp, key) => masters[key] || (ips.find(i => i !== primaryOf(comp)) || '')
    const inComps = c => comps.includes(c)
    const out = []
    if (inComps('hdfs')) {
      if (ip === primaryOf('hdfs')) out.push('NN1')
      else if (ip === secondaryOf('hdfs', 'hdfs_nn2')) out.push('NN2·Standby')
      else if ((masters.hdfs_jns ? String(masters.hdfs_jns).split(',') : ips.slice(0, 3)).includes(ip)) out.push('JN')
    }
    if (inComps('yarn') && ip === primaryOf('yarn')) out.push('RM1')
    if (inComps('yarn') && ip === secondaryOf('yarn', 'yarn_rm2')) out.push('RM2·Standby')
    if (inComps('spark') && ip === primaryOf('spark')) out.push('Master-1')
    if (inComps('spark') && ip === secondaryOf('spark', 'spark_m2')) out.push('Master-2')
    if (inComps('flink') && ip === primaryOf('flink')) out.push('JM1')
    if (inComps('flink') && ip === secondaryOf('flink', 'flink_jm2')) out.push('JM2·Standby')
    if (inComps('hbase') && ip === primaryOf('hbase')) out.push('HMaster-1')
    if (inComps('hbase') && ip === secondaryOf('hbase', 'hbase_hm2')) out.push('HMaster-2')
    if (inComps('hive')) {
      const ms = [primaryOf('hive')]; if (String(masters.hive_ms2 || '')) ms.push(masters.hive_ms2)
      const hs = [primaryOf('hive')]; if (String(masters.hive_hs2b || '')) hs.push(masters.hive_hs2b)
      if (ms.includes(ip)) out.push(ms[1] === ip ? 'MS2·Standby' : 'Metastore')
      if (hs.includes(ip)) out.push(hs[1] === ip ? 'HS2·Standby' : 'HiveServer2')
      if (ip === (masters.hive_db || primaryOf('hive'))) out.push('MetaDB')
    }
    if (inComps('zookeeper')) {
      const zk = masters.zookeeper_ips ? String(masters.zookeeper_ips).split(',') : ips.slice(0, 3)
      const idx = zk.indexOf(ip)
      if (idx >= 0) out.push('ZK-' + (idx + 1))
    }
    return out
  }

  P.register('bigdata', {
    hooks: {
      verifyTabbed() { return isBigdata(this) },
      verifyComponents() { return verifyComponents(this) },
      currentVerifyCompLabel() { return currentVerifyCompLabel(this) },
      verifyCompEndpoints() { return verifyCompEndpoints(this) },
      verifyCompEndpointsByHost() { return verifyCompEndpointsByHost(this) },
      verifyCompHostRows() { return verifyCompHostRows(this) },
      // 组件视角的「容器实例」列名（整体视角沿用骨架的「实例」）
      verifyCompCountLabel() { return '容器实例' },
      instIsHa(it) { return instIsHa(this, it) },
      memTags(h) { return haDrawnRoles(this, h.host_ip) },
      // 新建时 NameNode 必须明确指定（角色规划的锚点）
      step2BlockedHint() {
        if (this.useRolePlan && this.wizardOp === 'create' && this.bdSel.includes('hdfs') &&
          (!this.masters.hdfs || !this.selectedHostIds.has(this.masters.hdfs))) {
          return '请勾选主机并指定 HDFS NameNode'
        }
        return ''
      }
    }
  })
})()
"""

REDIS_HOOKS = """// Redis 套件 · 扩展点：成员角色文案、探活端点合成、主从「复制」列名。
// 逻辑自 pages/stacks.js 的 redis 分支原样迁入。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('redis', {
    hooks: {
      // 主从 / 哨兵模式下非主节点为「从」；集群模式交还骨架的「工作节点」
      memberRoleTag(isMaster) {
        if (isMaster) return ''
        return (this.mode === 'replication' || this.mode === 'sentinel') ? '从' : ''
      },
      // 探活接入地址：redis:// + 哨兵模式的 redis-sentinel://
      endpointFor(h, hp, p, pt) {
        const it = this.verifyTarget
        const isMaster = h.role === 'master'
        const name = (it.mode === 'replication' || it.mode === 'sentinel')
          ? (isMaster ? 'Redis 主' : 'Redis 从')
          : 'Redis'
        const out = [{ name, url: 'redis://' + h.host_ip + ':' + pt, role: h.role, host_ip: h.host_ip, host_name: h.host_name }]
        if (it.mode === 'sentinel') {
          out.push({ name: 'Sentinel', url: 'redis-sentinel://' + h.host_ip + ':' + (hp.sentinel_port || p.sentinel_port || '26379'), role: h.role, host_ip: h.host_ip, host_name: h.host_name })
        }
        return out
      },
      // 节点结果表的实例列：redis 下语义为「复制」
      verifyCountLabel() { return '复制' }
    }
  })
})()
"""

ES_INDEX = """// Elasticsearch 套件 · 前端入口（设计文档 §4.2 目录树）。
// forms 原样迁自 pages/stacks-forms.js；hooks 在 verify.js / cluster.js；
// 组件（stack-form-elasticsearch-roles）在 roles.js 内自注册。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('elasticsearch', {
{forms}
    components: {}
  })
})()
"""

BD_INDEX = """// 大数据底座套件 · 前端入口。
// 组件多选 / 加装卸载 / HA 全角色矩阵是本套件独有形态：
//   select.js 组件多选、op.js 加装卸载、roles.js 角色与 HA 矩阵、verify.js 组件化探活（均自注册）。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
{prelude}
  P.register('bigdata', {
{forms}
    components: {}
  })
})()
"""

REDIS_INDEX = """// Redis 套件 · 前端入口。
// 三模式（replication / sentinel / cluster）的差异落在 forms 插槽与本目录 hooks.js / topo.js。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('redis', {
{forms}
    components: {}
  })
})()
"""

SIMPLE_INDEX = """// {zh} 套件 · 前端入口。
// 该套件没有独有的表单向导组件与展示分支：全流程走通用骨架，
// 差异仅由 forms 的插槽（roleTag / hints 等）与后端蓝图驱动。
;(function () {
  const P = (window.StacksParts = window.StacksParts || {})
  P.register('{key}', {
{forms}
    components: {}
  })
})()
"""

SIMPLE_CSS = """/* {zh} 套件样式表（设计文档 §4.2 / §5.4：每套件一份 css、前缀 .stk-<key简写>-*）。
   该套件当前没有独立于通用骨架的样式；保留本文件以维持目录约定，
   后续该套件新增样式一律写在这里，不再回填全站 style.css。 */
"""


def main():
    tpl = build_tpl_parts()
    mixins = build_mixins()

    for name in [r[0] for r in TPL_RANGES]:
        write("common/tpl-%s.js" % name,
              "// 套件部署页模板片段 · %s（原样迁入；套件专属表达式已改为通用分发名，类名统一 .stk-*）。\n"
              ";(function () {\n  const P = (window.StacksParts = window.StacksParts || {})\n"
              "  P.tpl.%s = `%s`\n})()\n" % (name, name, tpl[name]))

    write("common/parts.js", PARTS_JS)
    write("registry.js", REGISTRY_JS)
    write("common/assets.js", ASSETS_JS)

    for fname in BUCKETS:
        write("common/%s" % fname, emit_mixin(fname, mixins[fname]))

    # wizard.js 追加页面的 data / 生命周期 / watch
    _, _, data_inner = find_block(SRC_LINES, "  data() {")
    _, _, watch_inner = find_block(SRC_LINES, "  watch: {")
    mounted_txt = [l for l in SRC_LINES if l.startswith("  mounted()")][0]
    unmount_txt = [l for l in SRC_LINES if l.startswith("  beforeUnmount()")][0]
    parts = ["  data() {"] + data_inner + ["  },", mounted_txt, "  watch: {"] + watch_inner + ["  },", unmount_txt]
    inject = "\n".join(parts) + "\n"
    p = OUT / "common" / "wizard.js"
    txt = p.read_text(encoding="utf-8")
    txt = txt.replace("  P.mixins.push({\n", "  P.mixins.push({\n" + inject, 1)
    p.write_text(txt, encoding="utf-8", newline="\n")

    # ---- 套件目录 ----
    write("elasticsearch/index.js", ES_INDEX.replace(
        "{forms}", "\n".join(emit_forms("elasticsearch", "elasticsearch"))))
    write("elasticsearch/roles.js",
          "// Elasticsearch 套件 · 冷热温节点角色勾选（原 StackFormElasticsearchRoles）。\n"
          "// 角色互斥规则与后端 validateCWHRoles 一致，逻辑原样迁入。\n"
          + rename("\n".join(slice_lines(553, 681)), "elasticsearch") + "\n"
          + "\n// 组件自注册（StacksParts.register 合并语义）：套件内 <script> 顺序不影响装配。\n"
          + ";(function () {\n"
          + "  const P = (window.StacksParts = window.StacksParts || {})\n"
          + "  P.register('elasticsearch', { components: { 'stack-form-elasticsearch-roles': window.StackFormElasticsearchRoles } })\n"
          + "})()\n")
    write("elasticsearch/verify.js", ES_VERIFY)
    write("elasticsearch/cluster.js", ES_CLUSTER)

    write("bigdata/index.js",
          BD_INDEX.replace("{forms}", "\n".join(emit_forms("bigdata", "bigdata")))
                  .replace("{prelude}", rename("\n".join(slice_lines(23, 44)), "bigdata")))
    write("bigdata/select.js", "\n".join(emit_component(
        "bigdata", "stack-form-bigdata-select", "StackFormBigdataSelect", slice_lines(225, 246))))
    write("bigdata/op.js", "\n".join(
        emit_component("bigdata", "stack-form-bigdata-op", "StackFormBigdataOp", slice_lines(252, 354)) + [""] +
        ["// metastore_db 等运行期组件的中文名"] +
        rename("\n".join(slice_lines(357, 359)), "bigdata").split("\n") + [""]))
    write("bigdata/roles.js", "\n".join(
        emit_component("bigdata", "stack-form-bigdata-roles", "StackFormBigdataRoles", slice_lines(362, 510)) + [""] +
        ["// 计算 HA 角色矩阵中「同组件主备同机」的冲突组（后端同样规则兜底拦截）"] +
        rename("\n".join(slice_lines(513, 534)), "bigdata").split("\n") + [""]))
    write("bigdata/verify.js", BD_VERIFY)

    write("redis/index.js", REDIS_INDEX.replace(
        "{forms}", "\n".join(emit_forms("redis", "redis"))))
    write("redis/topo.js", "\n".join(emit_component(
        "redis", "stack-form-redis-topo", "StackFormRedisTopo", slice_lines(537, 547))))
    write("redis/hooks.js", REDIS_HOOKS)

    for key, zh in (("elfk", "ELFK 编排"), ("kafka", "Kafka"), ("rabbitmq", "RabbitMQ"),
                    ("rocketmq", "RocketMQ"), ("nacos", "Nacos"), ("powerjob", "PowerJob")):
        write("%s/index.js" % key, SIMPLE_INDEX.replace("{key}", key).replace("{zh}", zh)
              .replace("{forms}", "\n".join(emit_forms(key))))
        write("%s/style.css" % key, SIMPLE_CSS.replace("{key}", key).replace("{zh}", zh))

    # ---- 挂载壳 ----
    write("../pages/stacks.js", SHELL_JS)
    # 原 stacks-forms.js 的注册表与 5 个套件组件已全部迁入 stacks/<key>/，
    # 本体不再是任何模块的依赖，按 F6 删除（保留会给「同一份逻辑两处存在」的错觉）。
    if FORMS_SRC.exists():
        FORMS_SRC.unlink()
        print("  已删除 template/static/pages/stacks-forms.js（内容全部迁入 stacks/<key>/）")
    print("已生成 template/static/stacks/** 与 pages/stacks.js 挂载壳")


if __name__ == "__main__":
    main()
