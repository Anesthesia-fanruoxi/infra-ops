// AI 输出高亮：把模型返回的自然语言结论里的重点信息着色。
// 用在 AI 解读面板的 summary / findings / suggestions 这类长文本上，
// 让「异常」「1.2 GB」「container_memory_usage{namespace="x"}」一眼可见，不用在整段里逐字找。
//
// 六类标记：code 表达式标识符 / name 实体名 / num 数值 / bad 风险词（红）/ warn 注意词（橙）/ good 正常词（绿）。
// 文本来自模型，走 v-html 渲染，因此这里先转义再拼标签——这是本文件唯一的注入面，务必保持转义在先。
window.AiHl = (function () {
  const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ESC[c])

  // 关键词表。命中策略是「长词先占位」，所以「无异常」不会被「异常」抢走、
  // 「不一致」不会被「一致」抢走——这些词只按字面匹配，不做语义判断，宁可漏也不误染。
  const GOOD = ['无异常', '无不正常', '没有异常', '未出现异常', '未发现异常', '一切正常', '运行平稳', '正常范围', '无不一致', '平稳', '稳定', '正常', '健康', '充足', '均衡', '一致', '符合预期']
  const BAD = ['异常', '不正常', '陡增', '陡降', '激增', '飙升', '突增', '突降', '失败', '报错', '错误', '超时', '拒绝', '不可用', '宕机', '崩溃', '溢出', 'OOM', '丢包', '积压', '阻塞', '熔断', '打满', '耗尽', '饱和', '超限', '超阈值', '全为 0', '全为0', '为 0', '为0', '缺失', '断点', '无数据', '失联', '掉线', '严重', '告警', '风险']
  const WARN = ['持续升高', '持续增长', '持续下降', '偏高', '偏低', '抖动', '波动较大', '波动', '缓慢', '增长', '上升', '下降', '接近', '排队', '延迟', '偏差', '倾斜', '不一致', '关注', '注意']

  const WORD_CLASS = new Map()
  ;[['good', GOOD], ['bad', BAD], ['warn', WARN]].forEach(([name, list]) => {
    list.forEach((w) => { if (!WORD_CLASS.has(w)) WORD_CLASS.set(w, name) })
  })
  // 同长度时 good 优先（「无异常」这类否定词必须压过其内部子串）
  const WORDS = [...WORD_CLASS.keys()].sort((a, b) =>
    b.length - a.length || (WORD_CLASS.get(a) === 'good' ? -1 : WORD_CLASS.get(b) === 'good' ? 1 : 0))
  const WORD_RE = new RegExp(WORDS.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|'), 'g')

  // 数值：千分位 / 小数 / 科学计数 + 常见单位（百分比、时长、容量、速率、条数、倍数）
  const NUM_RE = /[-+]?\d[\d,]*(?:\.\d+)?(?:\s?[eE][+-]?\d+)?\s?(?:%|ms|µs|us|ns|MiB|GiB|KiB|TiB|MB|GB|KB|TB|B|bytes|B\/s|KB\/s|MB\/s|GB\/s|tokens?|倍|分钟|小时|个月|天|周|秒|个|条|次|台|核|点|s|m|h|d)?/g
  // 时间点要整体识别，否则「14:20」会被数字规则拆成两段
  const TIME_RE = /\b\d{1,2}:\d{2}(?::\d{2})?\b/g
  // PromQL 标签选择器（含 =~ / !~）与带下划线的标识符（指标名 / 函数名）
  const LABEL_RE = /[A-Za-z_][A-Za-z0-9_]*\s*(?:=~|!~|=)\s*"[^"]*"/g
  const IDENT_RE = /\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b/gi
  // 实体名（容器 / 服务 / 主机 / 域名）：字母开头、含连字符或点的标识符。
  // 必须整体命中——否则「mall-order-7f9c」会被数字规则切成 name + 7 + f + 9 这样的碎片。
  const NAME_RE = /\b[a-z][a-z0-9]*(?:[-.][a-z0-9]+)+\b/gi

  // 收集某个正则的全部命中区间（空匹配要跳开，否则会死循环）
  function collect(src, re, rank, name, out) {
    re.lastIndex = 0
    let m
    while ((m = re.exec(src))) {
      if (!m[0]) { re.lastIndex++; continue }
      out.push({ s: m.index, e: m.index + m[0].length, rank, name })
    }
  }

  function html(text) {
    const src = String(text == null ? '' : text)
    if (!src) return ''
    const found = []
    // 反引号包裹的原文：只染内容，反引号本身留作普通文本
    let m
    const tick = /`([^`\n]+)`/g
    while ((m = tick.exec(src))) found.push({ s: m.index + 1, e: m.index + 1 + m[1].length, rank: 1, name: 'code' })
    collect(src, LABEL_RE, 1, 'code', found)
    collect(src, IDENT_RE, 1, 'code', found)
    collect(src, NAME_RE, 1, 'name', found)
    // 关键词按实际命中的词查类别（同一正则覆盖三类词）
    WORD_RE.lastIndex = 0
    while ((m = WORD_RE.exec(src))) {
      const name = WORD_CLASS.get(m[0])
      if (name) found.push({ s: m.index, e: m.index + m[0].length, rank: 2, name })
    }
    collect(src, TIME_RE, 3, 'num', found)
    collect(src, NUM_RE, 3, 'num', found)

    // 优先级（rank 小者先）→ 位置 → 长者优先；重叠的直接丢弃
    found.sort((a, b) => a.rank - b.rank || a.s - b.s || b.e - a.e)
    const kept = []
    for (const r of found) {
      if (kept.some((k) => r.s < k.e && k.s < r.e)) continue
      kept.push(r)
    }
    kept.sort((a, b) => a.s - b.s)

    let out = ''
    let pos = 0
    for (const r of kept) {
      if (r.s > pos) out += esc(src.slice(pos, r.s))
      out += '<span class="ai-hl ai-hl-' + r.name + '">' + esc(src.slice(r.s, r.e)) + '</span>'
      pos = r.e
    }
    return out + esc(src.slice(pos))
  }

  return { html }
})()
