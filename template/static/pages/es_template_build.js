// 索引模板可视化创建 · 纯构体层（无 DOM、无网络、无 Vue）。
// 把对话框的表单状态编译成「将发给 ES 的请求体」，并提供 ILM 阶段解读、pattern 重叠检测、
// 保存前模拟的请求体。对话框只负责收集与展示，判定与拼装逻辑全在这里——
// 这样 script/_fe_smoke.js 能在沙箱里**直接调用**这些函数做真值断言，
// 而不是只对源码做文本匹配（纯逻辑用文本匹配是测不出错的）。
window.EsTplBuild = (function () {

  // 默认值取自线上既有四个索引模板的共同项（es-default-index / big-es-default-index /
  // sendtimestamp-index / querytimestamp-index）：lifecycle hot-warm-cold-180days、
  // _tier_preference data_hot、refresh_interval 10s、translog 512m + async、replicas 0。
  const DEFAULTS = {
    name: '', patterns: [], priority: 21,
    lifecycleOn: true, policyName: '',
    shards: 3, replicas: 0, refreshInterval: '10s',
    translogFlush: '512m', translogDurability: 'async',
    tier: 'data_hot', timeField: '', timeFormat: 'epoch_millis',
    useBase: true, baseMode: 'new', baseName: 'xafq-logs-base'
  }

  // 公共层与索引模板的切分口径：
  //   公共层   = 「数据怎么流转、怎么写」—— 生命周期、初始分层、刷新间隔、translog
  //   索引模板 = 「数据怎么切分、怎么建模」—— 分片数、副本数、mappings
  // ES 合并时索引模板的声明覆盖组件模板，所以任何一项都能在索引模板里重新声明，不锁死。
  const COMMON_KEYS = ['lifecycle', 'routing', 'refresh_interval', 'translog']

  // 两个预设即线上四模板的两种形态：通配兜底（单分片 / priority 20）与业务明细（3 分片 / priority 21）。
  const PRESETS = [
    { key: 'wildcard', label: '日志 · 通配兜底',
      hint: '覆盖某前缀下全部索引：单分片、低优先级，重叠处由具体业务模板覆盖',
      patch: { priority: 20, shards: 1, useBase: true, baseMode: 'new', baseName: 'xafq-logs-base' } },
    { key: 'detail', label: '日志 · 业务明细',
      hint: '单条业务线独立模板：3 分片、高优先级，压过通配兜底',
      patch: { priority: 21, shards: 3, useBase: true, baseMode: 'new', baseName: 'xafq-logs-base' } }
  ]

  const PHASE_ORDER = ['hot', 'warm', 'cold', 'frozen', 'delete']

  function str(v, fallback) { return (v === undefined || v === null || v === '') ? fallback : String(v) }
  function num(v, fallback) { const n = Number(v); return isFinite(n) ? n : fallback }
  function phasesOf(policy) { return (((policy || {}).policy || {}).phases) || {} }

  // ---------- 设置项切分 ----------

  // 公共项（进组件模板，或在不用公共层时一并进索引模板）
  function commonIndexSettings(f) {
    const s = {
      refresh_interval: str(f.refreshInterval, DEFAULTS.refreshInterval),
      translog: {
        flush_threshold_size: str(f.translogFlush, DEFAULTS.translogFlush),
        durability: str(f.translogDurability, DEFAULTS.translogDurability)
      }
    }
    if (f.tier) s.routing = { allocation: { include: { _tier_preference: f.tier } } }
    if (f.lifecycleOn && f.policyName) s.lifecycle = { name: f.policyName }
    return s
  }

  // 本模板独有项。分片数按 ES 习惯写成字符串（与线上既有模板的写法一致）。
  function ownIndexSettings(f) {
    return {
      number_of_shards: String(num(f.shards, DEFAULTS.shards)),
      number_of_replicas: String(num(f.replicas, DEFAULTS.replicas))
    }
  }

  function buildMappings(f) {
    const field = str(f.timeField, '').trim()
    if (!field) return null
    const m = {}
    m[field] = { type: 'date', format: str(f.timeFormat, DEFAULTS.timeFormat) }
    return { properties: m }
  }

  // ---------- 请求体 ----------

  function buildBaseTemplate(f) {
    return { template: { settings: { index: commonIndexSettings(f) } } }
  }

  function usingBase(f) { return !!(f.useBase && str(f.baseName, '').trim()) }

  function buildIndexTemplate(f) {
    const index = {}
    // 用了公共层就不再重复声明公共项（重复声明会覆盖组件模板，反而把「一处改、处处生效」废掉）
    if (!usingBase(f)) Object.assign(index, commonIndexSettings(f))
    Object.assign(index, ownIndexSettings(f))
    const tpl = { settings: { index: index } }
    const mappings = buildMappings(f)
    if (mappings) tpl.mappings = mappings
    return {
      index_patterns: (f.patterns || []).filter(Boolean).slice(),
      priority: num(f.priority, DEFAULTS.priority),
      composed_of: usingBase(f) ? [str(f.baseName, '').trim()] : [],
      template: tpl
    }
  }

  // 将实际发出的请求（顺序即执行顺序：组件模板必须先于引用它的索引模板）
  function buildRequests(f) {
    const reqs = []
    if (usingBase(f) && f.baseMode !== 'existing') {
      reqs.push({ label: '公共层组件模板', method: 'PUT',
        path: '/_component_template/' + str(f.baseName, '').trim(), body: buildBaseTemplate(f) })
    }
    reqs.push({ label: '索引模板', method: 'PUT',
      path: '/_index_template/' + str(f.name, '').trim(), body: buildIndexTemplate(f) })
    return reqs
  }

  // 保存前模拟用的请求体。组件模板尚未创建时 composed_of 在 ES 侧解析不到（_simulate 会报 not found），
  // 所以「新建公共层」这一路把公共项内联进模板体，模拟的是**保存后的合并结果**；
  // 「复用已有组件模板」这一路保留 composed_of，交给 ES 真解析。两条路的口径要在界面上讲清。
  function simulateBody(f) {
    const body = buildIndexTemplate(f)
    if (usingBase(f) && f.baseMode !== 'existing') {
      body.template.settings.index = Object.assign({}, commonIndexSettings(f), body.template.settings.index)
      body.composed_of = []
    }
    return body
  }

  // ---------- ILM 解读 ----------

  function phaseChain(policy) {
    const phases = phasesOf(policy)
    const rank = n => { const i = PHASE_ORDER.indexOf(n); return i < 0 ? PHASE_ORDER.length : i }
    return Object.keys(phases).sort((a, b) => rank(a) - rank(b)).map(n => {
      const age = str((phases[n] || {}).min_age, '')
      return { name: n, min_age: age, label: (age && age !== '0ms') ? n + ' ' + age : n }
    })
  }

  function phaseTiers(policy) {
    const phases = phasesOf(policy)
    const out = []
    Object.keys(phases).forEach(n => {
      const a = (phases[n] || {}).actions || {}
      const t = ((a.allocate || {}).include || {})._tier_preference
      if (t && out.indexOf(t) < 0) out.push(t)
    })
    return out
  }

  function autoDeletes(policy) { return !!phasesOf(policy).delete }

  // ---------- pattern ----------

  // 通配符之前的前缀。空串表示以 * 开头（匹配一切）。
  function patternPrefix(p) {
    const s = String(p || '')
    const i = s.search(/[*?]/)
    return i < 0 ? s : s.slice(0, i)
  }

  // 重叠判定用「字面前缀互为前缀」这一保守近似。ES 的 pattern 只支持 * 与 ?，
  // 该近似在「谁覆盖谁」这类真实场景（xafq_* 与 xafq_xxx_record*）上判定正确，
  // 但对「两个 pattern 的公共部分落在通配符里」的极端写法偏保守（宁可多报，不静默漏报）。
  function patternOverlaps(a, b) {
    const pa = patternPrefix(a), pb = patternPrefix(b)
    return pa.indexOf(pb) === 0 || pb.indexOf(pa) === 0
  }

  // 与已有索引模板的重叠清单：给出重叠的一对 pattern，以及本模板是否压得住对方。
  function overlaps(f, existing) {
    const mine = (f.patterns || []).filter(Boolean)
    const myPriority = num(f.priority, DEFAULTS.priority)
    const out = []
    ;(existing || []).forEach(t => {
      if (!t || t.name === str(f.name, '').trim()) return
      const theirs = t.index_patterns || []
      const hit = []
      mine.forEach(a => theirs.forEach(b => { if (patternOverlaps(a, b)) hit.push(a + ' ↔ ' + b) }))
      if (!hit.length) return
      const p = (t.priority === undefined || t.priority === null) ? 0 : t.priority
      out.push({ name: t.name, patterns: theirs, priority: p, wins: myPriority >= p, hits: hit })
    })
    return out
  }

  // 引用了某个组件模板的索引模板（覆盖公共层前用它算影响面）
  function baseRefs(baseName, indexTemplates) {
    const n = str(baseName, '').trim()
    return (indexTemplates || [])
      .filter(t => (t.composed_of || []).indexOf(n) >= 0)
      .map(t => t.name)
  }

  // ---------- 校验 ----------

  // 与后端 validateResourceName 同口径：非空、不含 * , ?、不以 _ 或 . 开头。
  function nameError(name) {
    const n = str(name, '').trim()
    if (!n) return '不能为空'
    if (/[*,?]/.test(n)) return '不得含 * , ?'
    if (n.charAt(0) === '_' || n.charAt(0) === '.') return '不得以 _ 或 . 开头'
    return ''
  }

  function validate(f, existing) {
    const errs = []
    const ne = nameError(f.name)
    if (ne) errs.push('模板名称：' + ne)
    const pats = (f.patterns || []).filter(Boolean)
    if (!pats.length) errs.push('至少填写一个 index_patterns')
    pats.forEach(p => {
      if (/[,\s]/.test(p)) errs.push('pattern「' + p + '」不得含逗号或空格（多个 pattern 请分条添加）')
    })
    if (f.lifecycleOn && !str(f.policyName, '')) errs.push('已开启生命周期关联，必须选择一个 ILM 策略')
    if (f.useBase) {
      const be = nameError(f.baseName)
      if (be) errs.push('公共层名称：' + be)
    }
    return errs
  }

  // ---------- 展示辅助 ----------

  function previewText(f) {
    return buildRequests(f).map(r => r.method + ' ' + r.path + '\n' + JSON.stringify(r.body, null, 2)).join('\n\n')
  }

  function escapeHTML(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  }

  // 二次确认的请求回显（§3.3 硬约束：二次确认必须完整回显将发出的请求）。
  // ElMessageBox 的纯文本消息不保留换行，两份 JSON 摞在一起没法读，所以走 dangerouslyUseHTMLString——
  // 代价是必须自己转义：模板名与 pattern 都来自用户输入，不转义就是自造 XSS。
  function echoHTML(head, requests) {
    const body = (requests || []).map(r => r.method + ' ' + r.path + '\n' + JSON.stringify(r.body, null, 2)).join('\n\n')
    const pre = '<pre style="text-align:left;white-space:pre-wrap;word-break:break-all;'
      + 'font-family:var(--font-mono);font-size:12px;line-height:1.55;max-height:300px;overflow:auto;margin:8px 0 0">'
      + escapeHTML(body) + '</pre>'
    return '<div style="text-align:left">' + escapeHTML(head || '') + '</div>' + pre
  }

  // 模拟用的假想索引名：去掉通配符再加后缀（_simulate 不接受通配符）。
  // 前缀末尾不是分隔符时补一个 -，避免拼出 xafq_logtest-000001 这种读不出来的名字。
  function mockIndexName(f) {
    const p = (f.patterns || []).filter(Boolean)[0]
    if (!p) return 'test-000001'
    const pre = patternPrefix(p)
    return (pre && !/[-_.]$/.test(pre) ? pre + '-' : pre) + 'test-000001'
  }

  function withPreset(key) {
    const preset = PRESETS.filter(p => p.key === key)[0]
    return Object.assign(blank(), preset ? preset.patch : {})
  }

  // 表单初始状态。patterns 每次都要新数组——直接引用 DEFAULTS 会把两次打开的表单串在一起。
  function blank() {
    return Object.assign({}, DEFAULTS, { patterns: [] })
  }

  return {
    DEFAULTS: DEFAULTS, PRESETS: PRESETS, COMMON_KEYS: COMMON_KEYS, PHASE_ORDER: PHASE_ORDER,
    commonIndexSettings: commonIndexSettings, ownIndexSettings: ownIndexSettings,
    buildMappings: buildMappings, buildBaseTemplate: buildBaseTemplate,
    buildIndexTemplate: buildIndexTemplate, buildRequests: buildRequests, simulateBody: simulateBody,
    usingBase: usingBase, phaseChain: phaseChain, phaseTiers: phaseTiers, autoDeletes: autoDeletes,
    patternPrefix: patternPrefix, patternOverlaps: patternOverlaps, overlaps: overlaps,
    baseRefs: baseRefs, nameError: nameError, validate: validate,
    previewText: previewText, escapeHTML: escapeHTML, echoHTML: echoHTML,
    mockIndexName: mockIndexName, withPreset: withPreset, blank: blank
  }
})()
