// Redis 工具的前端共用小函数。列表、key 浏览器与值预览都要用，放一份免得三处各写一套。
window.RedisUtil = {
  // TTL 的取值语义由 Redis 定义：-1 永久、-2 已不存在，其余是剩余秒数。
  ttlText(t) {
    const n = Number(t)
    if (!Number.isFinite(n)) return '—'
    if (n === -1) return '永久'
    if (n < 0) return '已失效'
    if (n < 60) return n + 's'
    if (n < 3600) return Math.round(n / 60) + 'm'
    if (n < 86400) return (n / 3600).toFixed(1) + 'h'
    return (n / 86400).toFixed(1) + 'd'
  },
  // 类型徽标的颜色后缀；未知/空类型统一走 none（灰）。
  typeClass(t) {
    const known = ['string', 'list', 'set', 'zset', 'hash', 'stream']
    return 'redis-type--' + (known.indexOf(t) >= 0 ? t : 'none')
  },
  dbLabel(d) {
    if (!d) return '—'
    return d.keys < 0 ? 'db' + d.index : 'db' + d.index + '（' + d.keys + ' keys）'
  },
  // 错误文案：优先用后端返回的 message，其次 axios 的 message。
  errText(e, fallback) {
    return (e && e.response && e.response.data && e.response.data.message) || (e && e.message) || fallback || '操作失败'
  },
  fmtNum(n) {
    const v = Number(n)
    return Number.isFinite(v) ? v.toLocaleString('zh-CN') : '—'
  }
}
