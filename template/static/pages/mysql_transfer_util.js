// 导入 / 导出两个对话框共用的小工具。
//
// 单独成文件是为了让两边基于同一份实现——各写一份格式化与推断逻辑，
// 迟早会出现「导出写 200 行/批、导入显示 500 条/批」这种对不上的情况。
window.MySQLTransferUtil = {
  // 千分位。记录行数动辄六位数，不加分隔符读不出量级。
  fmtNum(n) {
    return Number(n || 0).toLocaleString('en-US')
  },
  fmtMs(n) {
    const v = Number(n || 0)
    if (v < 1000) return v + ' ms'
    if (v < 60000) return (v / 1000).toFixed(2) + ' s'
    return Math.floor(v / 60000) + ' 分 ' + Math.round((v % 60000) / 1000) + ' 秒'
  },
  fmtSize(n) {
    let v = Number(n || 0)
    if (v <= 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let i = 0
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
    return (i === 0 ? v.toFixed(0) : v.toFixed(1)) + ' ' + units[i]
  },
  // 压成一行：SQL 与错误信息要放进单行容器里显示
  oneLine(s, n) {
    const t = String(s || '').replace(/\s+/g, ' ').trim()
    return t.length > n ? t.slice(0, n) + '…' : t
  },
  // axios 的错误体在拦截器之外不易取，这里统一兜一层
  errText(e, fallback) {
    if (e && e.response && e.response.data && e.response.data.message) return e.response.data.message
    if (e && e.message) return e.message
    return fallback
  },
  // 进度轮询键：前端生成、随请求带给服务端，两边靠它认领同一件事
  newKey(prefix) {
    return prefix + Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
  },
  // 文件名会进确认弹窗（dangerouslyUseHTMLString），必须转义
  escapeHTML(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;')
  },
  // 从 SELECT 里猜 INSERT 的目标表，口径与后端 qualifyTable 一致：
  // 取第一个 FROM 后一到两段标识符的最后一段。多表 JOIN 时未必猜得准，
  // 所以只用来填默认值，界面上允许改。
  guessTable(sql) {
    const s = String(sql || '')
      .replace(/\/\*[\s\S]*?\*\//g, ' ')
      .replace(/^[ \t]*(--|#).*$/gm, ' ')
    const m = s.match(/\bfrom\s+((?:`[^`]+`|[A-Za-z_$][\w$]*)(?:\s*\.\s*(?:`[^`]+`|[A-Za-z_$][\w$]*))?)/i)
    if (!m) return ''
    const parts = m[1].split('.').map(p => p.trim().replace(/`/g, ''))
    return parts[parts.length - 1]
  }
}
